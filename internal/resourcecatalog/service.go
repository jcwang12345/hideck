package resourcecatalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrNotFound                        = errors.New("resource catalog item not found")
	ErrStableContainerIdentityRequired = errors.New("stable card container identity required")
	ErrEmptyProfileIdentity            = errors.New("subscription profile has no ICCID, AID, or IMSI")
)

type Service struct {
	database *gorm.DB
}

func NewService(database *gorm.DB) *Service {
	return &Service{database: database}
}

// CardContainerDTO is the read-only catalog representation returned to upper
// layers. It intentionally does not expose the internal locator hash.
type CardContainerDTO struct {
	ID             string                   `json:"id"`
	Type           CardContainerType        `json:"type"`
	EID            *string                  `json:"eid,omitempty"`
	LastDeviceID   string                   `json:"last_device_id,omitempty"`
	LastSlotID     string                   `json:"last_slot_id,omitempty"`
	Capabilities   CardCapability           `json:"capabilities"`
	Profiles       []SubscriptionProfileDTO `json:"profiles"`
	LastObservedAt time.Time                `json:"last_observed_at"`
	CreatedAt      time.Time                `json:"created_at"`
	UpdatedAt      time.Time                `json:"updated_at"`
}

type SubscriptionProfileDTO struct {
	ID              string    `json:"id"`
	ICCID           string    `json:"iccid,omitempty"`
	AID             string    `json:"aid,omitempty"`
	IMSI            string    `json:"imsi,omitempty"`
	Name            string    `json:"name,omitempty"`
	ServiceProvider string    `json:"service_provider,omitempty"`
	Present         bool      `json:"present"`
	Active          bool      `json:"active"`
	LastObservedAt  time.Time `json:"last_observed_at"`
}

// SyncObservation persists values that another layer has already observed.
// It performs no card I/O and never derives a CardContainer identity from an
// ICCID, AID, or IMSI.
func (s *Service) SyncObservation(ctx context.Context, observation CardObservation) (CardContainerDTO, error) {
	if s == nil || s.database == nil {
		return CardContainerDTO{}, errors.New("resource catalog database is nil")
	}

	normalized, err := normalizeObservation(observation)
	if err != nil {
		return CardContainerDTO{}, err
	}

	var result CardContainerDTO
	err = s.database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		container, err := resolveContainer(tx, normalized)
		if err != nil {
			return err
		}
		if err := syncContainer(tx, &container, normalized); err != nil {
			return err
		}
		if err := syncProfiles(tx, container.ID, normalized); err != nil {
			return err
		}
		loaded, err := loadContainer(tx, container.ID)
		if err != nil {
			return err
		}
		result = containerDTO(loaded)
		return nil
	})
	return result, err
}

// List returns immutable DTO values ordered by their stable container ID.
func (s *Service) List(ctx context.Context) ([]CardContainerDTO, error) {
	if s == nil || s.database == nil {
		return nil, errors.New("resource catalog database is nil")
	}
	var containers []CardContainer
	err := s.database.WithContext(ctx).
		Preload("Profiles", func(db *gorm.DB) *gorm.DB {
			return db.Order("last_observed_at DESC, id ASC")
		}).
		Order("id ASC").
		Find(&containers).Error
	if err != nil {
		return nil, err
	}
	result := make([]CardContainerDTO, 0, len(containers))
	for i := range containers {
		result = append(result, containerDTO(containers[i]))
	}
	return result, nil
}

// Get returns one immutable DTO by its CardContainer ID.
func (s *Service) Get(ctx context.Context, id string) (CardContainerDTO, error) {
	if s == nil || s.database == nil {
		return CardContainerDTO{}, errors.New("resource catalog database is nil")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return CardContainerDTO{}, ErrNotFound
	}
	container, err := loadContainer(s.database.WithContext(ctx), id)
	if err != nil {
		return CardContainerDTO{}, err
	}
	return containerDTO(container), nil
}

type normalizedObservation struct {
	CardObservation
	capability CardCapability
	eid        string
	isdrAID    string
	locatorID  string
	profiles   []normalizedProfileObservation
	observed   time.Time
}

