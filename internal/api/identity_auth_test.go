package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/yibaiba/hideck/internal/config"
	"github.com/yibaiba/hideck/internal/db"
	"github.com/yibaiba/hideck/internal/identity"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

func TestInitializeIdentityClaimsLegacyContactsForBootstrapAdmin(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "identity-contact-owner.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&db.PhoneContact{}); err != nil {
		t.Fatal(err)
	}
	legacy := db.PhoneContact{
		OwnerID: db.LegacyPhoneContactOwnerID, Number: "10086", Name: "旧联系人",
	}
	if err := database.Create(&legacy).Error; err != nil {
		t.Fatal(err)
	}
	previous := db.DB
	db.DB = database
	t.Cleanup(func() { db.DB = previous })
	server := &Server{auth: config.WebConfig{Username: "admin", Password: "CorrectHorse9!"}}
	server.initializeIdentity()
	if server.identityInitErr != nil {
		t.Fatal(server.identityInitErr)
	}
	users, err := server.identityService.ListUsers(t.Context())
	if err != nil || len(users) != 1 || users[0].Role != identity.RoleAdmin {
		t.Fatalf("bootstrap users=%+v err=%v", users, err)
	}
	var claimed db.PhoneContact
	if err := database.First(&claimed).Error; err != nil {
		t.Fatal(err)
	}
	if claimed.OwnerID != users[0].ID || claimed.Name != "旧联系人" {
		t.Fatalf("claimed contact=%+v admin=%+v", claimed, users[0])
	}
}

func newIdentityAPITestServer(t *testing.T) (*Server, *gin.Engine) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	database, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "api-identity.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := identity.Migrate(database); err != nil {
		t.Fatal(err)
	}
	service := identity.NewServiceWithOptions(database, identity.Options{BcryptCost: bcrypt.MinCost})
	if _, _, err := service.EnsureBootstrapAdmin(t.Context(), "admin", "CorrectHorse9!"); err != nil {
		t.Fatal(err)
	}
	server := &Server{
		auth:            config.WebConfig{Username: "admin", Password: "CorrectHorse9!"},
		identityService: service,
		loginAttempts:   make(map[string]loginAttempt),
	}
	router := gin.New()
	router.POST("/api/login", server.handleLogin)
	router.POST("/api/auth/login", server.handleLogin)
	authenticated := router.Group("/api", server.authMiddleware())
	server.registerIdentityRoutes(authenticated)
	server.registerIdentityAdminRoutes(authenticated.Group("/admin", RequireRole(identity.RoleAdmin)))
	return server, router
}

func performIdentityRequest(router http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	router.ServeHTTP(recorder, request)
	return recorder
}

func loginIdentityAPI(t *testing.T, router http.Handler, username, password string) (string, identity.UserView) {
	t.Helper()
	recorder := performIdentityRequest(router, http.MethodPost, "/api/login", "", `{"username":"`+username+`","password":"`+password+`"}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("login status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Token string            `json:"token"`
		User  identity.UserView `json:"user"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Token == "" || response.User.ID == "" {
		t.Fatalf("incomplete login response: %+v", response)
	}
	return response.Token, response.User
}

func TestIdentityLoginMeLogoutAndAdminRBAC(t *testing.T) {
	_, router := newIdentityAPITestServer(t)
	adminToken, admin := loginIdentityAPI(t, router, "admin", "CorrectHorse9!")

	me := performIdentityRequest(router, http.MethodGet, "/api/auth/me", adminToken, "")
	if me.Code != http.StatusOK || !strings.Contains(me.Body.String(), admin.ID) {
		t.Fatalf("me status=%d body=%s", me.Code, me.Body.String())
	}

	create := performIdentityRequest(router, http.MethodPost, "/api/admin/users", adminToken,
		`{"username":"operator","password":"OperatorPassword9!","role":"member"}`)
	if create.Code != http.StatusCreated {
		t.Fatalf("create user status=%d body=%s", create.Code, create.Body.String())
	}
	memberToken, _ := loginIdentityAPI(t, router, "operator", "OperatorPassword9!")
	forbidden := performIdentityRequest(router, http.MethodGet, "/api/admin/users", memberToken, "")
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("member admin access status=%d body=%s", forbidden.Code, forbidden.Body.String())
	}

	logout := performIdentityRequest(router, http.MethodPost, "/api/auth/logout", memberToken, "")
	if logout.Code != http.StatusNoContent {
		t.Fatalf("logout status=%d body=%s", logout.Code, logout.Body.String())
	}
	me = performIdentityRequest(router, http.MethodGet, "/api/auth/me", memberToken, "")
	if me.Code != http.StatusUnauthorized {
		t.Fatalf("revoked token status=%d body=%s", me.Code, me.Body.String())
	}
}

