package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

const phoneContactOwnerMigrationTable = "phone_contacts_owner_v2"

type phoneContactColumn struct {
	Name       string
	PrimaryKey int
}

// MigratePhoneContactOwners converts the legacy number-primary-key table to a
// composite (owner_id, number) table. Existing global rows are assigned to the
// first enabled administrator when one already exists, otherwise to the
// compatibility owner that initializeIdentity later moves to the first admin.
func MigratePhoneContactOwners(database *gorm.DB) error {
	if database == nil || !database.Migrator().HasTable(&PhoneContact{}) {
		return nil
	}
	ownerID, err := legacyPhoneContactMigrationOwner(database)
	if err != nil {
		return err
	}
	return database.Transaction(func(tx *gorm.DB) error {
		columns, err := phoneContactColumns(tx)
		if err != nil {
			return err
		}
		if phoneContactOwnerSchemaReady(columns) {
			return tx.Model(&PhoneContact{}).
				Where("TRIM(COALESCE(owner_id, '')) = ''").
				Update("owner_id", ownerID).Error
		}
		return rebuildPhoneContactsWithOwners(tx, columns, ownerID)
	})
}

// AssignLegacyPhoneContacts atomically claims contacts created before the
// identity service knew the first administrator's database ID.
func AssignLegacyPhoneContacts(ctx context.Context, database *gorm.DB, ownerID string) error {
	ownerID = strings.TrimSpace(ownerID)
	if database == nil || ownerID == "" {
		return ErrInvalidPhoneContact
	}
	if !database.Migrator().HasTable(&PhoneContact{}) || !database.Migrator().HasColumn(&PhoneContact{}, "owner_id") {
		return nil
	}
	return database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// A restart after a partially completed rollout can leave both the
		// compatibility owner and the real administrator with the same number.
		// Preserve the administrator's current row, discard only the duplicate
		// compatibility row, then move every remaining legacy contact atomically.
		if ownerID != LegacyPhoneContactOwnerID {
			if err := tx.Exec(`DELETE FROM phone_contacts
				WHERE owner_id = ?
				AND EXISTS (
					SELECT 1 FROM phone_contacts AS current
					WHERE current.owner_id = ? AND current.number = phone_contacts.number
				)`, LegacyPhoneContactOwnerID, ownerID).Error; err != nil {
				return err
			}
		}
		return tx.Model(&PhoneContact{}).
			Where("owner_id = ?", LegacyPhoneContactOwnerID).
			Update("owner_id", ownerID).Error
	})
}

func legacyPhoneContactMigrationOwner(database *gorm.DB) (string, error) {
	if !database.Migrator().HasTable("users") ||
		!database.Migrator().HasColumn("users", "id") ||
		!database.Migrator().HasColumn("users", "role") ||
		!database.Migrator().HasColumn("users", "disabled_at") ||
		!database.Migrator().HasColumn("users", "created_at") {
		return LegacyPhoneContactOwnerID, nil
	}
	var ownerID string
	err := database.Raw(`
		SELECT id FROM users
		WHERE role = ? AND disabled_at IS NULL
		ORDER BY created_at ASC, id ASC
		LIMIT 1`, "admin").Scan(&ownerID).Error
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(ownerID) == "" {
		return LegacyPhoneContactOwnerID, nil
	}
	return strings.TrimSpace(ownerID), nil
}

func legacyPhoneContactRuntimeOwner() string {
	if DB == nil {
		return LegacyPhoneContactOwnerID
	}
	ownerID, err := legacyPhoneContactMigrationOwner(DB)
	if err != nil || strings.TrimSpace(ownerID) == "" {
		return LegacyPhoneContactOwnerID
	}
	return ownerID
}

func phoneContactColumns(database *gorm.DB) (map[string]phoneContactColumn, error) {
	rows, err := database.Raw(`PRAGMA table_info('phone_contacts')`).Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns := make(map[string]phoneContactColumn)
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return nil, err
		}
		columns[strings.ToLower(name)] = phoneContactColumn{Name: name, PrimaryKey: primaryKey}
	}
	return columns, rows.Err()
}

func phoneContactOwnerSchemaReady(columns map[string]phoneContactColumn) bool {
	owner, hasOwner := columns["owner_id"]
	number, hasNumber := columns["number"]
	if !hasOwner || !hasNumber || owner.PrimaryKey == 0 || number.PrimaryKey == 0 {
		return false
	}
	primaryKeys := 0
	for _, column := range columns {
		if column.PrimaryKey > 0 {
			primaryKeys++
		}
	}
	return primaryKeys == 2
}

func rebuildPhoneContactsWithOwners(tx *gorm.DB, columns map[string]phoneContactColumn, ownerID string) error {
	if _, ok := columns["number"]; !ok {
		return errors.New("phone contacts migration: legacy table has no number column")
	}
	if _, ok := columns["name"]; !ok {
		return errors.New("phone contacts migration: legacy table has no name column")
	}
	var before int64
	if err := tx.Table("phone_contacts").Count(&before).Error; err != nil {
		return err
	}
	if err := tx.Exec(`DROP TABLE IF EXISTS ` + phoneContactOwnerMigrationTable).Error; err != nil {
		return err
	}
	if err := tx.Exec(`CREATE TABLE ` + phoneContactOwnerMigrationTable + ` (
		owner_id TEXT NOT NULL,
		number TEXT NOT NULL,
		contact_id TEXT,
		name TEXT NOT NULL,
		created_at DATETIME,
		updated_at DATETIME,
		PRIMARY KEY (owner_id, number)
	)`).Error; err != nil {
		return err
	}
	ownerExpression := "?"
	if _, ok := columns["owner_id"]; ok {
		ownerExpression = "COALESCE(NULLIF(TRIM(owner_id), ''), ?)"
	}
	contactIDExpression := "''"
	if _, ok := columns["contact_id"]; ok {
		contactIDExpression = "COALESCE(contact_id, '')"
	}
	createdAtExpression := "CURRENT_TIMESTAMP"
	if _, ok := columns["created_at"]; ok {
		createdAtExpression = "created_at"
	}
	updatedAtExpression := "CURRENT_TIMESTAMP"
	if _, ok := columns["updated_at"]; ok {
		updatedAtExpression = "updated_at"
	}
	statement := fmt.Sprintf(`INSERT INTO %s
		(owner_id, number, contact_id, name, created_at, updated_at)
		SELECT %s, number, %s, name, %s, %s FROM phone_contacts`,
		phoneContactOwnerMigrationTable, ownerExpression, contactIDExpression,
		createdAtExpression, updatedAtExpression)
	if err := tx.Exec(statement, ownerID).Error; err != nil {
		return err
	}
	var after int64
	if err := tx.Table(phoneContactOwnerMigrationTable).Count(&after).Error; err != nil {
		return err
	}
	if after != before {
		return fmt.Errorf("phone contacts migration: row count changed from %d to %d", before, after)
	}
	if err := tx.Exec(`DROP TABLE phone_contacts`).Error; err != nil {
		return err
	}
	return tx.Exec(`ALTER TABLE ` + phoneContactOwnerMigrationTable + ` RENAME TO phone_contacts`).Error
}
