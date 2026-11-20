package dispatch

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

var (
	ErrNoResource        = errors.New("dispatch: no eligible resource")
	ErrActiveUserLease   = errors.New("dispatch: user already has an active call")
	ErrLeaseConflict     = errors.New("dispatch: resource lease conflict")
	ErrInvalidTransition = errors.New("dispatch: invalid lease transition")
)

const defaultRemoteDeclineThreshold = 3

type Candidate struct {
	PoolID        string
	DeviceID      string
	CardID        string
	ProfileID     string
	NumberID      string
	Priority      int
	Online        bool
	Enabled       bool
	Faulted       bool
	Busy          bool
	Switching     bool
	Registering   bool
	AlreadyActive bool
	IMSReady      bool
	DailyUseCount int64
	LastUsedAt    time.Time
}

type ReserveInput struct {
	UserID           string
	PoolID           string
	Callee           string
	EnforceUserLimit bool
}

type Reservation struct {
	Request CallRequest   `json:"request"`
	Lease   ResourceLease `json:"lease"`
}

type OutcomeInput struct {
	CallID     string
	RequestID  string
	LeaseID    string
	UserID     string
	CardID     string
	ProfileID  string
	NumberID   string
	Kind       OutcomeKind
	SIPCode    int
	SIPReason  string
	Terminator string
	ObservedAt time.Time
	Threshold  int
}

type Service struct {
	db       *gorm.DB
	now      func() time.Time
	location *time.Location
}

func New(database *gorm.DB, now func() time.Time) (*Service, error) {
	if database == nil {
		return nil, errors.New("dispatch: database is required")
	}
	// OpenWrt images commonly omit the IANA timezone database. China has used a
	// constant UTC+8 offset since 1991, so an embedded fixed zone keeps dispatch
	// accounting deterministic without requiring host zoneinfo files.
	location := time.FixedZone("Asia/Shanghai", 8*60*60)
	if now == nil {
		now = time.Now
	}
	return &Service{db: database, now: now, location: location}, nil
}

func Migrate(database *gorm.DB) error {
	if database == nil {
		return errors.New("dispatch: database is required")
	}
	if err := database.AutoMigrate(&CallRequest{}, &ResourceLease{}, &CallOutcome{}, &CardDailyStat{}); err != nil {
		return err
	}
	statements := []string{
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_resource_leases_active_device ON resource_leases(device_id) WHERE released_at IS NULL`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_resource_leases_active_user ON resource_leases(user_id) WHERE released_at IS NULL AND enforce_user_limit = 1`,
	}
	for _, statement := range statements {
		if err := database.Exec(statement).Error; err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) Reserve(ctx context.Context, input ReserveInput, candidates []Candidate) (Reservation, error) {
	input.UserID = strings.TrimSpace(input.UserID)
	input.PoolID = strings.TrimSpace(input.PoolID)
	input.Callee = strings.TrimSpace(input.Callee)
	if input.UserID == "" || input.PoolID == "" || input.Callee == "" {
		return Reservation{}, errors.New("dispatch: user, pool and callee are required")
	}
	now := s.now()
	request := CallRequest{
		ID: uuid.NewString(), UserID: input.UserID, PoolID: input.PoolID, Callee: input.Callee,
		State: RequestPending, CreatedAt: now, UpdatedAt: now,
	}
	ranked := s.rankCandidates(ctx, input.PoolID, candidates, now)
	var reserved ResourceLease
	var allocationErr error
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&request).Error; err != nil {
			return err
		}
		if input.EnforceUserLimit {
			var count int64
			if err := tx.Model(&ResourceLease{}).Where("user_id = ? AND released_at IS NULL AND enforce_user_limit = ?", input.UserID, true).Count(&count).Error; err != nil {
				return err
			}
			if count > 0 {
				allocationErr = ErrActiveUserLease
				return markRequestFailed(tx, &request, "user_busy", allocationErr.Error(), now)
			}
		}
		for _, candidate := range ranked {
			lease := ResourceLease{
				ID: uuid.NewString(), RequestID: request.ID, UserID: input.UserID, PoolID: input.PoolID,
				DeviceID: candidate.DeviceID, CardID: candidate.CardID, ProfileID: candidate.ProfileID,
				NumberID: candidate.NumberID, State: LeaseReserved, Version: 1,
				EnforceUserLimit: input.EnforceUserLimit, AcquiredAt: now, HeartbeatAt: now,
				CreatedAt: now, UpdatedAt: now,
			}
			if err := tx.Create(&lease).Error; err != nil {
				if uniqueConstraintError(err) {
					continue
				}
				return err
			}
			reserved = lease
			request.State = RequestAllocated
			request.LeaseID = lease.ID
			request.SelectedDeviceID = lease.DeviceID
			request.SelectedCardID = lease.CardID
			request.SelectedProfileID = lease.ProfileID
			request.SelectedNumberID = lease.NumberID
			request.UpdatedAt = now
			return tx.Save(&request).Error
		}
		allocationErr = ErrNoResource
		return markRequestFailed(tx, &request, "no_resource", allocationErr.Error(), now)
	})
	if err != nil {
		return Reservation{}, err
	}
	if allocationErr != nil {
		return Reservation{Request: request}, allocationErr
	}
	return Reservation{Request: request, Lease: reserved}, nil
}