func TestAdminCannotDisableOrDemoteLastAdministrator(t *testing.T) {
	for _, test := range []struct {
		name, method, body string
	}{
		{name: "demote", method: http.MethodPatch, body: `{"role":"member"}`},
		{name: "disable by patch", method: http.MethodPatch, body: `{"disabled":true}`},
		{name: "disable by compatibility delete", method: http.MethodDelete},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, router := newIdentityAPITestServer(t)
			token, admin := loginIdentityAPI(t, router, "admin", "CorrectHorse9!")
			response := performIdentityRequest(router, test.method, "/api/admin/users/"+admin.ID, token, test.body)
			if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "last_admin") {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestAdminPasswordResetRevokesExistingSessions(t *testing.T) {
	_, router := newIdentityAPITestServer(t)
	adminToken, _ := loginIdentityAPI(t, router, "admin", "CorrectHorse9!")
	create := performIdentityRequest(router, http.MethodPost, "/api/admin/users", adminToken,
		`{"username":"operator","password":"OperatorPassword9!","role":"member"}`)
	var created struct {
		User identity.UserView `json:"user"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	memberToken, _ := loginIdentityAPI(t, router, "operator", "OperatorPassword9!")
	reset := performIdentityRequest(router, http.MethodPost, "/api/admin/users/"+created.User.ID+"/password", adminToken,
		`{"password":"ReplacementPassword9!"}`)
	if reset.Code != http.StatusOK {
		t.Fatalf("reset status=%d body=%s", reset.Code, reset.Body.String())
	}
	me := performIdentityRequest(router, http.MethodGet, "/api/auth/me", memberToken, "")
	if me.Code != http.StatusUnauthorized {
		t.Fatalf("old session status=%d body=%s", me.Code, me.Body.String())
	}
	loginIdentityAPI(t, router, "operator", "ReplacementPassword9!")
}

func TestAdminDisablingUserImmediatelyRevokesExistingSession(t *testing.T) {
	_, router := newIdentityAPITestServer(t)
	adminToken, _ := loginIdentityAPI(t, router, "admin", "CorrectHorse9!")
	create := performIdentityRequest(router, http.MethodPost, "/api/admin/users", adminToken,
		`{"username":"operator","password":"OperatorPassword9!","role":"member"}`)
	if create.Code != http.StatusCreated {
		t.Fatalf("create user status=%d body=%s", create.Code, create.Body.String())
	}
	var created struct {
		User identity.UserView `json:"user"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	memberToken, _ := loginIdentityAPI(t, router, "operator", "OperatorPassword9!")

	disable := performIdentityRequest(router, http.MethodPatch, "/api/admin/users/"+created.User.ID, adminToken, `{"disabled":true}`)
	if disable.Code != http.StatusOK {
		t.Fatalf("disable status=%d body=%s", disable.Code, disable.Body.String())
	}
	me := performIdentityRequest(router, http.MethodGet, "/api/auth/me", memberToken, "")
	if me.Code != http.StatusUnauthorized {
		t.Fatalf("disabled user's existing session remained valid: status=%d body=%s", me.Code, me.Body.String())
	}
	login := performIdentityRequest(router, http.MethodPost, "/api/login", "", `{"username":"operator","password":"OperatorPassword9!"}`)
	if login.Code != http.StatusUnauthorized {
		t.Fatalf("disabled user could log in again: status=%d body=%s", login.Code, login.Body.String())
	}
}
