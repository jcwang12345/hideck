package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yibaiba/hideck/internal/identity"
	"github.com/yibaiba/hideck/internal/phone"
)

type scopedPhoneStore struct {
	mu      sync.Mutex
	records map[string]phone.CallRecord
}

func newScopedPhoneStore() *scopedPhoneStore {
	return &scopedPhoneStore{records: make(map[string]phone.CallRecord)}
}

func (store *scopedPhoneStore) Upsert(_ context.Context, record phone.CallRecord) error {
	store.mu.Lock()
	store.records[record.CallID] = record
	store.mu.Unlock()
	return nil
}

func (store *scopedPhoneStore) List(_ context.Context, limit int) ([]phone.CallRecord, error) {
	return store.list("", limit), nil
}

func (store *scopedPhoneStore) ListByUser(_ context.Context, userID string, limit int) ([]phone.CallRecord, error) {
	return store.list(userID, limit), nil
}

func (store *scopedPhoneStore) list(userID string, limit int) []phone.CallRecord {
	store.mu.Lock()
	defer store.mu.Unlock()
	result := make([]phone.CallRecord, 0, len(store.records))
	for _, record := range store.records {
		if userID != "" && record.UserID != userID {
			continue
		}
		result = append(result, record)
		if limit > 0 && len(result) == limit {
			break
		}
	}
	return result
}

func (store *scopedPhoneStore) Delete(_ context.Context, ids []uint64) ([]phone.CallRecord, error) {
	return store.delete("", ids), nil
}

func (store *scopedPhoneStore) DeleteByUser(_ context.Context, userID string, ids []uint64) ([]phone.CallRecord, error) {
	return store.delete(userID, ids), nil
}

func (store *scopedPhoneStore) delete(userID string, ids []uint64) []phone.CallRecord {
	store.mu.Lock()
	defer store.mu.Unlock()
	wanted := make(map[uint64]struct{}, len(ids))
	for _, id := range ids {
		wanted[id] = struct{}{}
	}
	deleted := make([]phone.CallRecord, 0, len(ids))
	for callID, record := range store.records {
		if _, ok := wanted[record.ID]; !ok || userID != "" && record.UserID != userID {
			continue
		}
		deleted = append(deleted, record)
		delete(store.records, callID)
	}
	return deleted
}

func (store *scopedPhoneStore) FindRecording(_ context.Context, name, userID string) (phone.CallRecord, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	for _, record := range store.records {
		if record.RecordingName == name && (userID == "" || record.UserID == userID) {
			return record, true, nil
		}
	}
	return phone.CallRecord{}, false, nil
}

func (*scopedPhoneStore) AbandonIncomplete(context.Context, time.Time, string) error { return nil }

func TestPhoneRESTMembersCannotSelectLegacyDevicesOrDialDirectly(t *testing.T) {
	server, closeServer := newScopedPhoneServer(t, newScopedPhoneStore())
	defer closeServer()
	member := identity.Principal{UserID: "user-a", Username: "alice", Role: identity.RoleMember}
	memberRouter := phonePrincipalRouter(server, member)

	devices := performPrincipalPhoneRequest(memberRouter, http.MethodGet, "/api/phone/devices", nil)
	if devices.Code != http.StatusForbidden {
		t.Fatalf("member devices status=%d body=%s", devices.Code, devices.Body.String())
	}
	dial := performPrincipalPhoneRequest(memberRouter, http.MethodPost, "/api/phone/calls", map[string]string{
		"device_id": "dev-1", "callee": "18005550100", "media_id": "member-media",
	})
	if dial.Code != http.StatusForbidden {
		t.Fatalf("member legacy dial status=%d body=%s", dial.Code, dial.Body.String())
	}

	adminRouter := phonePrincipalRouter(server, identity.Principal{UserID: "admin-id", Username: "admin", Role: identity.RoleAdmin})
	devices = performPrincipalPhoneRequest(adminRouter, http.MethodGet, "/api/phone/devices", nil)
	if devices.Code != http.StatusOK {
		t.Fatalf("admin devices status=%d body=%s", devices.Code, devices.Body.String())
	}
}

