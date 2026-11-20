package dispatch

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func newTestService(t *testing.T, now *time.Time) (*Service, *gorm.DB) {
	t.Helper()
	database, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "dispatch.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := Migrate(database); err != nil {
		t.Fatal(err)
	}
	service, err := New(database, func() time.Time { return *now })
	if err != nil {
		t.Fatal(err)
	}
	return service, database
}

func eligibleCandidate(device, card, profile, number string) Candidate {
	return Candidate{PoolID: "pool-1", DeviceID: device, CardID: card, ProfileID: profile, NumberID: number, Online: true, Enabled: true}
}

func TestReservePrefersReadyThenLeastUsed(t *testing.T) {
	now := time.Date(2026, 11, 20, 9, 0, 0, 0, time.FixedZone("CST", 8*3600))
	service, _ := newTestService(t, &now)
	candidates := []Candidate{
		eligibleCandidate("dev-a", "card-a", "profile-a", "num-a"),
		eligibleCandidate("dev-b", "card-b", "profile-b", "num-b"),
		eligibleCandidate("dev-c", "card-c", "profile-c", "num-c"),
	}
	candidates[0].DailyUseCount = 0
	candidates[1].AlreadyActive, candidates[1].IMSReady, candidates[1].DailyUseCount = true, true, 7
	candidates[2].AlreadyActive, candidates[2].IMSReady, candidates[2].DailyUseCount = true, true, 1
	reservation, err := service.Reserve(context.Background(), ReserveInput{UserID: "user-1", PoolID: "pool-1", Callee: "+12025550100", EnforceUserLimit: true}, candidates)
	if err != nil {
		t.Fatal(err)
	}
	if reservation.Lease.DeviceID != "dev-c" {
		t.Fatalf("device=%q want dev-c", reservation.Lease.DeviceID)
	}
}

func TestReserveHonorsPoolPriorityWithinReadinessClass(t *testing.T) {
	now := time.Date(2026, 11, 20, 9, 0, 0, 0, time.FixedZone("CST", 8*3600))
	service, _ := newTestService(t, &now)
	high := eligibleCandidate("dev-high", "card-high", "profile-high", "num-high")
	low := eligibleCandidate("dev-low", "card-low", "profile-low", "num-low")
	high.AlreadyActive, high.IMSReady, high.Priority, high.DailyUseCount = true, true, 10, 0
	low.AlreadyActive, low.IMSReady, low.Priority, low.DailyUseCount = true, true, 1, 20
	reservation, err := service.Reserve(context.Background(), ReserveInput{UserID: "user-1", PoolID: "pool-1", Callee: "+12025550100", EnforceUserLimit: true}, []Candidate{high, low})
	if err != nil {
		t.Fatal(err)
	}
	if reservation.Lease.DeviceID != "dev-low" {
		t.Fatalf("device=%q want dev-low", reservation.Lease.DeviceID)
	}
}

func TestReserveEnforcesDeviceAndUserLease(t *testing.T) {
	now := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	service, _ := newTestService(t, &now)
	candidate := eligibleCandidate("dev-a", "card-a", "profile-a", "num-a")
	first, err := service.Reserve(context.Background(), ReserveInput{UserID: "user-1", PoolID: "pool-1", Callee: "100", EnforceUserLimit: true}, []Candidate{candidate})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Reserve(context.Background(), ReserveInput{UserID: "user-2", PoolID: "pool-1", Callee: "101", EnforceUserLimit: true}, []Candidate{candidate})
	if !errors.Is(err, ErrNoResource) {
		t.Fatalf("device conflict err=%v", err)
	}
	other := eligibleCandidate("dev-b", "card-b", "profile-b", "num-b")
	_, err = service.Reserve(context.Background(), ReserveInput{UserID: "user-1", PoolID: "pool-1", Callee: "102", EnforceUserLimit: true}, []Candidate{other})
	if !errors.Is(err, ErrActiveUserLease) {
		t.Fatalf("user conflict err=%v", err)
	}
	if err := service.Release(context.Background(), first.Lease.ID, "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Reserve(context.Background(), ReserveInput{UserID: "user-1", PoolID: "pool-1", Callee: "103", EnforceUserLimit: true}, []Candidate{other}); err != nil {
		t.Fatal(err)
	}
}

