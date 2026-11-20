package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yibaiba/hideck/internal/config"
	"github.com/yibaiba/hideck/internal/db"
	"github.com/yibaiba/hideck/internal/identity"
	"github.com/yibaiba/hideck/pkg/logger"
)

const (
	principalGinKey  = "identity.principal"
	webSessionGinKey = "identity.web_session"
)

func (s *Server) initializeIdentity() {
	if db.DB == nil {
		return
	}
	service := identity.NewService(db.DB)
	s.identityService = service
	if err := identity.Migrate(db.DB); err != nil {
		s.identityInitErr = err
		logger.Error("身份数据库迁移失败", "err", err)
		return
	}
	user, created, err := service.EnsureBootstrapAdmin(context.Background(), s.auth.Username, s.auth.Password)
	if err != nil {
		s.identityInitErr = err
		logger.Error("迁移 Web 管理员账号失败", "err", err)
		return
	}
	contactOwnerID, err := s.legacyPhoneContactOwnerID(context.Background())
	if err != nil {
		s.identityInitErr = err
		logger.Error("查找旧联系人接管管理员失败", "err", err)
		return
	}
	if contactOwnerID != db.LegacyPhoneContactOwnerID {
		err = db.AssignLegacyPhoneContacts(context.Background(), db.DB, contactOwnerID)
	}
	if err != nil {
		s.identityInitErr = err
		logger.Error("迁移旧联系人到首个管理员失败", "err", err, "user_id", contactOwnerID)
		return
	}
	if created {
		logger.Info("已将 Web 配置账号迁移为首个管理员", "username", user.Username, "user_id", user.ID)
		// Once the database account exists, keep the legacy config usable for
		// disaster recovery without leaving its plaintext password on disk.
		if s.auth.PasswordSource == config.WebPasswordSourceConfigFile && !isBcryptPassword(s.auth.Password) {
			if err := config.UpdateWebCredentialsInFile(s.configPath, user.Username, user.PasswordHash); err != nil {
				logger.Warn("管理员已安全迁移到数据库，但旧配置密码无法改写为摘要", "err", err)
			} else {
				s.setAuthPassword(user.PasswordHash)
			}
		}
	}
}

func (s *Server) authenticateSession(ctx context.Context, token string, now time.Time) (identity.Principal, identity.WebSession, error) {
	if s.identityInitErr != nil {
		return identity.Principal{}, identity.WebSession{}, s.identityInitErr
	}
	if s.identityService != nil {
		return s.identityService.Authenticate(ctx, token)
	}
	if !s.isLegacySessionTokenValid(token, now) {
		return identity.Principal{}, identity.WebSession{}, identity.ErrUnauthorized
	}
	credentials := s.authSnapshot()
	return identity.Principal{
		UserID:         "legacy-admin",
		Username:       credentials.Username,
		Role:           identity.RoleAdmin,
		SessionVersion: 1,
	}, identity.WebSession{}, nil
}

func installPrincipal(c *gin.Context, principal identity.Principal, session identity.WebSession) {
	c.Set(principalGinKey, principal)
	c.Set(webSessionGinKey, session)
	c.Request = c.Request.WithContext(identity.WithPrincipal(c.Request.Context(), principal))
}

func CurrentPrincipal(c *gin.Context) (identity.Principal, bool) {
	if c == nil {
		return identity.Principal{}, false
	}
	value, ok := c.Get(principalGinKey)
	if !ok {
		return identity.Principal{}, false
	}
	principal, ok := value.(identity.Principal)
	return principal, ok
}

func currentWebSession(c *gin.Context) (identity.WebSession, bool) {
	if c == nil {
		return identity.WebSession{}, false
	}
	value, ok := c.Get(webSessionGinKey)
	if !ok {
		return identity.WebSession{}, false
	}
	session, ok := value.(identity.WebSession)
	return session, ok
}

func RequireRole(roles ...identity.Role) gin.HandlerFunc {
	return func(c *gin.Context) {
		principal, ok := CurrentPrincipal(c)
		if !ok {
			abortAuthorization(c, http.StatusUnauthorized, "unauthorized", "未授权")
			return
		}
		if !principal.HasRole(roles...) {
			abortAuthorization(c, http.StatusForbidden, "forbidden", "权限不足")
			return
		}
		c.Next()
	}
}

