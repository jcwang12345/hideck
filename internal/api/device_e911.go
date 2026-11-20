package api

import (
	"context"
	"errors"
	"net"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/yibaiba/hideck/internal/e911"
)

func e911ErrorStatus(err error) int {
	switch {
	case errors.Is(err, e911.ErrNotSupported), errors.Is(err, e911.ErrProviderUnavailable):
		return http.StatusBadRequest
	case errors.Is(err, e911.ErrIdentityUnavailable):
		return http.StatusConflict
	case errors.Is(err, e911.ErrChallengeIncomplete):
		return http.StatusNotImplemented
	case errors.Is(err, e911.ErrCarrierWebsheetAbsent):
		return http.StatusBadGateway
	case e911ErrorIsTimeout(err):
		return http.StatusGatewayTimeout
	default:
		return http.StatusInternalServerError
	}
}

func e911ErrorCode(err error) string {
	switch {
	case errors.Is(err, e911.ErrNotSupported):
		return "e911_not_supported"
	case errors.Is(err, e911.ErrIdentityUnavailable):
		return "e911_identity_unavailable"
	case errors.Is(err, e911.ErrProviderUnavailable):
		return "e911_provider_unavailable"
	case errors.Is(err, e911.ErrChallengeIncomplete):
		return "e911_challenge_not_implemented"
	case errors.Is(err, e911.ErrCarrierWebsheetAbsent):
		return "e911_websheet_unavailable"
	case e911ErrorIsTimeout(err):
		return "e911_network_timeout"
	default:
		return "e911_start_failed"
	}
}

func e911ErrorMessage(err error) string {
	switch {
	case errors.Is(err, e911.ErrNotSupported):
		return "当前 SIM 或设备不支持 E911 地址设置"
	case errors.Is(err, e911.ErrIdentityUnavailable):
		return "SIM 或设备身份信息不完整，请稍后重试"
	case errors.Is(err, e911.ErrProviderUnavailable):
		return "当前运营商暂不支持 E911 地址设置"
	case errors.Is(err, e911.ErrChallengeIncomplete):
		return "E911 认证尚未完成，请检查 SIM 卡认证能力"
	case errors.Is(err, e911.ErrCarrierWebsheetAbsent):
		return "运营商未返回 E911 地址设置页面"
	case e911ErrorIsTimeout(err):
		return "连接运营商 E911 服务超时，请检查前置代理或网络连接后重试"
	default:
		return "E911 地址设置启动失败，请稍后重试"
	}
}

func e911ErrorIsTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var networkError net.Error
	return errors.As(err, &networkError) && networkError.Timeout()
}

func (s *Server) handleDeviceE911Websheet(c *gin.Context) {
	coord := &e911.Coordinator{
		Pool:      s.pool,
		Websheets: s.websheets,
	}
	info, err := coord.StartWebsheet(c.Request.Context(), deviceIDParam(c))
	if err != nil {
		c.JSON(e911ErrorStatus(err), gin.H{"status": "error", "code": e911ErrorCode(err), "message": e911ErrorMessage(err)})
		return
	}
	c.JSON(http.StatusCreated, info)
}
