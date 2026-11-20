package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yibaiba/hideck/internal/config"
	"github.com/yibaiba/hideck/internal/identity"
	"github.com/yibaiba/hideck/internal/websheet"
)

func TestRespondWebsheetErrorMapsStatuses(t *testing.T) {
	tests := []struct {
		err  error
		want int
	}{
		{err: websheet.ErrNotFound, want: http.StatusNotFound},
		{err: websheet.ErrExpired, want: http.StatusGone},
		{err: websheet.ErrUnsafeURL, want: http.StatusBadRequest},
		{err: websheet.ErrUnauthorized, want: http.StatusUnauthorized},
		{err: errors.New("boom"), want: http.StatusInternalServerError},
	}

	for _, tt := range tests {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		respondWebsheetError(c, tt.err)
		if rec.Code != tt.want {
			t.Fatalf("respondWebsheetError(%v)=%d want %d", tt.err, rec.Code, tt.want)
		}
	}
}

func TestWebsheetBootstrapUsesSessionTokenOutsideGlobalAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)

	broker := websheet.New(websheet.Config{AllowPrivateHosts: true})
	session, err := broker.Create(context.Background(), websheet.Request{URL: "https://203.0.113.10/start"})
	if err != nil {
		t.Fatal(err)
	}

	server := &Server{
		auth:      config.WebConfig{Password: "secret"},
		websheets: broker,
	}
	router := gin.New()
	api := router.Group("/api")
	server.registerWebsheetRoutes(api)
	api.Use(server.authMiddleware())
	api.GET("/protected", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	valid := httptest.NewRecorder()
	validReq := httptest.NewRequest(http.MethodGet, session.Info().EmbedURL, nil)
	router.ServeHTTP(valid, validReq)
	if valid.Code == http.StatusUnauthorized {
		t.Fatalf("bootstrap with websheet token returned auth 401: %s", valid.Body.String())
	}
	if valid.Code != http.StatusFound {
		t.Fatalf("bootstrap with websheet token status=%d want %d body=%s", valid.Code, http.StatusFound, valid.Body.String())
	}

	missing := httptest.NewRecorder()
	missingReq := httptest.NewRequest(http.MethodGet, "/api/websheets/"+session.Info().ID, nil)
	router.ServeHTTP(missing, missingReq)
	if missing.Code != http.StatusUnauthorized {
		t.Fatalf("bootstrap without websheet token status=%d want %d body=%s", missing.Code, http.StatusUnauthorized, missing.Body.String())
	}

	protected := httptest.NewRecorder()
	protectedReq := httptest.NewRequest(http.MethodGet, "/api/protected", nil)
	router.ServeHTTP(protected, protectedReq)
	if protected.Code != http.StatusUnauthorized {
		t.Fatalf("protected route status=%d want %d", protected.Code, http.StatusUnauthorized)
	}
}

