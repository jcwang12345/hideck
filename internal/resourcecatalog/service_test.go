package resourcecatalog

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestClassifyCardCapabilityRequiresEIDOrISDRForEUICC(t *testing.T) {
	tests := []struct {
		name        string
		observation CardObservation
		wantType    CardContainerType
		wantMulti   bool
	}{
		{
			name:        "unknown",
			observation: CardObservation{},
			wantType:    CardContainerTypeUnknown,
		},
		{
			name:        "inserted card without scan remains unknown",
			observation: CardObservation{CardPresent: true},
			wantType:    CardContainerTypeUnknown,
		},
		{
			name:        "ordinary physical USIM after negative eUICC scan",
			observation: CardObservation{CardPresent: true, PhysicalUICCConfirmed: true},
			wantType:    CardContainerTypePhysicalUICC,
		},
		{
			name:        "EID proves eUICC",
			observation: CardObservation{EID: "89 0490-32"},
			wantType:    CardContainerTypeEUICC,
		},
		{
			name:        "ISD-R proves eUICC",
			observation: CardObservation{ISDRAID: "A0000005591010FFFFFFFF8900000100"},
			wantType:    CardContainerTypeEUICC,
		},
		{
			name: "multiple profiles are capability evidence only",
			observation: CardObservation{Profiles: []ProfileObservation{
				{ICCID: "8901000000000000001"},
				{ICCID: "8901000000000000002"},
			}},
			wantType:  CardContainerTypeUnknown,
			wantMulti: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := ClassifyCardCapability(test.observation)
			if got.Type != test.wantType {
				t.Fatalf("Type = %q, want %q", got.Type, test.wantType)
			}
			if got.MultipleProfiles != test.wantMulti {
				t.Fatalf("MultipleProfiles = %v, want %v", got.MultipleProfiles, test.wantMulti)
			}
		})
	}
}

func TestSyncObservationCatalogsOrdinaryUSIMWithoutUsingICCIDAsContainerID(t *testing.T) {
	service := newTestService(t)
	ctx := context.Background()
	firstSeen := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)

	first, err := service.SyncObservation(ctx, CardObservation{
		DeviceID:              "modem-1",
		SlotID:                "slot-1",
		CardPresent:           true,
		PhysicalUICCConfirmed: true,
		ProfilesComplete:      true,
		ObservedAt:            firstSeen,
		Profiles: []ProfileObservation{{
			ICCID:               "8901 0000 0000 0000 001",
			IMSI:                "310260000000001",
			Name:                "Primary US",
			ServiceProviderName: "Example Mobile",
			Active:              true,
		}},
	})
	if err != nil {
		t.Fatalf("SyncObservation() error = %v", err)
	}
	if first.Type != CardContainerTypePhysicalUICC {
		t.Fatalf("Type = %q, want physical_uicc", first.Type)
	}
	if first.EID != nil {
		t.Fatalf("EID = %q, want nil", *first.EID)
	}
	if first.ID == first.Profiles[0].ICCID {
		t.Fatal("container ID must not be derived directly from ICCID")
	}
	if first.Profiles[0].Name != "Primary US" || first.Profiles[0].ServiceProvider != "Example Mobile" {
		t.Fatalf("profile metadata = %#v, want cached name and provider", first.Profiles[0])
	}

	second, err := service.SyncObservation(ctx, CardObservation{
		DeviceID:              "modem-1",
		SlotID:                "slot-1",
		CardPresent:           true,
		PhysicalUICCConfirmed: true,
		ProfilesComplete:      true,
		ObservedAt:            firstSeen.Add(time.Minute),
		Profiles: []ProfileObservation{{
			ICCID:  "8901000000000000002",
			IMSI:   "310260000000002",
			Active: true,
		}},
	})
	if err != nil {
		t.Fatalf("second SyncObservation() error = %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("container ID changed with ICCID: first=%q second=%q", first.ID, second.ID)
	}
	if len(second.Profiles) != 2 {
		t.Fatalf("profile history length = %d, want 2", len(second.Profiles))
	}
	profiles := profilesByICCID(second.Profiles)
	if profiles["8901000000000000001"].Present || profiles["8901000000000000001"].Active {
		t.Fatalf("old profile = %#v, want retained but inactive/not present", profiles["8901000000000000001"])
	}
	if !profiles["8901000000000000002"].Present || !profiles["8901000000000000002"].Active {
		t.Fatalf("new profile = %#v, want present and active", profiles["8901000000000000002"])
	}
}

