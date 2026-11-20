package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yibaiba/hideck/internal/dispatch"
	"github.com/yibaiba/hideck/internal/identity"
	"github.com/yibaiba/hideck/internal/phone"
	"github.com/yibaiba/hideck/internal/resourcecatalog"
	"github.com/yibaiba/hideck/pkg/logger"
	"gorm.io/gorm"
)

const dispatchPreparationTimeout = 2 * time.Minute

type dispatchCallRequest struct {
	PoolID  string `json:"pool_id"`
	Callee  string `json:"callee"`
	MediaID string `json:"media_id"`
}

type dispatchAttemptError struct {
	code string
	err  error
}

func (e dispatchAttemptError) Error() string { return e.err.Error() }
func (e dispatchAttemptError) Unwrap() error { return e.err }

func (s *Server) registerDispatchRoutes(api *gin.RouterGroup) {
	api.POST("/phone/call-requests", s.handleDispatchCallRequest)
}

func (s *Server) handleDispatchCallRequest(c *gin.Context) {
	principal, ok := CurrentPrincipal(c)
	if !ok {
		writeDispatchError(c, http.StatusUnauthorized, "unauthorized", "未授权")
		return
	}
	if s.resourceInitErr != nil || s.resourceCatalog == nil || s.dispatcher == nil || s.phone == nil || s.pool == nil {
		writeDispatchError(c, http.StatusServiceUnavailable, "dispatch_unavailable", "线路调度服务暂不可用")
		return
	}
	var request dispatchCallRequest
	if !decodePhoneJSON(c, &request) {
		return
	}
	request.PoolID = strings.TrimSpace(request.PoolID)
	request.Callee = strings.TrimSpace(request.Callee)
	request.MediaID = strings.TrimSpace(request.MediaID)
	if request.PoolID == "" || request.Callee == "" || request.MediaID == "" {
		writeDispatchError(c, http.StatusBadRequest, "invalid_request", "号码池、目标号码和媒体会话不能为空")
		return
	}

	admin := principal.Role == identity.RoleAdmin
	poolView, err := s.resourceCatalog.GetNumberPool(c.Request.Context(), request.PoolID, principal.UserID, admin)
	if err != nil {
		if errors.Is(err, resourcecatalog.ErrNotFound) || errors.Is(err, gorm.ErrRecordNotFound) {
			writeDispatchError(c, http.StatusForbidden, "forbidden_pool", "号码池不存在或当前用户无权使用")
			return
		}
		writeDispatchError(c, http.StatusInternalServerError, "dispatch_unavailable", "读取号码池失败")
		return
	}

	candidates, err := s.dispatchCandidates(c.Request.Context(), poolView)
	if err != nil {
		writeDispatchError(c, http.StatusInternalServerError, "dispatch_unavailable", "读取线路状态失败")
		return
	}
	if len(candidates) == 0 {
		writeDispatchError(c, http.StatusConflict, "no_resource", "号码池当前没有可用线路")
		return
	}

	var lastAttempt dispatchAttemptError
	for attempt := 0; attempt < 2 && len(candidates) > 0; attempt++ {
		reservation, reserveErr := s.dispatcher.Reserve(c.Request.Context(), dispatch.ReserveInput{
			UserID: principal.UserID, PoolID: poolView.ID, Callee: request.Callee,
			EnforceUserLimit: principal.Role != identity.RoleAdmin,
		}, candidates)
		if reserveErr != nil {
			writeDispatchReserveError(c, reserveErr)
			return
		}
		if err := s.resourceCatalog.RecordAudit(c.Request.Context(), principal.UserID, "dispatch.allocated", "call_request", reservation.Request.ID, map[string]any{
			"pool_id": reservation.Lease.PoolID, "device_id": reservation.Lease.DeviceID,
			"card_id": reservation.Lease.CardID, "profile_id": reservation.Lease.ProfileID,
			"number_id": reservation.Lease.NumberID,
		}); err != nil {
			logger.Warn("记录线路分配审计失败", "request_id", reservation.Request.ID, "err", err)
		}

		member, found := dispatchMember(poolView, reservation.Lease.NumberID)
		if !found {
			_ = s.dispatcher.Release(c.Request.Context(), reservation.Lease.ID, "no_resource", "selected number is no longer in the pool")
			writeDispatchError(c, http.StatusConflict, "no_resource", "已选线路不再属于该号码池")
			return
		}

		lease, prepareErr := s.prepareDispatchLease(c.Request.Context(), reservation.Lease, member)
		if prepareErr != nil {
			if !errors.As(prepareErr, &lastAttempt) {
				lastAttempt = dispatchAttemptError{code: "dispatch_unavailable", err: prepareErr}
			}
			_ = s.dispatcher.Release(c.Request.Context(), reservation.Lease.ID, lastAttempt.code, lastAttempt.err.Error())
			candidates = excludeDispatchCard(candidates, reservation.Lease.CardID)
			continue
		}

		call, dialErr := s.phone.StartCallScoped(phone.AccessScope{UserID: principal.UserID, Administrator: admin}, phone.StartCallRequest{
			Owner: principal.UserID, DeviceID: lease.DeviceID, Callee: request.Callee,
			MediaID: request.MediaID, Lease: phoneLease(c), PoolID: lease.PoolID,
			CardID: lease.CardID, ProfileID: lease.ProfileID, NumberID: lease.NumberID,
			SourceNumber: member.Number.E164, RequestID: lease.RequestID, ResourceLeaseID: lease.ID,
		})
		if dialErr != nil && strings.TrimSpace(call.CallID) == "" {
			_ = s.dispatcher.Release(c.Request.Context(), lease.ID, "dial_failed", dialErr.Error())
			writeDispatchError(c, http.StatusBadGateway, "dial_failed", dialErr.Error())
			return
		}
		if err := s.dispatcher.BindCall(c.Request.Context(), lease.ID, call.CallID); err != nil {
			_ = s.phone.Hangup(c.Request.Context(), principal.UserID, call.CallID, phoneLease(c))
			_ = s.dispatcher.Release(c.Request.Context(), lease.ID, "dispatch_bind_failed", err.Error())
			writeDispatchError(c, http.StatusInternalServerError, "dispatch_unavailable", "通话已建立但线路绑定失败")
			return
		}
		if updated, err := s.dispatcher.Transition(c.Request.Context(), lease.ID, lease.Version, dispatch.LeaseDialing); err == nil {
			lease = updated
		}
		response := gin.H{"status": "ok", "request": reservation.Request, "lease": lease, "call": call}
		if dialErr != nil {
			response["warning"] = dialErr.Error()
		}
		c.JSON(http.StatusAccepted, response)
		return
	}

	if lastAttempt.err != nil {
		writeDispatchError(c, http.StatusBadGateway, lastAttempt.code, lastAttempt.err.Error())
		return
	}
	writeDispatchError(c, http.StatusConflict, "no_resource", "号码池当前没有可用线路")
}