func TestPhoneRESTScopesHistoryDeleteAndRecordingDownloads(t *testing.T) {
	store := newScopedPhoneStore()
	server, closeServer := newScopedPhoneServer(t, store)
	defer closeServer()
	for _, record := range []phone.CallRecord{
		{ID: 1, CallID: "call-a", DeviceID: "dev-a", UserID: "user-a", Status: phone.StatusCompleted, RecordingName: "call_a.mp3"},
		{ID: 2, CallID: "call-b", DeviceID: "dev-b", UserID: "user-b", Status: phone.StatusCompleted, RecordingName: "call_b.mp3"},
	} {
		if err := store.Upsert(context.Background(), record); err != nil {
			t.Fatal(err)
		}
	}
	server.voiceRecordingDirectory = t.TempDir()
	if err := os.WriteFile(filepath.Join(server.voiceRecordingDirectory, "call_a.mp3"), []byte("owned-audio"), 0o600); err != nil {
		t.Fatal(err)
	}

	memberA := phonePrincipalRouter(server, identity.Principal{UserID: "user-a", Username: "alice", Role: identity.RoleMember})
	memberB := phonePrincipalRouter(server, identity.Principal{UserID: "user-b", Username: "bob", Role: identity.RoleMember})
	admin := phonePrincipalRouter(server, identity.Principal{UserID: "admin-id", Username: "admin", Role: identity.RoleAdmin})

	history := performPrincipalPhoneRequest(memberA, http.MethodGet, "/api/phone/history", nil)
	if history.Code != http.StatusOK {
		t.Fatalf("member history status=%d body=%s", history.Code, history.Body.String())
	}
	var body struct {
		Records []phone.CallRecord `json:"records"`
	}
	if err := json.Unmarshal(history.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Records) != 1 || body.Records[0].CallID != "call-a" {
		t.Fatalf("member history = %+v", body.Records)
	}

	foreignRecording := performPrincipalPhoneRequest(memberB, http.MethodGet, "/api/phone/recordings/call_a.mp3", nil)
	if foreignRecording.Code != http.StatusNotFound {
		t.Fatalf("foreign recording status=%d body=%s", foreignRecording.Code, foreignRecording.Body.String())
	}
	ownedRecording := performPrincipalPhoneRequest(memberA, http.MethodGet, "/api/phone/recordings/call_a.mp3", nil)
	if ownedRecording.Code != http.StatusOK || ownedRecording.Body.String() != "owned-audio" {
		t.Fatalf("owned recording status=%d body=%q", ownedRecording.Code, ownedRecording.Body.String())
	}
	adminRecording := performPrincipalPhoneRequest(admin, http.MethodGet, "/api/phone/recordings/call_a.mp3", nil)
	if adminRecording.Code != http.StatusOK {
		t.Fatalf("admin recording status=%d body=%s", adminRecording.Code, adminRecording.Body.String())
	}

	deleted := performPrincipalPhoneRequest(memberA, http.MethodPost, "/api/phone/history/delete", map[string]any{"ids": []uint64{1, 2}})
	if deleted.Code != http.StatusOK || !bytes.Contains(deleted.Body.Bytes(), []byte(`"deleted":1`)) {
		t.Fatalf("scoped delete status=%d body=%s", deleted.Code, deleted.Body.String())
	}
	adminHistory := performPrincipalPhoneRequest(admin, http.MethodGet, "/api/phone/history", nil)
	if adminHistory.Code != http.StatusOK {
		t.Fatalf("admin history status=%d body=%s", adminHistory.Code, adminHistory.Body.String())
	}
	body = struct {
		Records []phone.CallRecord `json:"records"`
	}{}
	if err := json.Unmarshal(adminHistory.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Records) != 1 || body.Records[0].CallID != "call-b" {
		t.Fatalf("admin remaining history = %+v", body.Records)
	}
}

func newScopedPhoneServer(t *testing.T, store phone.RecordStore) (*Server, func()) {
	t.Helper()
	service, err := phone.NewService(phone.ServiceOptions{
		Gateway: &phoneRouteGatewayStub{}, Store: store, WebRTCUDPAddress: "127.0.0.1:0", RecoveryGrace: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{phone: service, shutdownCh: make(chan struct{})}
	return server, func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := service.Close(ctx); err != nil {
			t.Errorf("close phone service: %v", err)
		}
	}
}

func phonePrincipalRouter(server *Server, principal identity.Principal) *gin.Engine {
	router := gin.New()
	api := router.Group("/api")
	api.Use(func(c *gin.Context) {
		installPrincipal(c, principal, identity.WebSession{})
		c.Next()
	})
	server.registerPhoneRoutes(api)
	return router
}

func performPrincipalPhoneRequest(router http.Handler, method, target string, body any) *httptest.ResponseRecorder {
	var payload []byte
	if body != nil {
		payload, _ = json.Marshal(body)
	}
	request := httptest.NewRequest(method, target, bytes.NewReader(payload))
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}