func RequirePermission(permission identity.Permission) gin.HandlerFunc {
	return func(c *gin.Context) {
		principal, ok := CurrentPrincipal(c)
		if !ok {
			abortAuthorization(c, http.StatusUnauthorized, "unauthorized", "未授权")
			return
		}
		if !principal.HasPermission(permission) {
			abortAuthorization(c, http.StatusForbidden, "forbidden", "权限不足")
			return
		}
		c.Next()
	}
}

func abortAuthorization(c *gin.Context, status int, code, message string) {
	c.JSON(status, gin.H{
		"status":     "error",
		"code":       code,
		"message":    message,
		"request_id": requestID(c),
	})
	c.Abort()
}

func (s *Server) registerIdentityRoutes(api *gin.RouterGroup) {
	api.GET("/auth/me", s.handleAuthMe)
	api.POST("/auth/logout", s.handleAuthLogout)
}

func (s *Server) registerIdentityAdminRoutes(admin *gin.RouterGroup) {
	admin.GET("/users", s.handleAdminListUsers)
	admin.POST("/users", s.handleAdminCreateUser)
	admin.GET("/users/:id", s.handleAdminGetUser)
	admin.PATCH("/users/:id", s.handleAdminUpdateUser)
	admin.PUT("/users/:id", s.handleAdminUpdateUser)
	admin.DELETE("/users/:id", s.handleAdminDisableUser)
	admin.POST("/users/:id/password", s.handleAdminResetPassword)
	admin.POST("/users/:id/sessions/revoke", s.handleAdminRevokeSessions)
}

func (s *Server) handleAuthMe(c *gin.Context) {
	principal, ok := CurrentPrincipal(c)
	if !ok {
		abortAuthorization(c, http.StatusUnauthorized, "unauthorized", "未授权")
		return
	}
	if s.identityService == nil {
		c.JSON(http.StatusOK, gin.H{
			"status": "ok",
			"user": identity.UserView{
				ID:             principal.UserID,
				Username:       principal.Username,
				Role:           principal.Role,
				SessionVersion: principal.SessionVersion,
			},
			"session": identity.SessionView{},
		})
		return
	}
	user, err := s.identityService.GetUser(c.Request.Context(), principal.UserID)
	if err != nil {
		writeIdentityError(c, err)
		return
	}
	session, _ := currentWebSession(c)
	c.JSON(http.StatusOK, gin.H{
		"status":  "ok",
		"user":    identity.ViewUser(user),
		"session": identity.ViewSession(session),
	})
}

func (s *Server) handleAuthLogout(c *gin.Context) {
	principal, ok := CurrentPrincipal(c)
	if !ok {
		abortAuthorization(c, http.StatusUnauthorized, "unauthorized", "未授权")
		return
	}
	if s.identityService != nil {
		if err := s.identityService.Logout(c.Request.Context(), principal); err != nil {
			writeIdentityError(c, err)
			return
		}
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) requireIdentityService(c *gin.Context) (*identity.Service, bool) {
	if s.identityInitErr != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "error", "code": "identity_unavailable", "message": "身份服务不可用", "request_id": requestID(c),
		})
		return nil, false
	}
	if s.identityService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "error", "code": "identity_unavailable", "message": "身份服务未启用", "request_id": requestID(c),
		})
		return nil, false
	}
	return s.identityService, true
}