type normalizedProfileObservation struct {
	ProfileObservation
	identityKey string
}

func normalizeObservation(observation CardObservation) (normalizedObservation, error) {
	normalized := normalizedObservation{CardObservation: observation}
	normalized.DeviceID = strings.TrimSpace(observation.DeviceID)
	normalized.SlotID = strings.TrimSpace(observation.SlotID)
	normalized.StableContainerKey = strings.TrimSpace(observation.StableContainerKey)
	normalized.eid = normalizeIdentifier(observation.EID)
	normalized.isdrAID = normalizeHexIdentifier(observation.ISDRAID)
	normalized.EID = normalized.eid
	normalized.ISDRAID = normalized.isdrAID
	normalized.capability = ClassifyCardCapability(normalized.CardObservation)
	normalized.observed = observation.ObservedAt.UTC()
	if normalized.observed.IsZero() {
		normalized.observed = time.Now().UTC()
	}

	normalized.locatorID = stableLocatorID(normalized.StableContainerKey, normalized.DeviceID, normalized.SlotID)
	if normalized.eid == "" && normalized.locatorID == "" {
		return normalizedObservation{}, ErrStableContainerIdentityRequired
	}

	if observation.Profiles != nil {
		normalized.profiles = make([]normalizedProfileObservation, 0, len(observation.Profiles))
		seen := make(map[string]struct{}, len(observation.Profiles))
		for _, profile := range observation.Profiles {
			profile.ICCID = normalizeIdentifier(profile.ICCID)
			profile.AID = normalizeHexIdentifier(profile.AID)
			profile.IMSI = normalizeIdentifier(profile.IMSI)
			profile.Name = strings.TrimSpace(profile.Name)
			profile.ServiceProviderName = strings.TrimSpace(profile.ServiceProviderName)
			identityKey := profileIdentityKey(profile)
			if identityKey == "" {
				return normalizedObservation{}, ErrEmptyProfileIdentity
			}
			if _, duplicate := seen[identityKey]; duplicate {
				return normalizedObservation{}, fmt.Errorf("duplicate subscription profile identity %q", identityKey)
			}
			seen[identityKey] = struct{}{}
			normalized.profiles = append(normalized.profiles, normalizedProfileObservation{
				ProfileObservation: profile,
				identityKey:        identityKey,
			})
		}
	}
	return normalized, nil
}

func resolveContainer(tx *gorm.DB, observation normalizedObservation) (CardContainer, error) {
	if observation.eid != "" {
		container, found, err := findContainer(tx, "eid = ?", observation.eid)
		if err != nil {
			return CardContainer{}, err
		}
		if found {
			if err := bindLocator(tx, container.ID, observation.locatorID); err != nil {
				return CardContainer{}, err
			}
			return container, nil
		}

		if observation.locatorID != "" {
			container, found, err = findContainer(tx, "locator_id = ?", observation.locatorID)
			if err != nil {
				return CardContainer{}, err
			}
			if found && (container.EID == nil || strings.TrimSpace(*container.EID) == "") {
				return container, nil
			}
			if found {
				if err := tx.Model(&CardContainer{}).Where("id = ?", container.ID).
					Update("locator_id", nil).Error; err != nil {
					return CardContainer{}, err
				}
			}
		}
		// A worker may first expose only its primary slot and later populate the
		// cached eUICC application AID. Those two observations use different
		// locators. Reconcile them only when exactly one EID-less container on
		// the same device shares a currently present profile; this avoids
		// splitting one card while still refusing to identify a card by ICCID.
		container, found, err = findUniqueContainerByObservedProfile(tx, observation)
		if err != nil {
			return CardContainer{}, err
		}
		if found {
			return container, nil
		}
		return CardContainer{ID: deterministicID("eid", observation.eid)}, nil
	}

	container, found, err := findContainer(tx, "locator_id = ?", observation.locatorID)
	if err != nil {
		return CardContainer{}, err
	}
	if found {
		return container, nil
	}
	return CardContainer{ID: deterministicID("locator", observation.locatorID)}, nil
}

