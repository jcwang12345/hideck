package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yibaiba/hideck/internal/dispatch"
	"github.com/yibaiba/hideck/internal/identity"
	"github.com/yibaiba/hideck/internal/phone"
	"github.com/yibaiba/hideck/internal/resourcecatalog"
	"github.com/yibaiba/hideck/pkg/logger"
)

const manualProfileSwitchTimeout = 2 * time.Minute

func (s *Server) handleActivateResourceProfile(c *gin.Context) {
	service, ok := s.requireResourceCatalog(c)
	if !ok {
		return
	}
	principal, ok := CurrentPrincipal(c)
	if !ok || principal.Role != identity.RoleAdmin {
		abortAuthorization(c, http.StatusForbidden, "forbidden", "仅管理员可以切换 Profile")
		return
	}
	if s.pool == nil || s.dispatcher == nil {
		writeProfileActionError(c, http.StatusServiceUnavailable, "resource_unavailable", "设备或调度服务不可用")
		return
	}
	cardID, profileID := strings.TrimSpace(c.Param("id")), strings.TrimSpace(c.Param("profile_id"))
	card, err := service.Get(c.Request.Context(), cardID)
	if err != nil {
		writeResourceError(c, err)
		return
	}
	if card.Type != resourcecatalog.CardContainerTypeEUICC {
		writeProfileActionError(c, http.StatusConflict, "unsupported_card", "只有已确认的 eUICC 才能切换 Profile")
		return
	}
	profile, found := resourceProfile(card, profileID)
	if !found {
		writeProfileActionError(c, http.StatusNotFound, "profile_not_found", "Profile 不属于该卡")
		return
	}
	if !profile.Present || strings.TrimSpace(profile.ICCID) == "" {
		writeProfileActionError(c, http.StatusConflict, "profile_unavailable", "Profile 当前不可用或缺少 ICCID")
		return
	}
	deviceID := strings.TrimSpace(card.LastDeviceID)
	worker := s.pool.GetWorker(deviceID)
	if worker == nil {
		writeProfileActionError(c, http.StatusConflict, "device_offline", "承载该卡的设备当前离线")
		return
	}
	if sameCardIdentity(worker.CurrentICCID(), profile.ICCID) && !s.pool.IsESIMSwitching(deviceID) {
		c.JSON(http.StatusOK, gin.H{
			"status": "ok", "changed": false, "card_id": card.ID, "profile_id": profile.ID,
			"device_id": deviceID, "message": "该 Profile 已经启用",
		})
		return
	}
	if s.resourceDeviceBusy(principal, deviceID) {
		writeProfileActionError(c, http.StatusConflict, "card_busy", "设备正在通话或执行线路切换")
		return
	}

	reservation, err := s.dispatcher.ReserveMaintenance(c.Request.Context(), dispatch.MaintenanceInput{
		UserID: principal.UserID, Operation: "profile_switch", DeviceID: deviceID,
		CardID: card.ID, ProfileID: profile.ID,
	})
	if err != nil {
		if errors.Is(err, dispatch.ErrNoResource) {
			writeProfileActionError(c, http.StatusConflict, "card_busy", "设备已被其他通话或操作占用")
			return
		}
		writeProfileActionError(c, http.StatusInternalServerError, "resource_unavailable", "无法锁定设备资源")
		return
	}
	lease := reservation.Lease
	lease, err = s.dispatcher.Transition(c.Request.Context(), lease.ID, lease.Version, dispatch.LeaseSwitching)
	if err != nil {
		_ = s.dispatcher.Release(c.Request.Context(), lease.ID, "profile_switch_failed", err.Error())
		writeProfileActionError(c, http.StatusConflict, "card_busy", "设备状态已变化，请刷新后重试")
		return
	}

	originalICCID := strings.TrimSpace(worker.CurrentICCID())
	ctx, cancel := context.WithTimeout(businessRequestContext(c), manualProfileSwitchTimeout)
	defer cancel()
	if err = s.pool.SwitchESIMProfileAndWait(ctx, deviceID, profile.ICCID, profile.AID); err != nil {
		s.finishFailedProfileSwitch(c, service, principal.UserID, lease.ID, card.ID, profile.ID, deviceID, originalICCID, "profile_switch_failed", err)
		writeProfileActionError(c, http.StatusBadGateway, "profile_switch_failed", "Profile 切换失败，已尝试恢复原 Profile")
		return
	}
	lease, err = s.dispatcher.Transition(ctx, lease.ID, lease.Version, dispatch.LeaseIdentityRefresh)
	if err == nil {
		lease, err = s.dispatcher.Transition(ctx, lease.ID, lease.Version, dispatch.LeaseRegistering)
	}
	if err == nil {
		err = s.ensureCallRegistrationReady(ctx, deviceID)
	}
	if err != nil {
		s.finishFailedProfileSwitch(c, service, principal.UserID, lease.ID, card.ID, profile.ID, deviceID, originalICCID, "ims_not_ready", err)
		writeProfileActionError(c, http.StatusBadGateway, "ims_not_ready", "Profile 已切换但 IMS 未就绪，已尝试恢复原 Profile")
		return
	}
	if updated, transitionErr := s.dispatcher.Transition(ctx, lease.ID, lease.Version, dispatch.LeaseReady); transitionErr == nil {
		lease = updated
	} else {
		s.finishFailedProfileSwitch(c, service, principal.UserID, lease.ID, card.ID, profile.ID, deviceID, originalICCID, "profile_switch_failed", transitionErr)
		writeProfileActionError(c, http.StatusConflict, "profile_switch_failed", "线路状态已变化，已尝试恢复原 Profile")
		return
	}

	if syncErr := s.syncCachedCardResources(c, service); syncErr != nil {
		logger.Warn("手动切换 Profile 后同步资源目录失败", "card_id", card.ID, "profile_id", profile.ID, "err", syncErr)
	}
	if err = service.RecordAudit(ctx, principal.UserID, "profile.manually_switched", "card", card.ID, gin.H{
		"profile_id": profile.ID, "device_id": deviceID, "request_id": reservation.Request.ID,
	}); err != nil {
		restoreErr := s.restoreDispatchProfile(deviceID, originalICCID)
		_ = s.dispatcher.Release(c.Request.Context(), lease.ID, "audit_failed", err.Error())
		if restoreErr != nil {
			logger.Error("Profile 审计失败且恢复原 Profile 失败", "device_id", deviceID, "err", restoreErr)
		}
		writeProfileActionError(c, http.StatusInternalServerError, "audit_failed", "切换操作无法写入审计记录，已尝试恢复")
		return
	}
	if err = s.dispatcher.Release(ctx, lease.ID, "", ""); err != nil {
		writeProfileActionError(c, http.StatusInternalServerError, "resource_unavailable", "Profile 已切换，但设备资源锁释放失败")
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"status": "ok", "changed": true, "card_id": card.ID, "profile_id": profile.ID,
		"device_id": deviceID, "request_id": reservation.Request.ID,
	})
}

