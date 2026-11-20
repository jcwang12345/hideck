package db

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/yibaiba/hideck/internal/identity"
	"gorm.io/gorm"
)

func TestMigratePhoneContactOwnersPreservesRowsForFirstAdmin(t *testing.T) {
	database := openPhoneContactOwnerTestDB(t, "legacy-owner.db")
	if err := database.Exec(`CREATE TABLE phone_contacts (
		number TEXT PRIMARY KEY, contact_id TEXT, name TEXT NOT NULL,
		created_at DATETIME, updated_at DATETIME
	)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Exec(`INSERT INTO phone_contacts (number, name) VALUES
		('10000', '甲'), ('10001', '乙')`).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&identity.User{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	users := []identity.User{
		{ID: "member-first", Username: "member", UsernameNormalized: "member", PasswordHash: "x", Role: identity.RoleMember, SessionVersion: 1, CreatedAt: now.Add(-time.Hour)},
		{ID: "admin-first", Username: "admin", UsernameNormalized: "admin", PasswordHash: "x", Role: identity.RoleAdmin, SessionVersion: 1, CreatedAt: now},
		{ID: "admin-later", Username: "admin2", UsernameNormalized: "admin2", PasswordHash: "x", Role: identity.RoleAdmin, SessionVersion: 1, CreatedAt: now.Add(time.Hour)},
	}
	if err := database.Create(&users).Error; err != nil {
		t.Fatal(err)
	}

	if err := MigratePhoneContactOwners(database); err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&PhoneContact{}); err != nil {
		t.Fatal(err)
	}
	if err := MigratePhoneContactIDs(database); err != nil {
		t.Fatal(err)
	}
	var rows []PhoneContact
	if err := database.Order("number ASC").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].OwnerID != "admin-first" || rows[1].OwnerID != "admin-first" {
		t.Fatalf("migrated rows = %+v", rows)
	}
	if rows[0].ContactID == "" || rows[1].ContactID == "" || rows[0].ContactID == rows[1].ContactID {
		t.Fatalf("contact IDs not repaired independently: %+v", rows)
	}

	previous := DB
	DB = database
	t.Cleanup(func() { DB = previous })
	if _, err := UpsertPhoneContactForOwner(t.Context(), "admin-later", "10000", "另一用户"); err != nil {
		t.Fatalf("same number for another owner: %v", err)
	}
	var count int64
	if err := database.Model(&PhoneContact{}).Where("number = ?", "10000").Count(&count).Error; err != nil || count != 2 {
		t.Fatalf("same-number row count=%d err=%v", count, err)
	}
}

func TestMigratePhoneContactOwnersClaimsSentinelAfterBootstrap(t *testing.T) {
	database := openPhoneContactOwnerTestDB(t, "sentinel-owner.db")
	if err := database.Exec(`CREATE TABLE phone_contacts (
		number TEXT PRIMARY KEY, name TEXT NOT NULL
	)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Exec(`INSERT INTO phone_contacts (number, name) VALUES ('10000', '旧联系人')`).Error; err != nil {
		t.Fatal(err)
	}
	if err := MigratePhoneContactOwners(database); err != nil {
		t.Fatal(err)
	}
	var before PhoneContact
	if err := database.First(&before).Error; err != nil {
		t.Fatal(err)
	}
	if before.OwnerID != LegacyPhoneContactOwnerID {
		t.Fatalf("owner before bootstrap = %q", before.OwnerID)
	}
	if err := AssignLegacyPhoneContacts(t.Context(), database, "real-admin-id"); err != nil {
		t.Fatal(err)
	}
	var after PhoneContact
	if err := database.First(&after).Error; err != nil {
		t.Fatal(err)
	}
	if after.OwnerID != "real-admin-id" || after.Number != "10000" || after.Name != "旧联系人" {
		t.Fatalf("contact after claim = %+v", after)
	}
}

func TestAssignLegacyPhoneContactsKeepsCurrentOwnerOnNumberConflict(t *testing.T) {
	database := openPhoneContactOwnerTestDB(t, "owner-conflict.db")
	if err := database.AutoMigrate(&PhoneContact{}); err != nil {
		t.Fatal(err)
	}
	rows := []PhoneContact{
		{OwnerID: LegacyPhoneContactOwnerID, Number: "10000", ContactID: "contact_00000000000000000000000000000001", Name: "旧名字"},
		{OwnerID: LegacyPhoneContactOwnerID, Number: "10001", ContactID: "contact_00000000000000000000000000000002", Name: "仅旧库"},
		{OwnerID: "admin-id", Number: "10000", ContactID: "contact_00000000000000000000000000000003", Name: "管理员新名字"},
	}
	if err := database.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if err := AssignLegacyPhoneContacts(t.Context(), database, "admin-id"); err != nil {
		t.Fatal(err)
	}
	var got []PhoneContact
	if err := database.Order("number ASC").Find(&got).Error; err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].OwnerID != "admin-id" || got[0].Name != "管理员新名字" || got[1].OwnerID != "admin-id" || got[1].Name != "仅旧库" {
		t.Fatalf("claimed contacts=%+v", got)
	}
	if err := AssignLegacyPhoneContacts(t.Context(), database, "admin-id"); err != nil {
		t.Fatalf("idempotent claim: %v", err)
	}
}

