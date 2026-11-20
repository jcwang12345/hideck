package api

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/yibaiba/hideck/internal/dispatch"
	"github.com/yibaiba/hideck/internal/phone"
	"github.com/yibaiba/hideck/internal/resourcecatalog"
	"gorm.io/gorm"
)

func TestDispatchTerminalOutcomeUsesPoolThresholdAndIsIdempotent(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "dispatch-api.db")), &gorm.Config{})
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
	now := time.Date(2026, 11, 20, 10, 0, 0, 0, time.FixedZone("CST", 8*3600))
	dispatcher, err := dispatch.New(database, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&resourcecatalog.NumberPool{ID: "pool-1", Name: "main", Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&resourcecatalog.DispatchPolicy{
		ID: "policy-1", NumberPoolID: "pool-1", Name: "threshold", Strategy: "active_registered_least_used",
		Enabled: true, ConfigJSON: `{"rejection_threshold":2}`,
	}).Error; err != nil {
		t.Fatal(err)
	}
	server := &Server{dispatcher: dispatcher, resourceCatalog: resourcecatalog.NewService(database)}
	declined := func(callID string) {
		server.handleDispatchTerminalCall(phone.CallRecord{
			CallID: callID, UserID: "user-1", PoolID: "pool-1", CardID: "card-1",
			ProfileID: "profile-1", NumberID: "number-1", ResourceLeaseID: "lease-" + callID,
			Status: phone.StatusDeclined, OutcomeKind: "declined", SIPCode: 603, Terminator: "remote", EndedAt: &now,
		})
	}
	declined("call-1")
	declined("call-2")
	declined("call-2")
	disabled, stat, err := dispatcher.CardDisabledToday(context.Background(), "card-1")
	if err != nil {
		t.Fatal(err)
	}
	if !disabled || stat.RemoteDeclineCount != 2 || stat.CallCount != 2 {
		t.Fatalf("disabled=%v stat=%+v", disabled, stat)
	}
	server.handleDispatchTerminalCall(phone.CallRecord{
		CallID: "call-busy", UserID: "user-1", PoolID: "pool-1", CardID: "card-1",
		ResourceLeaseID: "lease-busy", Status: phone.StatusBusy, OutcomeKind: "busy", SIPCode: 486, Terminator: "remote", EndedAt: &now,
	})
	_, stat, err = dispatcher.CardDisabledToday(context.Background(), "card-1")
	if err != nil {
		t.Fatal(err)
	}
	if stat.RemoteDeclineCount != 2 || stat.CallCount != 3 {
		t.Fatalf("busy changed rejection count: %+v", stat)
	}
	var autoDisableAudits int64
	if err := database.Model(&resourcecatalog.AuditEvent{}).Where("action = ?", "card.auto_disabled").Count(&autoDisableAudits).Error; err != nil {
		t.Fatal(err)
	}
	if autoDisableAudits != 1 {
		t.Fatalf("auto-disable audits=%d want 1", autoDisableAudits)
	}
}
