package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrUnauthorized       = errors.New("unauthorized")
	ErrUserNotFound       = errors.New("user not found")
	ErrUsernameTaken      = errors.New("username already exists")
	ErrInvalidUsername    = errors.New("invalid username")
	ErrInvalidPassword    = errors.New("invalid password")
	ErrInvalidRole        = errors.New("invalid role")
	ErrLastAdmin          = errors.New("cannot disable or demote the last enabled administrator")
)

const (
	defaultSessionTTL = 30 * 24 * time.Hour
	minimumPassword   = 8
)

type Options struct {
	Now        func() time.Time
	SessionTTL time.Duration
	BcryptCost int
	Random     io.Reader
}

type Service struct {
	db         *gorm.DB
	now        func() time.Time
	sessionTTL time.Duration
	bcryptCost int
	random     io.Reader
}

func NewService(database *gorm.DB) *Service {
	return NewServiceWithOptions(database, Options{})
}

func NewServiceWithOptions(database *gorm.DB, options Options) *Service {
	now := options.Now
	if now == nil {
		now = time.Now
	}
	ttl := options.SessionTTL
	if ttl <= 0 {
		ttl = defaultSessionTTL
	}
	cost := options.BcryptCost
	if cost == 0 {
		cost = bcrypt.DefaultCost
	}
	random := options.Random
	if random == nil {
		random = rand.Reader
	}
	return &Service{db: database, now: now, sessionTTL: ttl, bcryptCost: cost, random: random}
}

func Migrate(database *gorm.DB) error {
	if database == nil {
		return errors.New("identity: database is nil")
	}
	return database.AutoMigrate(&User{}, &WebSession{})
}

func NormalizeUsername(username string) string {
	return strings.ToLower(strings.TrimSpace(username))
}

func ValidateUsername(username string) error {
	trimmed := strings.TrimSpace(username)
	if trimmed == "" || len([]rune(trimmed)) > 64 {
		return ErrInvalidUsername
	}
	for _, r := range trimmed {
		if unicode.IsControl(r) {
			return ErrInvalidUsername
		}
	}
	return nil
}

func validatePassword(password string) error {
	if len([]rune(password)) < minimumPassword || strings.TrimSpace(password) == "" {
		return ErrInvalidPassword
	}
	return nil
}