func resourceProfile(card resourcecatalog.CardContainerDTO, profileID string) (resourcecatalog.SubscriptionProfileDTO, bool) {
	for _, profile := range card.Profiles {
		if profile.ID == profileID {
			return profile, true
		}
	}
	return resourcecatalog.SubscriptionProfileDTO{}, false
}

func (s *Server) resourceDeviceBusy(principal identity.Principal, deviceID string) bool {
	if s.pool.IsESIMSwitching(deviceID) {
		return true
	}
	if s.voiceGW != nil && s.voiceGW.ActiveCall(deviceID) != nil {
		return true
	}
	if s.phone == nil {
		return false
	}
	calls, err := s.phone.ActiveScoped(phone.AccessScope{UserID: principal.UserID, Administrator: true}, "")
	if err != nil {
		return true
	}
	for _, call := range calls {
		if call.DeviceID == deviceID {
			return true
		}
	}
	return false
}

func (s *Server) finishFailedProfileSwitch(c *gin.Context, service *resourcecatalog.Service, actorID, leaseID, cardID, profileID, deviceID, originalICCID, code string, cause error) {
	restoreErr := s.restoreDispatchProfile(deviceID, originalICCID)
	if restoreErr != nil {
		logger.Error("手动切换 Profile 失败且恢复原 Profile 失败", "device_id", deviceID, "err", restoreErr)
	}
	_ = s.dispatcher.Release(c.Request.Context(), leaseID, code, cause.Error())
	_ = service.RecordAudit(c.Request.Context(), actorID, "profile.switch_failed", "card", cardID, gin.H{
		"profile_id": profileID, "device_id": deviceID, "failure_code": code,
		"restored": restoreErr == nil,
	})
}

func writeProfileActionError(c *gin.Context, status int, code, message string) {
	c.JSON(status, gin.H{"status": "error", "code": code, "message": message, "request_id": requestID(c)})
}
