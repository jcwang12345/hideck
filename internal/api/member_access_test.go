package api

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/yibaiba/hideck/internal/config"
	"github.com/yibaiba/hideck/internal/identity"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

func testPrincipalMiddleware(role identity.Role) gin.HandlerFunc {
	return func(c *gin.Context) {
		installPrincipal(c, identity.Principal{
			UserID: "test-" + string(role), Username: string(role), Role: role, SessionVersion: 1,
		}, identity.WebSession{})
		c.Next()
	}
}

func TestMemberRouteAllowlistUsesExactMethodAndNormalizedPattern(t *testing.T) {
	tests := []struct {
		name, method, route string
		allowed             bool
	}{
		{name: "own identity", method: http.MethodGet, route: "/api/auth/me", allowed: true},
		{name: "own password", method: http.MethodPost, route: "/api/settings/password", allowed: true},
		{name: "assigned pools", method: http.MethodGet, route: "/api/number-pools", allowed: true},
		{name: "dispatch call", method: http.MethodPost, route: "/api/phone/call-requests", allowed: true},
		{name: "scoped call control", method: http.MethodPost, route: "/api/phone/calls/:call_id/answer", allowed: true},
		{name: "scoped recording", method: http.MethodGet, route: "/api/phone/recordings/:recording", allowed: true},
		{name: "scoped contact", method: http.MethodPut, route: "/api/phone/contacts", allowed: true},
		{name: "scoped sms SSE", method: http.MethodGet, route: "/api/sms/events", allowed: true},
		{name: "scoped sms delete", method: http.MethodDelete, route: "/api/sms/messages/:id", allowed: true},
		{name: "wrong method", method: http.MethodDelete, route: "/api/auth/me"},
		{name: "raw dynamic path is not a policy key", method: http.MethodPost, route: "/api/phone/calls/call-other/answer"},
		{name: "legacy direct dial", method: http.MethodPost, route: "/api/phone/calls"},
		{name: "legacy phone devices", method: http.MethodGet, route: "/api/phone/devices"},
		{name: "direct device call", method: http.MethodPost, route: "/api/devices/:device_id/vowifi/calls"},
		{name: "device inventory", method: http.MethodGet, route: "/api/devices"},
		{name: "eSIM profile", method: http.MethodGet, route: "/api/devices/:device_id/esim/profiles"},
		{name: "proxy credentials", method: http.MethodGet, route: "/api/upstream-proxies"},
		{name: "system logs", method: http.MethodGet, route: "/api/logs/history"},
		{name: "MCP tokens", method: http.MethodGet, route: "/api/mcp/tokens"},
		{name: "global disclaimer", method: http.MethodPut, route: "/api/settings/disclaimer"},
		{name: "automatic task", method: http.MethodGet, route: "/api/automatic-tasks"},
		{name: "command center", method: http.MethodGet, route: "/api/command-center/commands"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := memberRouteAllowed(test.method, test.route); got != test.allowed {
				t.Fatalf("memberRouteAllowed(%q, %q)=%v want %v", test.method, test.route, got, test.allowed)
			}
		})
	}
}

func newFullMemberAccessTestServer(t *testing.T) (*Server, *gin.Engine, string, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	database, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "member-access.db")), &gorm.Config{})
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
	if _, err := service.CreateUser(t.Context(), "member", "MemberPassword9!", identity.RoleMember); err != nil {
		t.Fatal(err)
	}
	adminLogin, err := service.Login(t.Context(), "admin", "CorrectHorse9!")
	if err != nil {
		t.Fatal(err)
	}
	memberLogin, err := service.Login(t.Context(), "member", "MemberPassword9!")
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{
		auth:            config.WebConfig{Username: "admin", Password: "CorrectHorse9!"},
		identityService: service,
		loginAttempts:   make(map[string]loginAttempt),
		shutdownCh:      make(chan struct{}),
	}
	return server, server.newRouter(), adminLogin.Token, memberLogin.Token
}

