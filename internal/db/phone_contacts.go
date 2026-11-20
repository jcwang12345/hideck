package db

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/yibaiba/hideck/internal/phonelookup"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrInvalidPhoneContact = errors.New("invalid phone contact")

const (
	// LegacyPhoneContactOwnerID is the compatibility owner used by internal and
	// MCP callers when the identity service has not created its first admin yet.
	// initializeIdentity atomically moves these rows to that first admin.
	LegacyPhoneContactOwnerID = "legacy-admin"
	phoneContactIDPrefix      = "contact_"
	phoneContactIDRandomBytes = 16
)

type PhoneContact struct {
	OwnerID   string    `gorm:"column:owner_id;primaryKey;type:text;not null;index:idx_phone_contacts_owner_contact,priority:1" json:"-"`
	Number    string    `gorm:"column:number;primaryKey;type:text;not null" json:"number"`
	ContactID string    `gorm:"column:contact_id;index:idx_phone_contacts_owner_contact,priority:2" json:"contact_id"`
	Name      string    `gorm:"column:name;not null" json:"name"`
	CreatedAt time.Time `gorm:"column:created_at" json:"created_at"`
	UpdatedAt time.Time `gorm:"column:updated_at" json:"updated_at"`
}

type PhoneContactInput struct {
	Number    string
	Name      string
	Region    string
	ContactID string
	GroupKey  string
}

type phoneContactIDRequest struct {
	Database  *gorm.DB
	OwnerID   string
	Number    string
	Requested string
}

func (PhoneContact) TableName() string { return "phone_contacts" }

func NormalizePhoneContactNumber(raw string) string {
	return NormalizePhoneContactNumberWithRegion(raw, "")
}

func NormalizePhoneContactNumberWithRegion(raw, region string) string {
	return phonelookup.CanonicalWithRegion(raw, region)
}

func ListPhoneContacts(ctx context.Context) ([]PhoneContact, error) {
	return ListPhoneContactsForOwner(ctx, legacyPhoneContactRuntimeOwner())
}

func ListPhoneContactsForOwner(ctx context.Context, ownerID string) ([]PhoneContact, error) {
	if DB == nil {
		return nil, gorm.ErrInvalidDB
	}
	ownerID, err := normalizePhoneContactOwner(ownerID)
	if err != nil {
		return nil, err
	}
	var rows []PhoneContact
	err = phoneContactsQuery(ctx, ownerID).Find(&rows).Error
	return rows, err
}

func ListPhoneContactsPage(ctx context.Context, limit, offset int) ([]PhoneContact, error) {
	return ListPhoneContactsPageForOwner(ctx, legacyPhoneContactRuntimeOwner(), limit, offset)
}

func ListPhoneContactsPageForOwner(ctx context.Context, ownerID string, limit, offset int) ([]PhoneContact, error) {
	if DB == nil {
		return nil, gorm.ErrInvalidDB
	}
	ownerID, err := normalizePhoneContactOwner(ownerID)
	if err != nil {
		return nil, err
	}
	var rows []PhoneContact
	err = phoneContactsQuery(ctx, ownerID).Limit(limit).Offset(offset).Find(&rows).Error
	return rows, err
}

func CountPhoneContacts(ctx context.Context) (int64, error) {
	return CountPhoneContactsForOwner(ctx, legacyPhoneContactRuntimeOwner())
}

func CountPhoneContactsForOwner(ctx context.Context, ownerID string) (int64, error) {
	if DB == nil {
		return 0, gorm.ErrInvalidDB
	}
	ownerID, err := normalizePhoneContactOwner(ownerID)
	if err != nil {
		return 0, err
	}
	var total int64
	err = DB.WithContext(ctx).Model(&PhoneContact{}).Where("owner_id = ?", ownerID).Count(&total).Error
	return total, err
}

func phoneContactsQuery(ctx context.Context, ownerID string) *gorm.DB {
	return DB.WithContext(ctx).Where("owner_id = ?", ownerID).Order("updated_at DESC, number ASC")
}

func UpsertPhoneContact(ctx context.Context, number, name string) (PhoneContact, error) {
	return UpsertPhoneContactForOwner(ctx, legacyPhoneContactRuntimeOwner(), number, name)
}

func UpsertPhoneContactForOwner(ctx context.Context, ownerID, number, name string) (PhoneContact, error) {
	return UpsertPhoneContactForOwnerWithRegion(ctx, ownerID, PhoneContactInput{Number: number, Name: name})
}

