package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/yibaiba/hideck/internal/db"
	"github.com/yibaiba/hideck/internal/identity"
)

// phoneContactOwnerID binds every REST operation to the authenticated user.
// MCP has no end-user principal, so it deliberately retains the historical
// administrator semantics instead of accepting an owner from tool arguments.
func (s *Server) phoneContactOwnerID(c businessContext) (string, bool) {
	if rest, ok := c.(*gin.Context); ok {
		principal, found := CurrentPrincipal(rest)
		if !found || strings.TrimSpace(principal.UserID) == "" {
			abortAuthorization(rest, http.StatusUnauthorized, "unauthorized", "未授权")
			return "", false
		}
		return strings.TrimSpace(principal.UserID), true
	}
	ownerID, err := s.legacyPhoneContactOwnerID(businessRequestContext(c))
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "error", "code": "contact_owner_unavailable", "message": "联系人归属不可用",
		})
		return "", false
	}
	return ownerID, true
}

func (s *Server) legacyPhoneContactOwnerID(ctx context.Context) (string, error) {
	if s == nil || s.identityService == nil {
		return db.LegacyPhoneContactOwnerID, nil
	}
	users, err := s.identityService.ListUsers(ctx)
	if err != nil {
		return "", err
	}
	for _, user := range users {
		if user.Role == identity.RoleAdmin && user.DisabledAt == nil && strings.TrimSpace(user.ID) != "" {
			return strings.TrimSpace(user.ID), nil
		}
	}
	return db.LegacyPhoneContactOwnerID, nil
}
