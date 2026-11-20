package api

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/yibaiba/hideck/internal/config"
	"github.com/yibaiba/hideck/internal/device"
	"github.com/yibaiba/hideck/internal/dispatch"
	"github.com/yibaiba/hideck/internal/resourcecatalog"
	"gorm.io/gorm"
)

func TestManagedOutboundLeaseEnforcesDeviceLockOutcomeAndDailyDisable(t *testing.T) {
	server, dispatcher := newManagedOutboundTestServer(t)
	ctx := context.Background()

	first, err := server.reserveDirectOutbound(ctx, "admin-1", "wwan0", "8901001", "", "+12025550199")
	if err != nil {
		t.Fatalf("first reserve: %v", err)
	}
	if _, err := server.reserveDirectOutbound(ctx, "admin-2", "wwan0", "8901001", "", "+12025550198"); err == nil || dispatchErrorCode(err) != "no_resource" {
		t.Fatalf("second reserve error = %v, want no_resource", err)
	}
	if err := server.transitionManagedOutbound(ctx, first, dispatch.LeaseReady); err != nil {
		t.Fatalf("ready transition: %v", err)
	}
	if err := server.bindManagedOutbound(ctx, first, "call-1"); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if err := server.completeManagedOutbound(ctx, first, "call-1", dispatch.OutcomeCompleted, ""); err != nil {
		t.Fatalf("complete: %v", err)
	}
	active, err := dispatcher.ActiveLeases(ctx)
	if err != nil || len(active) != 0 {
		t.Fatalf("active leases = %#v, err=%v", active, err)
	}
	stats, err := dispatcher.DailyStats(ctx, "")
	if err != nil || len(stats) != 1 || stats[0].CallCount != 1 {
		t.Fatalf("daily stats = %#v, err=%v", stats, err)
	}

	if _, err := dispatcher.DisableCardToday(ctx, "card-1", "admin-1", "manual test"); err != nil {
		t.Fatalf("DisableCardToday: %v", err)
	}
	_, err = server.reserveDirectOutbound(ctx, "admin-1", "wwan0", "8901001", "", "+12025550197")
	if err == nil || dispatchErrorCode(err) != "no_resource" {
		t.Fatalf("disabled reserve error = %v, want no_resource", err)
	}
}

func TestManagedOutboundRequiresCataloguedCurrentNumber(t *testing.T) {
	server, _ := newManagedOutboundTestServer(t)
	_, updateErr := server.resourceCatalog.UpsertPhoneNumber(context.Background(), "admin", resourcecatalog.PhoneNumberInput{
		ID: "number-1", SubscriptionProfileID: "profile-1", E164: "+12025550123", Status: "disabled",
	})
	if updateErr != nil {
		t.Fatalf("disable phone number: %v", updateErr)
	}
	_, err := server.reserveDirectOutbound(context.Background(), "admin", "wwan0", "8901001", "", "+12025550199")
	if err == nil || dispatchErrorCode(err) != "resource_unmapped" {
		t.Fatalf("reserve error = %v, want resource_unmapped", err)
	}
}

func TestPrepareDispatchLeaseRollsBackAfterIdentityRefreshTransitionFailure(t *testing.T) {
	server, dispatcher := newManagedOutboundTestServer(t)
	lease, member := reserveProfileSwitchTestLease(t, dispatcher)
	restored := ""
	server.dispatchProfileSwitch = func(ctx context.Context, _, _, _ string) error {
		active, err := dispatcher.ActiveLeases(ctx)
		if err != nil {
			return err
		}
		for _, current := range active {
			if current.ID == lease.ID {
				_, err = dispatcher.Transition(ctx, current.ID, current.Version, dispatch.LeaseIdentityRefresh)
				return err
			}
		}
		return errors.New("active lease not found")
	}
	server.dispatchProfileRestore = func(_ string, iccid string) error {
		restored = iccid
		return nil
	}
	server.callRegistrationReady = func(context.Context, string) error {
		t.Fatal("registration must not run after identity-refresh transition failure")
		return nil
	}

	_, err := server.prepareDispatchLease(context.Background(), lease, member)
	if !errors.Is(err, dispatch.ErrLeaseConflict) {
		t.Fatalf("prepare error = %v, want lease conflict", err)
	}
	if dispatchErrorCode(err) != "profile_switch_failed" {
		t.Fatalf("error code = %q, want profile_switch_failed", dispatchErrorCode(err))
	}
	if restored != "8901001" {
		t.Fatalf("restored ICCID = %q, want original ICCID", restored)
	}
}

