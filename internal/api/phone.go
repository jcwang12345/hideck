package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nyaruka/phonenumbers/v2"
	"github.com/yibaiba/hideck/internal/device"
	"github.com/yibaiba/hideck/internal/identity"
	"github.com/yibaiba/hideck/internal/phone"
	"github.com/yibaiba/hideck/pkg/logger"
)

const maxPhoneRequestBytes = 1 << 20

type phoneMediaRequest struct {
	SDP string `json:"sdp"`
}

type phoneCallRequest struct {
	DeviceID string `json:"device_id"`
	Callee   string `json:"callee"`
	MediaID  string `json:"media_id"`
}

type phoneControlRequest struct {
	MediaID string `json:"media_id"`
}

type phoneDTMFRequest struct {
	Digit string `json:"digit"`
}

type phoneRefreshRequest struct {
	MediaID  string `json:"media_id"`
	Takeover bool   `json:"takeover"`
}

type phoneHistoryDeleteRequest struct {
	IDs []uint64 `json:"ids"`
}

func (s *Server) registerPhoneRoutes(api *gin.RouterGroup) {
	api.GET("/phone/devices", s.handlePhoneDevices)
	api.POST("/phone/media", s.handlePhoneMedia)
	api.DELETE("/phone/media/:media_id", s.handlePhoneMediaCancel)
	api.POST("/phone/calls", s.handlePhoneStartCall)
	api.GET("/phone/calls/active", s.handlePhoneActiveCalls)
	api.POST("/phone/calls/:call_id/answer", s.handlePhoneAnswer)
	api.POST("/phone/calls/:call_id/reject", s.handlePhoneReject)
	api.POST("/phone/calls/:call_id/dtmf", s.handlePhoneDTMF)
	api.POST("/phone/calls/:call_id/hold", s.handlePhoneHold)
	api.POST("/phone/calls/:call_id/resume", s.handlePhoneResume)
	api.POST("/phone/calls/:call_id/switch", s.handlePhoneSwitch)
	api.DELETE("/phone/calls/:call_id", s.handlePhoneHangup)
	api.PUT("/phone/calls/:call_id/media", s.handlePhoneRefreshMedia)
	api.GET("/phone/events", s.handleScopedPhoneEvents)
	api.GET("/phone/history", s.handlePhoneHistory)
	api.POST("/phone/history/delete", s.handlePhoneHistoryDelete)
	api.GET("/phone/recordings/:recording", s.handlePhoneRecording)
	s.registerPhoneContactRoutes(api)
}

func (s *Server) handlePhoneDevices(c *gin.Context) {
	s.usePhoneDevices(c)
}

func (s *Server) usePhoneDevices(c businessContext) {
	if !s.requirePhone(c) {
		return
	}
	if scope, rest, ok := s.phoneAccess(c); !ok {
		return
	} else if rest && !scope.Administrator {
		abortAuthorization(c.(*gin.Context), http.StatusForbidden, "phone_device_access_forbidden", "普通用户不能直接选择电话设备")
		return
	}
	devices := make([]gin.H, 0)
	if s.pool != nil {
		for _, worker := range s.pool.GetAllWorkers() {
			class, err := device.ClassifyWorkerLebaraUK(worker)
			if err != nil {
				logger.Warn("电话设备列表识别 Lebara UK 射频策略失败", "device", worker.ID, "err", err)
			}
			voice := map[string]interface{}{}
			if s.pool.IsModemVoice(worker.ID) {
				voice = s.pool.ModemVoiceController().DeviceStatus(worker.ID)
			} else if s.pool.IsNativeVoLTE(worker.ID) {
				if ctl := s.pool.NativeVoLTEController(); ctl != nil {
					for key, value := range ctl.DeviceStatus(worker.ID) {
						voice[key] = value
					}
				}
			} else if s.voiceGW != nil {
				voice = s.voiceGW.DeviceStatus(worker.ID)
				for key, value := range s.voiceGW.DeviceStatusCurrent(worker.ID) {
					voice[key] = value
				}
			}
			phoneMode := worker.Config.PhoneMode
			if phoneMode == "" {
				phoneMode = "wifi"
			}
			item := gin.H{
				"id": worker.ID, "name": worker.Config.Name, "iccid": worker.CurrentICCID(),
				"voice":                voice,
				"phone_mode":           phoneMode,
				"data_strategy":        worker.Config.DataStrategy,
				"network_enabled":      worker.Config.NetworkEnabled,
				"vowifi_enabled":       worker.Config.VoWiFiEnabled,
				"vowifi_active":        s.pool.IsVoWiFiActive(worker.ID),
				"native_volte":         s.pool.NativeVoLTEStatus(worker.ID),
				"software_ims_blocked": device.WorkerSoftwareIMSBlocked(worker),
				"rf_lock":              class.RFLock(),
			}
			if region := s.phoneNumberRegion(worker.ID); region != "" {
				item["phone_region"] = region
				if code := phonenumbers.GetCountryCodeForRegion(region); code != 0 {
					item["phone_country_code"] = code
				}
			}
			if recoverSnap := s.pool.LebaraUKIdentityRecoverSnapshot(worker.ID); recoverSnap.Status != "" {
				item["lebara_identity_status"] = recoverSnap.Status
				if recoverSnap.Message != "" {
					item["lebara_identity_message"] = recoverSnap.Message
				}
				if recoverSnap.ICCID != "" {
					item["lebara_identity_iccid"] = recoverSnap.ICCID
				}
			}
			devices = append(devices, item)
		}
	}
	c.JSON(http.StatusOK, gin.H{"devices": devices})
}