func TestSyncObservationGroupsEUICCByEIDAndRetainsMultipleProfiles(t *testing.T) {
	service := newTestService(t)
	ctx := context.Background()
	eid := "89049032000001000000113509931049"

	first, err := service.SyncObservation(ctx, CardObservation{
		StableContainerKey: "reader-a/slot-1",
		DeviceID:           "reader-a",
		SlotID:             "slot-1",
		CardPresent:        true,
		EID:                "8904 9032-0000 0100 0000 1135 0993 1049",
		ISDRAID:            "A0000005591010FFFFFFFF8900000100",
		ProfilesComplete:   true,
		Profiles: []ProfileObservation{
			{ICCID: "8901000000000000011", AID: "A0000000871002", IMSI: "310260000000011", Active: true},
			{ICCID: "8901000000000000012", AID: "A0000000871002", IMSI: "310260000000012"},
		},
	})
	if err != nil {
		t.Fatalf("SyncObservation() error = %v", err)
	}
	if first.Type != CardContainerTypeEUICC {
		t.Fatalf("Type = %q, want euicc", first.Type)
	}
	if first.EID == nil || *first.EID != eid {
		t.Fatalf("EID = %#v, want %q", first.EID, eid)
	}
	if !first.Capabilities.HasISDR || !first.Capabilities.MultipleProfiles {
		t.Fatalf("Capabilities = %#v, want ISD-R and multiple-profile evidence", first.Capabilities)
	}
	if len(first.Profiles) != 2 {
		t.Fatalf("profiles = %d, want 2", len(first.Profiles))
	}

	// The same eUICC observed in a different reader still resolves by EID. A
	// switched ICCID is a new profile under the same physical container.
	second, err := service.SyncObservation(ctx, CardObservation{
		StableContainerKey: "reader-b/slot-2",
		DeviceID:           "reader-b",
		SlotID:             "slot-2",
		CardPresent:        true,
		EID:                eid,
		ProfilesComplete:   true,
		Profiles: []ProfileObservation{{
			ICCID:  "8901000000000000013",
			IMSI:   "310260000000013",
			Active: true,
		}},
	})
	if err != nil {
		t.Fatalf("second SyncObservation() error = %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("same EID produced different containers: first=%q second=%q", first.ID, second.ID)
	}
	if second.LastDeviceID != "reader-b" || second.LastSlotID != "slot-2" {
		t.Fatalf("last placement = %q/%q, want reader-b/slot-2", second.LastDeviceID, second.LastSlotID)
	}
	if !second.Capabilities.MultipleProfiles {
		t.Fatal("multiple-profile capability evidence must remain sticky")
	}
	if len(second.Profiles) != 3 {
		t.Fatalf("profile history length = %d, want 3", len(second.Profiles))
	}
}

func TestSyncObservationISDRWithoutEIDUsesStableContainerIdentity(t *testing.T) {
	service := newTestService(t)
	ctx := context.Background()
	eid := "89049032000001000000113509931050"

	first, err := service.SyncObservation(ctx, CardObservation{
		StableContainerKey: "pcsc-reader-1/slot-1",
		CardPresent:        true,
		ISDRAID:            "A0000005591010FFFFFFFF8900000100",
	})
	if err != nil {
		t.Fatalf("SyncObservation() error = %v", err)
	}
	if first.Type != CardContainerTypeEUICC || first.EID != nil {
		t.Fatalf("container = %#v, want EID-less eUICC", first)
	}

	second, err := service.SyncObservation(ctx, CardObservation{
		StableContainerKey: "pcsc-reader-1/slot-1",
		CardPresent:        true,
		ISDRAID:            "A0000005591010FFFFFFFF8900000100",
		Profiles:           []ProfileObservation{{ICCID: "8901000000000000021"}},
	})
	if err != nil {
		t.Fatalf("second SyncObservation() error = %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("stable EID-less container changed ID: first=%q second=%q", first.ID, second.ID)
	}

	// A later passive EID observation promotes the same stable container rather
	// than splitting it into a new physical card record.
	promoted, err := service.SyncObservation(ctx, CardObservation{
		StableContainerKey: "pcsc-reader-1/slot-1",
		CardPresent:        true,
		EID:                eid,
	})
	if err != nil {
		t.Fatalf("promotion SyncObservation() error = %v", err)
	}
	if promoted.ID != first.ID {
		t.Fatalf("EID promotion changed stable container: first=%q promoted=%q", first.ID, promoted.ID)
	}
	if promoted.EID == nil || *promoted.EID != eid {
		t.Fatalf("promoted EID = %#v, want %q", promoted.EID, eid)
	}
}

func TestSyncObservationReconcilesPrimarySlotWhenEUICCCacheArrives(t *testing.T) {
	service := newTestService(t)
	ctx := context.Background()
	initial, err := service.SyncObservation(ctx, CardObservation{
		StableContainerKey: "worker:modem-7/slot:primary",
		DeviceID:           "modem-7",
		SlotID:             "primary",
		CardPresent:        true,
		ProfilesComplete:   true,
		Profiles: []ProfileObservation{{
			ICCID: "8901000000000000071", IMSI: "310260000000071", Active: true,
		}},
	})
	if err != nil {
		t.Fatalf("initial SyncObservation() error = %v", err)
	}

	promoted, err := service.SyncObservation(ctx, CardObservation{
		StableContainerKey: "worker:modem-7/slot:A0000005591010FFFFFFFF8900000100",
		DeviceID:           "modem-7",
		SlotID:             "A0000005591010FFFFFFFF8900000100",
		CardPresent:        true,
		EID:                "89049032000001000000113509931071",
		ISDRAID:            "A0000005591010FFFFFFFF8900000100",
		ProfilesComplete:   true,
		Profiles: []ProfileObservation{{
			ICCID: "8901000000000000071", IMSI: "310260000000071", Name: "Travel", Active: true,
		}},
	})
	if err != nil {
		t.Fatalf("promoted SyncObservation() error = %v", err)
	}
	if promoted.ID != initial.ID {
		t.Fatalf("late eUICC cache split card: initial=%q promoted=%q", initial.ID, promoted.ID)
	}
	cards, err := service.List(ctx)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(cards) != 1 {
		t.Fatalf("card count = %d, want 1", len(cards))
	}
	if cards[0].Type != CardContainerTypeEUICC || cards[0].Profiles[0].Name != "Travel" {
		t.Fatalf("promoted card = %#v", cards[0])
	}
}

func TestSyncObservationUnknownAndRefusesICCIDFallback(t *testing.T) {
	service := newTestService(t)
	ctx := context.Background()

	unknown, err := service.SyncObservation(ctx, CardObservation{
		StableContainerKey: "reader-with-no-card/slot-1",
	})
	if err != nil {
		t.Fatalf("SyncObservation() error = %v", err)
	}
	if unknown.Type != CardContainerTypeUnknown {
		t.Fatalf("Type = %q, want unknown", unknown.Type)
	}

	_, err = service.SyncObservation(ctx, CardObservation{
		Profiles: []ProfileObservation{{ICCID: "8901000000000000099"}},
	})
	if !errors.Is(err, ErrStableContainerIdentityRequired) {
		t.Fatalf("SyncObservation() error = %v, want ErrStableContainerIdentityRequired", err)
	}
}

func TestListAndGetReturnReadOnlyDTOCopies(t *testing.T) {
	service := newTestService(t)
	ctx := context.Background()
	created, err := service.SyncObservation(ctx, CardObservation{
		DeviceID:              "modem-read-only",
		CardPresent:           true,
		PhysicalUICCConfirmed: true,
		ProfilesComplete:      true,
		Profiles: []ProfileObservation{{
			ICCID: "8901000000000000041",
		}},
	})
	if err != nil {
		t.Fatalf("SyncObservation() error = %v", err)
	}

	listed, err := service.List(ctx)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(listed) != 1 || listed[0].ID != created.ID {
		t.Fatalf("List() = %#v, want container %q", listed, created.ID)
	}
	listed[0].Type = CardContainerTypeUnknown
	listed[0].Profiles[0].ICCID = "mutated"

	loaded, err := service.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if loaded.Type != CardContainerTypePhysicalUICC {
		t.Fatalf("stored Type = %q after DTO mutation", loaded.Type)
	}
	if loaded.Profiles[0].ICCID != "8901000000000000041" {
		t.Fatalf("stored ICCID = %q after DTO mutation", loaded.Profiles[0].ICCID)
	}

	_, err = service.Get(ctx, "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get(missing) error = %v, want ErrNotFound", err)
	}
}

func newTestService(t *testing.T) *Service {
	t.Helper()
	database, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "resource-catalog.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm.Open() error = %v", err)
	}
	if err := database.AutoMigrate(
		&CardContainer{},
		&SubscriptionProfile{},
		&PhoneNumber{},
		&NumberPool{},
		&NumberPoolMember{},
		&NumberPoolUser{},
		&DispatchPolicy{},
		&AuditEvent{},
	); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	return NewService(database)
}

func profilesByICCID(profiles []SubscriptionProfileDTO) map[string]SubscriptionProfileDTO {
	result := make(map[string]SubscriptionProfileDTO, len(profiles))
	for _, profile := range profiles {
		result[profile.ICCID] = profile
	}
	return result
}
