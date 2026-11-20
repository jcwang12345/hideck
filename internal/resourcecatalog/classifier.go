package resourcecatalog

import (
	"strings"
	"time"
	"unicode"
)

// CardObservation contains values already observed by the modem/card layers.
// The catalog never opens a logical channel or sends an APDU itself.
type CardObservation struct {
	// StableContainerKey is a durable reader/slot/container key supplied by the
	// caller. It must describe hardware placement and must not be an ICCID.
	// When omitted, DeviceID plus SlotID is used as the stable locator.
	StableContainerKey string
	DeviceID           string
	SlotID             string
	CardPresent        bool
	// PhysicalUICCConfirmed is set only after the card layer completed its
	// eUICC application scan and found no supported ISD-R/eUICC application.
	// CardPresent alone is insufficient because removable adapters such as
	// 5ber/eSIM.me also appear as ordinary inserted UICCs before that scan.
	PhysicalUICCConfirmed bool
	EID                   string
	ISDRAID               string
	Profiles              []ProfileObservation
	// ProfilesComplete means Profiles is a complete snapshot. Profiles missing
	// from a complete snapshot are retained for history but marked not present.
	ProfilesComplete bool
	ObservedAt       time.Time
}

// ProfileObservation is a passive snapshot of one subscription profile.
type ProfileObservation struct {
	ICCID               string
	AID                 string
	IMSI                string
	Name                string
	ServiceProviderName string
	Active              bool
}

// CardCapability records both the conservative classification and evidence
// useful to callers. Multiple profiles are evidence of profile capability,
// but do not by themselves prove that the container is an eUICC.
type CardCapability struct {
	Type                  CardContainerType `json:"type"`
	HasEID                bool              `json:"has_eid"`
	HasISDR               bool              `json:"has_isdr"`
	MultipleProfiles      bool              `json:"multiple_profiles"`
	ProfileCount          int               `json:"profile_count"`
	PhysicalUICCConfirmed bool              `json:"physical_uicc_confirmed"`
}

// ClassifyCardCapability uses only positive EID or ISD-R evidence to identify
// an eUICC. Merely observing multiple profiles never promotes a card to eUICC.
func ClassifyCardCapability(observation CardObservation) CardCapability {
	hasEID := normalizeIdentifier(observation.EID) != ""
	hasISDR := normalizeHexIdentifier(observation.ISDRAID) != ""
	capability := CardCapability{
		Type:                  CardContainerTypeUnknown,
		HasEID:                hasEID,
		HasISDR:               hasISDR,
		MultipleProfiles:      len(observation.Profiles) > 1,
		ProfileCount:          len(observation.Profiles),
		PhysicalUICCConfirmed: observation.PhysicalUICCConfirmed,
	}
	switch {
	case hasEID || hasISDR:
		capability.Type = CardContainerTypeEUICC
	case observation.PhysicalUICCConfirmed:
		capability.Type = CardContainerTypePhysicalUICC
	}
	return capability
}

func normalizeIdentifier(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || r == '-' || r == ':' {
			return -1
		}
		return unicode.ToUpper(r)
	}, strings.TrimSpace(value))
}

func normalizeHexIdentifier(value string) string {
	return normalizeIdentifier(value)
}
