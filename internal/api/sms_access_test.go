package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/iniwex5/vowifi-go/runtimehost/voicehost"
	"github.com/yibaiba/hideck/internal/config"
	"github.com/yibaiba/hideck/internal/db"
	"github.com/yibaiba/hideck/internal/device"
	"github.com/yibaiba/hideck/internal/dispatch"
	"github.com/yibaiba/hideck/internal/identity"
	"github.com/yibaiba/hideck/internal/phone"
	"github.com/yibaiba/hideck/internal/resourcecatalog"
)

func smsAdminTestMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		installPrincipal(c, identity.Principal{UserID: "legacy-admin", Username: "admin", Role: identity.RoleAdmin}, identity.WebSession{})
		c.Next()
	}
}

func smsMemberTestMiddleware(userID string) gin.HandlerFunc {
	return func(c *gin.Context) {
		installPrincipal(c, identity.Principal{UserID: userID, Username: userID, Role: identity.RoleMember}, identity.WebSession{})
		c.Next()
	}
}

type smsAccessFixture struct {
	server *Server
	a, b   db.SMS
}

type smsFlushRecorder struct {
	*httptest.ResponseRecorder
	flushed chan struct{}
}

func (recorder *smsFlushRecorder) Flush() {
	recorder.ResponseRecorder.Flush()
	select {
	case recorder.flushed <- struct{}{}:
	default:
	}
}