func (s *Server) handlePhoneMedia(c *gin.Context) {
	if !s.requirePhone(c) {
		return
	}
	scope, _, ok := s.phoneAccess(c)
	if !ok {
		return
	}
	var request phoneMediaRequest
	if !decodePhoneJSON(c, &request) {
		return
	}
	answer, err := s.phone.CreateMediaScoped(c.Request.Context(), scope, request.SDP, c.Request.Host)
	if err != nil {
		s.respondPhoneError(c, err)
		return
	}
	c.JSON(http.StatusCreated, answer)
}

func (s *Server) handlePhoneMediaCancel(c *gin.Context) {
	if !s.requirePhone(c) {
		return
	}
	scope, _, ok := s.phoneAccess(c)
	if !ok {
		return
	}
	if err := s.phone.CancelMediaScoped(scope, c.Param("media_id"), phoneLease(c)); err != nil {
		s.respondPhoneError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) handlePhoneStartCall(c *gin.Context) {
	s.usePhoneStartCall(c)
}

func (s *Server) usePhoneStartCall(c businessContext) {
	scope, rest, ok := s.phoneAccess(c)
	if !ok {
		return
	}
	if rest && !scope.Administrator {
		abortAuthorization(c.(*gin.Context), http.StatusForbidden, "phone_legacy_dial_forbidden", "普通用户请通过号码调度接口发起通话")
		return
	}
	releaseOperation, allowed := s.acquireBusinessMutation(c)
	if !allowed {
		return
	}
	defer releaseOperation()

	if !s.requirePhone(c) {
		return
	}
	var request phoneCallRequest
	if !decodePhoneJSON(c, &request) {
		return
	}
	request.DeviceID = strings.TrimSpace(request.DeviceID)
	request.Callee = strings.TrimSpace(request.Callee)
	request.MediaID = strings.TrimSpace(request.MediaID)
	if request.DeviceID == "" || request.Callee == "" || request.MediaID == "" {
		respondManagedOutboundError(c, dispatchAttemptError{code: "invalid_request", err: errors.New("设备、目标号码和媒体会话不能为空")})
		return
	}
	iccid := ""
	if s.pool != nil {
		if worker := s.pool.GetWorker(request.DeviceID); worker != nil {
			iccid = worker.CurrentICCID()
		}
	}
	managed, err := s.reserveDirectOutbound(businessRequestContext(c), scope.UserID, request.DeviceID, iccid, "", request.Callee)
	if err != nil {
		respondManagedOutboundError(c, err)
		return
	}
	call, err := s.startManagedPhoneCall(businessRequestContext(c), managed, scope, request.Callee, request.MediaID, phoneLease(c))
	if err != nil && strings.TrimSpace(call.CallID) == "" {
		respondManagedOutboundError(c, dispatchAttemptError{code: "dial_failed", err: err})
		return
	}
	response := gin.H{"status": "ok", "request": managed.Reservation.Request, "lease": managed.Reservation.Lease, "call": call}
	if err != nil {
		response["warning"] = err.Error()
	}
	c.JSON(http.StatusAccepted, response)
}

func (s *Server) handlePhoneActiveCalls(c *gin.Context) {
	s.usePhoneActiveCalls(c)
}

func (s *Server) usePhoneActiveCalls(c businessContext) {
	if !s.requirePhone(c) {
		return
	}
	scope, _, ok := s.phoneAccess(c)
	if !ok {
		return
	}
	calls, err := s.phone.ActiveScoped(scope, phoneLease(c))
	if err != nil {
		s.respondPhoneError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"calls": calls})
}

