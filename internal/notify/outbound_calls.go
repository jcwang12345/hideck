package notify

import (
	"context"
	"strings"
	"sync"

	"github.com/iniwex5/vowifi-go/runtimehost/voicehost"
)

// ManagedCallRequest carries only trusted command metadata. The coordinator
// resolves the card/profile/number from its resource catalogue; callers cannot
// supply those identities except for an optional preselected number ID.
type ManagedCallRequest struct {
	ActorID     string
	DeviceID    string
	ICCID       string
	NumberID    string
	Callee      string
	HoldSeconds int
	OnConnected func()
}

type ManagedCallResult struct {
	CallID    string
	RequestID string
	LeaseID   string
	Voice     *voicehost.SimulateCallResult
}

// OutboundCallCoordinator is injected by the API/resource layer. Commands fail
// closed when it is absent so notification channels cannot become an
// unmetered, unlocked dialing backdoor.
type OutboundCallCoordinator interface {
	RunManagedSimulatedCall(context.Context, ManagedCallRequest) (ManagedCallResult, error)
}

type outboundCoordinatorHolder struct {
	mu          sync.RWMutex
	coordinator OutboundCallCoordinator
}

func (h *outboundCoordinatorHolder) set(coordinator OutboundCallCoordinator) {
	h.mu.Lock()
	h.coordinator = coordinator
	h.mu.Unlock()
}

func (h *outboundCoordinatorHolder) get() OutboundCallCoordinator {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.coordinator
}

func commandActorID(ctx CommandContext) string {
	if keyed, ok := ctx.(UserKeyedContext); ok {
		if key := strings.TrimSpace(keyed.UserKey()); key != "" {
			return "notification:" + key
		}
	}
	return "notification:administrator"
}
