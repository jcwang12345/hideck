package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yibaiba/hideck/internal/db"
	"github.com/yibaiba/hideck/internal/device"
	"github.com/yibaiba/hideck/internal/dispatch"
	"github.com/yibaiba/hideck/internal/identity"
	"github.com/yibaiba/hideck/internal/resourcecatalog"
	"github.com/yibaiba/hideck/pkg/logger"
	"gorm.io/gorm"
)

type smsRequestAccess struct {
	principal identity.Principal
	scope     db.SMSAccessScope
	rest      bool
}

func (s *Server) smsAccess(c businessContext) (smsRequestAccess, bool) {
	var principal identity.Principal
	if rest, ok := c.(*gin.Context); ok {
		var found bool
		principal, found = CurrentPrincipal(rest)
		if !found || strings.TrimSpace(principal.UserID) == "" {
			abortAuthorization(rest, http.StatusUnauthorized, "unauthorized", "未授权")
			return smsRequestAccess{}, false
		}
		if principal.Role == identity.RoleAdmin {
			return smsRequestAccess{principal: principal, scope: db.UnrestrictedSMSAccessScope(), rest: true}, true
		}
	} else {
		userID := strings.TrimSpace(s.authSnapshot().Username)
		if userID == "" {
			// businessCall is created only after MCP token authentication. Tests and
			// legacy installations may not carry a Web username into that transport.
			userID = "legacy-admin"
		}
		principal = identity.Principal{UserID: userID, Role: identity.RoleAdmin}
		return smsRequestAccess{principal: principal, scope: db.UnrestrictedSMSAccessScope()}, true
	}
	if s.resourceInitErr != nil || s.resourceCatalog == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "error", "code": "sms_access_unavailable", "message": "短信授权服务暂不可用"})
		return smsRequestAccess{}, false
	}
	identities, err := s.resourceCatalog.ListSMSAccessIdentities(businessRequestContext(c), principal.UserID)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "error", "code": "sms_access_unavailable", "message": "读取短信授权失败"})
		return smsRequestAccess{}, false
	}
	dbIdentities := make([]db.SMSAccessIdentity, 0, len(identities))
	for _, item := range identities {
		dbIdentities = append(dbIdentities, db.SMSAccessIdentity{ICCID: item.ICCID, IMSI: item.IMSI, LocalPhone: item.LocalPhone})
	}
	return smsRequestAccess{principal: principal, scope: db.NewSMSAccessScope(dbIdentities), rest: true}, true
}

type smsSendSelection struct {
	worker        *device.Worker
	poolID        string
	numberID      string
	cardID        string
	profileID     string
	expectedICCID string
	expectedIMSI  string
}

type smsSendResolveError struct {
	status  int
	code    string
	message string
}

func (e *smsSendResolveError) Error() string { return e.message }

func (s *Server) resolveAuthorizedSMSSendWorker(ctx context.Context, access smsRequestAccess, poolID, numberID string) (smsSendSelection, *smsSendResolveError) {
	poolID, numberID = strings.TrimSpace(poolID), strings.TrimSpace(numberID)
	if poolID == "" {
		return smsSendSelection{}, &smsSendResolveError{http.StatusBadRequest, "sms_pool_required", "普通用户发送短信必须指定 pool_id"}
	}
	if s.resourceInitErr != nil || s.resourceCatalog == nil || s.pool == nil {
		return smsSendSelection{}, &smsSendResolveError{http.StatusServiceUnavailable, "sms_dispatch_unavailable", "短信线路服务暂不可用"}
	}
	view, err := s.resourceCatalog.GetNumberPool(ctx, poolID, access.principal.UserID, access.principal.Role == identity.RoleAdmin)
	if err != nil {
		if errors.Is(err, resourcecatalog.ErrNotFound) || errors.Is(err, gorm.ErrRecordNotFound) {
			return smsSendSelection{}, &smsSendResolveError{http.StatusForbidden, "sms_pool_forbidden", "号码池不存在或当前用户无权使用"}
		}
		return smsSendSelection{}, &smsSendResolveError{http.StatusServiceUnavailable, "sms_dispatch_unavailable", "读取号码池失败"}
	}
	candidates := make([]resourcecatalog.NumberPoolMemberView, 0, len(view.Members))
	for _, member := range view.Members {
		if !member.Enabled || member.Profile == nil || member.Card == nil || smsNumberUnavailable(member.Number.Status) {
			continue
		}
		if numberID != "" && member.Number.ID != numberID {
			continue
		}
		candidates = append(candidates, member)
	}
	if len(candidates) == 0 {
		return smsSendSelection{}, &smsSendResolveError{http.StatusConflict, "sms_no_resource", "号码池中没有匹配的短信线路"}
	}
	if numberID != "" {
		return s.resolveSMSPoolMember(ctx, view.ID, candidates[0])
	}
	var firstConflict *smsSendResolveError
	for _, candidate := range candidates {
		selection, conflict := s.resolveSMSPoolMember(ctx, view.ID, candidate)
		if conflict == nil {
			return selection, nil
		}
		if firstConflict == nil {
			firstConflict = conflict
		}
	}
	return smsSendSelection{}, firstConflict
}

