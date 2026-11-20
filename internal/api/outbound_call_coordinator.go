package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/iniwex5/vowifi-go/runtimehost/voicehost"
	"github.com/yibaiba/hideck/internal/dispatch"
	"github.com/yibaiba/hideck/internal/notify"
	"github.com/yibaiba/hideck/internal/phone"
	"github.com/yibaiba/hideck/internal/resourcecatalog"
	"github.com/yibaiba/hideck/pkg/logger"
)

const directDispatchPoolPrefix = "__direct__:"

type managedOutboundLease struct {
	Reservation dispatch.Reservation
	Resource    resourcecatalog.CurrentVoiceResource
	finished    bool
}

func (lease *managedOutboundLease) memberView() resourcecatalog.NumberPoolMemberView {
	member := resourcecatalog.NumberPoolMember{
		ID:            "direct:" + lease.Resource.Number.ID,
		NumberPoolID:  lease.Reservation.Lease.PoolID,
		PhoneNumberID: lease.Resource.Number.ID,
		Priority:      0,
		Weight:        1,
		Enabled:       true,
	}
	if lease.Resource.Member != nil {
		member = *lease.Resource.Member
	}
	profile, card := lease.Resource.Profile, lease.Resource.Card
	return resourcecatalog.NumberPoolMemberView{
		NumberPoolMember: member,
		Number:           lease.Resource.Number,
		Profile:          &profile,
		Card:             &card,
	}
}

func (s *Server) reserveDirectOutbound(ctx context.Context, actorID, deviceID, iccid, numberID, callee string) (*managedOutboundLease, error) {
	actorID, deviceID, callee = strings.TrimSpace(actorID), strings.TrimSpace(deviceID), strings.TrimSpace(callee)
	if s == nil || s.resourceCatalog == nil || s.dispatcher == nil || s.pool == nil {
		return nil, dispatchAttemptError{code: "dispatch_unavailable", err: errors.New("线路调度服务暂不可用")}
	}
	if actorID == "" || deviceID == "" || callee == "" {
		return nil, dispatchAttemptError{code: "invalid_request", err: errors.New("用户、设备和目标号码不能为空")}
	}
	resource, err := s.resourceCatalog.ResolveCurrentVoiceResource(ctx, deviceID, iccid, numberID)
	if err != nil {
		if errors.Is(err, resourcecatalog.ErrNotFound) {
			return nil, dispatchAttemptError{code: "resource_unmapped", err: errors.New("当前设备、SIM Profile 与主叫号码尚未完整映射到资源目录")}
		}
		return nil, dispatchAttemptError{code: "dispatch_unavailable", err: err}
	}
	worker := s.pool.GetWorker(deviceID)
	if worker == nil {
		return nil, dispatchAttemptError{code: "no_resource", err: errors.New("线路设备已离线")}
	}
	active, err := s.dispatcher.ActiveLeases(ctx)
	if err != nil {
		return nil, dispatchAttemptError{code: "dispatch_unavailable", err: err}
	}
	busy := false
	for _, activeLease := range active {
		if activeLease.DeviceID == deviceID {
			busy = true
			break
		}
	}
	if !busy && s.phone != nil {
		for _, activeCall := range s.phone.Active("") {
			if activeCall.DeviceID == deviceID {
				busy = true
				break
			}
		}
	}
	if !busy && s.voiceGW != nil && s.voiceGW.ActiveCall(deviceID) != nil {
		busy = true
	}
	usage, err := s.dispatcher.DailyUsageCounts(ctx, "")
	if err != nil {
		return nil, dispatchAttemptError{code: "dispatch_unavailable", err: err}
	}
	current := sameICCID(worker.CurrentICCID(), resource.Profile.ICCID)
	state, hasState := s.pool.GetVoWiFiRuntimeState(deviceID)
	voiceReady := state.IMSReady
	if s.pool.IsNativeVoLTE(deviceID) || s.pool.IsModemVoice(deviceID) {
		voiceReady = true
	}
	status := strings.ToLower(strings.TrimSpace(resource.Number.Status))
	enabled := status != "disabled" && status != "suspended"
	if !current && resource.Card.Type != resourcecatalog.CardContainerTypeEUICC {
		enabled = false
	}
	poolID, priority := directDispatchPoolPrefix+resource.Number.ID, 0
	if resource.Pool != nil && resource.Member != nil {
		poolID, priority = resource.Pool.ID, resource.Member.Priority
	}
	candidate := dispatch.Candidate{
		PoolID: poolID, DeviceID: deviceID, CardID: resource.Card.ID,
		ProfileID: resource.Profile.ID, NumberID: resource.Number.ID, Priority: priority,
		Online: worker.GetCachedHealthy(), Enabled: enabled,
		Faulted: hasState && !voiceReady && strings.TrimSpace(state.LastError) != "" && (state.IMSState == "failed" || state.SessionState == "error"),
		Busy:    busy, Switching: s.pool.IsESIMSwitching(deviceID),
		Registering:   hasState && !voiceReady && (state.IMSState == "registering" || state.Phase == "registering"),
		AlreadyActive: current, IMSReady: voiceReady, DailyUseCount: usage[resource.Card.ID],
	}
	reservation, err := s.dispatcher.Reserve(ctx, dispatch.ReserveInput{
		UserID: actorID, PoolID: poolID, Callee: callee, EnforceUserLimit: false,
	}, []dispatch.Candidate{candidate})
	if err != nil {
		if errors.Is(err, dispatch.ErrNoResource) {
			return nil, dispatchAttemptError{code: "no_resource", err: errors.New("当前线路已停用、正被占用或尚未就绪")}
		}
		return nil, dispatchAttemptError{code: "dispatch_unavailable", err: err}
	}
	lease := &managedOutboundLease{Reservation: reservation, Resource: resource}
	if err := s.resourceCatalog.RecordAudit(ctx, actorID, "dispatch.allocated", "call_request", reservation.Request.ID, map[string]any{
		"pool_id": poolID, "device_id": deviceID, "card_id": resource.Card.ID,
		"profile_id": resource.Profile.ID, "number_id": resource.Number.ID, "mode": "controlled_direct",
	}); err != nil {
		logger.Warn("记录受控直拨线路分配审计失败", "request_id", reservation.Request.ID, "err", err)
	}
	return lease, nil
}