func TestPrepareDispatchLeaseRollsBackAfterIMSFailureAndPreservesErrors(t *testing.T) {
	server, dispatcher := newManagedOutboundTestServer(t)
	lease, member := reserveProfileSwitchTestLease(t, dispatcher)
	imsErr := errors.New("IMS registration rejected")
	rollbackErr := errors.New("reader disappeared during rollback")
	restored := ""
	server.dispatchProfileSwitch = func(context.Context, string, string, string) error { return nil }
	server.callRegistrationReady = func(context.Context, string) error { return imsErr }
	server.dispatchProfileRestore = func(_ string, iccid string) error {
		restored = iccid
		return rollbackErr
	}

	_, err := server.prepareDispatchLease(context.Background(), lease, member)
	if !errors.Is(err, imsErr) {
		t.Fatalf("prepare error = %v, want original IMS error", err)
	}
	if dispatchErrorCode(err) != "ims_not_ready" {
		t.Fatalf("error code = %q, want ims_not_ready", dispatchErrorCode(err))
	}
	if !strings.Contains(err.Error(), rollbackErr.Error()) {
		t.Fatalf("prepare error = %q, want appended rollback error", err)
	}
	if restored != "8901001" {
		t.Fatalf("restored ICCID = %q, want original ICCID", restored)
	}
}

func reserveProfileSwitchTestLease(t *testing.T, dispatcher *dispatch.Service) (dispatch.ResourceLease, resourcecatalog.NumberPoolMemberView) {
	t.Helper()
	reservation, err := dispatcher.Reserve(context.Background(), dispatch.ReserveInput{
		UserID: "admin", PoolID: "switch-test", Callee: "+12025550199",
	}, []dispatch.Candidate{{
		PoolID: "switch-test", DeviceID: "wwan0", CardID: "card-1",
		ProfileID: "profile-target", NumberID: "number-target", Online: true, Enabled: true,
	}})
	if err != nil {
		t.Fatalf("reserve switch test lease: %v", err)
	}
	profile := resourcecatalog.SubscriptionProfile{ID: "profile-target", CardContainerID: "card-1", ICCID: "8901002", AID: "A0000000871002", Present: true}
	card := resourcecatalog.CardContainer{ID: "card-1", Type: resourcecatalog.CardContainerTypeEUICC, LastDeviceID: "wwan0"}
	return reservation.Lease, resourcecatalog.NumberPoolMemberView{
		NumberPoolMember: resourcecatalog.NumberPoolMember{NumberPoolID: "switch-test", PhoneNumberID: "number-target", Enabled: true},
		Number:           resourcecatalog.PhoneNumber{ID: "number-target", E164: "+12025550123", Status: "available"},
		Profile:          &profile,
		Card:             &card,
	}
}

func newManagedOutboundTestServer(t *testing.T) (*Server, *dispatch.Service) {
	return newManagedOutboundTestServerForDevice(t, "wwan0")
}

func newManagedOutboundTestServerForDevice(t *testing.T, deviceID string) (*Server, *dispatch.Service) {
	t.Helper()
	database, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "outbound.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(
		&resourcecatalog.CardContainer{}, &resourcecatalog.SubscriptionProfile{}, &resourcecatalog.PhoneNumber{},
		&resourcecatalog.NumberPool{}, &resourcecatalog.NumberPoolMember{}, &resourcecatalog.NumberPoolUser{},
		&resourcecatalog.DispatchPolicy{}, &resourcecatalog.AuditEvent{},
	); err != nil {
		t.Fatal(err)
	}
	if err := dispatch.Migrate(database); err != nil {
		t.Fatal(err)
	}
	dispatcher, err := dispatch.New(database, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	profileID := "profile-1"
	rows := []any{
		&resourcecatalog.CardContainer{ID: "card-1", Type: resourcecatalog.CardContainerTypePhysicalUICC, LastDeviceID: deviceID, LastObservedAt: now},
		&resourcecatalog.SubscriptionProfile{ID: profileID, CardContainerID: "card-1", IdentityKey: "iccid:8901001", ICCID: "8901001", Present: true, Active: true, LastObservedAt: now},
		&resourcecatalog.PhoneNumber{ID: "number-1", SubscriptionProfileID: &profileID, E164: "+12025550123", Status: "available"},
	}
	for _, row := range rows {
		if err := database.Create(row).Error; err != nil {
			t.Fatalf("create %T: %v", row, err)
		}
	}
	pool := device.NewPool(&config.Config{})
	worker := &device.Worker{ID: deviceID, Pool: pool}
	setNestedPrivateField(t, worker, []string{"state", "Identity", "ICCID"}, "8901001")
	setNestedPrivateField(t, worker, []string{"state", "Runtime", "Ready"}, true)
	setNestedPrivateField(t, worker, []string{"state", "Meta", "Healthy"}, true)
	pool.AttachWorkerForTest(worker)
	return &Server{pool: pool, dispatcher: dispatcher, resourceCatalog: resourcecatalog.NewService(database)}, dispatcher
}