func newSMSAccessFixture(t *testing.T, profileAActive bool) smsAccessFixture {
	t.Helper()
	previous := db.DB
	if err := db.Init(filepath.Join(t.TempDir(), "sms-access.db")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.DB = previous })
	now := time.Now().UTC()
	for _, card := range []resourcecatalog.CardContainer{
		{ID: "card-a", Type: resourcecatalog.CardContainerTypePhysicalUICC, LastDeviceID: "dev-a", LastObservedAt: now},
		{ID: "card-b", Type: resourcecatalog.CardContainerTypePhysicalUICC, LastDeviceID: "dev-b", LastObservedAt: now},
	} {
		if err := db.DB.Create(&card).Error; err != nil {
			t.Fatal(err)
		}
	}
	profiles := []resourcecatalog.SubscriptionProfile{
		{ID: "profile-a", CardContainerID: "card-a", IdentityKey: "iccid:A", ICCID: "ICC-A", IMSI: "SHARED", Present: true, Active: profileAActive, LastObservedAt: now},
		{ID: "profile-b", CardContainerID: "card-b", IdentityKey: "iccid:B", ICCID: "ICC-B", IMSI: "SHARED", Present: true, Active: true, LastObservedAt: now},
	}
	for index := range profiles {
		if err := db.DB.Create(&profiles[index]).Error; err != nil {
			t.Fatal(err)
		}
	}
	profileA, profileB := "profile-a", "profile-b"
	for _, number := range []resourcecatalog.PhoneNumber{
		{ID: "number-a", SubscriptionProfileID: &profileA, E164: "+15550001", Status: "active"},
		{ID: "number-b", SubscriptionProfileID: &profileB, E164: "+15550002", Status: "active"},
	} {
		if err := db.DB.Create(&number).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, pool := range []resourcecatalog.NumberPool{{ID: "pool-a", Name: "A", Enabled: true}, {ID: "pool-b", Name: "B", Enabled: true}} {
		if err := db.DB.Create(&pool).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, member := range []resourcecatalog.NumberPoolMember{
		{ID: "member-a", NumberPoolID: "pool-a", PhoneNumberID: "number-a", Enabled: true},
		{ID: "member-b", NumberPoolID: "pool-b", PhoneNumberID: "number-b", Enabled: true},
	} {
		if err := db.DB.Create(&member).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, grant := range []resourcecatalog.NumberPoolUser{
		{ID: "grant-a", NumberPoolID: "pool-a", UserID: "user-a", Role: "member", Enabled: true},
		{ID: "grant-b", NumberPoolID: "pool-b", UserID: "user-b", Role: "member", Enabled: true},
	} {
		if err := db.DB.Create(&grant).Error; err != nil {
			t.Fatal(err)
		}
	}
	messages := []db.SMS{
		{ICCID: "ICC-A", IMSI: "SHARED", LocalPhone: "+15550001", Peer: "+100", Type: 1, Status: 0, Content: "message-a", Timestamp: now},
		{ICCID: "ICC-B", IMSI: "SHARED", LocalPhone: "+15550002", Peer: "+200", Type: 1, Status: 0, Content: "message-b", Timestamp: now.Add(time.Second)},
		{Peer: "+300", Type: 1, Status: 0, Content: "unassigned", Timestamp: now.Add(2 * time.Second)},
	}
	for index := range messages {
		if err := db.DB.Create(&messages[index]).Error; err != nil {
			t.Fatal(err)
		}
		if index < 2 {
			contact := db.SMSContact{ICCID: messages[index].ICCID, IMSI: messages[index].IMSI, Peer: messages[index].Peer, LastContent: messages[index].Content, LastTimestamp: messages[index].Timestamp, UnreadCount: 1}
			if err := db.DB.Create(&contact).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	pool := device.NewPool(&config.Config{})
	t.Cleanup(func() { _ = pool.Shutdown() })
	dispatcher, err := dispatch.New(db.DB, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	return smsAccessFixture{server: &Server{pool: pool, resourceCatalog: resourcecatalog.NewService(db.DB), dispatcher: dispatcher}, a: messages[0], b: messages[1]}
}

func attachSMSAccessWorker(t *testing.T, fixture smsAccessFixture) (*device.Worker, *atomic.Int32) {
	t.Helper()
	worker := &device.Worker{ID: "dev-a", Backend: &smsIdentityBackendStub{imsi: "SHARED"}}
	fixture.server.pool.AttachWorkerWithSMSIdentityForTest(worker, device.SMSIdentity{ICCID: "ICC-A", IMSI: "SHARED"})
	var sends atomic.Int32
	fixture.server.pool.SetRoutedSMSTestSenders(nil, func(string, string, string) error {
		sends.Add(1)
		return nil
	})
	return worker, &sends
}

func TestSMSMemberIsolationAcrossPoolsAndSharedIMSI(t *testing.T) {
	fixture := newSMSAccessFixture(t, true)
	for _, tc := range []struct {
		user, want string
	}{
		{"user-a", "message-a"}, {"user-b", "message-b"},
	} {
		router := gin.New()
		router.Use(smsMemberTestMiddleware(tc.user))
		router.GET("/sms/contacts", fixture.server.handleGetSMSContacts)
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/sms/contacts", nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s contacts status=%d body=%s", tc.user, recorder.Code, recorder.Body)
		}
		var contacts []SMSContactWithDevice
		if err := json.Unmarshal(recorder.Body.Bytes(), &contacts); err != nil || len(contacts) != 1 || contacts[0].LastContent != tc.want {
			t.Fatalf("%s contacts=%+v err=%v", tc.user, contacts, err)
		}
	}

	router := gin.New()
	router.Use(smsMemberTestMiddleware("user-a"))
	router.GET("/sms/messages/:id", fixture.server.handleSMSMessageTarget)
	router.DELETE("/sms/messages/:id", fixture.server.handleDeleteSMSMessage)
	router.GET("/sms/notifications", fixture.server.handleSMSNotifications)
	router.GET("/sms/thread", fixture.server.handleGetSMSThread)
	router.PATCH("/sms/thread", fixture.server.handleMarkSMSThreadRead)
	router.GET("/sms/delivery/:message_id", fixture.server.handleSMSDelivery)
	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(method, "/sms/messages/"+itoa(uint64(fixture.b.ID)), nil))
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("%s other user's message status=%d body=%s", method, recorder.Code, recorder.Body)
		}
	}
	var count int64
	if err := db.DB.Model(&db.SMS{}).Where("id = ?", fixture.b.ID).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("other message count=%d err=%v", count, err)
	}
	for _, delivery := range []db.SMSDelivery{
		{MessageID: "delivery-a", ICCID: "ICC-A", IMSI: "SHARED", State: db.SMSDeliveryStateAcked},
		{MessageID: "delivery-b", ICCID: "ICC-B", IMSI: "SHARED", State: db.SMSDeliveryStateAcked},
	} {
		if err := db.DB.Create(&delivery).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		messageID string
		status    int
	}{{"delivery-a", http.StatusOK}, {"delivery-b", http.StatusNotFound}} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/sms/delivery/"+tc.messageID, nil))
		if recorder.Code != tc.status {
			t.Fatalf("delivery %s status=%d body=%s", tc.messageID, recorder.Code, recorder.Body)
		}
	}

	notificationResponse := httptest.NewRecorder()
	router.ServeHTTP(notificationResponse, httptest.NewRequest(http.MethodGet, "/sms/notifications?after_id=0", nil))
	var notifications db.SMSNotifications
	if notificationResponse.Code != http.StatusOK || json.Unmarshal(notificationResponse.Body.Bytes(), &notifications) != nil || notifications.UnreadCount != 1 || notifications.Cursor != fixture.a.ID {
		t.Fatalf("member notifications status=%d result=%+v body=%s", notificationResponse.Code, notifications, notificationResponse.Body)
	}
	threadResponse := httptest.NewRecorder()
	router.ServeHTTP(threadResponse, httptest.NewRequest(http.MethodGet, "/sms/thread?iccid=ICC-B&peer=%2B200", nil))
	if threadResponse.Code != http.StatusNotFound {
		t.Fatalf("other thread status=%d body=%s", threadResponse.Code, threadResponse.Body)
	}
	readResponse := httptest.NewRecorder()
	readRequest := httptest.NewRequest(http.MethodPatch, "/sms/thread?iccid=ICC-B&peer=%2B200", bytes.NewBufferString(`{"through_id":`+itoa(uint64(fixture.b.ID))+`}`))
	readRequest.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(readResponse, readRequest)
	if readResponse.Code != http.StatusNotFound {
		t.Fatalf("other thread read status=%d body=%s", readResponse.Code, readResponse.Body)
	}

	adminRouter := gin.New()
	adminRouter.Use(smsAdminTestMiddleware())
	adminRouter.GET("/sms/notifications", fixture.server.handleSMSNotifications)
	adminResponse := httptest.NewRecorder()
	adminRouter.ServeHTTP(adminResponse, httptest.NewRequest(http.MethodGet, "/sms/notifications?after_id=0", nil))
	if adminResponse.Code != http.StatusOK || json.Unmarshal(adminResponse.Body.Bytes(), &notifications) != nil || notifications.UnreadCount != 3 {
		t.Fatalf("admin notifications status=%d result=%+v body=%s", adminResponse.Code, notifications, adminResponse.Body)
	}

	access := db.NewSMSAccessScope([]db.SMSAccessIdentity{{ICCID: "ICC-A", IMSI: "SHARED", LocalPhone: "+15550001"}})
	replay, err := loadSMSReplayScoped(t.Context(), access, uintPointer(0))
	if err != nil || len(replay.page) != 1 || replay.page[0].ID != fixture.a.ID {
		t.Fatalf("scoped SSE replay=%+v err=%v", replay.page, err)
	}
}

