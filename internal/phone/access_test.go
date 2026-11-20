package phone

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/iniwex5/vowifi-go/runtimehost/voicehost"
)

func TestScopedActiveCallsAndControlsEnforceUserOwnership(t *testing.T) {
	gateway := newFakeVoiceGateway()
	service := &Service{
		gateway: gateway,
		events:  newEventHub(),
		calls: map[string]*activeCall{
			"owned-a": {
				view:   CallView{CallID: "owned-a", DeviceID: "dev-a", UserID: "user-a", Status: StatusConnected},
				record: CallRecord{CallID: "owned-a", DeviceID: "dev-a", UserID: "user-a", Status: StatusConnected},
				owner:  "user-a", lease: "lease-a",
			},
			"owned-b": {
				view:   CallView{CallID: "owned-b", DeviceID: "dev-b", UserID: "user-b", Status: StatusConnected},
				record: CallRecord{CallID: "owned-b", DeviceID: "dev-b", UserID: "user-b", Status: StatusConnected},
				owner:  "user-b", lease: "lease-b",
			},
			"unassigned": {
				view:   CallView{CallID: "unassigned", DeviceID: "dev-c", Status: StatusRinging},
				record: CallRecord{CallID: "unassigned", DeviceID: "dev-c", Status: StatusRinging},
			},
		},
	}

	memberA := AccessScope{UserID: "user-a"}
	calls, err := service.ActiveScoped(memberA, "lease-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0].CallID != "owned-a" {
		t.Fatalf("member calls = %+v", calls)
	}
	adminCalls, err := service.ActiveScoped(AccessScope{UserID: "admin", Administrator: true}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(adminCalls) != 3 {
		t.Fatalf("admin calls = %+v", adminCalls)
	}

	if err := service.DTMFScoped(AccessScope{UserID: "user-b"}, "owned-a", "lease-a", "5"); !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("cross-user DTMF error = %v", err)
	}
	if err := service.DTMFScoped(AccessScope{UserID: "admin", Administrator: true}, "owned-a", "lease-a", "5"); err != nil {
		t.Fatalf("admin DTMF: %v", err)
	}
	select {
	case forwarded := <-gateway.dtmfCalls:
		if forwarded != "owned-a:5" {
			t.Fatalf("forwarded DTMF = %q", forwarded)
		}
	case <-time.After(time.Second):
		t.Fatal("admin DTMF was not forwarded")
	}
}

