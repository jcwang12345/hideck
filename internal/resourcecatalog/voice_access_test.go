package resourcecatalog

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestResolveCurrentVoiceResourceUsesDeviceAndCanonicalICCID(t *testing.T) {
	service := newTestService(t)
	ctx := context.Background()
	now := time.Now().UTC()
	card := CardContainer{ID: "card-1", Type: CardContainerTypePhysicalUICC, LastDeviceID: "wwan0", LastObservedAt: now}
	profile := SubscriptionProfile{ID: "profile-1", CardContainerID: card.ID, IdentityKey: "iccid:8901001", ICCID: "8901001F", Present: true, Active: true, LastObservedAt: now}
	profileID := profile.ID
	number := PhoneNumber{ID: "number-1", SubscriptionProfileID: &profileID, E164: "+12025550123", Status: "available"}
	pool := NumberPool{ID: "pool-1", Name: "US", Enabled: true}
	member := NumberPoolMember{ID: "member-1", NumberPoolID: pool.ID, PhoneNumberID: number.ID, Enabled: true, Priority: 4, Weight: 1}
	for _, row := range []any{&card, &profile, &number, &pool, &member} {
		if err := service.database.Create(row).Error; err != nil {
			t.Fatalf("create %T: %v", row, err)
		}
	}

	got, err := service.ResolveCurrentVoiceResource(ctx, "wwan0", "8901 001", "")
	if err != nil {
		t.Fatalf("ResolveCurrentVoiceResource() error = %v", err)
	}
	if got.Card.ID != card.ID || got.Profile.ID != profile.ID || got.Number.ID != number.ID {
		t.Fatalf("resolved = %#v", got)
	}
	if got.Pool == nil || got.Pool.ID != pool.ID || got.Member == nil || got.Member.Priority != 4 {
		t.Fatalf("pool membership = %#v/%#v", got.Pool, got.Member)
	}
}

func TestResolveCurrentVoiceResourceRejectsDisabledOrUnmappedNumber(t *testing.T) {
	service := newTestService(t)
	ctx := context.Background()
	now := time.Now().UTC()
	card := CardContainer{ID: "card-2", Type: CardContainerTypePhysicalUICC, LastDeviceID: "wwan1", LastObservedAt: now}
	profile := SubscriptionProfile{ID: "profile-2", CardContainerID: card.ID, IdentityKey: "iccid:8901002", ICCID: "8901002", Present: true, Active: true, LastObservedAt: now}
	profileID := profile.ID
	number := PhoneNumber{ID: "number-2", SubscriptionProfileID: &profileID, E164: "+12025550124", Status: "disabled"}
	for _, row := range []any{&card, &profile, &number} {
		if err := service.database.Create(row).Error; err != nil {
			t.Fatalf("create %T: %v", row, err)
		}
	}
	if _, err := service.ResolveCurrentVoiceResource(ctx, "wwan1", profile.ICCID, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}
	if _, err := service.ResolveCurrentVoiceResource(ctx, "missing", "", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing device error = %v, want ErrNotFound", err)
	}
}