func (s *Server) dispatchCandidates(ctx context.Context, pool resourcecatalog.NumberPoolView) ([]dispatch.Candidate, error) {
	active, err := s.dispatcher.ActiveLeases(ctx)
	if err != nil {
		return nil, err
	}
	busyDevices := make(map[string]bool, len(active))
	for _, lease := range active {
		busyDevices[lease.DeviceID] = true
	}
	if s.phone != nil {
		for _, call := range s.phone.Active("") {
			if strings.TrimSpace(call.DeviceID) != "" {
				busyDevices[call.DeviceID] = true
			}
		}
	}
	usage, err := s.dispatcher.DailyUsageCounts(ctx, "")
	if err != nil {
		return nil, err
	}
	lastUsed, err := s.dispatcher.LastUsedTimes(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]dispatch.Candidate, 0, len(pool.Members))
	for _, member := range pool.Members {
		if member.Profile == nil || member.Card == nil {
			continue
		}
		deviceID := strings.TrimSpace(member.Card.LastDeviceID)
		worker := s.pool.GetWorker(deviceID)
		current := worker != nil && sameICCID(worker.CurrentICCID(), member.Profile.ICCID)
		state, hasState := s.pool.GetVoWiFiRuntimeState(deviceID)
		voiceReady := state.IMSReady
		if s.pool.IsNativeVoLTE(deviceID) || s.pool.IsModemVoice(deviceID) {
			voiceReady = true
		}
		status := strings.ToLower(strings.TrimSpace(member.Number.Status))
		enabled := pool.Enabled && member.Enabled && status != "disabled" && status != "suspended"
		if !current && member.Card.Type != resourcecatalog.CardContainerTypeEUICC {
			enabled = false
		}
		busy := busyDevices[deviceID]
		if !busy && s.voiceGW != nil && s.voiceGW.ActiveCall(deviceID) != nil {
			busy = true
		}
		result = append(result, dispatch.Candidate{
			PoolID: pool.ID, DeviceID: deviceID, CardID: member.Card.ID,
			ProfileID: member.Profile.ID, NumberID: member.Number.ID, Priority: member.Priority,
			Online: worker != nil && worker.GetCachedHealthy(), Enabled: enabled,
			Faulted: hasState && !voiceReady && strings.TrimSpace(state.LastError) != "" && (state.IMSState == "failed" || state.SessionState == "error"),
			Busy:    busy, Switching: s.pool.IsESIMSwitching(deviceID),
			Registering:   hasState && !voiceReady && (state.IMSState == "registering" || state.Phase == "registering"),
			AlreadyActive: current, IMSReady: voiceReady, DailyUseCount: usage[member.Card.ID],
			LastUsedAt: lastUsed[member.Number.ID],
		})
	}
	return result, nil
}

