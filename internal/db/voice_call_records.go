package db

import (
	"context"
	"errors"
	"time"

	"github.com/yibaiba/hideck/internal/phone"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type VoiceCallRecord struct {
	ID              uint64     `gorm:"primaryKey;autoIncrement"`
	CallID          string     `gorm:"column:call_id;uniqueIndex;not null"`
	DeviceID        string     `gorm:"column:device_id;index;not null"`
	ICCID           string     `gorm:"column:iccid;index"`
	UserID          string     `gorm:"column:user_id;index"`
	PoolID          string     `gorm:"column:pool_id;index"`
	CardID          string     `gorm:"column:card_id;index"`
	ProfileID       string     `gorm:"column:profile_id;index"`
	NumberID        string     `gorm:"column:number_id;index"`
	SourceNumber    string     `gorm:"column:source_number;index"`
	RequestID       string     `gorm:"column:request_id;index"`
	ResourceLeaseID string     `gorm:"column:resource_lease_id;index"`
	Direction       string     `gorm:"column:direction;index;not null"`
	Peer            string     `gorm:"column:peer;index"`
	Status          string     `gorm:"column:status;index;not null"`
	StartedAt       time.Time  `gorm:"column:started_at;index;not null"`
	AnsweredAt      *time.Time `gorm:"column:answered_at"`
	EndedAt         *time.Time `gorm:"column:ended_at;index"`
	DurationSeconds int64      `gorm:"column:duration_seconds;not null"`
	EndReason       string     `gorm:"column:end_reason"`
	OutcomeKind     string     `gorm:"column:outcome_kind;index"`
	SIPCode         int        `gorm:"column:sip_code;index"`
	SIPReason       string     `gorm:"column:sip_reason"`
	Terminator      string     `gorm:"column:terminator"`
	Codec           string     `gorm:"column:codec"`
	RecordingName   string     `gorm:"column:recording_name;index"`
	PCAPName        string     `gorm:"column:pcap_name"`
	RecordingError  string     `gorm:"column:recording_error"`
	CreatedAt       time.Time  `gorm:"column:created_at"`
	UpdatedAt       time.Time  `gorm:"column:updated_at"`
}

func (VoiceCallRecord) TableName() string { return "voice_call_records" }

type VoiceCallStore struct{ database *gorm.DB }

func NewVoiceCallStore(database *gorm.DB) *VoiceCallStore {
	return &VoiceCallStore{database: database}
}

func (store *VoiceCallStore) Upsert(ctx context.Context, record phone.CallRecord) error {
	model := voiceCallRecordFromDomain(record)
	return store.database.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "call_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"device_id", "iccid", "user_id", "pool_id", "card_id", "profile_id", "number_id",
			"source_number", "request_id", "resource_lease_id", "direction", "peer", "status", "started_at", "answered_at",
			"ended_at", "duration_seconds", "end_reason", "codec", "recording_name", "pcap_name",
			"recording_error", "outcome_kind", "sip_code", "sip_reason", "terminator", "updated_at",
		}),
	}).Create(&model).Error
}

func (store *VoiceCallStore) ListByUser(ctx context.Context, userID string, limit int) ([]phone.CallRecord, error) {
	var models []VoiceCallRecord
	if err := store.database.WithContext(ctx).Where("user_id = ?", userID).Order("started_at desc, id desc").Limit(limit).Find(&models).Error; err != nil {
		return nil, err
	}
	records := make([]phone.CallRecord, 0, len(models))
	for _, model := range models {
		records = append(records, voiceCallRecordToDomain(model))
	}
	return records, nil
}

func (store *VoiceCallStore) List(ctx context.Context, limit int) ([]phone.CallRecord, error) {
	var models []VoiceCallRecord
	if err := store.database.WithContext(ctx).Order("started_at desc, id desc").Limit(limit).Find(&models).Error; err != nil {
		return nil, err
	}
	records := make([]phone.CallRecord, 0, len(models))
	for _, model := range models {
		records = append(records, voiceCallRecordToDomain(model))
	}
	return records, nil
}

func (store *VoiceCallStore) FindRecording(ctx context.Context, recordingName, userID string) (phone.CallRecord, bool, error) {
	query := store.database.WithContext(ctx).Where("recording_name = ?", recordingName)
	if userID != "" {
		query = query.Where("user_id = ?", userID)
	}
	var model VoiceCallRecord
	if err := query.Order("id desc").First(&model).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return phone.CallRecord{}, false, nil
		}
		return phone.CallRecord{}, false, err
	}
	return voiceCallRecordToDomain(model), true, nil
}

func (store *VoiceCallStore) Delete(ctx context.Context, ids []uint64) ([]phone.CallRecord, error) {
	return store.delete(ctx, "", ids)
}

func (store *VoiceCallStore) DeleteByUser(ctx context.Context, userID string, ids []uint64) ([]phone.CallRecord, error) {
	return store.delete(ctx, userID, ids)
}

