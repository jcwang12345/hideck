package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/yibaiba/hideck/internal/config"
	"github.com/yibaiba/hideck/internal/db"
	"github.com/yibaiba/hideck/internal/device"
	"github.com/yibaiba/hideck/internal/dispatch"
	"github.com/yibaiba/hideck/internal/identity"
	"github.com/yibaiba/hideck/internal/phone"
	"github.com/yibaiba/hideck/internal/resourcecatalog"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

type resourceAPITest struct {
	server *Server
	router http.Handler
	db     *gorm.DB
	admin  identity.User
	member identity.User
}

func newResourceAPITest(t *testing.T) resourceAPITest {
	t.Helper()
	gin.SetMode(gin.TestMode)
	database, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "api-resources.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := identity.Migrate(database); err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(
		&resourcecatalog.CardContainer{}, &resourcecatalog.SubscriptionProfile{},
		&resourcecatalog.PhoneNumber{}, &resourcecatalog.NumberPool{},
		&resourcecatalog.NumberPoolMember{}, &resourcecatalog.NumberPoolUser{},
		&resourcecatalog.DispatchPolicy{}, &resourcecatalog.AuditEvent{},
		&db.VoiceCallRecord{},
	); err != nil {
		t.Fatal(err)
	}
	if err := dispatch.Migrate(database); err != nil {
		t.Fatal(err)
	}
	identityService := identity.NewServiceWithOptions(database, identity.Options{BcryptCost: bcrypt.MinCost})
	admin, _, err := identityService.EnsureBootstrapAdmin(t.Context(), "admin", "CorrectHorse9!")
	if err != nil {
		t.Fatal(err)
	}
	member, err := identityService.CreateUser(t.Context(), "member", "MemberPassword9!", identity.RoleMember)
	if err != nil {
		t.Fatal(err)
	}
	dispatcher, err := dispatch.New(database, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{
		identityService: identityService,
		resourceCatalog: resourcecatalog.NewService(database),
		dispatcher:      dispatcher,
	}
	oldDatabase := db.DB
	db.DB = database
	t.Cleanup(func() { db.DB = oldDatabase })

	router := gin.New()
	api := router.Group("/api")
	api.Use(func(c *gin.Context) {
		var user identity.User
		switch c.GetHeader("X-Test-Principal") {
		case "admin":
			user = admin
		case "member":
			user = member
		default:
			c.Next()
			return
		}
		installPrincipal(c, identity.Principal{
			UserID: user.ID, Username: user.Username, Role: user.Role, SessionVersion: user.SessionVersion,
		}, identity.WebSession{})
		c.Next()
	})
	// Keep the existing card-policy wildcard mounted to catch Gin route-tree
	// conflicts when resource routes are registered in the production group.
	api.GET("/cards/:iccid/policy", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	server.registerResourceRoutes(api)
	return resourceAPITest{server: server, router: router, db: database, admin: admin, member: member}
}

func resourceRequest(router http.Handler, principal, method, path, body string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("X-Test-Principal", principal)
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	router.ServeHTTP(recorder, request)
	return recorder
}

func TestResourceRoutesEnforceRBACAndNumberPoolScope(t *testing.T) {
	test := newResourceAPITest(t)
	ctx := t.Context()
	first, err := test.server.resourceCatalog.CreateNumberPool(ctx, test.admin.ID, resourcecatalog.CreateNumberPoolInput{Name: "member pool"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := test.server.resourceCatalog.CreateNumberPool(ctx, test.admin.ID, resourcecatalog.CreateNumberPoolInput{Name: "admin only"}); err != nil {
		t.Fatal(err)
	}
	if _, err := test.server.resourceCatalog.ReplacePoolUsers(ctx, test.admin.ID, first.ID, []resourcecatalog.PoolUserInput{{UserID: test.member.ID}}); err != nil {
		t.Fatal(err)
	}
	unknown, err := test.server.resourceCatalog.SyncObservation(ctx, resourcecatalog.CardObservation{
		StableContainerKey: "cached-reader/slot-1", CardPresent: true, ProfilesComplete: true,
		Profiles: []resourcecatalog.ProfileObservation{{ICCID: "8901000000000000001", Active: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if unknown.Type != resourcecatalog.CardContainerTypeUnknown {
		t.Fatalf("card type=%s, want unknown", unknown.Type)
	}

	if response := resourceRequest(test.router, "member", http.MethodGet, "/api/cards", ""); response.Code != http.StatusForbidden {
		t.Fatalf("member cards status=%d body=%s", response.Code, response.Body.String())
	}
	if response := resourceRequest(test.router, "member", http.MethodPost, "/api/number-pools", `{"name":"forbidden"}`); response.Code != http.StatusForbidden {
		t.Fatalf("member pool create status=%d body=%s", response.Code, response.Body.String())
	}
	memberList := resourceRequest(test.router, "member", http.MethodGet, "/api/number-pools", "")
	if memberList.Code != http.StatusOK {
		t.Fatalf("member list status=%d body=%s", memberList.Code, memberList.Body.String())
	}
	var memberPayload struct {
		Pools []resourcecatalog.NumberPool `json:"number_pools"`
	}
	if err := json.Unmarshal(memberList.Body.Bytes(), &memberPayload); err != nil {
		t.Fatal(err)
	}
	if len(memberPayload.Pools) != 1 || memberPayload.Pools[0].ID != first.ID {
		t.Fatalf("member pools=%+v", memberPayload.Pools)
	}

	adminList := resourceRequest(test.router, "admin", http.MethodGet, "/api/number-pools", "")
	var adminPayload struct {
		Pools []resourcecatalog.NumberPool `json:"number_pools"`
	}
	if err := json.Unmarshal(adminList.Body.Bytes(), &adminPayload); err != nil {
		t.Fatal(err)
	}
	if len(adminPayload.Pools) != 2 {
		t.Fatalf("admin pools=%+v", adminPayload.Pools)
	}
	cards := resourceRequest(test.router, "admin", http.MethodGet, "/api/cards", "")
	if cards.Code != http.StatusOK || !strings.Contains(cards.Body.String(), `"card_type":"unknown"`) {
		t.Fatalf("cards status=%d body=%s", cards.Code, cards.Body.String())
	}
}

func TestResourceManagementEndpointsRejectMembersAndAnonymousUsers(t *testing.T) {
	test := newResourceAPITest(t)
	for _, request := range []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/api/cards", ""},
		{http.MethodPost, "/api/cards/card-1/disable", `{}`},
		{http.MethodPost, "/api/cards/card-1/enable", ""},
		{http.MethodPost, "/api/cards/card-1/profiles/profile-1/actions/activate", ""},
		{http.MethodPost, "/api/number-pools", `{"name":"pool"}`},
		{http.MethodPatch, "/api/number-pools/pool-1", `{"enabled":false}`},
		{http.MethodPut, "/api/number-pools/pool-1/members", `{"members":[]}`},
		{http.MethodPut, "/api/number-pools/pool-1/users", `{"users":[]}`},
		{http.MethodGet, "/api/phone-numbers", ""},
		{http.MethodPost, "/api/phone-numbers", `{"e164":"+12025550100"}`},
		{http.MethodGet, "/api/dispatch-policies", ""},
		{http.MethodPost, "/api/dispatch-policies", `{}`},
		{http.MethodGet, "/api/audit-events", ""},
	} {
		response := resourceRequest(test.router, "member", request.method, request.path, request.body)
		if response.Code != http.StatusForbidden {
			t.Errorf("member %s %s status=%d body=%s", request.method, request.path, response.Code, response.Body.String())
		}
	}
	for _, path := range []string{"/api/number-pools", "/api/call-records"} {
		response := resourceRequest(test.router, "", http.MethodGet, path, "")
		if response.Code != http.StatusUnauthorized {
			t.Errorf("anonymous GET %s status=%d body=%s", path, response.Code, response.Body.String())
		}
	}
}

func TestManualProfileActivationValidatesCardAndProfileBeforeDeviceMutation(t *testing.T) {
	test := newResourceAPITest(t)
	test.server.pool = device.NewPool(&config.Config{})
	physical, err := test.server.resourceCatalog.SyncObservation(t.Context(), resourcecatalog.CardObservation{
		StableContainerKey: "reader/physical", DeviceID: "dev-physical", CardPresent: true,
		PhysicalUICCConfirmed: true, ProfilesComplete: true,
		Profiles: []resourcecatalog.ProfileObservation{{ICCID: "8901000000000000081", Active: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	physicalResponse := resourceRequest(test.router, "admin", http.MethodPost,
		"/api/cards/"+physical.ID+"/profiles/"+physical.Profiles[0].ID+"/actions/activate", "")
	if physicalResponse.Code != http.StatusConflict || !strings.Contains(physicalResponse.Body.String(), `"code":"unsupported_card"`) {
		t.Fatalf("physical switch status=%d body=%s", physicalResponse.Code, physicalResponse.Body.String())
	}

	euicc, err := test.server.resourceCatalog.SyncObservation(t.Context(), resourcecatalog.CardObservation{
		StableContainerKey: "reader/euicc", DeviceID: "dev-euicc", CardPresent: true,
		EID: "89049032000001000000113509931081", ProfilesComplete: true,
		Profiles: []resourcecatalog.ProfileObservation{{ICCID: "8901000000000000082", Active: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	missing := resourceRequest(test.router, "admin", http.MethodPost,
		"/api/cards/"+euicc.ID+"/profiles/missing/actions/activate", "")
	if missing.Code != http.StatusNotFound || !strings.Contains(missing.Body.String(), `"code":"profile_not_found"`) {
		t.Fatalf("missing profile status=%d body=%s", missing.Code, missing.Body.String())
	}
	offline := resourceRequest(test.router, "admin", http.MethodPost,
		"/api/cards/"+euicc.ID+"/profiles/"+euicc.Profiles[0].ID+"/actions/activate", "")
	if offline.Code != http.StatusConflict || !strings.Contains(offline.Body.String(), `"code":"device_offline"`) {
		t.Fatalf("offline switch status=%d body=%s", offline.Code, offline.Body.String())
	}
}

func TestCardDailyDisableEnableAndStatsMapping(t *testing.T) {
	test := newResourceAPITest(t)
	card, err := test.server.resourceCatalog.SyncObservation(t.Context(), resourcecatalog.CardObservation{
		StableContainerKey: "cached-reader/slot-1", CardPresent: true, ProfilesComplete: true,
		Profiles: []resourcecatalog.ProfileObservation{{ICCID: "8901000000000000002", Active: true}},
	})
	if err != nil {
		t.Fatal(err)
	}

	missing := resourceRequest(test.router, "admin", http.MethodPost, "/api/cards/missing/disable", `{}`)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing card status=%d body=%s", missing.Code, missing.Body.String())
	}
	disable := resourceRequest(test.router, "admin", http.MethodPost, "/api/cards/"+card.ID+"/disable", `{"reason":"manual review"}`)
	if disable.Code != http.StatusOK || !strings.Contains(disable.Body.String(), `"disabled_reason":"manual review"`) {
		t.Fatalf("disable status=%d body=%s", disable.Code, disable.Body.String())
	}
	if err := test.db.Model(&dispatch.CardDailyStat{}).
		Where("card_id = ?", card.ID).
		Updates(map[string]any{"call_count": 4, "remote_decline_count": 2}).Error; err != nil {
		t.Fatal(err)
	}
	cards := resourceRequest(test.router, "admin", http.MethodGet, "/api/cards", "")
	for _, fragment := range []string{`"status":"disabled"`, `"daily_calls":4`, `"daily_rejections":2`, `"disabled_reason":"manual review"`} {
		if !strings.Contains(cards.Body.String(), fragment) {
			t.Fatalf("cards missing %s: status=%d body=%s", fragment, cards.Code, cards.Body.String())
		}
	}

	enable := resourceRequest(test.router, "admin", http.MethodPost, "/api/cards/"+card.ID+"/enable", "")
	if enable.Code != http.StatusOK {
		t.Fatalf("enable status=%d body=%s", enable.Code, enable.Body.String())
	}
	disabled, stat, err := test.server.dispatcher.CardDisabledToday(t.Context(), card.ID)
	if err != nil || disabled || stat.CallCount != 4 || stat.RemoteDeclineCount != 2 {
		t.Fatalf("enabled stat=%+v disabled=%v err=%v", stat, disabled, err)
	}
	audit := resourceRequest(test.router, "admin", http.MethodGet, "/api/audit-events?limit=20", "")
	if !strings.Contains(audit.Body.String(), "card.manually_disabled") || !strings.Contains(audit.Body.String(), "card.manually_enabled") {
		t.Fatalf("audit status=%d body=%s", audit.Code, audit.Body.String())
	}
}

func TestResourceMutationValidationAndAudit(t *testing.T) {
	test := newResourceAPITest(t)

	unknownField := resourceRequest(test.router, "admin", http.MethodPost, "/api/number-pools", `{"name":"pool","unexpected":true}`)
	if unknownField.Code != http.StatusBadRequest {
		t.Fatalf("unknown field status=%d body=%s", unknownField.Code, unknownField.Body.String())
	}
	create := resourceRequest(test.router, "admin", http.MethodPost, "/api/number-pools", `{"name":"pool"}`)
	if create.Code != http.StatusCreated {
		t.Fatalf("create pool status=%d body=%s", create.Code, create.Body.String())
	}
	var created struct {
		Pool resourcecatalog.NumberPool `json:"number_pool"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if response := resourceRequest(test.router, "admin", http.MethodPatch, "/api/number-pools/"+created.Pool.ID, `{}`); response.Code != http.StatusBadRequest {
		t.Fatalf("empty patch status=%d body=%s", response.Code, response.Body.String())
	}

	invalidNumber := resourceRequest(test.router, "admin", http.MethodPost, "/api/phone-numbers", `{"e164":"2025550100"}`)
	if invalidNumber.Code != http.StatusBadRequest {
		t.Fatalf("invalid number status=%d body=%s", invalidNumber.Code, invalidNumber.Body.String())
	}
	validNumber := resourceRequest(test.router, "admin", http.MethodPost, "/api/phone-numbers", `{"e164":"+12025550100","status":"available"}`)
	if validNumber.Code != http.StatusCreated {
		t.Fatalf("valid number status=%d body=%s", validNumber.Code, validNumber.Body.String())
	}
	var numberPayload struct {
		Number resourcecatalog.PhoneNumber `json:"phone_number"`
	}
	if err := json.Unmarshal(validNumber.Body.Bytes(), &numberPayload); err != nil {
		t.Fatal(err)
	}

	missingMember := resourceRequest(test.router, "admin", http.MethodPut, "/api/number-pools/"+created.Pool.ID+"/members", `{"members":[{"phone_number_id":"missing"}]}`)
	if missingMember.Code != http.StatusBadRequest {
		t.Fatalf("missing member status=%d body=%s", missingMember.Code, missingMember.Body.String())
	}
	validMember := resourceRequest(test.router, "admin", http.MethodPut, "/api/number-pools/"+created.Pool.ID+"/members", `{"members":[{"phone_number_id":"`+numberPayload.Number.ID+`"}]}`)
	if validMember.Code != http.StatusOK {
		t.Fatalf("valid member status=%d body=%s", validMember.Code, validMember.Body.String())
	}
	missingUser := resourceRequest(test.router, "admin", http.MethodPut, "/api/number-pools/"+created.Pool.ID+"/users", `{"users":[{"user_id":"missing"}]}`)
	if missingUser.Code != http.StatusBadRequest {
		t.Fatalf("missing user status=%d body=%s", missingUser.Code, missingUser.Body.String())
	}
	duplicateUser := resourceRequest(test.router, "admin", http.MethodPut, "/api/number-pools/"+created.Pool.ID+"/users", `{"users":[{"user_id":"`+test.member.ID+`"},{"user_id":"`+test.member.ID+`"}]}`)
	if duplicateUser.Code != http.StatusBadRequest {
		t.Fatalf("duplicate user status=%d body=%s", duplicateUser.Code, duplicateUser.Body.String())
	}

	invalidPolicy := resourceRequest(test.router, "admin", http.MethodPost, "/api/dispatch-policies", `{"number_pool_id":"`+created.Pool.ID+`","name":"default","config_json":"{"}`)
	if invalidPolicy.Code != http.StatusBadRequest {
		t.Fatalf("invalid policy status=%d body=%s", invalidPolicy.Code, invalidPolicy.Body.String())
	}
	validPolicy := resourceRequest(test.router, "admin", http.MethodPost, "/api/dispatch-policies", `{"number_pool_id":"`+created.Pool.ID+`","name":"default","config":{"rejection_threshold":3,"disable_scope":"card","timezone":"Asia/Shanghai","restore_time":"00:00"}}`)
	if validPolicy.Code != http.StatusCreated {
		t.Fatalf("valid policy status=%d body=%s", validPolicy.Code, validPolicy.Body.String())
	}
	policies := resourceRequest(test.router, "admin", http.MethodGet, "/api/dispatch-policies", "")
	if policies.Code != http.StatusOK || !strings.Contains(policies.Body.String(), `"rejection_threshold":3`) || !strings.Contains(policies.Body.String(), `"pool_name":"pool"`) {
		t.Fatalf("policies status=%d body=%s", policies.Code, policies.Body.String())
	}

	if response := resourceRequest(test.router, "member", http.MethodGet, "/api/audit-events", ""); response.Code != http.StatusForbidden {
		t.Fatalf("member audit status=%d body=%s", response.Code, response.Body.String())
	}
	audit := resourceRequest(test.router, "admin", http.MethodGet, "/api/audit-events?limit=20", "")
	if audit.Code != http.StatusOK || !strings.Contains(audit.Body.String(), "number_pool.created") {
		t.Fatalf("audit status=%d body=%s", audit.Code, audit.Body.String())
	}
}

func TestCallRecordsAreScopedToCurrentUser(t *testing.T) {
	test := newResourceAPITest(t)
	store := db.NewVoiceCallStore(test.db)
	now := time.Now().UTC().Truncate(time.Second)
	for _, record := range []phone.CallRecord{
		{CallID: "member-call", DeviceID: "dev-1", UserID: test.member.ID, Direction: "outbound", Peer: "+12025550101", Status: phone.StatusCompleted, StartedAt: now},
		{CallID: "admin-call", DeviceID: "dev-2", UserID: test.admin.ID, Direction: "outbound", Peer: "+12025550102", Status: phone.StatusFailed, StartedAt: now.Add(-time.Minute)},
	} {
		if err := store.Upsert(t.Context(), record); err != nil {
			t.Fatal(err)
		}
	}

	member := resourceRequest(test.router, "member", http.MethodGet, "/api/call-records", "")
	if member.Code != http.StatusOK || !strings.Contains(member.Body.String(), "member-call") || strings.Contains(member.Body.String(), "admin-call") {
		t.Fatalf("member records status=%d body=%s", member.Code, member.Body.String())
	}
	admin := resourceRequest(test.router, "admin", http.MethodGet, "/api/call-records", "")
	if admin.Code != http.StatusOK || !strings.Contains(admin.Body.String(), "member-call") || !strings.Contains(admin.Body.String(), "admin-call") {
		t.Fatalf("admin records status=%d body=%s", admin.Code, admin.Body.String())
	}
	invalidLimit := resourceRequest(test.router, "admin", http.MethodGet, "/api/call-records?limit=0", "")
	if invalidLimit.Code != http.StatusBadRequest {
		t.Fatalf("invalid limit status=%d body=%s", invalidLimit.Code, invalidLimit.Body.String())
	}
}