func UpsertPhoneContactWithRegion(ctx context.Context, input PhoneContactInput) (PhoneContact, error) {
	return UpsertPhoneContactForOwnerWithRegion(ctx, legacyPhoneContactRuntimeOwner(), input)
}

func UpsertPhoneContactForOwnerWithRegion(ctx context.Context, ownerID string, input PhoneContactInput) (PhoneContact, error) {
	if DB == nil {
		return PhoneContact{}, gorm.ErrInvalidDB
	}
	return upsertPhoneContactWithDB(ctx, DB, ownerID, input)
}

func upsertPhoneContactWithDB(ctx context.Context, database *gorm.DB, ownerID string, input PhoneContactInput) (PhoneContact, error) {
	ownerID, err := normalizePhoneContactOwner(ownerID)
	if err != nil {
		return PhoneContact{}, err
	}
	number := NormalizePhoneContactNumberWithRegion(input.Number, input.Region)
	name := strings.TrimSpace(input.Name)
	if number == "" || name == "" {
		return PhoneContact{}, ErrInvalidPhoneContact
	}
	contactID, err := resolvePhoneContactID(ctx, phoneContactIDRequest{
		Database: database, OwnerID: ownerID, Number: number, Requested: input.ContactID,
	})
	if err != nil {
		return PhoneContact{}, err
	}
	row := PhoneContact{OwnerID: ownerID, Number: number, ContactID: contactID, Name: name, UpdatedAt: time.Now()}
	err = database.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "owner_id"}, {Name: "number"}},
		DoUpdates: clause.AssignmentColumns([]string{"contact_id", "name", "updated_at"}),
	}).Create(&row).Error
	if err != nil {
		return PhoneContact{}, err
	}
	if err := database.WithContext(ctx).Where("owner_id = ? AND number = ?", ownerID, number).First(&row).Error; err != nil {
		return PhoneContact{}, err
	}
	return row, nil
}

func GetPhoneContact(ctx context.Context, number string) (PhoneContact, error) {
	return GetPhoneContactForOwner(ctx, legacyPhoneContactRuntimeOwner(), number)
}

func GetPhoneContactForOwner(ctx context.Context, ownerID, number string) (PhoneContact, error) {
	number = NormalizePhoneContactNumber(number)
	if number == "" || DB == nil {
		return PhoneContact{}, gorm.ErrRecordNotFound
	}
	ownerID, err := normalizePhoneContactOwner(ownerID)
	if err != nil {
		return PhoneContact{}, err
	}
	var row PhoneContact
	err = DB.WithContext(ctx).Where("owner_id = ? AND number = ?", ownerID, number).First(&row).Error
	return row, err
}

func DeletePhoneContact(ctx context.Context, number string) error {
	return DeletePhoneContactWithRegion(ctx, number, "")
}

func DeletePhoneContactWithRegion(ctx context.Context, number, region string) error {
	return DeletePhoneContactForOwnerWithRegion(ctx, legacyPhoneContactRuntimeOwner(), number, region)
}

func DeletePhoneContactForOwnerWithRegion(ctx context.Context, ownerID, number, region string) error {
	_, err := DeletePhoneContactsForOwnerWithRegion(ctx, ownerID, []string{number}, region)
	return err
}

func UpsertPhoneContactsWithRegion(ctx context.Context, inputs []PhoneContactInput, region string) (imported, skipped int, err error) {
	return UpsertPhoneContactsForOwnerWithRegion(ctx, legacyPhoneContactRuntimeOwner(), inputs, region)
}

func UpsertPhoneContactsForOwnerWithRegion(ctx context.Context, ownerID string, inputs []PhoneContactInput, region string) (imported, skipped int, err error) {
	rows, skipped, err := upsertPhoneContactBatch(ctx, inputs, phoneContactBatchOptions{
		OwnerID: ownerID, Region: region, SkipInvalid: true,
	})
	return len(rows), skipped, err
}

func UpsertPhoneContactBatchWithRegion(
	ctx context.Context,
	inputs []PhoneContactInput,
	region string,
) ([]PhoneContact, error) {
	return UpsertPhoneContactBatchForOwnerWithRegion(ctx, legacyPhoneContactRuntimeOwner(), inputs, region)
}

func UpsertPhoneContactBatchForOwnerWithRegion(
	ctx context.Context,
	ownerID string,
	inputs []PhoneContactInput,
	region string,
) ([]PhoneContact, error) {
	rows, _, err := upsertPhoneContactBatch(ctx, inputs, phoneContactBatchOptions{OwnerID: ownerID, Region: region})
	return rows, err
}