func (s *Server) transitionManagedOutbound(ctx context.Context, managed *managedOutboundLease, next dispatch.LeaseState) error {
	if managed == nil || managed.finished {
		return errors.New("outbound lease is unavailable")
	}
	updated, err := s.dispatcher.Transition(ctx, managed.Reservation.Lease.ID, managed.Reservation.Lease.Version, next)
	if err == nil {
		managed.Reservation.Lease = updated
	}
	return err
}

func (s *Server) prepareManagedOutbound(ctx context.Context, managed *managedOutboundLease) error {
	if managed == nil {
		return errors.New("outbound lease is unavailable")
	}
	updated, err := s.prepareDispatchLease(ctx, managed.Reservation.Lease, managed.memberView())
	managed.Reservation.Lease = updated
	return err
}

func (s *Server) prepareManagedSimulation(ctx context.Context, managed *managedOutboundLease) error {
	if managed == nil {
		return errors.New("outbound lease is unavailable")
	}
	// A registered voice agent is the authoritative readiness signal for the
	// timed/recorded-call path. simulateCall itself still prepares on-demand
	// cellular data. Avoid restarting IMS after the caller has already proved
	// the exact agent is ready.
	if s.voiceAgentReady(managed.Reservation.Lease.DeviceID) {
		if err := s.transitionManagedOutbound(ctx, managed, dispatch.LeaseRegistering); err != nil {
			return err
		}
		return s.transitionManagedOutbound(ctx, managed, dispatch.LeaseReady)
	}
	return s.prepareManagedOutbound(ctx, managed)
}

