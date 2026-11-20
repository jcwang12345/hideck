package db

import (
	"context"
	"errors"
	"testing"
	"time"

	"gorm.io/gorm"
)

func TestSMSAccessScopeKeepsRealICCIDsSeparateWithSharedIMSI(t *testing.T) {
	openTestDB(t)
	now := time.Now().UTC()
	rows := []SMS{
		{ICCID: "CARD-A", IMSI: "shared", LocalPhone: "+15550001", Peer: "+100", Type: smsTypeIncoming, Status: smsStatusUnread, Content: "a", Timestamp: now},
		{ICCID: "CARD-B", IMSI: "shared", LocalPhone: "+15550002", Peer: "+200", Type: smsTypeIncoming, Status: smsStatusUnread, Content: "b", Timestamp: now.Add(time.Second)},
		{ICCID: "imsi:shared", IMSI: "shared", Peer: "+300", Type: smsTypeIncoming, Status: smsStatusUnread, Content: "legacy", Timestamp: now.Add(2 * time.Second)},
		{LocalPhone: "+15550001", Peer: "+400", Type: smsTypeIncoming, Status: smsStatusUnread, Content: "phone fallback", Timestamp: now.Add(3 * time.Second)},
		{Peer: "+500", Type: smsTypeIncoming, Status: smsStatusUnread, Content: "unassigned", Timestamp: now.Add(4 * time.Second)},
	}
	for index := range rows {
		if err := DB.Create(&rows[index]).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, index := range []int{0, 1, 2} {
		contact := SMSContact{ICCID: rows[index].ICCID, IMSI: rows[index].IMSI, Peer: rows[index].Peer, LastContent: rows[index].Content, LastTimestamp: rows[index].Timestamp, UnreadCount: 1}
		if err := DB.Create(&contact).Error; err != nil {
			t.Fatal(err)
		}
	}
	scope := NewSMSAccessScope([]SMSAccessIdentity{{ICCID: "CARD-A", IMSI: "shared", LocalPhone: "+15550001"}})
	result, err := GetSMSNotificationsScoped(context.Background(), scope, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.UnreadCount != 3 || result.Cursor != rows[3].ID {
		t.Fatalf("notifications=%+v; want A, legacy and phone fallback only", result)
	}
	if _, err := GetSMSMessageTargetScoped(context.Background(), scope, rows[1].ID); !errors.Is(err, ErrSMSNotFound) {
		t.Fatalf("other card target err=%v, want ErrSMSNotFound", err)
	}
	if _, err := GetSMSMessageTargetScoped(context.Background(), scope, rows[0].ID); err != nil {
		t.Fatalf("authorized target err=%v", err)
	}
	contacts, err := GetSMSContactsScoped(scope, 50, nil, "")
	if err != nil || len(contacts) != 2 {
		t.Fatalf("scoped contacts=%+v err=%v", contacts, err)
	}
	if _, _, _, err := DeleteSMSByIDScoped(scope, rows[1].ID); !errors.Is(err, ErrSMSNotFound) {
		t.Fatalf("other card delete err=%v, want ErrSMSNotFound", err)
	}
	var count int64
	if err := DB.Model(&SMS{}).Where("id = ?", rows[1].ID).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("other card row count=%d err=%v", count, err)
	}
	for _, delivery := range []SMSDelivery{
		{MessageID: "delivery-a", ICCID: "CARD-A", IMSI: "shared", Peer: "+100", State: SMSDeliveryStateAcked},
		{MessageID: "delivery-b", ICCID: "CARD-B", IMSI: "shared", Peer: "+200", State: SMSDeliveryStateAcked},
	} {
		if err := DB.Create(&delivery).Error; err != nil {
			t.Fatal(err)
		}
	}
	if _, err := GetSMSDeliveryStatusScoped(scope, "delivery-a"); err != nil {
		t.Fatalf("authorized delivery err=%v", err)
	}
	if _, err := GetSMSDeliveryStatusScoped(scope, "delivery-b"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("other card delivery err=%v", err)
	}
}

func TestScopedIncomingSMSFiltersBeforeSubscriberBuffer(t *testing.T) {
	scope := NewSMSAccessScope([]SMSAccessIdentity{{ICCID: "CARD-A"}})
	stream, cancel := SubscribeIncomingSMSScoped(scope)
	defer cancel()
	for id := uint(1); id <= incomingSMSSubscriberBuffer+10; id++ {
		incomingSMS.publish(SMS{ID: id, ICCID: "CARD-B", Type: smsTypeIncoming})
	}
	want := SMS{ID: 999, ICCID: "CARD-A", Type: smsTypeIncoming, Content: "authorized"}
	incomingSMS.publish(want)
	select {
	case got, ok := <-stream:
		if !ok || got.ID != want.ID {
			t.Fatalf("event=%+v open=%v", got, ok)
		}
	case <-time.After(time.Second):
		t.Fatal("authorized event was blocked by unrelated traffic")
	}
}
