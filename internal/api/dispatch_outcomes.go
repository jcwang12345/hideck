package api

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/yibaiba/hideck/internal/dispatch"
	"github.com/yibaiba/hideck/internal/phone"
	"github.com/yibaiba/hideck/pkg/logger"
)

func (s *Server) handleDispatchTerminalCall(record phone.CallRecord) {
	if s == nil || s.dispatcher == nil || strings.TrimSpace(record.ResourceLeaseID) == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	threshold := s.dispatchRemoteDeclineThreshold(ctx, record.PoolID)
	kind := record.OutcomeKind
	if strings.TrimSpace(kind) == "" {
		kind = record.Status
	}
	observedAt := time.Now()
	if record.EndedAt != nil && !record.EndedAt.IsZero() {
		observedAt = *record.EndedAt
	}
	outcome, stat, inserted, err := s.dispatcher.RecordOutcome(ctx, dispatch.OutcomeInput{
		CallID: record.CallID, RequestID: record.RequestID, LeaseID: record.ResourceLeaseID,
		UserID: record.UserID, CardID: record.CardID, ProfileID: record.ProfileID, NumberID: record.NumberID,
		Kind:    dispatch.NormalizeVoiceOutcome(kind, record.SIPCode, record.Terminator, record.AnsweredAt != nil),
		SIPCode: record.SIPCode, SIPReason: record.SIPReason, Terminator: record.Terminator,
		ObservedAt: observedAt, Threshold: threshold,
	})
	if err != nil {
		logger.Error("记录调度通话结果失败", "call_id", record.CallID, "lease_id", record.ResourceLeaseID, "err", err)
		return
	}
	if !inserted || s.resourceCatalog == nil {
		return
	}
	payload := map[string]any{
		"call_id": outcome.CallID, "request_id": outcome.RequestID, "lease_id": outcome.LeaseID,
		"card_id": outcome.CardID, "profile_id": outcome.ProfileID, "number_id": outcome.NumberID,
		"outcome": outcome.Kind, "sip_code": outcome.SIPCode, "terminator": outcome.Terminator,
	}
	if err := s.resourceCatalog.RecordAudit(ctx, record.UserID, "dispatch.released", "call", record.CallID, payload); err != nil {
		logger.Warn("记录线路释放审计失败", "call_id", record.CallID, "err", err)
	}
	if outcome.Kind == dispatch.OutcomeRemoteDeclined && stat.DisabledAt != nil && stat.DisabledReason == "remote_decline_threshold" && stat.RemoteDeclineCount == threshold {
		if err := s.resourceCatalog.RecordAudit(ctx, record.UserID, "card.auto_disabled", "card", record.CardID, map[string]any{
			"business_date": stat.BusinessDate, "remote_declines": stat.RemoteDeclineCount, "threshold": threshold,
		}); err != nil {
			logger.Warn("记录卡自动停用审计失败", "card_id", record.CardID, "err", err)
		}
	}
}

func (s *Server) dispatchRemoteDeclineThreshold(ctx context.Context, poolID string) int {
	const fallback = 3
	if s == nil || s.resourceCatalog == nil || strings.TrimSpace(poolID) == "" {
		return fallback
	}
	policies, err := s.resourceCatalog.ListDispatchPolicies(ctx)
	if err != nil {
		return fallback
	}
	for _, policy := range policies {
		if !policy.Enabled || policy.NumberPoolID != poolID || strings.TrimSpace(policy.ConfigJSON) == "" {
			continue
		}
		var config struct {
			RejectionThreshold int `json:"rejection_threshold"`
		}
		if json.Unmarshal([]byte(policy.ConfigJSON), &config) == nil && config.RejectionThreshold > 0 {
			return config.RejectionThreshold
		}
	}
	return fallback
}

func (s *Server) reconcileDispatchLeases() {
	if s == nil || s.dispatcher == nil || s.phone == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	leases, err := s.dispatcher.ActiveLeases(ctx)
	if err != nil {
		logger.Error("读取遗留线路占用失败", "err", err)
		return
	}
	activeCalls := make(map[string]struct{})
	for _, call := range s.phone.Active("") {
		activeCalls[call.CallID] = struct{}{}
	}
	for _, lease := range leases {
		if _, ok := activeCalls[lease.CallID]; ok && lease.CallID != "" {
			continue
		}
		if s.voiceGW != nil && s.voiceGW.ActiveCall(lease.DeviceID) != nil {
			continue
		}
		if err := s.dispatcher.Release(ctx, lease.ID, "orphaned_after_restart", "no matching live call was found"); err != nil {
			logger.Error("释放遗留线路占用失败", "lease_id", lease.ID, "err", err)
			continue
		}
		if s.resourceCatalog != nil {
			_ = s.resourceCatalog.RecordAudit(ctx, "system", "dispatch.orphan_released", "resource_lease", lease.ID, map[string]any{
				"request_id": lease.RequestID, "device_id": lease.DeviceID, "call_id": lease.CallID,
			})
		}
	}
}