func (s *Server) prepareDispatchLease(parent context.Context, lease dispatch.ResourceLease, member resourcecatalog.NumberPoolMemberView) (preparedLease dispatch.ResourceLease, prepareErr error) {
	ctx, cancel := context.WithTimeout(parent, dispatchPreparationTimeout)
	defer cancel()
	preparedLease = lease
	worker := s.pool.GetWorker(lease.DeviceID)
	if worker == nil || member.Profile == nil {
		return lease, dispatchAttemptError{code: "no_resource", err: errors.New("线路设备已离线")}
	}
	current := sameICCID(worker.CurrentICCID(), member.Profile.ICCID)
	originalICCID := ""
	switched := false
	defer func() {
		if !switched || prepareErr == nil {
			return
		}
		if rollbackErr := s.rollbackDispatchProfile(lease.DeviceID, originalICCID); rollbackErr != nil {
			prepareErr = fmt.Errorf("%w; restore original profile failed: %v", prepareErr, rollbackErr)
		}
	}()
	var err error
	if !current {
		originalICCID = strings.TrimSpace(worker.CurrentICCID())
		lease, err = s.dispatcher.Transition(ctx, lease.ID, lease.Version, dispatch.LeaseSwitching)
		if err != nil {
			return lease, dispatchAttemptError{code: "profile_switch_failed", err: err}
		}
		if err = s.switchDispatchProfileAndWait(ctx, lease.DeviceID, member.Profile.ICCID, member.Profile.AID); err != nil {
			rollbackErr := s.rollbackDispatchProfile(lease.DeviceID, originalICCID)
			if rollbackErr != nil {
				err = fmt.Errorf("%w; restore original profile failed: %v", err, rollbackErr)
			}
			return lease, dispatchAttemptError{code: "profile_switch_failed", err: err}
		}
		switched = true
		if auditErr := s.resourceCatalog.RecordAudit(ctx, lease.UserID, "profile.switched", "card", lease.CardID, map[string]any{
			"device_id": lease.DeviceID, "profile_id": lease.ProfileID, "request_id": lease.RequestID,
		}); auditErr != nil {
			logger.Warn("记录 Profile 切换审计失败", "request_id", lease.RequestID, "err", auditErr)
		}
		lease, err = s.dispatcher.Transition(ctx, lease.ID, lease.Version, dispatch.LeaseIdentityRefresh)
		if err != nil {
			return lease, dispatchAttemptError{code: "profile_switch_failed", err: err}
		}
	}
	lease, err = s.dispatcher.Transition(ctx, lease.ID, lease.Version, dispatch.LeaseRegistering)
	if err != nil {
		return lease, dispatchAttemptError{code: "ims_not_ready", err: err}
	}
	if err = s.ensureCallRegistrationReady(ctx, lease.DeviceID); err != nil {
		return lease, dispatchAttemptError{code: "ims_not_ready", err: err}
	}
	lease, err = s.dispatcher.Transition(ctx, lease.ID, lease.Version, dispatch.LeaseReady)
	if err != nil {
		return lease, dispatchAttemptError{code: "ims_not_ready", err: err}
	}
	return lease, nil
}