func DeletePhoneContactsWithRegion(ctx context.Context, numbers []string, region string) (deleted int, err error) {
	return DeletePhoneContactsForOwnerWithRegion(ctx, legacyPhoneContactRuntimeOwner(), numbers, region)
}

func DeletePhoneContactsForOwnerWithRegion(ctx context.Context, ownerID string, numbers []string, region string) (deleted int, err error) {
	if DB == nil {
		return 0, gorm.ErrInvalidDB
	}
	ownerID, err = normalizePhoneContactOwner(ownerID)
	if err != nil {
		return 0, err
	}
	canonical := make([]string, 0, len(numbers))
	seen := map[string]struct{}{}
	for _, raw := range numbers {
		number := NormalizePhoneContactNumberWithRegion(raw, region)
		if number == "" {
			continue
		}
		if _, ok := seen[number]; ok {
			continue
		}
		seen[number] = struct{}{}
		canonical = append(canonical, number)
	}
	if len(canonical) == 0 {
		return 0, ErrInvalidPhoneContact
	}
	result := DB.WithContext(ctx).Where("owner_id = ? AND number IN ?", ownerID, canonical).Delete(&PhoneContact{})
	return int(result.RowsAffected), result.Error
}

func LookupPhoneIdentity(ctx context.Context, raw string) phonelookup.Result {
	return LookupPhoneIdentityForOwnerWithRegion(ctx, legacyPhoneContactRuntimeOwner(), raw, "")
}

func LookupPhoneIdentityWithRegion(ctx context.Context, raw, region string) phonelookup.Result {
	return LookupPhoneIdentityForOwnerWithRegion(ctx, legacyPhoneContactRuntimeOwner(), raw, region)
}

func LookupPhoneIdentityForOwner(ctx context.Context, ownerID, raw string) phonelookup.Result {
	return LookupPhoneIdentityForOwnerWithRegion(ctx, ownerID, raw, "")
}

func LookupPhoneIdentityForOwnerWithRegion(ctx context.Context, ownerID, raw, region string) phonelookup.Result {
	result := phonelookup.LookupWithRegion(raw, region)
	canonical := phonelookup.CanonicalWithRegion(raw, region)
	if result.Number == "" && canonical == "" {
		return result
	}
	row, err := GetPhoneContactForOwner(ctx, ownerID, canonical)
	if err != nil || strings.TrimSpace(row.Name) == "" {
		return result
	}
	return result.WithContact(row.ContactID, row.Name)
}

func PhoneContactIdentity(row PhoneContact) phonelookup.Result {
	return phonelookup.Lookup(row.Number).WithContact(row.ContactID, row.Name)
}

func resolvePhoneContactID(ctx context.Context, request phoneContactIDRequest) (string, error) {
	if id := strings.TrimSpace(request.Requested); id != "" {
		if !validPhoneContactID(id) {
			return "", ErrInvalidPhoneContact
		}
		return id, nil
	}
	var existing PhoneContact
	err := request.Database.WithContext(ctx).
		Select("contact_id").Where("owner_id = ? AND number = ?", request.OwnerID, request.Number).First(&existing).Error
	if err == nil && strings.TrimSpace(existing.ContactID) != "" {
		return existing.ContactID, nil
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return "", err
	}
	return newPhoneContactID()
}

func normalizePhoneContactOwner(ownerID string) (string, error) {
	ownerID = strings.TrimSpace(ownerID)
	if ownerID == "" {
		return "", ErrInvalidPhoneContact
	}
	return ownerID, nil
}

func assignImportContactID(input *PhoneContactInput, groups map[string]string) error {
	if input == nil || strings.TrimSpace(input.ContactID) != "" {
		return nil
	}
	key := strings.TrimSpace(input.GroupKey)
	if key == "" {
		return nil
	}
	if id := groups[key]; id != "" {
		input.ContactID = id
		return nil
	}
	id, err := newPhoneContactID()
	if err != nil {
		return err
	}
	groups[key] = id
	input.ContactID = id
	return nil
}

func newPhoneContactID() (string, error) {
	var raw [phoneContactIDRandomBytes]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return phoneContactIDPrefix + hex.EncodeToString(raw[:]), nil
}

func validPhoneContactID(id string) bool {
	if len(id) != len(phoneContactIDPrefix)+(phoneContactIDRandomBytes*2) ||
		!strings.HasPrefix(id, phoneContactIDPrefix) {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(id, phoneContactIDPrefix))
	return err == nil
}