func (s *Server) handlePhoneAnswer(c *gin.Context) {
	s.usePhoneAnswer(c)
}

func (s *Server) usePhoneAnswer(c businessContext) {
	scope, _, ok := s.phoneAccess(c)
	if !ok {
		return
	}
	releaseOperation, allowed := s.acquireBusinessMutation(c)
	if !allowed {
		return
	}
	defer releaseOperation()

	if !s.requirePhone(c) {
		return
	}
	request, ok := s.decodePhoneControl(c, scope)
	if !ok {
		return
	}
	call, err := s.phone.AnswerScoped(businessRequestContext(c), scope, request)
	if err != nil {
		s.respondPhoneError(c, err)
		return
	}
	c.JSON(http.StatusOK, call)
}

func (s *Server) handlePhoneReject(c *gin.Context) {
	s.usePhoneReject(c)
}

func (s *Server) usePhoneReject(c businessContext) {
	scope, _, ok := s.phoneAccess(c)
	if !ok {
		return
	}
	releaseOperation, allowed := s.acquireBusinessMutation(c)
	if !allowed {
		return
	}
	defer releaseOperation()

	if !s.requirePhone(c) {
		return
	}
	request, ok := s.decodePhoneControl(c, scope)
	if !ok {
		return
	}
	if err := s.phone.RejectScoped(scope, request); err != nil {
		s.respondPhoneError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) handlePhoneDTMF(c *gin.Context) {
	s.usePhoneDTMF(c)
}

func (s *Server) usePhoneDTMF(c businessContext) {
	scope, _, ok := s.phoneAccess(c)
	if !ok {
		return
	}
	releaseOperation, allowed := s.acquireBusinessMutation(c)
	if !allowed {
		return
	}
	defer releaseOperation()

	if !s.requirePhone(c) {
		return
	}
	var request phoneDTMFRequest
	if !decodePhoneJSON(c, &request) {
		return
	}
	err := s.phone.DTMFScoped(scope, c.Param("call_id"), phoneLease(c), request.Digit)
	if err != nil {
		s.respondPhoneError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) handlePhoneHold(c *gin.Context) {
	s.usePhoneHold(c)
}

func (s *Server) usePhoneHold(c businessContext) {
	scope, _, ok := s.phoneAccess(c)
	if !ok {
		return
	}
	releaseOperation, allowed := s.acquireBusinessMutation(c)
	if !allowed {
		return
	}
	defer releaseOperation()

	if !s.requirePhone(c) {
		return
	}
	err := s.phone.HoldScoped(businessRequestContext(c), scope, c.Param("call_id"), phoneLease(c))
	if err != nil {
		s.respondPhoneError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) handlePhoneResume(c *gin.Context) {
	s.usePhoneResume(c)
}

func (s *Server) usePhoneResume(c businessContext) {
	scope, _, ok := s.phoneAccess(c)
	if !ok {
		return
	}
	releaseOperation, allowed := s.acquireBusinessMutation(c)
	if !allowed {
		return
	}
	defer releaseOperation()

	if !s.requirePhone(c) {
		return
	}
	err := s.phone.ResumeScoped(businessRequestContext(c), scope, c.Param("call_id"), phoneLease(c))
	if err != nil {
		s.respondPhoneError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) handlePhoneSwitch(c *gin.Context) {
	s.usePhoneSwitch(c)
}

func (s *Server) usePhoneSwitch(c businessContext) {
	scope, _, ok := s.phoneAccess(c)
	if !ok {
		return
	}
	releaseOperation, allowed := s.acquireBusinessMutation(c)
	if !allowed {
		return
	}
	defer releaseOperation()

	if !s.requirePhone(c) {
		return
	}
	err := s.phone.SwitchScoped(scope, c.Param("call_id"), phoneLease(c))
	if err != nil {
		s.respondPhoneError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) handlePhoneHangup(c *gin.Context) {
	s.usePhoneHangup(c)
}

func (s *Server) usePhoneHangup(c businessContext) {
	scope, _, ok := s.phoneAccess(c)
	if !ok {
		return
	}
	releaseOperation, allowed := s.acquireBusinessMutation(c)
	if !allowed {
		return
	}
	defer releaseOperation()

	if !s.requirePhone(c) {
		return
	}
	err := s.phone.HangupScoped(businessRequestContext(c), scope, c.Param("call_id"), phoneLease(c))
	if err != nil {
		s.respondPhoneError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) handlePhoneRefreshMedia(c *gin.Context) {
	if !s.requirePhone(c) {
		return
	}
	scope, _, ok := s.phoneAccess(c)
	if !ok {
		return
	}
	var request phoneRefreshRequest
	if !decodePhoneJSON(c, &request) {
		return
	}
	call, lease, err := s.phone.RefreshMediaScoped(scope, phone.RefreshRequest{
		CallID: c.Param("call_id"), MediaID: request.MediaID,
		Lease: phoneLease(c), Takeover: request.Takeover,
	})
	if err != nil {
		s.respondPhoneError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"call": call, "lease": lease})
}

func (s *Server) handlePhoneHistory(c *gin.Context) {
	s.usePhoneHistory(c)
}

func (s *Server) usePhoneHistory(c businessContext) {
	if !s.requirePhone(c) {
		return
	}
	scope, _, ok := s.phoneAccess(c)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(c.Query("limit"))
	records, err := s.phone.HistoryScoped(businessRequestContext(c), scope, limit)
	if err != nil {
		s.respondPhoneError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"records": records})
}

func (s *Server) handlePhoneHistoryDelete(c *gin.Context) {
	s.usePhoneHistoryDelete(c)
}

func (s *Server) usePhoneHistoryDelete(c businessContext) {
	scope, _, ok := s.phoneAccess(c)
	if !ok {
		return
	}
	releaseOperation, allowed := s.acquireBusinessMutation(c)
	if !allowed {
		return
	}
	defer releaseOperation()

	if !s.requirePhone(c) {
		return
	}
	var request phoneHistoryDeleteRequest
	if !decodePhoneJSON(c, &request) {
		return
	}
	deleted, err := s.phone.DeleteHistoryScoped(businessRequestContext(c), scope, request.IDs)
	if err != nil {
		s.respondPhoneError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": deleted})
}

func (s *Server) handleScopedPhoneEvents(c *gin.Context) {
	if !s.requirePhone(c) {
		return
	}
	scope, _, ok := s.phoneAccess(c)
	if !ok {
		return
	}
	backlog, stream, cancel, err := s.phone.SubscribeScoped(scope, phoneLastEventID(c))
	if err != nil {
		s.respondPhoneError(c, err)
		return
	}
	defer cancel()
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)
	_, _ = fmt.Fprint(c.Writer, ": connected\n\n")
	c.Writer.Flush()
	for _, event := range backlog {
		writePhoneEvent(c, event)
	}
	c.Writer.Flush()
	heartbeat := time.NewTicker(phoneEventHeartbeatInterval)
	defer heartbeat.Stop()
	for {
		select {
		case event, open := <-stream:
			if !open {
				return
			}
			writePhoneEvent(c, event)
			c.Writer.Flush()
		case <-heartbeat.C:
			_, _ = fmt.Fprint(c.Writer, ": heartbeat\n\n")
			c.Writer.Flush()
		case <-c.Request.Context().Done():
			return
		case <-s.shutdownCh:
			return
		}
	}
}

func (s *Server) handlePhoneRecording(c *gin.Context) {
	if !s.requirePhone(c) {
		return
	}
	name := strings.TrimSpace(c.Param("recording"))
	if !validVoiceRecordingName(name) {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "code": "phone_invalid_recording", "message": "录音文件名无效"})
		return
	}
	scope, _, ok := s.phoneAccess(c)
	if !ok {
		return
	}
	if err := s.phone.AuthorizeRecording(c.Request.Context(), scope, name); err != nil {
		s.respondPhoneError(c, err)
		return
	}
	s.handleCommandRecording(c)
}

func (s *Server) phoneAccess(c businessContext) (phone.AccessScope, bool, bool) {
	if rest, ok := c.(*gin.Context); ok {
		principal, found := CurrentPrincipal(rest)
		if !found || strings.TrimSpace(principal.UserID) == "" {
			abortAuthorization(rest, http.StatusUnauthorized, "unauthorized", "未授权")
			return phone.AccessScope{}, true, false
		}
		return phone.AccessScope{
			UserID:        strings.TrimSpace(principal.UserID),
			Administrator: principal.Role == identity.RoleAdmin,
		}, true, true
	}
	userID := strings.TrimSpace(s.authSnapshot().Username)
	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"status": "error", "code": "unauthorized", "message": "未授权"})
		return phone.AccessScope{}, false, false
	}
	// MCP keeps its legacy administrator semantics while REST always uses the
	// authenticated identity principal above.
	return phone.AccessScope{UserID: userID, Administrator: true}, false, true
}

