package db

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/yibaiba/hideck/internal/phone"
	"gorm.io/gorm"
)

func newVoiceCallTestStore(t *testing.T) *VoiceCallStore {
	t.Helper()
	database, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "calls.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&VoiceCallRecord{}); err != nil {
		t.Fatal(err)
	}
	return NewVoiceCallStore(database)
}

func TestVoiceCallStoreUpsertsAndListsNewestFirst(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "calls.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&VoiceCallRecord{}); err != nil {
		t.Fatal(err)
	}
	store := NewVoiceCallStore(database)
	ctx := context.Background()
	older := time.Now().Add(-time.Minute).UTC()
	newer := time.Now().UTC()
	if err := store.Upsert(ctx, phone.CallRecord{
		CallID: "call-old", DeviceID: "dev-1", Direction: "inbound",
		Status: phone.StatusRinging, StartedAt: older,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Upsert(ctx, phone.CallRecord{
		CallID: "call-new", DeviceID: "dev-2", ICCID: "iccid-2", Direction: "outbound",
		Peer: "888", Status: phone.StatusCalling, StartedAt: newer,
	}); err != nil {
		t.Fatal(err)
	}
	endedAt := newer.Add(5 * time.Second)
	if err := store.Upsert(ctx, phone.CallRecord{
		CallID: "call-new", DeviceID: "dev-2", ICCID: "iccid-2", Direction: "outbound",
		Peer: "888", Status: phone.StatusCompleted, StartedAt: newer, AnsweredAt: &newer,
		EndedAt: &endedAt, DurationSeconds: 5, EndReason: "local_hangup", Codec: "PCMU",
		RecordingName: "call-new.mp3", PCAPName: "call-new.pcap",
	}); err != nil {
		t.Fatal(err)
	}
	records, err := store.List(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 || records[0].CallID != "call-new" || records[1].CallID != "call-old" {
		t.Fatalf("records = %+v", records)
	}
	if records[0].Status != phone.StatusCompleted || records[0].RecordingName != "call-new.mp3" || records[0].PCAPName != "call-new.pcap" {
		t.Fatalf("updated record = %+v", records[0])
	}
}

func TestVoiceCallStoreAbandonsIncompleteRecords(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "calls.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&VoiceCallRecord{}); err != nil {
		t.Fatal(err)
	}
	store := NewVoiceCallStore(database)
	ctx := context.Background()
	started := time.Now().Add(-20 * time.Second).UTC()
	if err := store.Upsert(ctx, phone.CallRecord{
		CallID: "ghost-ring", DeviceID: "wwan0", Direction: "inbound",
		Peer: "+1555550100", Status: phone.StatusRinging, StartedAt: started,
	}); err != nil {
		t.Fatal(err)
	}
	ended := started.Add(20 * time.Second)
	if err := store.AbandonIncomplete(ctx, ended, "process_restart"); err != nil {
		t.Fatal(err)
	}
	records, err := store.List(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Status != phone.StatusMissed || records[0].EndReason != "process_restart" {
		t.Fatalf("abandoned = %+v", records)
	}
	if records[0].EndedAt == nil || records[0].DurationSeconds != 20 {
		t.Fatalf("abandoned timestamps = %+v", records[0])
	}
}

func TestVoiceCallStoreKeepsBothRecordsWhenQMISlotIsReused(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "calls.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&VoiceCallRecord{}); err != nil {
		t.Fatal(err)
	}
	store := NewVoiceCallStore(database)
	ctx := context.Background()
	firstStart := time.Now().Add(-2 * time.Minute).UTC()
	firstEnd := firstStart.Add(116 * time.Second)
	if err := store.Upsert(ctx, phone.CallRecord{
		CallID: "volte-wwan0-1-100-1", DeviceID: "wwan0", Direction: "inbound",
		Peer: "13200000002", Status: phone.StatusRejected, StartedAt: firstStart,
		EndedAt: &firstEnd, DurationSeconds: 116, EndReason: "rejected",
	}); err != nil {
		t.Fatal(err)
	}
	secondStart := time.Now().UTC()
	secondEnd := secondStart.Add(29 * time.Second)
	if err := store.Upsert(ctx, phone.CallRecord{
		CallID: "volte-wwan0-1-200-2", DeviceID: "wwan0", Direction: "outbound",
		Peer: "10000", Status: phone.StatusFailed, StartedAt: secondStart,
		EndedAt: &secondEnd, DurationSeconds: 29, EndReason: "local_hangup",
	}); err != nil {
		t.Fatal(err)
	}
	records, err := store.List(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("records = %d, want 2 (QMI slot reuse must not overwrite history)", len(records))
	}
	if records[0].CallID != "volte-wwan0-1-200-2" || records[1].CallID != "volte-wwan0-1-100-1" {
		t.Fatalf("records = %+v", records)
	}
	if records[1].Status != phone.StatusRejected || records[1].Peer != "13200000002" {
		t.Fatalf("first inbound overwritten: %+v", records[1])
	}
}

func TestVoiceCallStoreDeletesSelectedFinishedRecords(t *testing.T) {
	store := newVoiceCallTestStore(t)
	ctx := context.Background()
	for _, callID := range []string{"keep", "delete-one", "delete-two"} {
		if err := store.Upsert(ctx, phone.CallRecord{
			CallID: callID, DeviceID: "wwan0", Direction: "outbound",
			Status: phone.StatusCompleted, StartedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	records, err := store.List(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]uint64, 0, 2)
	for _, record := range records {
		if record.CallID != "keep" {
			ids = append(ids, record.ID)
		}
	}
	deleted, err := store.Delete(ctx, ids)
	if err != nil {
		t.Fatal(err)
	}
	if len(deleted) != 2 {
		t.Fatalf("deleted = %d, want 2", len(deleted))
	}
	remaining, err := store.List(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 1 || remaining[0].CallID != "keep" {
		t.Fatalf("remaining = %+v", remaining)
	}
}

func TestVoiceCallStoreRejectsBatchContainingActiveRecord(t *testing.T) {
	store := newVoiceCallTestStore(t)
	ctx := context.Background()
	for callID, status := range map[string]string{
		"finished": phone.StatusCompleted,
		"active":   phone.StatusConnected,
	} {
		if err := store.Upsert(ctx, phone.CallRecord{
			CallID: callID, DeviceID: "wwan0", Direction: "outbound",
			Status: status, StartedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	records, err := store.List(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]uint64, 0, len(records))
	for _, record := range records {
		ids = append(ids, record.ID)
	}
	if _, err := store.Delete(ctx, ids); !errors.Is(err, phone.ErrActiveCallHistoryDelete) {
		t.Fatalf("Delete error = %v", err)
	}
	remaining, err := store.List(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 2 {
		t.Fatalf("remaining = %d, want 2", len(remaining))
	}
}

func TestVoiceCallStoreScopesHistoryDeleteAndRecordingByUser(t *testing.T) {
	store := newVoiceCallTestStore(t)
	ctx := context.Background()
	started := time.Now().UTC()
	for _, record := range []phone.CallRecord{
		{CallID: "user-a-call", DeviceID: "dev-a", UserID: "user-a", Direction: "outbound", Status: phone.StatusCompleted, StartedAt: started, RecordingName: "call_a.mp3"},
		{CallID: "user-b-call", DeviceID: "dev-b", UserID: "user-b", Direction: "outbound", Status: phone.StatusConnected, StartedAt: started.Add(time.Second), RecordingName: "call_b.mp3"},
	} {
		if err := store.Upsert(ctx, record); err != nil {
			t.Fatal(err)
		}
	}

	userA, err := store.ListByUser(ctx, "user-a", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(userA) != 1 || userA[0].CallID != "user-a-call" {
		t.Fatalf("user-a history = %+v", userA)
	}
	if _, found, err := store.FindRecording(ctx, "call_b.mp3", "user-a"); err != nil || found {
		t.Fatalf("cross-user recording found=%v err=%v", found, err)
	}
	if record, found, err := store.FindRecording(ctx, "call_b.mp3", ""); err != nil || !found || record.UserID != "user-b" {
		t.Fatalf("admin recording = %+v found=%v err=%v", record, found, err)
	}

	all, err := store.List(ctx, 50)
	if err != nil {
		t.Fatal(err)
	}
	var userAID, userBID uint64
	for _, record := range all {
		switch record.UserID {
		case "user-a":
			userAID = record.ID
		case "user-b":
			userBID = record.ID
		}
	}
	deleted, err := store.DeleteByUser(ctx, "user-a", []uint64{userAID, userBID})
	if err != nil {
		t.Fatal(err)
	}
	if len(deleted) != 1 || deleted[0].UserID != "user-a" {
		t.Fatalf("scoped deleted = %+v", deleted)
	}
	remaining, err := store.List(ctx, 50)
	if err != nil || len(remaining) != 1 || remaining[0].UserID != "user-b" {
		t.Fatalf("remaining = %+v err=%v", remaining, err)
	}
}