func TestSMSMemberCannotForgeDeviceAndInactiveProfileConflicts(t *testing.T) {
	fixture := newSMSAccessFixture(t, false)
	router := gin.New()
	router.Use(smsMemberTestMiddleware("user-a"))
	router.POST("/sms/send", fixture.server.handleSendSMS)

	for _, tc := range []struct {
		body string
		want int
		code string
	}{
		{`{"pool_id":"pool-a","device_id":"dev-b","phone":"+100","message":"x"}`, http.StatusForbidden, "sms_device_forbidden"},
		{`{"pool_id":"pool-b","phone":"+100","message":"x"}`, http.StatusForbidden, "sms_pool_forbidden"},
		{`{"pool_id":"pool-a","phone":"+100","message":"x"}`, http.StatusConflict, "sms_profile_inactive"},
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/sms/send", bytes.NewBufferString(tc.body))
		request.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(recorder, request)
		if recorder.Code != tc.want || !bytes.Contains(recorder.Body.Bytes(), []byte(tc.code)) {
			t.Fatalf("body=%s status=%d response=%s", tc.body, recorder.Code, recorder.Body)
		}
	}
}

func TestSMSMemberGetsExplicitProfileSwitchConflict(t *testing.T) {
	fixture := newSMSAccessFixture(t, true)
	worker := &device.Worker{ID: "dev-a", Backend: &smsIdentityBackendStub{imsi: "SHARED"}}
	setNestedPrivateField(t, worker, []string{"state", "Identity", "ICCID"}, "ICC-A")
	setNestedPrivateField(t, worker, []string{"state", "Identity", "IMSI"}, "SHARED")
	fixture.server.pool.AttachWorkerForTest(worker)
	setNestedPrivateField(t, fixture.server.pool, []string{"switchingDevices"}, map[string]bool{"dev-a": true})

	router := gin.New()
	router.Use(smsMemberTestMiddleware("user-a"))
	router.POST("/sms/send", fixture.server.handleSendSMS)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/sms/send", bytes.NewBufferString(`{"pool_id":"pool-a","phone":"+100","message":"x"}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusConflict || !bytes.Contains(recorder.Body.Bytes(), []byte("sms_profile_switching")) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body)
	}
}