func (s *Server) resolveSMSPoolMember(ctx context.Context, poolID string, member resourcecatalog.NumberPoolMemberView) (smsSendSelection, *smsSendResolveError) {
	if !member.Profile.Present || !member.Profile.Active {
		return smsSendSelection{}, &smsSendResolveError{http.StatusConflict, "sms_profile_inactive", "所选订阅 Profile 未激活；短信不会自动切换 Profile"}
	}
	deviceID := strings.TrimSpace(member.Card.LastDeviceID)
	if deviceID == "" {
		return smsSendSelection{}, &smsSendResolveError{http.StatusConflict, "sms_device_offline", "所选线路没有当前设备"}
	}
	if s.pool.IsESIMSwitching(deviceID) {
		return smsSendSelection{}, &smsSendResolveError{http.StatusConflict, "sms_profile_switching", "设备正在切换 SIM Profile，请稍后重试"}
	}
	worker := s.pool.GetWorker(deviceID)
	if worker == nil {
		return smsSendSelection{}, &smsSendResolveError{http.StatusConflict, "sms_device_offline", "所选线路设备不在线"}
	}
	expectedICCID := db.CanonicalICCID(member.Profile.ICCID)
	expectedIMSI := strings.TrimSpace(member.Profile.IMSI)
	if expectedICCID == "" && expectedIMSI == "" {
		return smsSendSelection{}, &smsSendResolveError{http.StatusConflict, "sms_profile_inactive", "所选 Profile 缺少可验证的 ICCID/IMSI"}
	}
	if expectedICCID != "" && db.CanonicalICCID(worker.CurrentICCID()) != expectedICCID {
		return smsSendSelection{}, &smsSendResolveError{http.StatusConflict, "sms_profile_inactive", "设备当前 Profile 与所选号码不一致；短信不会自动切换 Profile"}
	}
	if expectedIMSI != "" {
		if current := strings.TrimSpace(worker.ProjectDeviceStatus().IMSI); current != expectedIMSI {
			return smsSendSelection{}, &smsSendResolveError{http.StatusConflict, "sms_profile_inactive", "设备当前订阅与所选号码不一致；短信不会自动切换 Profile"}
		}
	}
	busy, err := s.smsDeviceBusy(ctx, deviceID, "")
	if err != nil {
		return smsSendSelection{}, &smsSendResolveError{http.StatusServiceUnavailable, "sms_dispatch_unavailable", "读取设备占用状态失败"}
	}
	if busy {
		return smsSendSelection{}, &smsSendResolveError{http.StatusConflict, "sms_device_busy", "设备正在通话或执行线路操作，暂不能发送短信"}
	}
	return smsSendSelection{
		worker: worker, poolID: poolID, numberID: member.Number.ID,
		cardID: member.Card.ID, profileID: member.Profile.ID,
		expectedICCID: expectedICCID, expectedIMSI: expectedIMSI,
	}, nil
}

func smsNumberUnavailable(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "disabled", "suspended":
		return true
	default:
		return false
	}
}

func (s *Server) smsDeviceBusy(ctx context.Context, deviceID, ignoredLeaseID string) (bool, error) {
	deviceID, ignoredLeaseID = strings.TrimSpace(deviceID), strings.TrimSpace(ignoredLeaseID)
	if deviceID == "" {
		return true, nil
	}
	if s.dispatcher == nil {
		return true, errors.New("SMS dispatch lease service is unavailable")
	}
	activeLeases, err := s.dispatcher.ActiveLeases(ctx)
	if err != nil {
		return true, err
	}
	for _, lease := range activeLeases {
		if strings.TrimSpace(lease.DeviceID) == deviceID && strings.TrimSpace(lease.ID) != ignoredLeaseID {
			return true, nil
		}
	}
	if s.phone != nil {
		for _, call := range s.phone.Active("") {
			if strings.TrimSpace(call.DeviceID) == deviceID {
				return true, nil
			}
		}
	}
	if s.voiceGW != nil && s.voiceGW.ActiveCall(deviceID) != nil {
		return true, nil
	}
	if s.pool != nil {
		if s.pool.IsESIMSwitching(deviceID) {
			return true, nil
		}
		if controller := s.pool.NativeVoLTEController(); controller != nil && controller.ActiveCall(deviceID) != nil {
			return true, nil
		}
		if controller := s.pool.ModemVoiceController(); controller != nil && controller.ActiveCall(deviceID) != nil {
			return true, nil
		}
	}
	return false, nil
}