func findUniqueContainerByObservedProfile(tx *gorm.DB, observation normalizedObservation) (CardContainer, bool, error) {
	if strings.TrimSpace(observation.DeviceID) == "" || len(observation.profiles) == 0 {
		return CardContainer{}, false, nil
	}
	identities := make(map[string]struct{}, len(observation.profiles))
	for _, profile := range observation.profiles {
		identities[profile.identityKey] = struct{}{}
	}
	var candidates []CardContainer
	if err := tx.Preload("Profiles", "present = ?", true).
		Where("last_device_id = ? AND (eid IS NULL OR TRIM(eid) = '')", observation.DeviceID).
		Order("last_observed_at DESC").
		Limit(3).
		Find(&candidates).Error; err != nil {
		return CardContainer{}, false, err
	}
	var matched *CardContainer
	for index := range candidates {
		candidateMatches := false
		for _, profile := range candidates[index].Profiles {
			if _, ok := identities[profile.IdentityKey]; ok {
				candidateMatches = true
				break
			}
		}
		if !candidateMatches {
			continue
		}
		if matched != nil {
			return CardContainer{}, false, nil
		}
		candidate := candidates[index]
		matched = &candidate
	}
	if matched == nil {
		return CardContainer{}, false, nil
	}
	return *matched, true, nil
}

func syncContainer(tx *gorm.DB, container *CardContainer, observation normalizedObservation) error {
	isNew := container.CreatedAt.IsZero()
	containerType := mergeContainerType(container.Type, observation.capability.Type)
	if isNew {
		container.Type = containerType
		container.LastDeviceID = observation.DeviceID
		container.LastSlotID = observation.SlotID
		container.ISDRObserved = observation.capability.HasISDR
		container.MultipleProfilesObserved = observation.capability.MultipleProfiles
		container.ObservedProfileCount = observation.capability.ProfileCount
		container.LastObservedAt = observation.observed
		if observation.eid != "" {
			eid := observation.eid
			container.EID = &eid
		}
		if observation.locatorID != "" {
			locatorID := observation.locatorID
			container.LocatorID = &locatorID
		}
		return tx.Create(container).Error
	}

	updates := map[string]any{
		"container_type":             containerType,
		"isdr_observed":              container.ISDRObserved || observation.capability.HasISDR,
		"multiple_profiles_observed": container.MultipleProfilesObserved || observation.capability.MultipleProfiles,
		"last_observed_at":           observation.observed,
		"updated_at":                 observation.observed,
	}
	if observation.eid != "" {
		updates["eid"] = observation.eid
	}
	if observation.locatorID != "" {
		if err := bindLocator(tx, container.ID, observation.locatorID); err != nil {
			return err
		}
		updates["locator_id"] = observation.locatorID
	}
	if observation.DeviceID != "" {
		updates["last_device_id"] = observation.DeviceID
	}
	if observation.SlotID != "" {
		updates["last_slot_id"] = observation.SlotID
	}
	if observation.Profiles != nil || observation.ProfilesComplete {
		updates["observed_profile_count"] = observation.capability.ProfileCount
	}
	return tx.Model(&CardContainer{}).Where("id = ?", container.ID).Updates(updates).Error
}

func syncProfiles(tx *gorm.DB, containerID string, observation normalizedObservation) error {
	profilesObserved := observation.Profiles != nil || observation.ProfilesComplete
	if !profilesObserved {
		return nil
	}
	if observation.ProfilesComplete {
		if err := tx.Model(&SubscriptionProfile{}).
			Where("card_container_id = ?", containerID).
			Updates(map[string]any{
				"present":    false,
				"active":     false,
				"updated_at": observation.observed,
			}).Error; err != nil {
			return err
		}
	}
	for _, observed := range observation.profiles {
		profile := SubscriptionProfile{
			ID:              deterministicID("profile", containerID+"\x00"+observed.identityKey),
			CardContainerID: containerID,
			IdentityKey:     observed.identityKey,
			ICCID:           observed.ICCID,
			AID:             observed.AID,
			IMSI:            observed.IMSI,
			Name:            observed.Name,
			ServiceProvider: observed.ServiceProviderName,
			Present:         true,
			Active:          observed.Active,
			LastObservedAt:  observation.observed,
			UpdatedAt:       observation.observed,
		}
		if err := tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "id"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"card_container_id", "identity_key", "iccid", "aid", "imsi", "name", "service_provider",
				"present", "active", "last_observed_at", "updated_at",
			}),
		}).Create(&profile).Error; err != nil {
			return err
		}
	}
	return nil
}

