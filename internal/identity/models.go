package identity

import "time"

type Role string

const (
	RoleAdmin  Role = "admin"
	RoleMember Role = "member"
)

func (r Role) Valid() bool {
	return r == RoleAdmin || r == RoleMember
}

type Permission string

const (
	PermissionUsersRead  Permission = "users:read"
	PermissionUsersWrite Permission = "users:write"
)

// Principal is the authenticated identity propagated through API and service
// contexts. Authorization is derived from the current database user on every
// request; callers cannot supply any of these fields.
type Principal struct {
	UserID         string `json:"user_id"`
	Username       string `json:"username"`
	Role           Role   `json:"role"`
	SessionID      string `json:"session_id"`
	SessionVersion uint64 `json:"session_version"`
}

func (p Principal) HasRole(roles ...Role) bool {
	for _, role := range roles {
		if p.Role == role {
			return true
		}
	}
	return false
}

func (p Principal) HasPermission(permission Permission) bool {
	if p.Role == RoleAdmin {
		return true
	}
	return false
}

// User is an application account. UsernameNormalized is the unique login key;
// Username remains the user-facing spelling.
type User struct {
	ID                 string     `gorm:"type:text;primaryKey" json:"id"`
	Username           string     `gorm:"type:text;not null" json:"username"`
	UsernameNormalized string     `gorm:"type:text;not null;uniqueIndex:idx_users_username_normalized" json:"-"`
	PasswordHash       string     `gorm:"type:text;not null" json:"-"`
	Role               Role       `gorm:"type:text;not null;index" json:"role"`
	DisabledAt         *time.Time `gorm:"index" json:"-"`
	SessionVersion     uint64     `gorm:"not null;default:1" json:"session_version"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

func (User) TableName() string { return "users" }

type WebSession struct {
	ID             string     `gorm:"type:text;primaryKey" json:"id"`
	UserID         string     `gorm:"type:text;not null;index" json:"user_id"`
	TokenDigest    string     `gorm:"type:text;not null;uniqueIndex:idx_web_sessions_token_digest" json:"-"`
	SessionVersion uint64     `gorm:"not null" json:"session_version"`
	ExpiresAt      time.Time  `gorm:"not null;index" json:"expires_at"`
	RevokedAt      *time.Time `gorm:"index" json:"revoked_at,omitempty"`
	LastSeenAt     time.Time  `gorm:"not null" json:"last_seen_at"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	User           User       `gorm:"foreignKey:UserID;constraint:OnUpdate:CASCADE,OnDelete:CASCADE" json:"-"`
}

func (WebSession) TableName() string { return "web_sessions" }

type UserView struct {
	ID             string    `json:"id"`
	Username       string    `json:"username"`
	Role           Role      `json:"role"`
	Disabled       bool      `json:"disabled"`
	SessionVersion uint64    `json:"session_version"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

func ViewUser(user User) UserView {
	return UserView{
		ID:             user.ID,
		Username:       user.Username,
		Role:           user.Role,
		Disabled:       user.DisabledAt != nil,
		SessionVersion: user.SessionVersion,
		CreatedAt:      user.CreatedAt,
		UpdatedAt:      user.UpdatedAt,
	}
}

type SessionView struct {
	ID        string    `json:"id"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}

func ViewSession(session WebSession) SessionView {
	return SessionView{ID: session.ID, ExpiresAt: session.ExpiresAt, CreatedAt: session.CreatedAt}
}