func TestInitMigratesLegacyPhoneContactsWithSingleSQLiteConnection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "init-legacy-owner.db")
	legacy, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := legacy.Exec(`CREATE TABLE phone_contacts (
		number TEXT PRIMARY KEY, name TEXT NOT NULL, created_at DATETIME, updated_at DATETIME
	)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := legacy.Exec(`INSERT INTO phone_contacts (number, name) VALUES ('10086', '旧客服')`).Error; err != nil {
		t.Fatal(err)
	}
	legacySQL, err := legacy.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := legacySQL.Close(); err != nil {
		t.Fatal(err)
	}

	previous := DB
	if err := Init(path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { DB = previous })
	var row PhoneContact
	if err := DB.First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.OwnerID != LegacyPhoneContactOwnerID || row.Number != "10086" || row.Name != "旧客服" || row.ContactID == "" {
		t.Fatalf("Init migrated contact = %+v", row)
	}
}

func TestPhoneContactsAreIsolatedByOwnerIncludingGroups(t *testing.T) {
	database := openPhoneContactOwnerTestDB(t, "owner-isolation.db")
	if err := database.AutoMigrate(&PhoneContact{}); err != nil {
		t.Fatal(err)
	}
	previous := DB
	DB = database
	t.Cleanup(func() { DB = previous })

	ownerA := "user-a"
	ownerB := "user-b"
	rowsA, err := UpsertPhoneContactBatchForOwnerWithRegion(t.Context(), ownerA, []PhoneContactInput{
		{Number: "10000", Name: "甲", GroupKey: "person-a"},
		{Number: "10001", Name: "甲", GroupKey: "person-a"},
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := UpsertPhoneContactForOwnerWithRegion(t.Context(), ownerB, PhoneContactInput{
		Number: "10000", Name: "乙", ContactID: rowsA[0].ContactID,
	}); err != nil {
		t.Fatal(err)
	}
	if got := LookupPhoneIdentityForOwner(t.Context(), ownerA, "10000"); got.Name != "甲" {
		t.Fatalf("owner A lookup = %+v", got)
	}
	if got := LookupPhoneIdentityForOwner(t.Context(), ownerB, "10000"); got.Name != "乙" {
		t.Fatalf("owner B lookup = %+v", got)
	}

	updatedB, err := UpdatePhoneContactGroupNameForOwner(t.Context(), ownerB, rowsA[0].ContactID, "乙新")
	if err != nil || len(updatedB) != 1 || updatedB[0].Name != "乙新" {
		t.Fatalf("owner B group update rows=%+v err=%v", updatedB, err)
	}
	for _, number := range []string{"10000", "10001"} {
		row, err := GetPhoneContactForOwner(t.Context(), ownerA, number)
		if err != nil || row.Name != "甲" {
			t.Fatalf("owner A row %s after B update = %+v err=%v", number, row, err)
		}
	}
	if _, err := DeletePhoneContactGroupForOwner(t.Context(), "user-c", rowsA[0].ContactID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("foreign group delete error = %v", err)
	}
	if _, err := DeletePhoneContactGroupForOwner(t.Context(), ownerB, rowsA[0].ContactID); err != nil {
		t.Fatal(err)
	}
	if _, err := GetPhoneContactForOwner(t.Context(), ownerB, "10000"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("owner B deleted row lookup error = %v", err)
	}
	if rows, err := ListPhoneContactsForOwner(t.Context(), ownerA); err != nil || len(rows) != 2 {
		t.Fatalf("owner A rows=%+v err=%v", rows, err)
	}
}

func TestLegacyPhoneContactEntryPointsUseFirstAdminWhenAvailable(t *testing.T) {
	database := openPhoneContactOwnerTestDB(t, "legacy-admin-semantics.db")
	if err := database.AutoMigrate(&identity.User{}, &PhoneContact{}); err != nil {
		t.Fatal(err)
	}
	admin := identity.User{
		ID: "admin-owner", Username: "admin", UsernameNormalized: "admin", PasswordHash: "x",
		Role: identity.RoleAdmin, SessionVersion: 1, CreatedAt: time.Now().UTC(),
	}
	if err := database.Create(&admin).Error; err != nil {
		t.Fatal(err)
	}
	previous := DB
	DB = database
	t.Cleanup(func() { DB = previous })
	row, err := UpsertPhoneContact(t.Context(), "10086", "管理员联系人")
	if err != nil {
		t.Fatal(err)
	}
	if row.OwnerID != admin.ID {
		t.Fatalf("legacy entry owner = %q, want %q", row.OwnerID, admin.ID)
	}
	if got := LookupPhoneIdentity(t.Context(), "10086"); got.Name != "管理员联系人" {
		t.Fatalf("legacy lookup = %+v", got)
	}
}

func openPhoneContactOwnerTestDB(t *testing.T, name string) *gorm.DB {
	t.Helper()
	database, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), name)), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	return database
}