func (s *Server) switchDispatchProfileAndWait(ctx context.Context, deviceID, iccid, aid string) error {
	if s != nil && s.dispatchProfileSwitch != nil {
		return s.dispatchProfileSwitch(ctx, deviceID, iccid, aid)
	}
	return s.pool.SwitchESIMProfileAndWait(ctx, deviceID, iccid, aid)
}

func (s *Server) rollbackDispatchProfile(deviceID, iccid string) error {
	if s != nil && s.dispatchProfileRestore != nil {
		return s.dispatchProfileRestore(deviceID, iccid)
	}
	return s.restoreDispatchProfile(deviceID, iccid)
}

func (s *Server) ensureCallRegistrationReady(ctx context.Context, deviceID string) error {
	if s != nil && s.callRegistrationReady != nil {
		return s.callRegistrationReady(ctx, deviceID)
	}
	if s == nil || s.pool == nil {
		return errors.New("device pool is unavailable")
	}
	if err := s.pool.PrepareCellularCall(ctx, deviceID); err != nil {
		return err
	}
	if s.pool.IsNativeVoLTE(deviceID) || s.pool.IsModemVoice(deviceID) {
		return nil
	}
	if state, ok := s.pool.GetVoWiFiRuntimeState(deviceID); ok && state.IMSReady {
		return nil
	}
	if !s.pool.IsVoWiFiActive(deviceID) {
		if err := s.pool.EnableVoWiFi(deviceID); err != nil {
			return err
		}
	}
	return s.pool.WaitVoWiFiIMSReady(ctx, deviceID)
}

func (s *Server) restoreDispatchProfile(deviceID, iccid string) error {
	iccid = strings.TrimSpace(iccid)
	if iccid == "" || s.pool == nil {
		return errors.New("original profile identity is unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := s.pool.SwitchESIMProfileAndWait(ctx, deviceID, iccid, ""); err != nil {
		return err
	}
	if s.pool.IsNativeVoLTE(deviceID) || s.pool.IsModemVoice(deviceID) {
		return nil
	}
	if !s.pool.IsVoWiFiActive(deviceID) {
		if err := s.pool.EnableVoWiFi(deviceID); err != nil {
			return err
		}
	}
	if err := s.pool.WaitVoWiFiIMSReady(ctx, deviceID); err != nil {
		return err
	}
	logger.Info("调度切卡失败后已恢复原 Profile", "device_id", deviceID)
	return nil
}

func dispatchMember(pool resourcecatalog.NumberPoolView, numberID string) (resourcecatalog.NumberPoolMemberView, bool) {
	for _, member := range pool.Members {
		if member.Number.ID == numberID {
			return member, true
		}
	}
	return resourcecatalog.NumberPoolMemberView{}, false
}

func excludeDispatchCard(candidates []dispatch.Candidate, cardID string) []dispatch.Candidate {
	result := candidates[:0]
	for _, candidate := range candidates {
		if candidate.CardID != cardID {
			result = append(result, candidate)
		}
	}
	return result
}

func sameICCID(left, right string) bool {
	normalize := func(value string) string {
		value = strings.TrimSpace(value)
		return strings.TrimRight(value, "Ff")
	}
	return normalize(left) != "" && normalize(left) == normalize(right)
}

func writeDispatchReserveError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, dispatch.ErrActiveUserLease):
		writeDispatchError(c, http.StatusConflict, "user_busy", "当前账户已有进行中的通话")
	case errors.Is(err, dispatch.ErrNoResource):
		writeDispatchError(c, http.StatusConflict, "no_resource", "号码池当前没有可用线路")
	default:
		writeDispatchError(c, http.StatusInternalServerError, "dispatch_unavailable", "线路分配失败")
	}
}

func writeDispatchError(c *gin.Context, status int, code, message string) {
	c.JSON(status, gin.H{"status": "error", "code": code, "message": message, "request_id": requestID(c)})
}
