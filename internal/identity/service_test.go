package identity

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

func newTestService(t *testing.T) (*Service, *gorm.DB, *time.Time) {
	t.Helper()
	database, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "identity.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := Migrate(database); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.November, 20, 8, 0, 0, 0, time.UTC)
	service := NewServiceWithOptions(database, Options{
		Now:        func() time.Time { return now },
		SessionTTL: time.Hour,
		BcryptCost: bcrypt.MinCost,
	})
	return service, database, &now
}

func TestBootstrapAdminHashesLegacyPasswordAndIsIdempotent(t *testing.T) {
	service, database, _ := newTestService(t)
	ctx := context.Background()
	admin, created, err := service.EnsureBootstrapAdmin(ctx, "Admin", "admin")
	if err != nil {
		t.Fatal(err)
	}
	if !created || admin.Role != RoleAdmin || admin.UsernameNormalized != "admin" {
		t.Fatalf("unexpected bootstrap user: %+v created=%v", admin, created)
	}
	if admin.PasswordHash == "admin" || bcrypt.CompareHashAndPassword([]byte(admin.PasswordHash), []byte("admin")) != nil {
		t.Fatal("legacy password was not stored as a valid bcrypt digest")
	}
	second, created, err := service.EnsureBootstrapAdmin(ctx, "different", "DifferentPassword9!")
	if err != nil {
		t.Fatal(err)
	}
	if created || second.ID != admin.ID {
		t.Fatalf("bootstrap was not idempotent: first=%s second=%s created=%v", admin.ID, second.ID, created)
	}
	var count int64
	if err := database.Model(&User{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("user count=%d err=%v", count, err)
	}
}

func TestBootstrapAdminRecoversConfiguredMemberWhenNoAdminExists(t *testing.T) {
	service, database, _ := newTestService(t)
	ctx := context.Background()
	member, err := service.CreateUser(ctx, "Admin", "MemberPassword9!", RoleMember)
	if err != nil {
		t.Fatal(err)
	}
	oldSession, err := service.Login(ctx, "admin", "MemberPassword9!")
	if err != nil {
		t.Fatal(err)
	}

	recovered, bootstrapped, err := service.EnsureBootstrapAdmin(ctx, "Admin", "LegacyAdminPassword9!")
	if err != nil {
		t.Fatal(err)
	}
	if !bootstrapped || recovered.ID != member.ID || recovered.Role != RoleAdmin || recovered.DisabledAt != nil {
		t.Fatalf("recovered user=%+v bootstrapped=%v member=%+v", recovered, bootstrapped, member)
	}
	if _, _, err := service.Authenticate(ctx, oldSession.Token); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("pre-recovery session err=%v, want unauthorized", err)
	}
	if _, err := service.Login(ctx, "admin", "MemberPassword9!"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("pre-recovery password err=%v, want invalid credentials", err)
	}
	if _, err := service.Login(ctx, "admin", "LegacyAdminPassword9!"); err != nil {
		t.Fatalf("legacy administrator password did not recover access: %v", err)
	}
	var count int64
	if err := database.Model(&User{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("user count=%d err=%v", count, err)
	}
}

func TestBootstrapAdminReturnsEnabledAdminAfterEarlierMember(t *testing.T) {
	service, _, _ := newTestService(t)
	ctx := context.Background()
	if _, err := service.CreateUser(ctx, "member-first", "MemberPassword9!", RoleMember); err != nil {
		t.Fatal(err)
	}
	admin, err := service.CreateUser(ctx, "existing-admin", "AdminPassword9!", RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	got, bootstrapped, err := service.EnsureBootstrapAdmin(ctx, "legacy-admin", "LegacyPassword9!")
	if err != nil {
		t.Fatal(err)
	}
	if bootstrapped || got.ID != admin.ID || got.Role != RoleAdmin {
		t.Fatalf("bootstrap selected user=%+v bootstrapped=%v, want admin=%+v", got, bootstrapped, admin)
	}
}

func TestOpaqueSessionStoresOnlyDigestAndExpires(t *testing.T) {
	service, database, now := newTestService(t)
	ctx := context.Background()
	if _, _, err := service.EnsureBootstrapAdmin(ctx, "admin", "CorrectHorse9!"); err != nil {
		t.Fatal(err)
	}
	result, err := service.Login(ctx, "ADMIN", "CorrectHorse9!")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(result.Token, "hd1_") || result.Session.TokenDigest == result.Token || strings.Contains(result.Session.TokenDigest, result.Token) {
		t.Fatalf("token was not opaque/digested: token=%q digest=%q", result.Token, result.Session.TokenDigest)
	}
	var persisted WebSession
	if err := database.First(&persisted, "id = ?", result.Session.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.TokenDigest != tokenDigest(result.Token) {
		t.Fatal("persisted token digest does not match")
	}
	principal, _, err := service.Authenticate(ctx, result.Token)
	if err != nil || principal.UserID != result.User.ID || principal.SessionID != result.Session.ID {
		t.Fatalf("authenticate principal=%+v err=%v", principal, err)
	}
	*now = now.Add(time.Hour)
	if _, _, err := service.Authenticate(ctx, result.Token); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expired session err=%v want unauthorized", err)
	}
}

func TestSessionVersionRevokesExistingSessions(t *testing.T) {
	service, _, _ := newTestService(t)
	ctx := context.Background()
	if _, _, err := service.EnsureBootstrapAdmin(ctx, "admin", "CorrectHorse9!"); err != nil {
		t.Fatal(err)
	}
	member, err := service.CreateUser(ctx, "Member", "MemberPassword9!", RoleMember)
	if err != nil {
		t.Fatal(err)
	}
	login, err := service.Login(ctx, "member", "MemberPassword9!")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.RevokeUserSessions(ctx, member.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Authenticate(ctx, login.Token); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("revoked session err=%v want unauthorized", err)
	}
	login, err = service.Login(ctx, "member", "MemberPassword9!")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ResetPassword(ctx, member.ID, "ReplacementPassword9!"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Authenticate(ctx, login.Token); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("password-reset session err=%v want unauthorized", err)
	}
	if _, err := service.Login(ctx, "member", "MemberPassword9!"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("old password err=%v want invalid credentials", err)
	}
	if _, err := service.Login(ctx, "member", "ReplacementPassword9!"); err != nil {
		t.Fatalf("replacement password login: %v", err)
	}
}

func TestLastEnabledAdminCannotBeDisabledOrDemoted(t *testing.T) {
	service, _, _ := newTestService(t)
	ctx := context.Background()
	admin, _, err := service.EnsureBootstrapAdmin(ctx, "admin", "CorrectHorse9!")
	if err != nil {
		t.Fatal(err)
	}
	disabled := true
	if _, err := service.UpdateUser(ctx, admin.ID, UserPatch{Disabled: &disabled}); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("disable last admin err=%v", err)
	}
	memberRole := RoleMember
	if _, err := service.UpdateUser(ctx, admin.ID, UserPatch{Role: &memberRole}); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("demote last admin err=%v", err)
	}
	if _, err := service.CreateUser(ctx, "second-admin", "SecondAdminPassword9!", RoleAdmin); err != nil {
		t.Fatal(err)
	}
	updated, err := service.UpdateUser(ctx, admin.ID, UserPatch{Disabled: &disabled})
	if err != nil || updated.DisabledAt == nil {
		t.Fatalf("disable with another admin user=%+v err=%v", updated, err)
	}
}

func TestUsernameUniquenessUsesNormalizedValue(t *testing.T) {
	service, _, _ := newTestService(t)
	ctx := context.Background()
	if _, _, err := service.EnsureBootstrapAdmin(ctx, "Admin", "CorrectHorse9!"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateUser(ctx, " admin ", "AnotherPassword9!", RoleMember); !errors.Is(err, ErrUsernameTaken) {
		t.Fatalf("duplicate normalized username err=%v", err)
	}
}