func (s *Server) reserveSMSSend(ctx context.Context, access smsRequestAccess, selection smsSendSelection) (string, *smsSendResolveError) {
	if s == nil || s.pool == nil || s.dispatcher == nil || selection.worker == nil {
		return "", &smsSendResolveError{http.StatusServiceUnavailable, "sms_dispatch_unavailable", "短信线路占用服务暂不可用"}
	}
	deviceID := strings.TrimSpace(selection.worker.ID)
	if s.pool.IsESIMSwitching(deviceID) {
		return "", &smsSendResolveError{http.StatusConflict, "sms_profile_switching", "设备正在切换 SIM Profile，请稍后重试"}
	}
	busy, err := s.smsDeviceBusy(ctx, deviceID, "")
	if err != nil {
		return "", &smsSendResolveError{http.StatusServiceUnavailable, "sms_dispatch_unavailable", "读取设备占用状态失败"}
	}
	if busy {
		return "", &smsSendResolveError{http.StatusConflict, "sms_device_busy", "设备正在通话或执行线路操作，暂不能发送短信"}
	}

	cardID, profileID := strings.TrimSpace(selection.cardID), strings.TrimSpace(selection.profileID)
	if cardID == "" || profileID == "" {
		if s.resourceCatalog != nil {
			resource, resolveErr := s.resourceCatalog.ResolveCurrentVoiceResource(ctx, deviceID, selection.worker.CurrentICCID(), selection.numberID)
			switch {
			case resolveErr == nil:
				cardID, profileID = resource.Card.ID, resource.Profile.ID
			case errors.Is(resolveErr, resourcecatalog.ErrNotFound), errors.Is(resolveErr, gorm.ErrRecordNotFound):
				// A diagnostic route may intentionally target a current SIM that has
				// not yet been imported into the resource catalog. It still receives
				// the same device-level dispatch lease below.
			default:
				return "", &smsSendResolveError{http.StatusServiceUnavailable, "sms_dispatch_unavailable", "读取当前短信线路失败"}
			}
		}
		if cardID == "" {
			cardID = "diagnostic-device:" + deviceID
		}
		if profileID == "" {
			profileID = "diagnostic-profile:" + deviceID
		}
	}

	reservation, err := s.dispatcher.ReserveMaintenance(ctx, dispatch.MaintenanceInput{
		UserID: strings.TrimSpace(access.principal.UserID), Operation: "sms_send",
		DeviceID: deviceID, CardID: cardID, ProfileID: profileID,
	})
	if err != nil {
		if errors.Is(err, dispatch.ErrNoResource) {
			return "", &smsSendResolveError{http.StatusConflict, "sms_device_busy", "设备已被其他通话或线路操作占用"}
		}
		return "", &smsSendResolveError{http.StatusServiceUnavailable, "sms_dispatch_unavailable", "无法锁定短信线路"}
	}
	leaseID := reservation.Lease.ID
	fail := func(resolveErr *smsSendResolveError) (string, *smsSendResolveError) {
		s.releaseSMSSendLease(leaseID, errors.New(resolveErr.message))
		return "", resolveErr
	}
	if s.pool.GetWorker(deviceID) != selection.worker {
		return fail(&smsSendResolveError{http.StatusConflict, "sms_device_offline", "所选线路设备已变化，请重试"})
	}
	if s.pool.IsESIMSwitching(deviceID) {
		return fail(&smsSendResolveError{http.StatusConflict, "sms_profile_switching", "设备正在切换 SIM Profile，请稍后重试"})
	}
	if selection.expectedICCID != "" && db.CanonicalICCID(selection.worker.CurrentICCID()) != selection.expectedICCID {
		return fail(&smsSendResolveError{http.StatusConflict, "sms_profile_inactive", "设备当前 Profile 已变化；短信不会自动切换 Profile"})
	}
	if selection.expectedIMSI != "" && strings.TrimSpace(selection.worker.ProjectDeviceStatus().IMSI) != selection.expectedIMSI {
		return fail(&smsSendResolveError{http.StatusConflict, "sms_profile_inactive", "设备当前订阅已变化；短信不会自动切换 Profile"})
	}
	busy, err = s.smsDeviceBusy(ctx, deviceID, leaseID)
	if err != nil {
		return fail(&smsSendResolveError{http.StatusServiceUnavailable, "sms_dispatch_unavailable", "复核设备占用状态失败"})
	}
	if busy {
		return fail(&smsSendResolveError{http.StatusConflict, "sms_device_busy", "设备正在通话或执行线路操作，暂不能发送短信"})
	}
	return leaseID, nil
}

func (s *Server) releaseSMSSendLease(leaseID string, cause error) {
	if s == nil || s.dispatcher == nil || strings.TrimSpace(leaseID) == "" {
		return
	}
	code, reason := "", ""
	if cause != nil {
		code, reason = "sms_send_failed", cause.Error()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.dispatcher.Release(ctx, leaseID, code, reason); err != nil {
		logger.Error("释放短信线路占用失败", "lease_id", leaseID, "err", err)
	}
}