func TestSMSSendRejectsActiveDispatchLeaseForMemberAndAdmin(t *testing.T) {
	fixture := newSMSAccessFixture(t, true)
	_, sends := attachSMSAccessWorker(t, fixture)
	reservation, err := fixture.server.dispatcher.ReserveMaintenance(t.Context(), dispatch.MaintenanceInput{
		UserID: "call-owner", Operation: "profile_switch", DeviceID: "dev-a", CardID: "card-a", ProfileID: "profile-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fixture.server.dispatcher.Release(context.Background(), reservation.Lease.ID, "", "") }()

	tests := []struct {
		name       string
		middleware gin.HandlerFunc
		body       string
	}{
		{name: "member pool", middleware: smsMemberTestMiddleware("user-a"), body: `{"pool_id":"pool-a","phone":"+100","message":"x"}`},
		{name: "administrator diagnostic", middleware: smsAdminTestMiddleware(), body: `{"device_id":"dev-a","phone":"+100","message":"x"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			router := gin.New()
			router.Use(test.middleware)
			router.POST("/sms/send", fixture.server.handleSendSMS)
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/sms/send", bytes.NewBufferString(test.body))
			request.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusConflict || !bytes.Contains(recorder.Body.Bytes(), []byte("sms_device_busy")) {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body)
			}
		})
	}
	if sends.Load() != 0 {
		t.Fatalf("SMS send reached modem while lease active: sends=%d", sends.Load())
	}
}

func TestSMSSendRejectsActivePhoneCallForMemberAndAdmin(t *testing.T) {
	fixture := newSMSAccessFixture(t, true)
	_, sends := attachSMSAccessWorker(t, fixture)
	gateway := &phoneRouteGatewayStub{}
	service, err := phone.NewService(phone.ServiceOptions{
		Gateway: gateway, WebRTCUDPAddress: "127.0.0.1:0", RecoveryGrace: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture.server.phone = service
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := service.Close(ctx); err != nil {
			t.Errorf("close phone service: %v", err)
		}
	})
	if gateway.incoming == nil {
		t.Fatal("phone service did not subscribe to incoming calls")
	}
	gateway.incoming(voicehost.IncomingCall{
		DeviceID: "dev-a", CallID: "incoming-sms-guard", Caller: "+15550100", ReceivedAt: time.Now(),
	})

	tests := []struct {
		name       string
		middleware gin.HandlerFunc
		body       string
	}{
		{name: "member pool", middleware: smsMemberTestMiddleware("user-a"), body: `{"pool_id":"pool-a","phone":"+100","message":"x"}`},
		{name: "administrator diagnostic", middleware: smsAdminTestMiddleware(), body: `{"device_id":"dev-a","phone":"+100","message":"x"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			router := gin.New()
			router.Use(test.middleware)
			router.POST("/sms/send", fixture.server.handleSendSMS)
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/sms/send", bytes.NewBufferString(test.body))
			request.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusConflict || !bytes.Contains(recorder.Body.Bytes(), []byte("sms_device_busy")) {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body)
			}
		})
	}
	if sends.Load() != 0 {
		t.Fatalf("SMS send reached modem during active call: sends=%d", sends.Load())
	}
	gateway.emit(voicehost.CallEvent{
		Type: "CallEnded", DeviceID: "dev-a", CallID: "incoming-sms-guard", Time: time.Now(),
	})
	gateway.emit(voicehost.CallEvent{
		Type: "CallFinalized", DeviceID: "dev-a", CallID: "incoming-sms-guard", Time: time.Now(),
	})
}

func TestSMSSendLeaseBlocksCallAllocationUntilSendCompletes(t *testing.T) {
	fixture := newSMSAccessFixture(t, true)
	worker := &device.Worker{ID: "dev-a", Backend: &smsIdentityBackendStub{imsi: "SHARED"}}
	fixture.server.pool.AttachWorkerWithSMSIdentityForTest(worker, device.SMSIdentity{ICCID: "ICC-A", IMSI: "SHARED"})
	entered, releaseSend := make(chan struct{}), make(chan struct{})
	fixture.server.pool.SetRoutedSMSTestSenders(nil, func(string, string, string) error {
		close(entered)
		<-releaseSend
		return nil
	})

	router := gin.New()
	router.Use(smsMemberTestMiddleware("user-a"))
	router.POST("/sms/send", fixture.server.handleSendSMS)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/sms/send", bytes.NewBufferString(`{"pool_id":"pool-a","phone":"+100","message":"x"}`))
	request.Header.Set("Content-Type", "application/json")
	done := make(chan struct{})
	go func() {
		router.ServeHTTP(recorder, request)
		close(done)
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("SMS sender was not reached")
	}

	_, err := fixture.server.dispatcher.Reserve(t.Context(), dispatch.ReserveInput{
		UserID: "caller", PoolID: "call-pool", Callee: "10086", EnforceUserLimit: true,
	}, []dispatch.Candidate{{
		PoolID: "call-pool", DeviceID: "dev-a", CardID: "card-a", ProfileID: "profile-a", NumberID: "number-a",
		Online: true, Enabled: true,
	}})
	if !errors.Is(err, dispatch.ErrNoResource) {
		close(releaseSend)
		<-done
		t.Fatalf("call allocation during SMS err=%v, want ErrNoResource", err)
	}
	close(releaseSend)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("SMS handler did not finish")
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body)
	}
	active, err := fixture.server.dispatcher.ActiveLeases(t.Context())
	if err != nil || len(active) != 0 {
		t.Fatalf("active leases after SMS=%+v err=%v", active, err)
	}
}