func (s *Server) bindManagedOutbound(ctx context.Context, managed *managedOutboundLease, callID string) error {
	if managed == nil || managed.finished {
		return errors.New("outbound lease is unavailable")
	}
	callID = strings.TrimSpace(callID)
	if err := s.dispatcher.BindCall(ctx, managed.Reservation.Lease.ID, callID); err != nil {
		return err
	}
	managed.Reservation.Lease.CallID = callID
	return s.transitionManagedOutbound(ctx, managed, dispatch.LeaseDialing)
}

func (s *Server) releaseManagedOutbound(ctx context.Context, managed *managedOutboundLease, code string, cause error) {
	if managed == nil || managed.finished {
		return
	}
	reason := ""
	if cause != nil {
		reason = cause.Error()
	}
	if err := s.dispatcher.Release(ctx, managed.Reservation.Lease.ID, strings.TrimSpace(code), reason); err != nil {
		logger.Error("释放外呼线路失败", "lease_id", managed.Reservation.Lease.ID, "err", err)
	}
	managed.finished = true
}

func (s *Server) completeManagedOutbound(ctx context.Context, managed *managedOutboundLease, callID string, kind dispatch.OutcomeKind, reason string) error {
	if managed == nil || managed.finished {
		return nil
	}
	lease := managed.Reservation.Lease
	outcome, stat, inserted, err := s.dispatcher.RecordOutcome(ctx, dispatch.OutcomeInput{
		CallID: callID, RequestID: lease.RequestID, LeaseID: lease.ID, UserID: lease.UserID,
		CardID: lease.CardID, ProfileID: lease.ProfileID, NumberID: lease.NumberID,
		Kind: kind, SIPReason: strings.TrimSpace(reason), ObservedAt: time.Now(),
		Threshold: s.dispatchRemoteDeclineThreshold(ctx, lease.PoolID),
	})
	if err != nil {
		_ = s.dispatcher.Release(ctx, lease.ID, "outcome_record_failed", err.Error())
		managed.finished = true
		return err
	}
	managed.finished = true
	if inserted && s.resourceCatalog != nil {
		_ = s.resourceCatalog.RecordAudit(ctx, lease.UserID, "dispatch.released", "call", callID, map[string]any{
			"request_id": lease.RequestID, "lease_id": lease.ID, "card_id": lease.CardID,
			"profile_id": lease.ProfileID, "number_id": lease.NumberID, "outcome": outcome.Kind,
		})
		if outcome.Kind == dispatch.OutcomeRemoteDeclined && stat.DisabledAt != nil && stat.DisabledReason == "remote_decline_threshold" && stat.RemoteDeclineCount == s.dispatchRemoteDeclineThreshold(ctx, lease.PoolID) {
			_ = s.resourceCatalog.RecordAudit(ctx, lease.UserID, "card.auto_disabled", "card", lease.CardID, map[string]any{
				"business_date": stat.BusinessDate, "remote_declines": stat.RemoteDeclineCount,
			})
		}
	}
	return nil
}

func (s *Server) startManagedPhoneCall(ctx context.Context, managed *managedOutboundLease, scope phone.AccessScope, callee, mediaID, controlLease string) (phone.CallView, error) {
	if err := s.prepareManagedOutbound(ctx, managed); err != nil {
		s.releaseManagedOutbound(ctx, managed, dispatchErrorCode(err), err)
		return phone.CallView{}, err
	}
	lease := managed.Reservation.Lease
	call, dialErr := s.phone.StartCallScoped(scope, phone.StartCallRequest{
		Owner: scope.UserID, DeviceID: lease.DeviceID, Callee: callee, MediaID: mediaID, Lease: controlLease,
		PoolID: lease.PoolID, CardID: lease.CardID, ProfileID: lease.ProfileID, NumberID: lease.NumberID,
		SourceNumber: managed.Resource.Number.E164, RequestID: lease.RequestID, ResourceLeaseID: lease.ID,
	})
	if strings.TrimSpace(call.CallID) == "" {
		s.releaseManagedOutbound(ctx, managed, "dial_failed", dialErr)
		if dialErr == nil {
			dialErr = errors.New("电话服务未返回 call_id")
		}
		return call, dialErr
	}
	if err := s.bindManagedOutbound(ctx, managed, call.CallID); err != nil {
		_ = s.phone.Hangup(ctx, scope.UserID, call.CallID, controlLease)
		s.releaseManagedOutbound(ctx, managed, "dispatch_bind_failed", err)
		return call, fmt.Errorf("bind outbound call: %w", err)
	}
	// The phone service terminal observer owns the lease from this point.
	managed.finished = true
	return call, dialErr
}

