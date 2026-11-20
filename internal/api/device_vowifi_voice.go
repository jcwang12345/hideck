package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/iniwex5/vowifi-go/runtimehost/voicehost"
	"github.com/yibaiba/hideck/internal/notify"
	"github.com/yibaiba/hideck/pkg/logger"
)

const voWiFiCallSetupGrace = 45 * time.Second

type deviceVoWiFiCallRequest struct {
	Callee      string `json:"callee"`
	HoldSeconds int    `json:"hold_seconds"`
}

func (s *Server) handleDeviceVoWiFiCall(c *gin.Context) {
	s.useDeviceVoWiFiCall(c)
}

func (s *Server) useDeviceVoWiFiCall(c businessContext) {
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

	deviceID := strings.TrimSpace(c.Param("device_id"))
	var req deviceVoWiFiCallRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "code": "invalid_request", "message": err.Error()})
		return
	}
	req.Callee = strings.TrimSpace(req.Callee)
	holdSeconds, errMessage := validateVoWiFiCallRequest(deviceID, req)
	if errMessage != "" {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "code": "invalid_request", "message": errMessage})
		return
	}
	if !s.voiceAgentReady(deviceID) {
		c.JSON(http.StatusConflict, gin.H{"status": "error", "code": "vowifi_voice_not_ready", "message": "VoWiFi 语音未就绪"})
		return
	}

	traceID := requestID(c)
	ctx, cancel := context.WithTimeout(businessRequestContext(c), time.Duration(holdSeconds)*time.Second+voWiFiCallSetupGrace)
	defer cancel()
	logger.Info("VoWiFi 外呼请求开始", "device", deviceID, "callee_suffix", maskedCallee(req.Callee), "hold_seconds", holdSeconds, "trace_id", traceID)
	iccid := ""
	if s.pool != nil {
		if worker := s.pool.GetWorker(deviceID); worker != nil {
			iccid = worker.CurrentICCID()
		}
	}
	managed, err := s.RunManagedSimulatedCall(ctx, notify.ManagedCallRequest{
		ActorID: scope.UserID, DeviceID: deviceID, ICCID: iccid,
		Callee: req.Callee, HoldSeconds: holdSeconds,
		OnConnected: func() { logger.Info("VoWiFi 外呼已接通并启动 RTP", "device", deviceID, "trace_id", traceID) },
	})
	if err != nil {
		if respondOutboundLimit(c, err) {
			return
		}
		logger.Error("VoWiFi 外呼失败", "device", deviceID, "err", err, "trace_id", traceID)
		respondManagedOutboundError(c, err)
		return
	}
	result := managed.Voice
	if result == nil {
		c.JSON(http.StatusBadGateway, gin.H{"status": "error", "code": "vowifi_call_failed", "message": "未返回通话结果", "trace_id": traceID})
		return
	}
	logger.Info(
		"VoWiFi 外呼完成并已发送 BYE",
		"device", deviceID, "duration_ms", result.DurationMs,
		"audio_path", result.AudioPath, "audio_codec", result.AudioCodec,
		"source_audio_path", result.SourceAudioPath, "source_audio_codec", result.SourceAudioCodec,
		"pcap_path", result.PCAPPath, "trace_id", traceID,
	)
	c.JSON(http.StatusOK, gin.H{
		"status": "ok", "success": result.Success, "message": result.Message,
		"call_id": managed.CallID, "request_id": managed.RequestID, "lease_id": managed.LeaseID,
		"duration_ms": result.DurationMs, "trace_id": traceID,
		"pcap_path": result.PCAPPath, "audio_path": result.AudioPath,
		"audio_codec":        result.AudioCodec,
		"source_audio_path":  result.SourceAudioPath,
		"source_audio_codec": result.SourceAudioCodec,
	})
}

func validateVoWiFiCallRequest(deviceID string, req deviceVoWiFiCallRequest) (int, string) {
	if deviceID == "" {
		return 0, "device_id 不能为空"
	}
	if req.Callee == "" {
		return 0, "callee 不能为空"
	}
	if req.HoldSeconds < 0 || req.HoldSeconds > voicehost.MaxSimulateCallHoldSeconds {
		return 0, "hold_seconds 必须在 0 到 60 之间"
	}
	if req.HoldSeconds == 0 {
		return voicehost.DefaultSimulateCallHoldSeconds, ""
	}
	return req.HoldSeconds, ""
}

func (s *Server) voiceAgentReady(deviceID string) bool {
	if s == nil || s.voiceGW == nil || s.voiceGW.GetAgentCurrent(deviceID) == nil {
		return false
	}
	ready, _ := s.voiceGW.DeviceStatusCurrent(deviceID)["ready"].(bool)
	return ready
}

func maskedCallee(callee string) string {
	if len(callee) <= 4 {
		return callee
	}
	return "***" + callee[len(callee)-4:]
}

// simulateCall routes through pool.SimulateCallWithCellularData when available
// (enables on_demand data hook for cellular mode), falling back to direct
// voiceGW.SimulateCall for tests and setups without a pool.
func (s *Server) simulateCall(ctx context.Context, deviceID string, req voicehost.SimulateCallRequest) (*voicehost.SimulateCallResult, error) {
	if s.pool != nil {
		return s.pool.SimulateCallWithCellularData(ctx, deviceID, req)
	}
	return s.voiceGW.SimulateCall(ctx, deviceID, req)
}