func (s *Service) ReserveMaintenance(ctx context.Context, input MaintenanceInput) (Reservation, error) {
	input.UserID = strings.TrimSpace(input.UserID)
	input.Operation = strings.TrimSpace(input.Operation)
	input.DeviceID = strings.TrimSpace(input.DeviceID)
	input.CardID = strings.TrimSpace(input.CardID)
	input.ProfileID = strings.TrimSpace(input.ProfileID)
	if input.UserID == "" || input.Operation == "" || input.DeviceID == "" || input.CardID == "" || input.ProfileID == "" {
		return Reservation{}, errors.New("dispatch: maintenance user, operation, device, card and profile are required")
	}
	now := s.now()
	request := CallRequest{
		ID: uuid.NewString(), UserID: input.UserID, PoolID: "__maintenance__", Callee: input.Operation,
		State: RequestAllocated, SelectedDeviceID: input.DeviceID, SelectedCardID: input.CardID,
		SelectedProfileID: input.ProfileID, SelectedNumberID: "maintenance:" + input.ProfileID,
		CreatedAt: now, UpdatedAt: now,
	}
	lease := ResourceLease{
		ID: uuid.NewString(), RequestID: request.ID, UserID: input.UserID, PoolID: request.PoolID,
		DeviceID: input.DeviceID, CardID: input.CardID, ProfileID: input.ProfileID,
		NumberID: request.SelectedNumberID, State: LeaseReserved, Version: 1,
		AcquiredAt: now, HeartbeatAt: now, CreatedAt: now, UpdatedAt: now,
	}
	request.LeaseID = lease.ID
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&request).Error; err != nil {
			return err
		}
		if err := tx.Create(&lease).Error; err != nil {
			if uniqueConstraintError(err) {
				return ErrNoResource
			}
			return err
		}
		return nil
	})
	if err != nil {
		return Reservation{}, err
	}
	return Reservation{Request: request, Lease: lease}, nil
}

func (s *Service) rankCandidates(ctx context.Context, poolID string, candidates []Candidate, now time.Time) []Candidate {
	date := s.businessDate(now)
	disabled := make(map[string]struct{})
	var stats []CardDailyStat
	_ = s.db.WithContext(ctx).Where("business_date = ? AND disabled_at IS NOT NULL", date).Find(&stats).Error
	for _, stat := range stats {
		disabled[stat.CardID] = struct{}{}
	}
	ranked := make([]Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate.PoolID) != poolID || !validCandidate(candidate) {
			continue
		}
		if _, blocked := disabled[candidate.CardID]; blocked {
			continue
		}
		ranked = append(ranked, candidate)
	}
	sort.SliceStable(ranked, func(left, right int) bool {
		a, b := ranked[left], ranked[right]
		aReady := a.AlreadyActive && a.IMSReady
		bReady := b.AlreadyActive && b.IMSReady
		if aReady != bReady {
			return aReady
		}
		if a.Priority != b.Priority {
			return a.Priority < b.Priority
		}
		if a.DailyUseCount != b.DailyUseCount {
			return a.DailyUseCount < b.DailyUseCount
		}
		if !a.LastUsedAt.Equal(b.LastUsedAt) {
			if a.LastUsedAt.IsZero() {
				return true
			}
			if b.LastUsedAt.IsZero() {
				return false
			}
			return a.LastUsedAt.Before(b.LastUsedAt)
		}
		return a.NumberID < b.NumberID
	})
	return ranked
}