func TestSMSIgnoresCallOnlyCardDisablePolicy(t *testing.T) {
	fixture := newSMSAccessFixture(t, true)
	_, sends := attachSMSAccessWorker(t, fixture)
	if _, err := fixture.server.dispatcher.DisableCardToday(t.Context(), "card-a", "admin", "call decline threshold"); err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.Use(smsMemberTestMiddleware("user-a"))
	router.POST("/sms/send", fixture.server.handleSendSMS)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/sms/send", bytes.NewBufferString(`{"pool_id":"pool-a","phone":"+100","message":"x"}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || sends.Load() != 1 {
		t.Fatalf("call-only card policy changed SMS semantics: status=%d sends=%d body=%s", recorder.Code, sends.Load(), recorder.Body)
	}
}

func TestSMSEventEndpointFiltersMemberReplay(t *testing.T) {
	fixture := newSMSAccessFixture(t, true)
	requestContext, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest(http.MethodGet, "/sms/events?after_id=0", nil).WithContext(requestContext)
	recorder := &smsFlushRecorder{ResponseRecorder: httptest.NewRecorder(), flushed: make(chan struct{}, 4)}
	c, _ := gin.CreateTestContext(recorder)
	c.Request = request
	installPrincipal(c, identity.Principal{UserID: "user-a", Username: "user-a", Role: identity.RoleMember}, identity.WebSession{})
	done := make(chan struct{})
	go func() {
		fixture.server.handleSMSEvents(c)
		close(done)
	}()
	for flushes := 0; flushes < 2; flushes++ {
		select {
		case <-recorder.flushed:
		case <-time.After(2 * time.Second):
			cancel()
			t.Fatal("timed out waiting for SSE replay")
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("SSE handler did not stop after cancellation")
	}
	body := recorder.Body.String()
	if !strings.Contains(body, "message-a") || strings.Contains(body, "message-b") || strings.Contains(body, "unassigned") {
		t.Fatalf("member SSE body leaked another scope: %s", body)
	}
}

func uintPointer(value uint) *uint { return &value }

func itoa(value uint64) string {
	const digits = "0123456789"
	if value == 0 {
		return "0"
	}
	var buffer [20]byte
	index := len(buffer)
	for value > 0 {
		index--
		buffer[index] = digits[value%10]
		value /= 10
	}
	return string(buffer[index:])
}
