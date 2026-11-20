package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/gin-gonic/gin"
	"github.com/yibaiba/hideck/internal/config"
	"github.com/yibaiba/hideck/internal/identity"
)

const defaultWebPassword = "admin"

const (
	minimumPasswordLength       = 8
	singleClassPassphraseLength = 12
)

type passwordCredentialStatus struct {
	ChangeRequired      bool   `json:"change_required"`
	Management          string `json:"management"`
	EnvironmentVariable string `json:"environment_variable,omitempty"`
}

type authenticatedSession struct {
	Token      string
	ExpiresAt  time.Time
	Credential passwordCredentialStatus
	User       identity.UserView
	Session    identity.SessionView
}

func isBcryptPassword(password string) bool {
	return strings.HasPrefix(password, "$2a$") ||
		strings.HasPrefix(password, "$2b$") ||
		strings.HasPrefix(password, "$2y$")
}

func passwordStatusFor(credentials config.WebConfig, suppliedPassword string) passwordCredentialStatus {
	if credentials.PasswordSource == config.WebPasswordSourceEnvironment {
		return passwordCredentialStatus{
			ChangeRequired:      passwordRequiresChange(credentials, suppliedPassword),
			Management:          string(config.WebPasswordSourceEnvironment),
			EnvironmentVariable: config.WebPasswordEnvironmentVariable,
		}
	}
	return passwordCredentialStatus{
		ChangeRequired: passwordRequiresChange(credentials, suppliedPassword),
		Management:     string(config.WebPasswordSourceConfigFile),
	}
}

func passwordRequiresChange(credentials config.WebConfig, suppliedPassword string) bool {
	if !isBcryptPassword(credentials.Password) {
		if credentials.PasswordSource != config.WebPasswordSourceEnvironment {
			return true
		}
		return isWeakPassword(credentials.Password, credentials.Username)
	}
	return suppliedPassword != "" && isWeakPassword(suppliedPassword, credentials.Username)
}

func isWeakPassword(password string, username string) bool {
	normalized := strings.TrimSpace(password)
	runes := []rune(normalized)
	if len(runes) < minimumPasswordLength {
		return true
	}
	if username != "" && strings.EqualFold(normalized, strings.TrimSpace(username)) {
		return true
	}
	lowercase := strings.ToLower(normalized)
	if isCommonPassword(lowercase) || containsObviousSequence(lowercase) || containsSingleRepeatedRune(runes) {
		return true
	}
	return len(runes) < singleClassPassphraseLength && passwordCharacterClasses(runes) < 2
}

func containsObviousSequence(password string) bool {
	sequences := []string{
		"0123456789", "1234567890", "9876543210",
		"abcdefghijklmnopqrstuvwxyz", "zyxwvutsrqponmlkjihgfedcba",
		"qwertyuiop", "poiuytrewq", "asdfghjkl", "lkjhgfdsa",
	}
	for _, sequence := range sequences {
		if strings.Contains(password, sequence) || strings.Contains(sequence, password) {
			return true
		}
	}
	return false
}

func isCommonPassword(password string) bool {
	switch password {
	case defaultWebPassword, "password", "password1", "12345678", "123456789", "qwerty123", "admin123", "hideck123":
		return true
	default:
		return false
	}
}

func containsSingleRepeatedRune(runes []rune) bool {
	for i := 1; i < len(runes); i++ {
		if runes[i] != runes[0] {
			return false
		}
	}
	return len(runes) > 0
}

func passwordCharacterClasses(runes []rune) int {
	classes := 0
	hasLower, hasUpper, hasDigit, hasSymbol := false, false, false, false
	for _, r := range runes {
		hasLower = hasLower || unicode.IsLower(r)
		hasUpper = hasUpper || unicode.IsUpper(r)
		hasDigit = hasDigit || unicode.IsDigit(r)
		hasSymbol = hasSymbol || (!unicode.IsLetter(r) && !unicode.IsDigit(r))
	}
	for _, present := range []bool{hasLower, hasUpper, hasDigit, hasSymbol} {
		if present {
			classes++
		}
	}
	return classes
}

func (s *Server) authSnapshot() config.WebConfig {
	s.authMu.RLock()
	defer s.authMu.RUnlock()
	return s.auth
}

func (s *Server) createLoginSession(ctx context.Context, username string, password string) (authenticatedSession, bool, error) {
	if s.identityInitErr != nil {
		return authenticatedSession{}, true, s.identityInitErr
	}
	if s.identityService != nil {
		result, err := s.identityService.Login(ctx, username, password)
		if errors.Is(err, identity.ErrInvalidCredentials) {
			return authenticatedSession{}, false, nil
		}
		if err != nil {
			return authenticatedSession{}, true, err
		}
		credential := passwordCredentialStatus{
			ChangeRequired: isWeakPassword(password, result.User.Username),
			Management:     "database",
		}
		return authenticatedSession{
			Token:      result.Token,
			ExpiresAt:  result.Session.ExpiresAt,
			Credential: credential,
			User:       identity.ViewUser(result.User),
			Session:    identity.ViewSession(result.Session),
		}, true, nil
	}
	s.authChangeMu.Lock()
	defer s.authChangeMu.Unlock()
	credentials := s.authSnapshot()
	if username != credentials.Username || !checkPassword(credentials.Password, password) {
		return authenticatedSession{}, false, nil
	}
	token, expiresAt, err := s.issueSessionToken()
	if err != nil {
		return authenticatedSession{}, true, err
	}
	return authenticatedSession{
		Token:      token,
		ExpiresAt:  expiresAt,
		Credential: s.passwordStatus(password),
		User: identity.UserView{
			ID:       "legacy-admin",
			Username: credentials.Username,
			Role:     identity.RoleAdmin,
		},
	}, true, nil
}

func (s *Server) setAuthPassword(password string) {
	s.authMu.Lock()
	s.auth.Password = password
	s.auth.PasswordSource = config.WebPasswordSourceConfigFile
	s.passwordChangeRequired = false
	s.authMu.Unlock()
}

func (s *Server) passwordStatus(suppliedPassword string) passwordCredentialStatus {
	s.authMu.Lock()
	defer s.authMu.Unlock()
	status := passwordStatusFor(s.auth, suppliedPassword)
	if status.ChangeRequired {
		s.passwordChangeRequired = true
	}
	status.ChangeRequired = s.passwordChangeRequired
	return status
}

func (s *Server) handleGetPasswordStatus(c *gin.Context) {
	if s.identityService != nil {
		c.JSON(http.StatusOK, passwordCredentialStatus{Management: "database"})
		return
	}
	c.JSON(http.StatusOK, s.passwordStatus(""))
}
