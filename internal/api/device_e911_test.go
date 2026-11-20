package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/yibaiba/hideck/internal/e911"
)

func TestE911ErrorStatus(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{name: "not supported", err: e911.ErrNotSupported, wantStatus: http.StatusBadRequest, wantCode: "e911_not_supported"},
		{name: "provider unavailable", err: e911.ErrProviderUnavailable, wantStatus: http.StatusBadRequest, wantCode: "e911_provider_unavailable"},
		{name: "identity unavailable", err: e911.ErrIdentityUnavailable, wantStatus: http.StatusConflict, wantCode: "e911_identity_unavailable"},
		{name: "challenge incomplete", err: e911.ErrChallengeIncomplete, wantStatus: http.StatusNotImplemented, wantCode: "e911_challenge_not_implemented"},
		{name: "carrier websheet absent", err: e911.ErrCarrierWebsheetAbsent, wantStatus: http.StatusBadGateway, wantCode: "e911_websheet_unavailable"},
		{name: "deadline exceeded", err: fmt.Errorf("request failed: %w", context.DeadlineExceeded), wantStatus: http.StatusGatewayTimeout, wantCode: "e911_network_timeout"},
		{name: "unknown", err: errors.New("boom"), wantStatus: http.StatusInternalServerError, wantCode: "e911_start_failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := e911ErrorStatus(tt.err); got != tt.wantStatus {
				t.Fatalf("e911ErrorStatus(%v)=%d want %d", tt.err, got, tt.wantStatus)
			}
			if got := e911ErrorCode(tt.err); got != tt.wantCode {
				t.Fatalf("e911ErrorCode(%v)=%q want %q", tt.err, got, tt.wantCode)
			}
		})
	}
}

func TestE911ErrorMessageDoesNotExposeEntitlementIdentity(t *testing.T) {
	const (
		imsi        = "310260123456789"
		imei        = "356938035643809"
		eapIdentity = "0310260123456789@nai.epc.mnc260.mcc310.3gppnetwork.org"
		endpoint    = "https://ts43.example.test/ts43"
	)
	sensitiveDetail := endpoint + "?EAP_ID=" + url.QueryEscape(eapIdentity) + "&terminal_id=" + imei + "&imsi=" + imsi

	tests := []struct {
		name        string
		err         error
		wantMessage string
	}{
		{
			name:        "unknown error",
			err:         errors.New("TS.43 request failed: " + sensitiveDetail),
			wantMessage: "E911 地址设置启动失败，请稍后重试",
		},
		{
			name: "network timeout",
			err: &url.Error{
				Op:  "Get",
				URL: sensitiveDetail,
				Err: context.DeadlineExceeded,
			},
			wantMessage: "连接运营商 E911 服务超时，请检查前置代理或网络连接后重试",
		},
		{
			name:        "classified wrapped error",
			err:         fmt.Errorf("TS.43 response from %s: %w", sensitiveDetail, e911.ErrCarrierWebsheetAbsent),
			wantMessage: "运营商未返回 E911 地址设置页面",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			message := e911ErrorMessage(tt.err)
			if message != tt.wantMessage {
				t.Fatalf("e911ErrorMessage()=%q want %q", message, tt.wantMessage)
			}
			for _, secret := range []string{endpoint, "EAP_ID", eapIdentity, "terminal_id", imsi, imei} {
				if strings.Contains(message, secret) {
					t.Errorf("message leaked sensitive value %q: %q", secret, message)
				}
			}
		})
	}
}

func TestE911WrappedClassificationTakesPrecedenceOverTimeout(t *testing.T) {
	err := errors.Join(e911.ErrProviderUnavailable, context.DeadlineExceeded)
	if got := e911ErrorStatus(err); got != http.StatusBadRequest {
		t.Fatalf("e911ErrorStatus()=%d want %d", got, http.StatusBadRequest)
	}
	if got := e911ErrorCode(err); got != "e911_provider_unavailable" {
		t.Fatalf("e911ErrorCode()=%q want %q", got, "e911_provider_unavailable")
	}
	if got := e911ErrorMessage(err); got != "当前运营商暂不支持 E911 地址设置" {
		t.Fatalf("e911ErrorMessage()=%q", got)
	}
}