func (s *Service) EnsureBootstrapAdmin(ctx context.Context, username, legacyPassword string) (User, bool, error) {
	if s == nil || s.db == nil {
		return User{}, false, errors.New("identity: database is nil")
	}
	var existing User
	err := s.db.WithContext(ctx).
		Where("role = ? AND disabled_at IS NULL", RoleAdmin).
		Order("created_at ASC, id ASC").
		First(&existing).Error
	if err == nil {
		return existing, false, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return User{}, false, err
	}
	if strings.TrimSpace(username) == "" {
		username = "admin"
	}
	if err := ValidateUsername(username); err != nil {
		return User{}, false, err
	}
	if strings.TrimSpace(legacyPassword) == "" {
		return User{}, false, fmt.Errorf("bootstrap admin: %w", ErrInvalidPassword)
	}
	passwordHash, err := s.bootstrapPasswordHash(legacyPassword)
	if err != nil {
		return User{}, false, err
	}
	now := s.now().UTC()
	created := User{
		ID:                 uuid.NewString(),
		Username:           strings.TrimSpace(username),
		UsernameNormalized: NormalizeUsername(username),
		PasswordHash:       passwordHash,
		Role:               RoleAdmin,
		SessionVersion:     1,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	var persisted User
	bootstrapped := false
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Recheck inside the transaction in case another startup path completed
		// while the bootstrap password was being prepared.
		var admin User
		err := tx.Where("role = ? AND disabled_at IS NULL", RoleAdmin).
			Order("created_at ASC, id ASC").
			First(&admin).Error
		if err == nil {
			persisted = admin
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		// A partially migrated database may already contain the configured Web
		// account as a member or disabled user. Recover that same stable identity
		// instead of leaving the installation with no enabled administrator.
		var configured User
		err = tx.Where("username_normalized = ?", created.UsernameNormalized).First(&configured).Error
		if err == nil {
			configured.Username = created.Username
			configured.UsernameNormalized = created.UsernameNormalized
			configured.PasswordHash = created.PasswordHash
			configured.Role = RoleAdmin
			configured.DisabledAt = nil
			if configured.SessionVersion == 0 {
				configured.SessionVersion = 1
			} else {
				configured.SessionVersion++
			}
			configured.UpdatedAt = now
			if err := tx.Save(&configured).Error; err != nil {
				return translateUsernameError(err)
			}
			if err := revokeSessionsTx(tx, configured.ID, now); err != nil {
				return err
			}
			persisted, bootstrapped = configured, true
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err := tx.Create(&created).Error; err != nil {
			return translateUsernameError(err)
		}
		persisted, bootstrapped = created, true
		return nil
	})
	if err != nil {
		return User{}, false, err
	}
	return persisted, bootstrapped, nil
}

func (s *Service) bootstrapPasswordHash(password string) (string, error) {
	if isBcryptHash(password) {
		if _, err := bcrypt.Cost([]byte(password)); err != nil {
			return "", fmt.Errorf("bootstrap admin password hash: %w", err)
		}
		return password, nil
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), s.bcryptCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

func isBcryptHash(value string) bool {
	return strings.HasPrefix(value, "$2a$") || strings.HasPrefix(value, "$2b$") || strings.HasPrefix(value, "$2y$")
}

func (s *Service) hashPassword(password string) (string, error) {
	if err := validatePassword(password); err != nil {
		return "", err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), s.bcryptCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

type LoginResult struct {
	Token   string
	User    User
	Session WebSession
}

func (s *Service) Login(ctx context.Context, username, password string) (LoginResult, error) {
	user, err := s.verifyCredentials(ctx, username, password)
	if err != nil {
		return LoginResult{}, err
	}
	return s.issueSession(ctx, user)
}

func (s *Service) VerifyCredentials(ctx context.Context, username, password string) (User, error) {
	return s.verifyCredentials(ctx, username, password)
}

func (s *Service) verifyCredentials(ctx context.Context, username, password string) (User, error) {
	if s == nil || s.db == nil {
		return User{}, ErrInvalidCredentials
	}
	var user User
	err := s.db.WithContext(ctx).Where("username_normalized = ?", NormalizeUsername(username)).First(&user).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return User{}, ErrInvalidCredentials
		}
		return User{}, err
	}
	if user.DisabledAt != nil || bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)) != nil {
		return User{}, ErrInvalidCredentials
	}
	return user, nil
}

func (s *Service) issueSession(ctx context.Context, user User) (LoginResult, error) {
	token, digest, err := s.newToken()
	if err != nil {
		return LoginResult{}, err
	}
	now := s.now().UTC()
	session := WebSession{
		ID:             uuid.NewString(),
		UserID:         user.ID,
		TokenDigest:    digest,
		SessionVersion: user.SessionVersion,
		ExpiresAt:      now.Add(s.sessionTTL),
		LastSeenAt:     now,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := s.db.WithContext(ctx).Create(&session).Error; err != nil {
		return LoginResult{}, err
	}
	return LoginResult{Token: token, User: user, Session: session}, nil
}

func (s *Service) newToken() (string, string, error) {
	raw := make([]byte, 32)
	if _, err := io.ReadFull(s.random, raw); err != nil {
		return "", "", err
	}
	token := "hd1_" + base64.RawURLEncoding.EncodeToString(raw)
	return token, tokenDigest(token), nil
}

func tokenDigest(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}

func (s *Service) Authenticate(ctx context.Context, token string) (Principal, WebSession, error) {
	if s == nil || s.db == nil || strings.TrimSpace(token) == "" {
		return Principal{}, WebSession{}, ErrUnauthorized
	}
	var session WebSession
	err := s.db.WithContext(ctx).Preload("User").Where("token_digest = ?", tokenDigest(strings.TrimSpace(token))).First(&session).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Principal{}, WebSession{}, ErrUnauthorized
		}
		return Principal{}, WebSession{}, err
	}
	now := s.now().UTC()
	if session.RevokedAt != nil || !now.Before(session.ExpiresAt) || session.User.DisabledAt != nil || session.SessionVersion != session.User.SessionVersion {
		return Principal{}, WebSession{}, ErrUnauthorized
	}
	if now.Sub(session.LastSeenAt) >= 5*time.Minute {
		_ = s.db.WithContext(ctx).Model(&WebSession{}).Where("id = ?", session.ID).Updates(map[string]any{
			"last_seen_at": now,
			"updated_at":   now,
		}).Error
		session.LastSeenAt = now
	}
	return Principal{
		UserID:         session.User.ID,
		Username:       session.User.Username,
		Role:           session.User.Role,
		SessionID:      session.ID,
		SessionVersion: session.User.SessionVersion,
	}, session, nil
}