func TestMaintenanceLeaseBlocksCallsAndCompletesOnRelease(t *testing.T) {
	now := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	service, database := newTestService(t, &now)
	maintenance, err := service.ReserveMaintenance(context.Background(), MaintenanceInput{
		UserID: "admin", Operation: "profile_switch", DeviceID: "dev-a", CardID: "card-a", ProfileID: "profile-b",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Reserve(context.Background(), ReserveInput{
		UserID: "user-1", PoolID: "pool-1", Callee: "100", EnforceUserLimit: true,
	}, []Candidate{eligibleCandidate("dev-a", "card-a", "profile-a", "num-a")})
	if !errors.Is(err, ErrNoResource) {
		t.Fatalf("call during maintenance err=%v", err)
	}
	if err := service.Release(context.Background(), maintenance.Lease.ID, "", ""); err != nil {
		t.Fatal(err)
	}
	var request CallRequest
	if err := database.First(&request, "id = ?", maintenance.Request.ID).Error; err != nil {
		t.Fatal(err)
	}
	if request.State != RequestCompleted {
		t.Fatalf("maintenance request state=%q want completed", request.State)
	}
	if _, err := service.Reserve(context.Background(), ReserveInput{
		UserID: "user-1", PoolID: "pool-1", Callee: "101", EnforceUserLimit: true,
	}, []Candidate{eligibleCandidate("dev-a", "card-a", "profile-a", "num-a")}); err != nil {
		t.Fatal(err)
	}
}

func TestLeaseTransitionUsesCAS(t *testing.T) {
	now := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	service, _ := newTestService(t, &now)
	reservation, err := service.Reserve(context.Background(), ReserveInput{UserID: "user-1", PoolID: "pool-1", Callee: "100", EnforceUserLimit: true}, []Candidate{eligibleCandidate("dev-a", "card-a", "profile-a", "num-a")})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := service.Transition(context.Background(), reservation.Lease.ID, 1, LeaseReady)
	if err != nil || lease.Version != 2 {
		t.Fatalf("transition lease=%+v err=%v", lease, err)
	}
	if _, err := service.Transition(context.Background(), reservation.Lease.ID, 1, LeaseDialing); !errors.Is(err, ErrLeaseConflict) {
		t.Fatalf("stale transition err=%v", err)
	}
	if _, err := service.Transition(context.Background(), reservation.Lease.ID, 2, LeaseConnected); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("invalid transition err=%v", err)
	}
}

func TestRemoteDeclineIsIdempotentAndDisablesThirdTime(t *testing.T) {
	now := time.Date(2026, 11, 20, 23, 30, 0, 0, time.FixedZone("CST", 8*3600))
	service, _ := newTestService(t, &now)
	for index, callID := range []string{"call-1", "call-2", "call-3"} {
		outcome, stat, inserted, err := service.RecordOutcome(context.Background(), OutcomeInput{CallID: callID, CardID: "card-a", Kind: OutcomeRemoteDeclined, SIPCode: 603, Terminator: "remote", ObservedAt: now})
		if err != nil || !inserted || !outcome.Counted || stat.RemoteDeclineCount != index+1 {
			t.Fatalf("index=%d outcome=%+v stat=%+v inserted=%v err=%v", index, outcome, stat, inserted, err)
		}
	}
	disabled, stat, err := service.CardDisabledToday(context.Background(), "card-a")
	if err != nil || !disabled || stat.RemoteDeclineCount != 3 {
		t.Fatalf("disabled=%v stat=%+v err=%v", disabled, stat, err)
	}
	_, duplicate, inserted, err := func() (CallOutcome, CardDailyStat, bool, error) {
		return service.RecordOutcome(context.Background(), OutcomeInput{CallID: "call-3", CardID: "card-a", Kind: OutcomeRemoteDeclined, SIPCode: 603, Terminator: "remote", ObservedAt: now})
	}()
	if err != nil || inserted || duplicate.RemoteDeclineCount != 0 {
		t.Fatalf("duplicate stat=%+v inserted=%v err=%v", duplicate, inserted, err)
	}
	var persisted CardDailyStat
	if err := service.db.First(&persisted, "card_id = ? AND business_date = ?", "card-a", "2026-11-20").Error; err != nil || persisted.RemoteDeclineCount != 3 {
		t.Fatalf("persisted=%+v err=%v", persisted, err)
	}
}

func TestBusyNoAnswerAndNextDayDoNotStayDisabled(t *testing.T) {
	now := time.Date(2026, 11, 20, 23, 59, 0, 0, time.FixedZone("CST", 8*3600))
	service, _ := newTestService(t, &now)
	for _, input := range []OutcomeInput{
		{CallID: "busy", CardID: "card-a", Kind: OutcomeBusy, SIPCode: 486, ObservedAt: now},
		{CallID: "no-answer", CardID: "card-a", Kind: OutcomeNoAnswer, SIPCode: 408, ObservedAt: now},
	} {
		outcome, _, _, err := service.RecordOutcome(context.Background(), input)
		if err != nil || outcome.Counted {
			t.Fatalf("outcome=%+v err=%v", outcome, err)
		}
	}
	for _, id := range []string{"decline-1", "decline-2", "decline-3"} {
		if _, _, _, err := service.RecordOutcome(context.Background(), OutcomeInput{CallID: id, CardID: "card-a", Kind: OutcomeRemoteDeclined, SIPCode: 607, Terminator: "remote", ObservedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	now = now.Add(2 * time.Minute)
	disabled, _, err := service.CardDisabledToday(context.Background(), "card-a")
	if err != nil || disabled {
		t.Fatalf("next day disabled=%v err=%v", disabled, err)
	}
}

func TestNormalizeVoiceOutcomeOnlyCountsExplicitRemoteDecline(t *testing.T) {
	tests := []struct {
		kind       string
		code       int
		terminator string
		want       OutcomeKind
	}{
		{"declined", 603, "remote", OutcomeRemoteDeclined},
		{"declined", 607, "remote", OutcomeRemoteDeclined},
		{"busy", 486, "remote", OutcomeBusy},
		{"declined", 603, "local", OutcomeUnknownFailed},
		{"declined", 486, "remote", OutcomeUnknownFailed},
	}
	for _, test := range tests {
		if got := NormalizeVoiceOutcome(test.kind, test.code, test.terminator, false); got != test.want {
			t.Fatalf("kind=%s code=%d terminator=%s got=%s want=%s", test.kind, test.code, test.terminator, got, test.want)
		}
	}
}

func TestReleaseMarksFailedRequestAndDailyUsageCountsAllOutcomes(t *testing.T) {
	now := time.Date(2026, 11, 20, 9, 0, 0, 0, time.FixedZone("CST", 8*3600))
	service, database := newTestService(t, &now)
	reservation, err := service.Reserve(context.Background(), ReserveInput{UserID: "user-1", PoolID: "pool-1", Callee: "100", EnforceUserLimit: true}, []Candidate{eligibleCandidate("dev-a", "card-a", "profile-a", "num-a")})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Release(context.Background(), reservation.Lease.ID, "ims_not_ready", "timeout"); err != nil {
		t.Fatal(err)
	}
	var request CallRequest
	if err := database.First(&request, "id = ?", reservation.Request.ID).Error; err != nil {
		t.Fatal(err)
	}
	if request.State != RequestFailed || request.FailureCode != "ims_not_ready" {
		t.Fatalf("request=%+v", request)
	}
	for _, input := range []OutcomeInput{
		{CallID: "completed", CardID: "card-a", NumberID: "num-a", Kind: OutcomeCompleted, ObservedAt: now.Add(-time.Minute)},
		{CallID: "busy", CardID: "card-a", NumberID: "num-a", Kind: OutcomeBusy, ObservedAt: now},
		{CallID: "older", CardID: "card-b", NumberID: "num-b", Kind: OutcomeCompleted, ObservedAt: now.Add(-time.Hour)},
	} {
		if _, _, _, err := service.RecordOutcome(context.Background(), input); err != nil {
			t.Fatal(err)
		}
	}
	counts, err := service.DailyUsageCounts(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if counts["card-a"] != 2 || counts["card-b"] != 1 {
		t.Fatalf("daily usage=%v", counts)
	}
	lastUsed, err := service.LastUsedTimes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !lastUsed["num-a"].Equal(now) || !lastUsed["num-b"].Equal(now.Add(-time.Hour)) {
		t.Fatalf("last used=%v", lastUsed)
	}
}