func dispatchErrorCode(err error) string {
	var attempt dispatchAttemptError
	if errors.As(err, &attempt) && strings.TrimSpace(attempt.code) != "" {
		return attempt.code
	}
	return "dispatch_unavailable"
}

func respondManagedOutboundError(c businessContext, err error) {
	code := dispatchErrorCode(err)
	status := http.StatusInternalServerError
	switch code {
	case "invalid_request":
		status = http.StatusBadRequest
	case "resource_unmapped", "no_resource":
		status = http.StatusConflict
	case "profile_switch_failed", "ims_not_ready", "dial_failed":
		status = http.StatusBadGateway
	case "dispatch_unavailable":
		status = http.StatusServiceUnavailable
	}
	c.JSON(status, map[string]any{"status": "error", "code": code, "message": err.Error()})
}

// RunManagedSimulatedCall implements notify.OutboundCallCoordinator. It is
// also used by the management API so bot and browser calls cannot bypass the
// resource catalogue or the per-device lease.
func (s *Server) RunManagedSimulatedCall(ctx context.Context, request notify.ManagedCallRequest) (notify.ManagedCallResult, error) {
	managed, err := s.reserveDirectOutbound(ctx, request.ActorID, request.DeviceID, request.ICCID, request.NumberID, request.Callee)
	if err != nil {
		return notify.ManagedCallResult{}, err
	}
	if err := s.prepareManagedSimulation(ctx, managed); err != nil {
		s.releaseManagedOutbound(ctx, managed, dispatchErrorCode(err), err)
		return notify.ManagedCallResult{}, err
	}
	callID := "managed-" + uuid.NewString()
	if err := s.bindManagedOutbound(ctx, managed, callID); err != nil {
		s.releaseManagedOutbound(ctx, managed, "dispatch_bind_failed", err)
		return notify.ManagedCallResult{}, err
	}
	result, callErr := s.simulateCall(ctx, request.DeviceID, voicehost.SimulateCallRequest{
		Callee: request.Callee, HoldSeconds: request.HoldSeconds, OnConnected: request.OnConnected,
	})
	kind, reason := dispatch.OutcomeCompleted, ""
	if callErr != nil {
		kind, reason = dispatch.OutcomeNetworkFailed, callErr.Error()
		if errors.Is(callErr, context.Canceled) || errors.Is(callErr, context.DeadlineExceeded) {
			kind = dispatch.OutcomeLocalCanceled
		}
	} else if result == nil || !result.Success {
		kind = dispatch.OutcomeUnknownFailed
		if result != nil {
			reason = result.Reason
		}
	}
	finishCtx, finishCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer finishCancel()
	completeErr := s.completeManagedOutbound(finishCtx, managed, callID, kind, reason)
	response := notify.ManagedCallResult{
		CallID: callID, RequestID: managed.Reservation.Request.ID, LeaseID: managed.Reservation.Lease.ID,
		Voice: result,
	}
	if callErr != nil {
		return response, dispatchAttemptError{code: "dial_failed", err: callErr}
	}
	if completeErr != nil {
		return response, completeErr
	}
	return response, nil
}
