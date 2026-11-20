package resourcecatalog

import (
	"context"
	"errors"
	"strings"

	"gorm.io/gorm"
)

// CurrentVoiceResource is the catalog identity behind one explicitly selected
// device. Callers use it to acquire a dispatch lease before touching the modem.
// Pool is optional: legacy administrative calls may use a catalogued number
// which has not yet been added to a shared number pool.
type CurrentVoiceResource struct {
	Card    CardContainer       `json:"card"`
	Profile SubscriptionProfile `json:"profile"`
	Number  PhoneNumber         `json:"number"`
	Pool    *NumberPool         `json:"pool,omitempty"`
	Member  *NumberPoolMember   `json:"member,omitempty"`
}

// ResolveCurrentVoiceResource resolves already-catalogued identities only. It
// deliberately performs no modem, SIM or APDU I/O. When iccid is non-empty it
// is authoritative; otherwise the most recently observed active/present
// profile for the device is selected. numberID can further constrain the
// number used for a diagnostic call.
func (s *Service) ResolveCurrentVoiceResource(ctx context.Context, deviceID, iccid, numberID string) (CurrentVoiceResource, error) {
	if s == nil || s.database == nil {
		return CurrentVoiceResource{}, errors.New("resource catalog database is nil")
	}
	deviceID, numberID = strings.TrimSpace(deviceID), strings.TrimSpace(numberID)
	iccid = canonicalVoiceICCID(iccid)
	if deviceID == "" {
		return CurrentVoiceResource{}, ErrNotFound
	}

	var candidates []SubscriptionProfile
	query := s.database.WithContext(ctx).
		Model(&SubscriptionProfile{}).
		Joins("JOIN resource_card_containers cards ON cards.id = resource_subscription_profiles.card_container_id").
		Where("cards.last_device_id = ?", deviceID)
	if iccid != "" {
		// Historical rows may contain a trailing BCD filler F. Fetch the small
		// per-device set and compare canonically below instead of relying on a
		// database-specific expression.
		query = query.Order("resource_subscription_profiles.last_observed_at DESC, resource_subscription_profiles.id ASC")
	} else {
		query = query.Order("resource_subscription_profiles.active DESC, resource_subscription_profiles.present DESC, resource_subscription_profiles.last_observed_at DESC, resource_subscription_profiles.id ASC")
	}
	if err := query.Find(&candidates).Error; err != nil {
		return CurrentVoiceResource{}, err
	}
	var profile SubscriptionProfile
	for _, candidate := range candidates {
		if iccid == "" || canonicalVoiceICCID(candidate.ICCID) == iccid {
			profile = candidate
			break
		}
	}
	if profile.ID == "" {
		return CurrentVoiceResource{}, ErrNotFound
	}

	var card CardContainer
	if err := s.database.WithContext(ctx).Where("id = ?", profile.CardContainerID).First(&card).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return CurrentVoiceResource{}, ErrNotFound
		}
		return CurrentVoiceResource{}, err
	}

	var number PhoneNumber
	numberQuery := s.database.WithContext(ctx).
		Where("subscription_profile_id = ?", profile.ID).
		Where("LOWER(TRIM(status)) NOT IN ?", []string{"disabled", "suspended"}).
		Order("updated_at DESC, id ASC")
	if numberID != "" {
		numberQuery = numberQuery.Where("id = ?", numberID)
	}
	if err := numberQuery.First(&number).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return CurrentVoiceResource{}, ErrNotFound
		}
		return CurrentVoiceResource{}, err
	}

	result := CurrentVoiceResource{Card: card, Profile: profile, Number: number}
	var member NumberPoolMember
	err := s.database.WithContext(ctx).
		Model(&NumberPoolMember{}).
		Joins("JOIN resource_number_pools pools ON pools.id = resource_number_pool_members.number_pool_id").
		Where("resource_number_pool_members.phone_number_id = ? AND resource_number_pool_members.enabled = ? AND pools.enabled = ?", number.ID, true, true).
		Order("resource_number_pool_members.priority ASC, resource_number_pool_members.id ASC").
		First(&member).Error
	if err == nil {
		var pool NumberPool
		if loadErr := s.database.WithContext(ctx).Where("id = ?", member.NumberPoolID).First(&pool).Error; loadErr != nil {
			return CurrentVoiceResource{}, loadErr
		}
		result.Member, result.Pool = &member, &pool
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return CurrentVoiceResource{}, err
	}
	return result, nil
}

func canonicalVoiceICCID(value string) string {
	return strings.TrimRight(normalizeIdentifier(value), "F")
}