func TestScopedHistoryDeleteAndRecordingAuthorization(t *testing.T) {
	store := newMemoryCallStore()
	ctx := context.Background()
	for _, record := range []CallRecord{
		{ID: 1, CallID: "call-a", DeviceID: "dev-a", UserID: "user-a", Status: StatusCompleted, RecordingName: "call_a.mp3"},
		{ID: 2, CallID: "call-b", DeviceID: "dev-b", UserID: "user-b", Status: StatusCompleted, RecordingName: "call_b.mp3"},
	} {
		if err := store.Upsert(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	service := &Service{store: store, ctx: ctx, deletedRecords: make(map[string]struct{})}
	memberA := AccessScope{UserID: "user-a"}

	records, err := service.HistoryScoped(ctx, memberA, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].CallID != "call-a" {
		t.Fatalf("member history = %+v", records)
	}
	adminRecords, err := service.HistoryScoped(ctx, AccessScope{UserID: "admin", Administrator: true}, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(adminRecords) != 2 {
		t.Fatalf("admin history = %+v", adminRecords)
	}

	deleted, err := service.DeleteHistoryScoped(ctx, memberA, []uint64{2})
	if err != nil || deleted != 0 {
		t.Fatalf("cross-user delete = %d, %v", deleted, err)
	}
	if err := service.AuthorizeRecording(ctx, memberA, "call_b.mp3"); !errors.Is(err, ErrRecordingNotFound) {
		t.Fatalf("cross-user recording error = %v", err)
	}
	if err := service.AuthorizeRecording(ctx, memberA, "call_a.mp3"); err != nil {
		t.Fatalf("owned recording: %v", err)
	}
	if err := service.AuthorizeRecording(ctx, AccessScope{UserID: "admin", Administrator: true}, "call_b.mp3"); err != nil {
		t.Fatalf("admin recording: %v", err)
	}
	deleted, err = service.DeleteHistoryScoped(ctx, memberA, []uint64{1, 2})
	if err != nil || deleted != 1 {
		t.Fatalf("owned delete = %d, %v", deleted, err)
	}
	remaining, err := service.History(ctx, 50)
	if err != nil || len(remaining) != 1 || remaining[0].CallID != "call-b" {
		t.Fatalf("remaining = %+v, %v", remaining, err)
	}
}

func TestScopedEventSubscriptionNeverQueuesOtherUsersEvents(t *testing.T) {
	service := &Service{events: newEventHub()}
	service.events.publish(Event{Type: "call_started", Call: CallView{CallID: "a-1", UserID: "user-a"}})
	service.events.publish(Event{Type: "call_started", Call: CallView{CallID: "b-1", UserID: "user-b"}})
	backlog, stream, cancel, err := service.SubscribeScoped(AccessScope{UserID: "user-a"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	if len(backlog) != 1 || backlog[0].Call.CallID != "a-1" {
		t.Fatalf("member backlog = %+v", backlog)
	}

	for index := 0; index < subscriberBufferSize*2; index++ {
		service.events.publish(Event{Type: "call_ringing", Call: CallView{CallID: "b-live", UserID: "user-b"}})
	}
	service.events.publish(Event{Type: "call_ringing", Call: CallView{CallID: "a-live", UserID: "user-a"}})
	select {
	case event, open := <-stream:
		if !open || event.Call.CallID != "a-live" {
			t.Fatalf("member live event = %+v, open=%v", event, open)
		}
	case <-time.After(time.Second):
		t.Fatal("owned live event was not delivered")
	}
}

func TestTerminalObserverRunsOnceAfterPersistence(t *testing.T) {
	store := newMemoryCallStore()
	observed := make(chan CallRecord, 2)
	service := &Service{
		store: store, ctx: context.Background(), events: newEventHub(),
		calls: map[string]*activeCall{
			"terminal": {
				view: CallView{CallID: "terminal", DeviceID: "dev", UserID: "user-a", Direction: "outbound", Status: StatusCalling},
				record: CallRecord{
					CallID: "terminal", DeviceID: "dev", UserID: "user-a", Direction: "outbound", Status: StatusCalling,
				},
				terminalDone: make(chan struct{}),
			},
		},
		deviceCalls:       map[string]string{"dev": "terminal"},
		mediaCalls:        make(map[string]string),
		pendingMediaDrops: make(map[string]*pendingMediaDrop),
		pendingEvents:     make(map[string][]voicehost.CallEvent),
		terminalSeen:      make(map[string]struct{}),
		deletedRecords:    make(map[string]struct{}),
	}
	service.SetTerminalObserver(func(record CallRecord) {
		if persisted := store.record(record.CallID); persisted.Status != record.Status {
			t.Errorf("observer ran before persistence: persisted=%+v record=%+v", persisted, record)
		}
		observed <- record
	})
	event := voicehost.CallEvent{Type: "CallEnded", CallID: "terminal", DeviceID: "dev", Reason: "remote_bye", Time: time.Now()}
	service.finishCall(event)
	service.finishCall(event)
	select {
	case record := <-observed:
		if record.CallID != "terminal" || record.Status != StatusFailed {
			t.Fatalf("terminal record = %+v", record)
		}
	case <-time.After(time.Second):
		t.Fatal("terminal observer was not called")
	}
	select {
	case duplicate := <-observed:
		t.Fatalf("terminal observer called twice: %+v", duplicate)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestServiceOptionsInstallsTerminalObserver(t *testing.T) {
	gateway := newFakeVoiceGateway()
	observed := make(chan CallRecord, 1)
	service, err := NewService(ServiceOptions{
		Gateway: gateway, WebRTCUDPAddress: "127.0.0.1:0",
		OnTerminal: func(record CallRecord) { observed <- record },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := service.Close(ctx); err != nil {
			t.Errorf("close phone service: %v", err)
		}
	})
	gateway.emitIncoming(voicehost.IncomingCall{DeviceID: "dev", CallID: "option-call", ReceivedAt: time.Now()})
	gateway.emitEvent(voicehost.CallEvent{Type: "CallCanceled", DeviceID: "dev", CallID: "option-call", Time: time.Now()})
	select {
	case record := <-observed:
		if record.CallID != "option-call" {
			t.Fatalf("observed = %+v", record)
		}
	case <-time.After(time.Second):
		t.Fatal("ServiceOptions.OnTerminal was not called")
	}
}

func TestServiceCloseWaitsForTerminalObserver(t *testing.T) {
	gateway := newFakeVoiceGateway()
	started := make(chan struct{})
	release := make(chan struct{})
	service, err := NewService(ServiceOptions{
		Gateway: gateway, WebRTCUDPAddress: "127.0.0.1:0",
		OnTerminal: func(CallRecord) {
			close(started)
			<-release
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	service.notifyTerminal(CallRecord{CallID: "close-observer"})
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("terminal observer did not start")
	}

	closed := make(chan error, 1)
	go func() { closed <- service.Close(context.Background()) }()
	select {
	case err := <-closed:
		t.Fatalf("service closed before terminal observer completed: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("close phone service: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("service did not close after terminal observer completed")
	}
}