func loadContainer(database *gorm.DB, id string) (CardContainer, error) {
	var container CardContainer
	err := database.
		Preload("Profiles", func(db *gorm.DB) *gorm.DB {
			return db.Order("last_observed_at DESC, id ASC")
		}).
		First(&container, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return CardContainer{}, ErrNotFound
	}
	return container, err
}

func findContainer(tx *gorm.DB, query string, args ...any) (CardContainer, bool, error) {
	var container CardContainer
	err := tx.Where(query, args...).First(&container).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return CardContainer{}, false, nil
	}
	return container, err == nil, err
}

func bindLocator(tx *gorm.DB, containerID, locatorID string) error {
	if locatorID == "" {
		return nil
	}
	return tx.Model(&CardContainer{}).
		Where("locator_id = ? AND id <> ?", locatorID, containerID).
		Update("locator_id", nil).Error
}

func mergeContainerType(current, observed CardContainerType) CardContainerType {
	if current == CardContainerTypeEUICC || observed == CardContainerTypeEUICC {
		return CardContainerTypeEUICC
	}
	if current == CardContainerTypePhysicalUICC || observed == CardContainerTypePhysicalUICC {
		return CardContainerTypePhysicalUICC
	}
	return CardContainerTypeUnknown
}

func stableLocatorID(stableKey, deviceID, slotID string) string {
	stableKey = strings.TrimSpace(stableKey)
	if stableKey != "" {
		return deterministicID("stable-container", stableKey)
	}
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return ""
	}
	slotID = strings.TrimSpace(slotID)
	if slotID == "" {
		slotID = "default"
	}
	return deterministicID("device-slot", deviceID+"\x00"+slotID)
}

func profileIdentityKey(profile ProfileObservation) string {
	switch {
	case profile.ICCID != "":
		return "iccid:" + profile.ICCID
	case profile.AID != "":
		return "aid:" + profile.AID
	case profile.IMSI != "":
		return "imsi:" + profile.IMSI
	default:
		return ""
	}
}

func deterministicID(namespace, value string) string {
	digest := sha256.Sum256([]byte(namespace + "\x00" + value))
	return hex.EncodeToString(digest[:])
}

func containerDTO(container CardContainer) CardContainerDTO {
	var eid *string
	if container.EID != nil {
		value := *container.EID
		eid = &value
	}
	profiles := make([]SubscriptionProfileDTO, 0, len(container.Profiles))
	for _, profile := range container.Profiles {
		profiles = append(profiles, SubscriptionProfileDTO{
			ID:              profile.ID,
			ICCID:           profile.ICCID,
			AID:             profile.AID,
			IMSI:            profile.IMSI,
			Name:            profile.Name,
			ServiceProvider: profile.ServiceProvider,
			Present:         profile.Present,
			Active:          profile.Active,
			LastObservedAt:  profile.LastObservedAt,
		})
	}
	return CardContainerDTO{
		ID:           container.ID,
		Type:         container.Type,
		EID:          eid,
		LastDeviceID: container.LastDeviceID,
		LastSlotID:   container.LastSlotID,
		Capabilities: CardCapability{
			Type:                  container.Type,
			HasEID:                container.EID != nil && strings.TrimSpace(*container.EID) != "",
			HasISDR:               container.ISDRObserved,
			MultipleProfiles:      container.MultipleProfilesObserved,
			ProfileCount:          container.ObservedProfileCount,
			PhysicalUICCConfirmed: container.Type == CardContainerTypePhysicalUICC,
		},
		Profiles:       profiles,
		LastObservedAt: container.LastObservedAt,
		CreatedAt:      container.CreatedAt,
		UpdatedAt:      container.UpdatedAt,
	}
}
