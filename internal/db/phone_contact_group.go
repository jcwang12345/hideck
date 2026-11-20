package db

import (
	"context"
	"strings"
	"time"

	"gorm.io/gorm"
)

func UpdatePhoneContactGroupName(ctx context.Context, contactID, name string) ([]PhoneContact, error) {
	return UpdatePhoneContactGroupNameForOwner(ctx, legacyPhoneContactRuntimeOwner(), contactID, name)
}

func UpdatePhoneContactGroupNameForOwner(ctx context.Context, ownerID, contactID, name string) ([]PhoneContact, error) {
	contactID = strings.TrimSpace(contactID)
	name = strings.TrimSpace(name)
	if !validPhoneContactID(contactID) || name == "" {
		return nil, ErrInvalidPhoneContact
	}
	if DB == nil {
		return nil, gorm.ErrInvalidDB
	}
	ownerID, err := normalizePhoneContactOwner(ownerID)
	if err != nil {
		return nil, err
	}
	var rows []PhoneContact
	err = DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("owner_id = ? AND contact_id = ?", ownerID, contactID).Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			return gorm.ErrRecordNotFound
		}
		result := tx.Model(&PhoneContact{}).Where("owner_id = ? AND contact_id = ?", ownerID, contactID).Updates(map[string]any{
			"name": name, "updated_at": time.Now(),
		})
		if result.Error != nil {
			return result.Error
		}
		rows = rows[:0]
		return tx.Where("owner_id = ? AND contact_id = ?", ownerID, contactID).
			Order("updated_at DESC, number ASC").Find(&rows).Error
	})
	return rows, err
}

func DeletePhoneContactGroup(ctx context.Context, contactID string) ([]string, error) {
	return DeletePhoneContactGroupForOwner(ctx, legacyPhoneContactRuntimeOwner(), contactID)
}

func DeletePhoneContactGroupForOwner(ctx context.Context, ownerID, contactID string) ([]string, error) {
	contactID = strings.TrimSpace(contactID)
	if !validPhoneContactID(contactID) {
		return nil, ErrInvalidPhoneContact
	}
	if DB == nil {
		return nil, gorm.ErrInvalidDB
	}
	ownerID, err := normalizePhoneContactOwner(ownerID)
	if err != nil {
		return nil, err
	}
	var rows []PhoneContact
	err = DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("owner_id = ? AND contact_id = ?", ownerID, contactID).Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			return gorm.ErrRecordNotFound
		}
		return tx.Where("owner_id = ? AND contact_id = ?", ownerID, contactID).Delete(&PhoneContact{}).Error
	})
	if err != nil {
		return nil, err
	}
	numbers := make([]string, 0, len(rows))
	for _, row := range rows {
		numbers = append(numbers, row.Number)
	}
	return numbers, nil
}
