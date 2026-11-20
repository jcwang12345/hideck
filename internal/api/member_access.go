package api

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/yibaiba/hideck/internal/identity"
)

// memberRouteKey is deliberately based on Gin's normalized route pattern,
// rather than the caller-controlled request path. This keeps dynamic resource
// identifiers (call IDs, media IDs and recording names) inside the same exact
// authorization rule without allowing an unreviewed route by prefix.
type memberRouteKey struct {
	method string
	path   string
}

// memberRouteAllowlist is the complete set of authenticated REST operations a
// non-administrator may reach. Handlers behind these routes still enforce
// ownership and number-pool scope; this middleware is the fail-closed boundary
// that prevents legacy device-management routes from bypassing those checks.
var memberRouteAllowlist = map[memberRouteKey]struct{}{
	{http.MethodGet, "/api/auth/me"}:      {},
	{http.MethodPost, "/api/auth/logout"}: {},

	{http.MethodGet, "/api/settings/password"}:  {},
	{http.MethodPost, "/api/settings/password"}: {},

	{http.MethodGet, "/api/number-pools"}:         {},
	{http.MethodGet, "/api/call-records"}:         {},
	{http.MethodPost, "/api/phone/call-requests"}: {},

	{http.MethodPost, "/api/phone/media"}:                 {},
	{http.MethodDelete, "/api/phone/media/:media_id"}:     {},
	{http.MethodGet, "/api/phone/calls/active"}:           {},
	{http.MethodPost, "/api/phone/calls/:call_id/answer"}: {},
	{http.MethodPost, "/api/phone/calls/:call_id/reject"}: {},
	{http.MethodPost, "/api/phone/calls/:call_id/dtmf"}:   {},
	{http.MethodPost, "/api/phone/calls/:call_id/hold"}:   {},
	{http.MethodPost, "/api/phone/calls/:call_id/resume"}: {},
	{http.MethodPost, "/api/phone/calls/:call_id/switch"}: {},
	{http.MethodDelete, "/api/phone/calls/:call_id"}:      {},
	{http.MethodPut, "/api/phone/calls/:call_id/media"}:   {},
	{http.MethodGet, "/api/phone/events"}:                 {},
	{http.MethodGet, "/api/phone/history"}:                {},
	{http.MethodPost, "/api/phone/history/delete"}:        {},
	{http.MethodGet, "/api/phone/recordings/:recording"}:  {},
	{http.MethodGet, "/api/phone/lookup"}:                 {},
	{http.MethodGet, "/api/phone/contacts"}:               {},
	{http.MethodPut, "/api/phone/contacts"}:               {},
	{http.MethodDelete, "/api/phone/contacts"}:            {},
	{http.MethodPut, "/api/phone/contacts/group"}:         {},
	{http.MethodDelete, "/api/phone/contacts/group"}:      {},
	{http.MethodPost, "/api/phone/contacts/batch"}:        {},
	{http.MethodPost, "/api/phone/contacts/delete"}:       {},
	{http.MethodGet, "/api/phone/contacts/export"}:        {},
	{http.MethodPost, "/api/phone/contacts/import"}:       {},

	{http.MethodPost, "/api/sms/send"}:                {},
	{http.MethodGet, "/api/sms/delivery/:message_id"}: {},
	{http.MethodGet, "/api/sms/contacts"}:             {},
	{http.MethodGet, "/api/sms/notifications"}:        {},
	{http.MethodGet, "/api/sms/events"}:               {},
	{http.MethodGet, "/api/sms/messages/:id"}:         {},
	{http.MethodGet, "/api/sms/thread"}:               {},
	{http.MethodPatch, "/api/sms/thread"}:             {},
	{http.MethodDelete, "/api/sms/messages/:id"}:      {},
	{http.MethodDelete, "/api/sms/thread"}:            {},
}

func memberRouteAllowed(method, routePath string) bool {
	_, ok := memberRouteAllowlist[memberRouteKey{
		method: strings.ToUpper(strings.TrimSpace(method)),
		path:   strings.TrimSpace(routePath),
	}]
	return ok
}

func (s *Server) memberRouteGuardMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		principal, ok := CurrentPrincipal(c)
		if !ok {
			abortAuthorization(c, http.StatusUnauthorized, "unauthorized", "未授权")
			return
		}
		if principal.Role == identity.RoleAdmin {
			c.Next()
			return
		}
		if principal.Role != identity.RoleMember || !memberRouteAllowed(c.Request.Method, c.FullPath()) {
			abortAuthorization(c, http.StatusForbidden, "forbidden", "权限不足")
			return
		}
		c.Next()
	}
}