func performMemberAccessRequest(router http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func TestFullRouterMemberBoundary(t *testing.T) {
	_, router, adminToken, memberToken := newFullMemberAccessTestServer(t)

	login := performMemberAccessRequest(router, http.MethodPost, "/api/auth/login", "", `{"username":"member","password":"MemberPassword9!"}`)
	if login.Code != http.StatusOK {
		t.Fatalf("public login was placed behind member guard: status=%d body=%s", login.Code, login.Body.String())
	}

	tests := []struct {
		name, method, path, body string
		want                     int
		forbidden                bool
	}{
		{name: "identity", method: http.MethodGet, path: "/api/auth/me", want: http.StatusOK},
		{name: "password status", method: http.MethodGet, path: "/api/settings/password", want: http.StatusOK},
		{name: "assigned pools reaches scoped handler", method: http.MethodGet, path: "/api/number-pools", want: http.StatusServiceUnavailable},
		{name: "call records reaches scoped handler", method: http.MethodGet, path: "/api/call-records", want: http.StatusServiceUnavailable},
		{name: "dispatch reaches scoped handler", method: http.MethodPost, path: "/api/phone/call-requests", body: `{}`, want: http.StatusServiceUnavailable},
		{name: "dynamic recording reaches scoped handler", method: http.MethodGet, path: "/api/phone/recordings/call_owned.mp3", want: http.StatusServiceUnavailable},
		{name: "contacts reach owner scoped handler", method: http.MethodGet, path: "/api/phone/contacts", want: http.StatusInternalServerError},
		{name: "sms reaches pool scoped handler", method: http.MethodGet, path: "/api/sms/notifications", want: http.StatusServiceUnavailable},

		{name: "legacy direct dial denied", method: http.MethodPost, path: "/api/phone/calls", body: `{}`, forbidden: true},
		{name: "legacy phone device list denied", method: http.MethodGet, path: "/api/phone/devices", forbidden: true},
		{name: "direct device call denied", method: http.MethodPost, path: "/api/devices/device-a/vowifi/calls", body: `{}`, forbidden: true},
		{name: "device data denied", method: http.MethodGet, path: "/api/devices/device-a/status", forbidden: true},
		{name: "eSIM denied", method: http.MethodGet, path: "/api/devices/device-a/esim/profiles", forbidden: true},
		{name: "proxy data denied", method: http.MethodGet, path: "/api/upstream-proxies", forbidden: true},
		{name: "logs denied", method: http.MethodGet, path: "/api/logs/history", forbidden: true},
		{name: "MCP token management denied", method: http.MethodGet, path: "/api/mcp/tokens", forbidden: true},
		{name: "automatic tasks denied", method: http.MethodGet, path: "/api/automatic-tasks", forbidden: true},
		{name: "command center denied", method: http.MethodGet, path: "/api/command-center/commands", forbidden: true},
		{name: "legacy rotate endpoint is administrator only", method: http.MethodPost, path: "/api/rotateip", body: `{}`, forbidden: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := performMemberAccessRequest(router, test.method, test.path, memberToken, test.body)
			if test.forbidden {
				if response.Code != http.StatusForbidden {
					t.Fatalf("status=%d want 403 body=%s", response.Code, response.Body.String())
				}
				return
			}
			if response.Code == http.StatusForbidden || response.Code != test.want {
				t.Fatalf("status=%d want %d (and not 403) body=%s", response.Code, test.want, response.Body.String())
			}
		})
	}

	admin := performMemberAccessRequest(router, http.MethodGet, "/api/settings/mcp", adminToken, "")
	if admin.Code != http.StatusOK {
		t.Fatalf("administrator did not reach management route: status=%d body=%s", admin.Code, admin.Body.String())
	}
}

func TestFullRouterRejectsEveryUnlistedAuthenticatedRouteForMember(t *testing.T) {
	_, router, _, memberToken := newFullMemberAccessTestServer(t)
	registered := make(map[memberRouteKey]bool)
	for _, route := range router.Routes() {
		key := memberRouteKey{method: route.Method, path: route.Path}
		registered[key] = true
		if !strings.HasPrefix(route.Path, "/api/") || memberRouteAllowed(route.Method, route.Path) || routeRegisteredBeforeMemberGuard(route.Method, route.Path) {
			continue
		}
		path := concreteRoutePath(route.Path)
		response := performMemberAccessRequest(router, route.Method, path, memberToken, `{}`)
		if response.Code != http.StatusForbidden {
			t.Errorf("unlisted route %s %s was not fail-closed: status=%d body=%s", route.Method, route.Path, response.Code, response.Body.String())
		}
	}
	for key := range memberRouteAllowlist {
		if !registered[key] {
			t.Errorf("member allowlist contains an unregistered route: %s %s", key.method, key.path)
		}
	}
}

func routeRegisteredBeforeMemberGuard(method, routePath string) bool {
	key := memberRouteKey{method: method, path: routePath}
	switch key {
	case memberRouteKey{http.MethodGet, "/api/docs"},
		memberRouteKey{http.MethodGet, "/api/docs/assets/*filepath"},
		memberRouteKey{http.MethodPost, "/api/auth/login"},
		memberRouteKey{http.MethodPost, "/api/login"},
		memberRouteKey{http.MethodGet, "/api/phone/ca.crt"},
		memberRouteKey{http.MethodOptions, "/api/logs/stream"}:
		return true
	}
	return strings.HasPrefix(routePath, "/api/websheets/")
}

func concreteRoutePath(routePath string) string {
	parts := strings.Split(routePath, "/")
	for index, part := range parts {
		if strings.HasPrefix(part, ":") || strings.HasPrefix(part, "*") {
			parts[index] = "test"
		}
	}
	return strings.Join(parts, "/")
}

func TestMCPManagementIsAdminOnlyWhenMountedWithoutGlobalGuard(t *testing.T) {
	for _, test := range []struct {
		name string
		role identity.Role
		want int
	}{
		{name: "member", role: identity.RoleMember, want: http.StatusForbidden},
		{name: "administrator", role: identity.RoleAdmin, want: http.StatusOK},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := &Server{}
			router := gin.New()
			group := router.Group("/api", testPrincipalMiddleware(test.role))
			server.registerMCPManagement(group)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/settings/mcp", nil))
			if response.Code != test.want {
				t.Fatalf("status=%d want %d body=%s", response.Code, test.want, response.Body.String())
			}
		})
	}
}

func TestDebugEmbedIsAdministratorOnly(t *testing.T) {
	server, _, _, memberToken := newFullMemberAccessTestServer(t)
	server.cfg.Debug = true
	router := server.newRouter()
	response := performMemberAccessRequest(router, http.MethodGet, "/debug/embed", memberToken, "")
	if response.Code != http.StatusForbidden {
		t.Fatalf("member reached debug endpoint: status=%d body=%s", response.Code, response.Body.String())
	}
}