func (store *VoiceCallStore) delete(ctx context.Context, userID string, ids []uint64) ([]phone.CallRecord, error) {
	if len(ids) == 0 {
		return []phone.CallRecord{}, nil
	}
	var deleted []phone.CallRecord
	err := store.database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var models []VoiceCallRecord
		query := tx.Where("id IN ?", ids)
		if userID != "" {
			query = query.Where("user_id = ?", userID)
		}
		if err := query.Find(&models).Error; err != nil {
			return err
		}
		for _, model := range models {
			if activeCallStatus(model.Status) {
				return phone.ErrActiveCallHistoryDelete
			}
			deleted = append(deleted, voiceCallRecordToDomain(model))
		}
		if len(models) == 0 {
			return nil
		}
		deleteQuery := tx.Where("id IN ?", ids)
		if userID != "" {
			deleteQuery = deleteQuery.Where("user_id = ?", userID)
		}
		return deleteQuery.Delete(&VoiceCallRecord{}).Error
	})
	return deleted, err
}

func activeCallStatus(status string) bool {
	switch status {
	case phone.StatusCalling, phone.StatusRinging, phone.StatusWaiting, phone.StatusConnected:
		return true
	default:
		return false
	}
}

func (store *VoiceCallStore) AbandonIncomplete(ctx context.Context, endedAt time.Time, reason string) error {
	var models []VoiceCallRecord
	err := store.database.WithContext(ctx).Where(
		"status IN ? AND ended_at IS NULL",
		[]string{phone.StatusCalling, phone.StatusRinging, phone.StatusConnected},
	).Find(&models).Error
	if err != nil {
		return err
	}
	for _, model := range models {
		record := voiceCallRecordToDomain(model)
		ended := endedAt
		record.EndedAt = &ended
		record.EndReason = reason
		record.Status = abandonedCallStatus(record)
		record.DurationSeconds = abandonedCallDuration(record, ended)
		if err := store.Upsert(ctx, record); err != nil {
			return err
		}
	}
	return nil
}

func abandonedCallStatus(record phone.CallRecord) string {
	if record.AnsweredAt != nil {
		return phone.StatusCompleted
	}
	if record.Direction == "inbound" {
		return phone.StatusMissed
	}
	return phone.StatusFailed
}

func abandonedCallDuration(record phone.CallRecord, endedAt time.Time) int64 {
	start := record.StartedAt
	if record.AnsweredAt != nil {
		start = *record.AnsweredAt
	}
	if start.IsZero() || endedAt.Before(start) {
		return 0
	}
	return int64(endedAt.Sub(start) / time.Second)
}

func voiceCallRecordFromDomain(record phone.CallRecord) VoiceCallRecord {
	return VoiceCallRecord{
		ID: record.ID, CallID: record.CallID, DeviceID: record.DeviceID, ICCID: record.ICCID,
		UserID: record.UserID, PoolID: record.PoolID, CardID: record.CardID,
		ProfileID: record.ProfileID, NumberID: record.NumberID, SourceNumber: record.SourceNumber,
		RequestID: record.RequestID, ResourceLeaseID: record.ResourceLeaseID,
		Direction: record.Direction, Peer: record.Peer, Status: record.Status,
		StartedAt: record.StartedAt, AnsweredAt: record.AnsweredAt, EndedAt: record.EndedAt,
		DurationSeconds: record.DurationSeconds, EndReason: record.EndReason, Codec: record.Codec,
		RecordingName: record.RecordingName, PCAPName: record.PCAPName,
		RecordingError: record.RecordingError,
		OutcomeKind:    record.OutcomeKind, SIPCode: record.SIPCode, SIPReason: record.SIPReason,
		Terminator: record.Terminator,
	}
}

func voiceCallRecordToDomain(record VoiceCallRecord) phone.CallRecord {
	return phone.CallRecord{
		ID: record.ID, CallID: record.CallID, DeviceID: record.DeviceID, ICCID: record.ICCID,
		UserID: record.UserID, PoolID: record.PoolID, CardID: record.CardID,
		ProfileID: record.ProfileID, NumberID: record.NumberID, SourceNumber: record.SourceNumber,
		RequestID: record.RequestID, ResourceLeaseID: record.ResourceLeaseID,
		Direction: record.Direction, Peer: record.Peer, Status: record.Status,
		StartedAt: record.StartedAt, AnsweredAt: record.AnsweredAt, EndedAt: record.EndedAt,
		DurationSeconds: record.DurationSeconds, EndReason: record.EndReason, Codec: record.Codec,
		RecordingName: record.RecordingName, PCAPName: record.PCAPName,
		RecordingError: record.RecordingError,
		OutcomeKind:    record.OutcomeKind, SIPCode: record.SIPCode, SIPReason: record.SIPReason,
		Terminator: record.Terminator,
	}
}