func (s *Server) decodePhoneControl(c businessContext, scope phone.AccessScope) (phone.ControlRequest, bool) {
	var request phoneControlRequest
	if !decodePhoneJSON(c, &request) {
		return phone.ControlRequest{}, false
	}
	return phone.ControlRequest{
		Owner: scope.UserID, CallID: c.Param("call_id"), MediaID: request.MediaID, Lease: phoneLease(c),
	}, true
}

func decodePhoneJSON(c businessContext, target interface{}) bool {
	limitBusinessJSON(c, maxPhoneRequestBytes)
	if err := c.ShouldBindJSON(target); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "code": "phone_invalid_request", "message": err.Error()})
		return false
	}
	return true
}

func (s *Server) requirePhone(c businessContext) bool {
	if s.phone != nil {
		return true
	}
	c.JSON(http.StatusServiceUnavailable, gin.H{"status": "error", "code": "phone_unavailable", "message": "电话服务未启用"})
	return false
}

func (s *Server) respondPhoneError(c businessContext, err error) {
	if respondOutboundLimit(c, err) {
		return
	}
	message := err.Error()
	status := http.StatusBadRequest
	code := "phone_error"
	switch {
	case errors.Is(err, phone.ErrAccessScopeRequired):
		status = http.StatusUnauthorized
		code = "phone_unauthorized"
		message = "未授权"
	case errors.Is(err, phone.ErrAccessDenied):
		status = http.StatusForbidden
		code = "phone_forbidden"
		message = "无权访问该通话"
	case errors.Is(err, phone.ErrRecordingNotFound):
		status = http.StatusNotFound
		code = "phone_recording_not_found"
		message = "通话录音不存在"
	case errors.Is(err, phone.ErrScopedStoreUnavailable):
		status = http.StatusServiceUnavailable
		code = "phone_scope_unavailable"
		message = "电话记录隔离服务不可用"
	case errors.Is(err, phone.ErrActiveCallHistoryDelete):
		status = http.StatusConflict
		code = "phone_history_active"
		message = phone.ErrActiveCallHistoryDelete.Error()
	case errors.Is(err, phone.ErrHoldUnavailable):
		status = http.StatusServiceUnavailable
		code = "phone_hold_unavailable"
		message = phone.ErrHoldUnavailable.Error()
	case strings.Contains(message, "lease"), strings.Contains(message, "another browser"):
		status = http.StatusForbidden
	case strings.Contains(message, "not found"):
		status = http.StatusNotFound
	case strings.Contains(message, "already has"), strings.Contains(message, "busy"):
		status = http.StatusConflict
	case strings.Contains(message, "unavailable"):
		status = http.StatusServiceUnavailable
	}
	c.JSON(status, gin.H{"status": "error", "code": code, "message": message})
}

func phoneLease(c businessContext) string {
	return strings.TrimSpace(c.GetHeader("X-Phone-Lease"))
}