func (s *Service) Logout(ctx context.Context, principal Principal) error {
	if strings.TrimSpace(principal.SessionID) == "" || strings.TrimSpace(principal.UserID) == "" {
		return ErrUnauthorized
	}
	now := s.now().UTC()
	result := s.db.WithContext(ctx).Model(&WebSession{}).
		Where("id = ? AND user_id = ? AND revoked_at IS NULL", principal.SessionID, principal.UserID).
		Updates(map[string]any{"revoked_at": now, "updated_at": now})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrUnauthorized
	}
	return nil
}

func (s *Service) ListUsers(ctx context.Context) ([]User, error) {
	var users []User
	err := s.db.WithContext(ctx).Order("created_at ASC, id ASC").Find(&users).Error
	return users, err
}

func (s *Service) GetUser(ctx context.Context, id string) (User, error) {
	var user User
	if err := s.db.WithContext(ctx).Where("id = ?", strings.TrimSpace(id)).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return User{}, ErrUserNotFound
		}
		return User{}, err
	}
	return user, nil
}

func (s *Service) CreateUser(ctx context.Context, username, password string, role Role) (User, error) {
	if err := ValidateUsername(username); err != nil {
		return User{}, err
	}
	if !role.Valid() {
		return User{}, ErrInvalidRole
	}
	hash, err := s.hashPassword(password)
	if err != nil {
		return User{}, err
	}
	now := s.now().UTC()
	user := User{
		ID:                 uuid.NewString(),
		Username:           strings.TrimSpace(username),
		UsernameNormalized: NormalizeUsername(username),
		PasswordHash:       hash,
		Role:               role,
		SessionVersion:     1,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	if err := s.db.WithContext(ctx).Create(&user).Error; err != nil {
		return User{}, translateUsernameError(err)
	}
	return user, nil
}

type UserPatch struct {
	Username *string
	Role     *Role
	Disabled *bool
}

func (s *Service) UpdateUser(ctx context.Context, id string, patch UserPatch) (User, error) {
	if patch.Username != nil {
		if err := ValidateUsername(*patch.Username); err != nil {
			return User{}, err
		}
	}
	if patch.Role != nil && !patch.Role.Valid() {
		return User{}, ErrInvalidRole
	}
	var result User
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Acquire SQLite's writer lock before checking the enabled-admin count.
		// This prevents two concurrent demotions from both observing two admins.
		lock := tx.Exec("UPDATE users SET updated_at = updated_at WHERE id = ?", strings.TrimSpace(id))
		if lock.Error != nil {
			return lock.Error
		}
		if lock.RowsAffected == 0 {
			return ErrUserNotFound
		}
		var user User
		if err := tx.Where("id = ?", strings.TrimSpace(id)).First(&user).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrUserNotFound
			}
			return err
		}

		willBeRole := user.Role
		if patch.Role != nil {
			willBeRole = *patch.Role
		}
		willBeDisabled := user.DisabledAt != nil
		if patch.Disabled != nil {
			willBeDisabled = *patch.Disabled
		}
		if user.Role == RoleAdmin && user.DisabledAt == nil && (willBeRole != RoleAdmin || willBeDisabled) {
			var enabledAdmins int64
			if err := tx.Model(&User{}).Where("role = ? AND disabled_at IS NULL", RoleAdmin).Count(&enabledAdmins).Error; err != nil {
				return err
			}
			if enabledAdmins <= 1 {
				return ErrLastAdmin
			}
		}

		invalidateSessions := willBeRole != user.Role || willBeDisabled != (user.DisabledAt != nil)
		if patch.Username != nil {
			user.Username = strings.TrimSpace(*patch.Username)
			user.UsernameNormalized = NormalizeUsername(*patch.Username)
		}
		user.Role = willBeRole
		if willBeDisabled && user.DisabledAt == nil {
			now := s.now().UTC()
			user.DisabledAt = &now
		} else if !willBeDisabled {
			user.DisabledAt = nil
		}
		if invalidateSessions {
			user.SessionVersion++
		}
		user.UpdatedAt = s.now().UTC()
		if err := tx.Save(&user).Error; err != nil {
			return translateUsernameError(err)
		}
		if invalidateSessions {
			if err := revokeSessionsTx(tx, user.ID, s.now().UTC()); err != nil {
				return err
			}
		}
		result = user
		return nil
	})
	return result, err
}