func TestWebsheetCallbackMarksTerminalSessionDone(t *testing.T) {
	gin.SetMode(gin.TestMode)

	broker := websheet.New(websheet.Config{AllowPrivateHosts: true})
	session, err := broker.Create(context.Background(), websheet.Request{URL: "https://203.0.113.10/start"})
	if err != nil {
		t.Fatal(err)
	}

	server := &Server{websheets: broker}
	router := gin.New()
	api := router.Group("/api")
	server.registerWebsheetRoutes(api)

	req := httptest.NewRequest(http.MethodPost, "/api/websheets/"+session.Info().ID+"/callback?token="+websheetToken(session), strings.NewReader(`{
		"source":"vowifi",
		"event":"entitlementChanged",
		"method":"e911AddressValidated",
		"resultCode":"success"
	}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("callback status=%d body=%s", rec.Code, rec.Body.String())
	}

	waitCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := session.WaitDone(waitCtx); err != nil {
		t.Fatalf("session was not marked done after terminal callback: %v", err)
	}
}

func TestWebsheetRoutesEnforceSessionOwnerWithoutBreakingCapability(t *testing.T) {
	server, router, adminToken, ownerToken := newFullMemberAccessTestServer(t)
	owner, _, err := server.authenticateSession(t.Context(), ownerToken, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.identityService.CreateUser(t.Context(), "other-member", "OtherMemberPassword9!", identity.RoleMember); err != nil {
		t.Fatal(err)
	}
	otherLogin, err := server.identityService.Login(t.Context(), "other-member", "OtherMemberPassword9!")
	if err != nil {
		t.Fatal(err)
	}

	broker := websheet.New(websheet.Config{AllowPrivateHosts: true})
	server.websheets = broker
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/plain"}},
			Body:       io.NopCloser(strings.NewReader("carrier page")),
			Request:    req,
		}, nil
	})
	accessSession, err := broker.Create(t.Context(), websheet.Request{
		URL:         "https://203.0.113.10/address",
		OwnerUserID: owner.UserID,
		Transport:   transport,
	})
	if err != nil {
		t.Fatal(err)
	}
	basePath := "/api/websheets/" + accessSession.Info().ID

	tests := []struct {
		name, method, path, token string
		want                      int
	}{
		{name: "owner bootstrap", method: http.MethodGet, path: basePath, token: ownerToken, want: http.StatusFound},
		{name: "other user bootstrap", method: http.MethodGet, path: basePath, token: otherLogin.Token, want: http.StatusUnauthorized},
		{name: "administrator bootstrap", method: http.MethodGet, path: basePath, token: adminToken, want: http.StatusFound},
		{name: "other user with capability", method: http.MethodGet, path: accessSession.Info().EmbedURL, token: otherLogin.Token, want: http.StatusFound},
		{name: "other user proxy", method: http.MethodGet, path: basePath + "/proxy", token: otherLogin.Token, want: http.StatusUnauthorized},
		{name: "owner proxy", method: http.MethodGet, path: basePath + "/proxy", token: ownerToken, want: http.StatusOK},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(test.method, test.path, nil)
			req.Header.Set("Authorization", "Bearer "+test.token)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != test.want {
				t.Fatalf("status=%d want %d body=%s", rec.Code, test.want, rec.Body.String())
			}
		})
	}

	callbackSession, err := broker.Create(t.Context(), websheet.Request{
		URL:         "https://203.0.113.11/address",
		OwnerUserID: owner.UserID,
	})
	if err != nil {
		t.Fatal(err)
	}
	callbackPath := "/api/websheets/" + callbackSession.Info().ID
	callbackBody := `{"event":"phoneServicesAccountStatusChanged","resultCode":"success"}`

	otherCallback := performWebsheetRequest(router, http.MethodPost, callbackPath+"/callback", otherLogin.Token, callbackBody)
	if otherCallback.Code != http.StatusUnauthorized {
		t.Fatalf("other user callback status=%d want %d body=%s", otherCallback.Code, http.StatusUnauthorized, otherCallback.Body.String())
	}
	ownerCallback := performWebsheetRequest(router, http.MethodPost, callbackPath+"/callback", ownerToken, callbackBody)
	if ownerCallback.Code != http.StatusOK {
		t.Fatalf("owner callback status=%d want %d body=%s", ownerCallback.Code, http.StatusOK, ownerCallback.Body.String())
	}
	waitCtx, cancelCallback := context.WithTimeout(t.Context(), time.Second)
	callback, err := callbackSession.WaitCallback(waitCtx)
	cancelCallback()
	if err != nil || callback.Event != "phoneServicesAccountStatusChanged" {
		t.Fatalf("owner callback poll result=%+v error=%v", callback, err)
	}

	otherDone := performWebsheetRequest(router, http.MethodPost, callbackPath+"/done", otherLogin.Token, "")
	if otherDone.Code != http.StatusUnauthorized {
		t.Fatalf("other user done status=%d want %d body=%s", otherDone.Code, http.StatusUnauthorized, otherDone.Body.String())
	}
	if _, err := broker.Get(callbackSession.Info().ID); err != nil {
		t.Fatalf("cross-user done removed session: %v", err)
	}
	ownerDone := performWebsheetRequest(router, http.MethodPost, callbackPath+"/done", ownerToken, "")
	if ownerDone.Code != http.StatusOK {
		t.Fatalf("owner done status=%d want %d body=%s", ownerDone.Code, http.StatusOK, ownerDone.Body.String())
	}
	waitDoneCtx, cancelDone := context.WithTimeout(t.Context(), time.Second)
	defer cancelDone()
	if err := callbackSession.WaitDone(waitDoneCtx); err != nil {
		t.Fatalf("owner done did not complete session: %v", err)
	}
	if _, err := broker.Get(callbackSession.Info().ID); !errors.Is(err, websheet.ErrNotFound) {
		t.Fatalf("completed session lookup error=%v, want ErrNotFound", err)
	}
}

func performWebsheetRequest(router http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func websheetToken(session *websheet.Session) string {
	parts := strings.SplitN(session.Info().EmbedURL, "token=", 2)
	if len(parts) != 2 {
		return ""
	}
	return parts[1]
}