func validCandidate(candidate Candidate) bool {
	return candidate.Online && candidate.Enabled && !candidate.Faulted && !candidate.Busy &&
		!candidate.Switching && !candidate.Registering && strings.TrimSpace(candidate.DeviceID) != "" &&
		strings.TrimSpace(candidate.CardID) != "" && strings.TrimSpace(candidate.ProfileID) != "" &&
		strings.TrimSpace(candidate.NumberID) != ""
}

func markRequestFailed(tx *gorm.DB, request *CallRequest, code, reason string, now time.Time) error {
	request.State, request.FailureCode, request.FailureReason, request.UpdatedAt = RequestFailed, code, reason, now
	return tx.Save(request).Error
}

func uniqueConstraintError(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique constraint") || strings.Contains(message, "constraint failed") || strings.Contains(message, "duplicate")
}

func (s *Service) Transition(ctx context.Context, leaseID string, expectedVersion uint64, next LeaseState) (ResourceLease, error) {
	leaseID = strings.TrimSpace(leaseID)
	if leaseID == "" || expectedVersion == 0 {
		return ResourceLease{}, ErrLeaseConflict
	}
	var lease ResourceLease
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("id = ?", leaseID).First(&lease).Error; err != nil {
			return err
		}
		if lease.Version != expectedVersion || lease.ReleasedAt != nil {
			return ErrLeaseConflict
		}
		if !allowedTransition(lease.State, next) {
			return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, lease.State, next)
		}
		now := s.now()
		result := tx.Model(&ResourceLease{}).
			Where("id = ? AND version = ? AND released_at IS NULL", leaseID, expectedVersion).
			Updates(map[string]any{"state": next, "version": expectedVersion + 1, "heartbeat_at": now, "updated_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrLeaseConflict
		}
		lease.State, lease.Version, lease.HeartbeatAt, lease.UpdatedAt = next, expectedVersion+1, now, now
		return nil
	})
	return lease, err
}

func allowedTransition(current, next LeaseState) bool {
	allowed := map[LeaseState]map[LeaseState]bool{
		LeaseReserved:        {LeaseSwitching: true, LeaseIdentityRefresh: true, LeaseRegistering: true, LeaseReady: true, LeaseReleasing: true, LeaseFailed: true, LeaseFaulted: true},
		LeaseSwitching:       {LeaseIdentityRefresh: true, LeaseReleasing: true, LeaseFailed: true, LeaseFaulted: true},
		LeaseIdentityRefresh: {LeaseRegistering: true, LeaseReleasing: true, LeaseFailed: true, LeaseFaulted: true},
		LeaseRegistering:     {LeaseReady: true, LeaseReleasing: true, LeaseFailed: true, LeaseFaulted: true},
		LeaseReady:           {LeaseDialing: true, LeaseReleasing: true, LeaseFailed: true, LeaseFaulted: true},
		LeaseDialing:         {LeaseRinging: true, LeaseConnected: true, LeaseReleasing: true, LeaseFailed: true, LeaseFaulted: true},
		LeaseRinging:         {LeaseConnected: true, LeaseReleasing: true, LeaseFailed: true, LeaseFaulted: true},
		LeaseConnected:       {LeaseReleasing: true, LeaseFailed: true, LeaseFaulted: true},
		LeaseReleasing:       {LeaseReleased: true, LeaseFailed: true, LeaseFaulted: true, LeaseOrphaned: true},
		LeaseFailed:          {LeaseReleasing: true, LeaseReleased: true},
		LeaseFaulted:         {LeaseReleasing: true, LeaseReleased: true},
		LeaseOrphaned:        {LeaseReleasing: true, LeaseReleased: true},
	}
	return allowed[current][next]
}

