package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/yibaiba/hideck/internal/db"
	"github.com/yibaiba/hideck/internal/device"
	"github.com/yibaiba/hideck/internal/identity"
	"github.com/yibaiba/hideck/pkg/smscodec"

	"github.com/yibaiba/hideck/pkg/logger"

	"github.com/gin-gonic/gin"
)

func (s *Server) useSendSMS(c businessContext) {
	releaseOperation, allowed := s.acquireBusinessMutation(c)
	if !allowed {
		return
	}
	defer releaseOperation()
	access, authorized := s.smsAccess(c)
	if !authorized {
		return
	}

	type SendSMSRequest struct {
		PoolID   string `json:"pool_id"`
		NumberID string `json:"number_id"`
		DeviceID string `json:"device_id"`
		IMSI     string `json:"imsi"`
		ICCID    string `json:"iccid"`
		Phone    string `json:"phone" binding:"required"`
		Message  string `json:"message" binding:"required"`
		Encoding string `json:"encoding"`
	}

	var req SendSMSRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": "参数错误: " + err.Error()})
		return
	}

	encoding, err := smscodec.NormalizeSMSEncoding(req.Encoding)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": "短信编码参数错误: " + err.Error()})
		return
	}
	var selection smsSendSelection
	member := access.rest && access.principal.Role != identity.RoleAdmin
	legacySelector := strings.TrimSpace(req.DeviceID) != "" || strings.TrimSpace(req.IMSI) != "" || strings.TrimSpace(req.ICCID) != ""
	if member && legacySelector {
		c.JSON(http.StatusForbidden, gin.H{"status": "error", "code": "sms_device_forbidden", "message": "普通用户不能直接指定 device_id、imsi 或 iccid；请使用已授权的 pool_id"})
		return
	}
	if strings.TrimSpace(req.PoolID) != "" || member {
		if legacySelector {
			c.JSON(http.StatusBadRequest, gin.H{"status": "error", "code": "sms_selector_conflict", "message": "pool_id 不能与 device_id、imsi 或 iccid 同时使用"})
			return
		}
		var resolveErr *smsSendResolveError
		selection, resolveErr = s.resolveAuthorizedSMSSendWorker(businessRequestContext(c), access, req.PoolID, req.NumberID)
		if resolveErr != nil {
			c.JSON(resolveErr.status, gin.H{"status": "error", "code": resolveErr.code, "message": resolveErr.message})
			return
		}
	} else {
		worker, status, msg := s.resolveSMSSendWorker(req.DeviceID, req.IMSI, req.ICCID)
		if status != 0 {
			c.JSON(status, gin.H{"status": "error", "message": msg})
			return
		}
		selection.worker = worker
	}
	worker := selection.worker
	deviceID := worker.ID
	sendOpts := smscodec.SubmitOptions{Encoding: encoding}
	leaseID, reserveErr := s.reserveSMSSend(businessRequestContext(c), access, selection)
	if reserveErr != nil {
		c.JSON(reserveErr.status, gin.H{"status": "error", "code": reserveErr.code, "message": reserveErr.message})
		return
	}
	var sendErr error
	defer func() { s.releaseSMSSendLease(leaseID, sendErr) }()

	messageID := ""
	partsTotal := 1
	deliveryState := "acked"

	routed, err := s.pool.SendRoutedSMS(businessRequestContext(c), worker, req.Phone, req.Message, sendOpts)
	sendErr = err
	if respondOutboundLimit(c, err) {
		return
	}
	if routed.Outcome.PartsTotal > 0 {
		partsTotal = routed.Outcome.PartsTotal
	}
	if strings.TrimSpace(routed.Outcome.DeliveryState) != "" {
		deliveryState = strings.TrimSpace(routed.Outcome.DeliveryState)
	}
	messageID = strings.TrimSpace(routed.Outcome.MessageID)
	if err != nil {
		if routed.Via == device.RoutedSMSViaVoWiFi || routed.FellBackToCS {
			deliveryState = "failed"
			recordErr := device.RecordVoWiFiSMSSendFailure(s.pool, device.VoWiFiSMSSendFailure{
				DeviceID: deviceID, Target: req.Phone, Content: req.Message,
				Identity: routed.Identity, IdentityRead: routed.IdentityRead, At: time.Now(),
			})
			if recordErr != nil {
				logger.Warn("VoWiFi 短信发送失败记录入库失败", "device", deviceID, "err", recordErr)
			}
			c.JSON(http.StatusInternalServerError, gin.H{
				"status":         "error",
				"message":        "VoWiFi 短信发送失败: " + err.Error(),
				"device":         deviceID,
				"phone":          req.Phone,
				"destination":    routed.Destination,
				"message_id":     messageID,
				"parts_total":    partsTotal,
				"delivery_state": deliveryState,
			})
			return
		}
		if persistErr := saveRoutedSMSRecord(routedSMSRecord{
			Identity: routed.Identity, Sender: worker.ID, Recipient: req.Phone,
			Content: req.Message, Status: 3,
		}); persistErr != nil {
			logger.Warn("短信发送失败记录入库失败", "device", deviceID, "err", persistErr)
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"status":      "error",
			"message":     "发送失败: " + err.Error(),
			"device":      deviceID,
			"phone":       req.Phone,
			"destination": routed.Destination,
		})
		return
	}
	if routed.Via == device.RoutedSMSViaCS {
		if routed.FellBackToCS {
			deliveryState = "acked"
		}
		if persistErr := saveRoutedSMSRecord(routedSMSRecord{
			Identity: routed.Identity, Sender: worker.ID, Recipient: req.Phone,
			Content: req.Message, Status: 2,
		}); persistErr != nil {
			logger.Warn("短信发送成功但入库失败", "device", deviceID, "err", persistErr)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"status":         "ok",
		"message":        "短信已提交",
		"device":         deviceID,
		"phone":          req.Phone,
		"destination":    routed.Destination,
		"message_id":     messageID,
		"parts_total":    partsTotal,
		"delivery_state": deliveryState,
		"pool_id":        selection.poolID,
		"number_id":      selection.numberID,
	})
}

func (s *Server) useSMSDelivery(c businessContext) {
	access, ok := s.smsAccess(c)
	if !ok {
		return
	}
	messageID := strings.TrimSpace(c.Param("message_id"))
	if messageID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": "message_id 不能为空"})
		return
	}
	if s.pool == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "error", "message": "服务未就绪"})
		return
	}
	var stored *db.SMSDeliveryStatus
	if !access.scope.Unrestricted() {
		var storedErr error
		stored, storedErr = db.GetSMSDeliveryStatusScoped(access.scope, messageID)
		if storedErr != nil {
			c.JSON(http.StatusNotFound, gin.H{"status": "error", "message": "未找到对应短信投递记录"})
			return
		}
	}
	services := s.pool.GetAllVoWiFiApps()
	for _, svc := range services {
		if svc == nil {
			continue
		}
		status, err := svc.GetSMSDeliveryStatusContext(businessRequestContext(c), messageID)
		if err != nil {
			continue
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok", "delivery": status})
		return
	}
	if stored == nil {
		stored, _ = db.GetSMSDeliveryStatus(messageID)
	}
	if stored == nil {
		c.JSON(http.StatusNotFound, gin.H{"status": "error", "message": "未找到对应短信投递记录"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok", "delivery": stored})
}
