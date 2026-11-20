package dispatch

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func NormalizeVoiceOutcome(kind string, sipCode int, terminator string, answered bool) OutcomeKind {
	kind = strings.ToLower(strings.TrimSpace(kind))
	terminator = strings.ToLower(strings.TrimSpace(terminator))
	if answered || kind == "completed" {
		return OutcomeCompleted
	}
	if (kind == "declined" || kind == "remote_declined") && terminator != "local" && (sipCode == 603 || sipCode == 607) {
		return OutcomeRemoteDeclined
	}
	switch kind {
	case "busy":
		return OutcomeBusy
	case "no_answer":
		return OutcomeNoAnswer
	case "temporarily_unavailable":
		return OutcomeTemporarilyUnavailable
	case "local_cancel", "local_canceled", "local_hangup":
		return OutcomeLocalCanceled
	case "media_failed":
		return OutcomeMediaFailed
	case "network_failed", "redirected":
		return OutcomeNetworkFailed
	default:
		return OutcomeUnknownFailed
	}
}

func (s *Service) RecordOutcome(ctx context.Context, input OutcomeInput) (CallOutcome, CardDailyStat, bool, error) {
	input.CallID = strings.TrimSpace(input.CallID)
	if input.CallID == "" {
		return CallOutcome{}, CardDailyStat{}, false, errors.New("dispatch: call id is required")
	}
	if input.ObservedAt.IsZero() {
		input.ObservedAt = s.now()
	}
	if input.Threshold <= 0 {
		input.Threshold = defaultRemoteDeclineThreshold
	}
	date := s.businessDate(input.ObservedAt)
	outcome := CallOutcome{
		ID: uuid.NewString(), CallID: input.CallID, RequestID: strings.TrimSpace(input.RequestID),
		LeaseID: strings.TrimSpace(input.LeaseID), UserID: strings.TrimSpace(input.UserID),
		CardID: strings.TrimSpace(input.CardID), ProfileID: strings.TrimSpace(input.ProfileID),
		NumberID: strings.TrimSpace(input.NumberID), Kind: input.Kind, SIPCode: input.SIPCode,
		SIPReason: strings.TrimSpace(input.SIPReason), Terminator: strings.TrimSpace(input.Terminator),
		BusinessDate: date, ObservedAt: input.ObservedAt, CreatedAt: s.now(),
	}
	var stat CardDailyStat
	inserted := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "call_id"}}, DoNothing: true}).Create(&outcome)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			var existing CallOutcome
			if err := tx.Where("call_id = ?", input.CallID).First(&existing).Error; err != nil {
				return err
			}
			outcome = existing
			return nil
		}
		inserted = true
		if outcome.CardID != "" {
			stat = CardDailyStat{CardID: outcome.CardID, BusinessDate: date, CreatedAt: s.now(), UpdatedAt: s.now()}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&stat).Error; err != nil {
				return err
			}
			if err := tx.Where("card_id = ? AND business_date = ?", outcome.CardID, date).First(&stat).Error; err != nil {
				return err
			}
			stat.CallCount++
			stat.LastOutcomeID = outcome.ID
			stat.UpdatedAt = s.now()
			if outcome.Kind == OutcomeRemoteDeclined {
				stat.RemoteDeclineCount++
				if stat.RemoteDeclineCount >= input.Threshold && stat.DisabledAt == nil {
					disabledAt := input.ObservedAt
					stat.DisabledAt = &disabledAt
					stat.DisabledReason = "remote_decline_threshold"
				}
			}
			if err := tx.Save(&stat).Error; err != nil {
				return err
			}
			if outcome.Kind == OutcomeRemoteDeclined {
				outcome.Counted = true
				if err := tx.Model(&CallOutcome{}).Where("id = ?", outcome.ID).Update("counted", true).Error; err != nil {
					return err
				}
			}
		}
		if outcome.RequestID != "" {
			if err := tx.Model(&CallRequest{}).Where("id = ?", outcome.RequestID).Updates(map[string]any{"state": RequestCompleted, "updated_at": s.now()}).Error; err != nil {
				return err
			}
		}
		if outcome.LeaseID != "" {
			now := s.now()
			if err := tx.Model(&ResourceLease{}).Where("id = ? AND released_at IS NULL", outcome.LeaseID).Updates(map[string]any{
				"state": LeaseReleased, "released_at": now, "heartbeat_at": now,
				"version": gorm.Expr("version + 1"), "updated_at": now,
			}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	return outcome, stat, inserted, err
}

func (s *Service) DisableCardToday(ctx context.Context, cardID, actorID, reason string) (CardDailyStat, error) {
	cardID = strings.TrimSpace(cardID)
	if cardID == "" {
		return CardDailyStat{}, errors.New("dispatch: card id is required")
	}
	date, now := s.businessDate(s.now()), s.now()
	stat := CardDailyStat{CardID: cardID, BusinessDate: date, CreatedAt: now, UpdatedAt: now}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&stat).Error; err != nil {
			return err
		}
		if err := tx.Where("card_id = ? AND business_date = ?", cardID, date).First(&stat).Error; err != nil {
			return err
		}
		stat.DisabledAt = &now
		stat.DisabledBy = strings.TrimSpace(actorID)
		stat.DisabledReason = strings.TrimSpace(reason)
		if stat.DisabledReason == "" {
			stat.DisabledReason = "manual"
		}
		stat.UpdatedAt = now
		return tx.Save(&stat).Error
	})
	return stat, err
}

func (s *Service) EnableCardToday(ctx context.Context, cardID string) (CardDailyStat, error) {
	cardID = strings.TrimSpace(cardID)
	if cardID == "" {
		return CardDailyStat{}, errors.New("dispatch: card id is required")
	}
	date, now := s.businessDate(s.now()), s.now()
	stat := CardDailyStat{CardID: cardID, BusinessDate: date, CreatedAt: now, UpdatedAt: now}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&stat).Error; err != nil {
			return err
		}
		if err := tx.Where("card_id = ? AND business_date = ?", cardID, date).First(&stat).Error; err != nil {
			return err
		}
		stat.DisabledAt, stat.DisabledReason, stat.DisabledBy, stat.UpdatedAt = nil, "", "", now
		return tx.Save(&stat).Error
	})
	return stat, err
}

func (s *Service) CardDisabledToday(ctx context.Context, cardID string) (bool, CardDailyStat, error) {
	var stat CardDailyStat
	err := s.db.WithContext(ctx).Where("card_id = ? AND business_date = ?", strings.TrimSpace(cardID), s.businessDate(s.now())).First(&stat).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, CardDailyStat{}, nil
	}
	return stat.DisabledAt != nil, stat, err
}

func (s *Service) DailyStats(ctx context.Context, date string) ([]CardDailyStat, error) {
	date = strings.TrimSpace(date)
	if date == "" {
		date = s.businessDate(s.now())
	}
	var stats []CardDailyStat
	err := s.db.WithContext(ctx).Where("business_date = ?", date).Order("card_id asc").Find(&stats).Error
	return stats, err
}