func (s *Server) handleAdminListUsers(c *gin.Context) {
	service, ok := s.requireIdentityService(c)
	if !ok {
		return
	}
	users, err := service.ListUsers(c.Request.Context())
	if err != nil {
		writeIdentityError(c, err)
		return
	}
	views := make([]identity.UserView, 0, len(users))
	for _, user := range users {
		views = append(views, identity.ViewUser(user))
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok", "users": views})
}

func (s *Server) handleAdminGetUser(c *gin.Context) {
	service, ok := s.requireIdentityService(c)
	if !ok {
		return
	}
	user, err := service.GetUser(c.Request.Context(), c.Param("id"))
	if err != nil {
		writeIdentityError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok", "user": identity.ViewUser(user)})
}

func (s *Server) handleAdminCreateUser(c *gin.Context) {
	service, ok := s.requireIdentityService(c)
	if !ok {
		return
	}
	var request struct {
		Username string        `json:"username"`
		Password string        `json:"password"`
		Role     identity.Role `json:"role"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "code": "invalid_request", "message": "参数错误"})
		return
	}
	if request.Role == "" {
		request.Role = identity.RoleMember
	}
	if isWeakPassword(request.Password, request.Username) {
		writeWeakPassword(c)
		return
	}
	user, err := service.CreateUser(c.Request.Context(), request.Username, request.Password, request.Role)
	if err != nil {
		writeIdentityError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"status": "ok", "user": identity.ViewUser(user)})
}

func (s *Server) handleAdminUpdateUser(c *gin.Context) {
	service, ok := s.requireIdentityService(c)
	if !ok {
		return
	}
	var request struct {
		Username *string        `json:"username"`
		Role     *identity.Role `json:"role"`
		Disabled *bool          `json:"disabled"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "code": "invalid_request", "message": "参数错误"})
		return
	}
	user, err := service.UpdateUser(c.Request.Context(), c.Param("id"), identity.UserPatch{
		Username: request.Username,
		Role:     request.Role,
		Disabled: request.Disabled,
	})
	if err != nil {
		writeIdentityError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok", "user": identity.ViewUser(user)})
}

func (s *Server) handleAdminDisableUser(c *gin.Context) {
	service, ok := s.requireIdentityService(c)
	if !ok {
		return
	}
	user, err := service.DisableUser(c.Request.Context(), c.Param("id"))
	if err != nil {
		writeIdentityError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok", "user": identity.ViewUser(user)})
}

func (s *Server) handleAdminResetPassword(c *gin.Context) {
	service, ok := s.requireIdentityService(c)
	if !ok {
		return
	}
	var request struct {
		Password    string `json:"password"`
		NewPassword string `json:"new_password"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "code": "invalid_request", "message": "参数错误"})
		return
	}
	password := request.Password
	if password == "" {
		password = request.NewPassword
	}
	target, err := service.GetUser(c.Request.Context(), c.Param("id"))
	if err != nil {
		writeIdentityError(c, err)
		return
	}
	if isWeakPassword(password, target.Username) {
		writeWeakPassword(c)
		return
	}
	user, err := service.ResetPassword(c.Request.Context(), target.ID, password)
	if err != nil {
		writeIdentityError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok", "user": identity.ViewUser(user)})
}

func (s *Server) handleAdminRevokeSessions(c *gin.Context) {
	service, ok := s.requireIdentityService(c)
	if !ok {
		return
	}
	if err := service.RevokeUserSessions(c.Request.Context(), c.Param("id")); err != nil {
		writeIdentityError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func writeWeakPassword(c *gin.Context) {
	c.JSON(http.StatusBadRequest, gin.H{
		"status": "error", "code": "weak_password",
		"message": "密码强度不足：至少 8 位；少于 12 位时需包含至少两类字符，且不能使用常见密码或与用户名相同",
	})
}

func writeIdentityError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, identity.ErrInvalidCredentials):
		c.JSON(http.StatusUnauthorized, gin.H{"status": "error", "code": "invalid_credentials", "message": "用户名或密码错误"})
	case errors.Is(err, identity.ErrUnauthorized):
		c.JSON(http.StatusUnauthorized, gin.H{"status": "error", "code": "unauthorized", "message": "未授权"})
	case errors.Is(err, identity.ErrUserNotFound):
		c.JSON(http.StatusNotFound, gin.H{"status": "error", "code": "user_not_found", "message": "用户不存在"})
	case errors.Is(err, identity.ErrUsernameTaken):
		c.JSON(http.StatusConflict, gin.H{"status": "error", "code": "username_taken", "message": "用户名已存在"})
	case errors.Is(err, identity.ErrLastAdmin):
		c.JSON(http.StatusConflict, gin.H{"status": "error", "code": "last_admin", "message": "不能禁用或降级最后一个可用管理员"})
	case errors.Is(err, identity.ErrInvalidUsername), errors.Is(err, identity.ErrInvalidRole), errors.Is(err, identity.ErrInvalidPassword):
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "code": "invalid_user", "message": "用户参数无效"})
	default:
		logger.Error("身份服务请求失败", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "code": "internal_error", "message": "身份服务请求失败"})
	}
}

func bearerToken(header string) string {
	parts := strings.Fields(strings.TrimSpace(header))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}
