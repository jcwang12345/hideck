package resourcecatalog

import (
	"context"
	"testing"
)

func TestNumberPoolManagementAndUserVisibility(t *testing.T) {
	service := newTestService(t)
	ctx := context.Background()
	pool, err := service.CreateNumberPool(ctx, "admin", CreateNumberPoolInput{Name: "US primary"})
	if err != nil {
		t.Fatal(err)
	}
	card, err := service.SyncObservation(ctx, CardObservation{
		StableContainerKey: "reader/slot", CardPresent: true, PhysicalUICCConfirmed: true,
		ProfilesComplete: true, Profiles: []ProfileObservation{{ICCID: "8901001", Active: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	number, err := service.UpsertPhoneNumber(ctx, "admin", PhoneNumberInput{SubscriptionProfileID: card.Profiles[0].ID, E164: "+12025550123", Label: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ReplacePoolMembers(ctx, "admin", pool.ID, []PoolMemberInput{{PhoneNumberID: number.ID}}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ReplacePoolUsers(ctx, "admin", pool.ID, []PoolUserInput{{UserID: "member-1"}}); err != nil {
		t.Fatal(err)
	}
	memberPools, err := service.ListNumberPools(ctx, "member-1", false)
	if err != nil || len(memberPools) != 1 || len(memberPools[0].Members) != 1 {
		t.Fatalf("member pools=%+v err=%v", memberPools, err)
	}
	if memberPools[0].Members[0].Card == nil || memberPools[0].Members[0].Card.ID != card.ID {
		t.Fatalf("member card=%+v", memberPools[0].Members[0].Card)
	}
	otherPools, err := service.ListNumberPools(ctx, "member-2", false)
	if err != nil || len(otherPools) != 0 {
		t.Fatalf("other pools=%+v err=%v", otherPools, err)
	}
	adminPools, err := service.ListNumberPools(ctx, "admin", true)
	if err != nil || len(adminPools) != 1 || len(adminPools[0].Users) != 1 {
		t.Fatalf("admin pools=%+v err=%v", adminPools, err)
	}
}

func TestReplacePoolMembersIsAtomicAndAudited(t *testing.T) {
	service := newTestService(t)
	ctx := context.Background()
	pool, err := service.CreateNumberPool(ctx, "admin", CreateNumberPoolInput{Name: "pool"})
	if err != nil {
		t.Fatal(err)
	}
	number, err := service.UpsertPhoneNumber(ctx, "admin", PhoneNumberInput{E164: "+12025550124"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ReplacePoolMembers(ctx, "admin", pool.ID, []PoolMemberInput{{PhoneNumberID: number.ID}, {PhoneNumberID: "missing"}}); err == nil {
		t.Fatal("ReplacePoolMembers accepted missing number")
	}
	view, err := service.GetNumberPool(ctx, pool.ID, "admin", true)
	if err != nil || len(view.Members) != 0 {
		t.Fatalf("members=%+v err=%v", view.Members, err)
	}
	if _, err := service.ReplacePoolMembers(ctx, "admin", pool.ID, []PoolMemberInput{{PhoneNumberID: number.ID}}); err != nil {
		t.Fatal(err)
	}
	events, err := service.ListAuditEvents(ctx, 20)
	if err != nil || len(events) < 3 {
		t.Fatalf("events=%+v err=%v", events, err)
	}
}

func TestPhoneNumberRequiresE164AndPolicyJSON(t *testing.T) {
	service := newTestService(t)
	ctx := context.Background()
	if _, err := service.UpsertPhoneNumber(ctx, "admin", PhoneNumberInput{E164: "2025550123"}); err == nil {
		t.Fatal("accepted non-E164 number")
	}
	pool, err := service.CreateNumberPool(ctx, "admin", CreateNumberPoolInput{Name: "pool"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpsertDispatchPolicy(ctx, "admin", DispatchPolicyInput{NumberPoolID: pool.ID, Name: "default", ConfigJSON: "{"}); err == nil {
		t.Fatal("accepted invalid JSON")
	}
	if _, err := service.UpsertDispatchPolicy(ctx, "admin", DispatchPolicyInput{NumberPoolID: pool.ID, Name: "default", ConfigJSON: `{"decline_threshold":3}`}); err != nil {
		t.Fatal(err)
	}
}