func (s *Service) DisableUser(ctx context.Context, id string) (User, error) {
	disabled := true
	return s.UpdateUser(ctx, id, UserPatch{Disabled: &disabled})
}

func (s *Service) ResetPassword(ctx context.Context, id, password string) (User, error) {
	hash, err := s.hashPassword(password)
	if err != nil {
		return User{}, err
	}
	var result User
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var user User
		if err := tx.Where("id = ?", strings.TrimSpace(id)).First(&user).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrUserNotFound
			}
			return err
		}
		user.PasswordHash = hash
		user.SessionVersion++
		user.UpdatedAt = s.now().UTC()
		if err := tx.Save(&user).Error; err != nil {
			return err
		}
		if err := revokeSessionsTx(tx, user.ID, s.now().UTC()); err != nil {
			return err
		}
		result = user
		return nil
	})
	return result, err
}

func (s *Service) ChangePassword(ctx context.Context, principal Principal, oldPassword, newPassword string) (LoginResult, error) {
	if strings.TrimSpace(principal.UserID) == "" {
		return LoginResult{}, ErrUnauthorized
	}
	hash, err := s.hashPassword(newPassword)
	if err != nil {
		return LoginResult{}, err
	}
	var user User
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("id = ?", principal.UserID).First(&user).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrUnauthorized
			}
			return err
		}
		if user.DisabledAt != nil || bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(oldPassword)) != nil {
			return ErrInvalidCredentials
		}
		user.PasswordHash = hash
		user.SessionVersion++
		user.UpdatedAt = s.now().UTC()
		if err := tx.Save(&user).Error; err != nil {
			return err
		}
		return revokeSessionsTx(tx, user.ID, s.now().UTC())
	})
	if err != nil {
		return LoginResult{}, err
	}
	return s.issueSession(ctx, user)
}

func (s *Service) RevokeUserSessions(ctx context.Context, id string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var user User
		if err := tx.Where("id = ?", strings.TrimSpace(id)).First(&user).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrUserNotFound
			}
			return err
		}
		user.SessionVersion++
		user.UpdatedAt = s.now().UTC()
		if err := tx.Save(&user).Error; err != nil {
			return err
		}
		return revokeSessionsTx(tx, user.ID, s.now().UTC())
	})
}

func revokeSessionsTx(tx *gorm.DB, userID string, now time.Time) error {
	return tx.Model(&WebSession{}).Where("user_id = ? AND revoked_at IS NULL", userID).Updates(map[string]any{
		"revoked_at": now,
		"updated_at": now,
	}).Error
}

func translateUsernameError(err error) error {
	if err == nil {
		return nil
	}
	message := strings.ToLower(err.Error())
	if errors.Is(err, gorm.ErrDuplicatedKey) || (strings.Contains(message, "unique") && strings.Contains(message, "username")) {
		return ErrUsernameTaken
	}
	return err
}