func (s *Service) BindCall(ctx context.Context, leaseID, callID string) error {
	leaseID, callID = strings.TrimSpace(leaseID), strings.TrimSpace(callID)
	if leaseID == "" || callID == "" {
		return errors.New("dispatch: lease and call are required")
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var lease ResourceLease
		if err := tx.Where("id = ? AND released_at IS NULL", leaseID).First(&lease).Error; err != nil {
			return err
		}
		if err := tx.Model(&ResourceLease{}).Where("id = ?", leaseID).Updates(map[string]any{"call_id": callID, "updated_at": s.now()}).Error; err != nil {
			return err
		}
		return tx.Model(&CallRequest{}).Where("id = ?", lease.RequestID).Updates(map[string]any{"call_id": callID, "state": RequestDialing, "updated_at": s.now()}).Error
	})
}

func (s *Service) Release(ctx context.Context, leaseID, failureCode, failureReason string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var lease ResourceLease
		if err := tx.Where("id = ?", strings.TrimSpace(leaseID)).First(&lease).Error; err != nil {
			return err
		}
		if lease.ReleasedAt != nil {
			return nil
		}
		now := s.now()
		if err := tx.Model(&ResourceLease{}).Where("id = ? AND released_at IS NULL", lease.ID).Updates(map[string]any{
			"state": LeaseReleased, "released_at": now, "heartbeat_at": now,
			"failure_code": strings.TrimSpace(failureCode), "failure_reason": strings.TrimSpace(failureReason),
			"version": gorm.Expr("version + 1"), "updated_at": now,
		}).Error; err != nil {
			return err
		}
		updates := map[string]any{"state": RequestCompleted, "updated_at": now}
		if strings.TrimSpace(failureCode) != "" {
			updates["state"] = RequestFailed
			updates["failure_code"] = strings.TrimSpace(failureCode)
			updates["failure_reason"] = strings.TrimSpace(failureReason)
		}
		return tx.Model(&CallRequest{}).Where("id = ?", lease.RequestID).Updates(updates).Error
	})
}

func (s *Service) ActiveLeases(ctx context.Context) ([]ResourceLease, error) {
	var leases []ResourceLease
	err := s.db.WithContext(ctx).
		Where("released_at IS NULL").
		Order("acquired_at ASC, id ASC").
		Find(&leases).Error
	return leases, err
}

func (s *Service) Lease(ctx context.Context, leaseID string) (ResourceLease, error) {
	var lease ResourceLease
	err := s.db.WithContext(ctx).Where("id = ?", strings.TrimSpace(leaseID)).First(&lease).Error
	return lease, err
}

func (s *Service) DailyUsageCounts(ctx context.Context, date string) (map[string]int64, error) {
	date = strings.TrimSpace(date)
	if date == "" {
		date = s.businessDate(s.now())
	}
	type row struct {
		CardID string
		Count  int64
	}
	var rows []row
	if err := s.db.WithContext(ctx).Model(&CallOutcome{}).
		Select("card_id, COUNT(*) AS count").
		Where("business_date = ? AND card_id <> ''", date).
		Group("card_id").Scan(&rows).Error; err != nil {
		return nil, err
	}
	result := make(map[string]int64, len(rows))
	for _, item := range rows {
		result[item.CardID] = item.Count
	}
	return result, nil
}

func (s *Service) LastUsedTimes(ctx context.Context) (map[string]time.Time, error) {
	var rows []CallOutcome
	if err := s.db.WithContext(ctx).Model(&CallOutcome{}).
		Select("number_id, observed_at").
		Where("number_id <> ''").
		Order("observed_at DESC").Scan(&rows).Error; err != nil {
		return nil, err
	}
	result := make(map[string]time.Time, len(rows))
	for _, item := range rows {
		if _, exists := result[item.NumberID]; !exists {
			result[item.NumberID] = item.ObservedAt
		}
	}
	return result, nil
}

func (s *Service) businessDate(at time.Time) string {
	return at.In(s.location).Format("2006-01-02")
}
