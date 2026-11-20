package resourcecatalog

import (
	"context"
	"strings"
)

// SMSAccessIdentity is a read-only projection of a pool member's subscription
// identity. It deliberately contains no secrets and never changes profiles.
type SMSAccessIdentity struct {
	PoolID      string
	NumberID    string
	ProfileID   string
	CardID      string
	DeviceID    string
	ICCID       string
	IMSI        string
	LocalPhone  string
	ProfileLive bool
}

// ListSMSAccessIdentities returns identities reachable through enabled pool
// grants. Disabled pools, members and numbers do not confer SMS access.
func (s *Service) ListSMSAccessIdentities(ctx context.Context, userID string) ([]SMSAccessIdentity, error) {
	pools, err := s.ListNumberPools(ctx, strings.TrimSpace(userID), false)
	if err != nil {
		return nil, err
	}
	result := make([]SMSAccessIdentity, 0)
	seen := make(map[string]struct{})
	for _, pool := range pools {
		for _, member := range pool.Members {
			if !member.Enabled || member.Profile == nil || member.Card == nil || smsNumberDisabled(member.Number.Status) {
				continue
			}
			key := pool.ID + "\x00" + member.Number.ID + "\x00" + member.Profile.ID
			if _, duplicate := seen[key]; duplicate {
				continue
			}
			seen[key] = struct{}{}
			result = append(result, SMSAccessIdentity{
				PoolID: pool.ID, NumberID: member.Number.ID, ProfileID: member.Profile.ID,
				CardID: member.Card.ID, DeviceID: strings.TrimSpace(member.Card.LastDeviceID),
				ICCID: strings.TrimSpace(member.Profile.ICCID), IMSI: strings.TrimSpace(member.Profile.IMSI),
				LocalPhone:  strings.TrimSpace(member.Number.E164),
				ProfileLive: member.Profile.Present && member.Profile.Active,
			})
		}
	}
	return result, nil
}

func smsNumberDisabled(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "disabled", "suspended":
		return true
	default:
		return false
	}
}
