package resourcecatalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

var e164Pattern = regexp.MustCompile(`^\+[1-9][0-9]{1,14}$`)

type NumberPoolView struct {
	NumberPool
	Members  []NumberPoolMemberView `json:"members"`
	Users    []NumberPoolUser       `json:"users,omitempty"`
	Policies []DispatchPolicy       `json:"policies,omitempty"`
}

type NumberPoolMemberView struct {
	NumberPoolMember
	Number  PhoneNumber          `json:"number"`
	Profile *SubscriptionProfile `json:"profile,omitempty"`
	Card    *CardContainer       `json:"card,omitempty"`
}

type CreateNumberPoolInput struct {
	Name        string
	Description string
	Enabled     *bool
}

type UpdateNumberPoolInput struct {
	Name        *string
	Description *string
	Enabled     *bool
}

type PoolMemberInput struct {
	PhoneNumberID string `json:"phone_number_id"`
	Priority      int    `json:"priority"`
	Weight        int    `json:"weight"`
	Enabled       *bool  `json:"enabled,omitempty"`
}

type PoolUserInput struct {
	UserID  string `json:"user_id"`
	Role    string `json:"role"`
	Enabled *bool  `json:"enabled,omitempty"`
}

type PhoneNumberInput struct {
	ID                    string
	SubscriptionProfileID string
	E164                  string
	DisplayNumber         string
	Label                 string
	Status                string
}

type DispatchPolicyInput struct {
	ID           string
	NumberPoolID string
	Name         string
	Strategy     string
	Priority     int
	Enabled      *bool
	ConfigJSON   string
}

func (s *Service) ListNumberPools(ctx context.Context, userID string, admin bool) ([]NumberPoolView, error) {
	if s == nil || s.database == nil {
		return nil, errors.New("resource catalog database is nil")
	}
	query := s.database.WithContext(ctx).Model(&NumberPool{}).Order("resource_number_pools.name ASC, resource_number_pools.id ASC")
	if !admin {
		query = query.Joins("JOIN resource_number_pool_users grants ON grants.number_pool_id = resource_number_pools.id AND grants.user_id = ? AND grants.enabled = ?", strings.TrimSpace(userID), true).
			Where("resource_number_pools.enabled = ?", true)
	}
	var pools []NumberPool
	if err := query.Find(&pools).Error; err != nil {
		return nil, err
	}
	views := make([]NumberPoolView, 0, len(pools))
	for _, pool := range pools {
		view, err := s.loadNumberPoolView(ctx, pool, admin)
		if err != nil {
			return nil, err
		}
		views = append(views, view)
	}
	return views, nil
}

func (s *Service) GetNumberPool(ctx context.Context, id, userID string, admin bool) (NumberPoolView, error) {
	var pool NumberPool
	query := s.database.WithContext(ctx).Where("id = ?", strings.TrimSpace(id))
	if !admin {
		query = query.Where("enabled = ? AND EXISTS (SELECT 1 FROM resource_number_pool_users grants WHERE grants.number_pool_id = resource_number_pools.id AND grants.user_id = ? AND grants.enabled = ?)", true, strings.TrimSpace(userID), true)
	}
	if err := query.First(&pool).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return NumberPoolView{}, ErrNotFound
		}
		return NumberPoolView{}, err
	}
	return s.loadNumberPoolView(ctx, pool, admin)
}

func (s *Service) loadNumberPoolView(ctx context.Context, pool NumberPool, includeManagement bool) (NumberPoolView, error) {
	view := NumberPoolView{NumberPool: pool, Members: []NumberPoolMemberView{}}
	var members []NumberPoolMember
	if err := s.database.WithContext(ctx).Where("number_pool_id = ?", pool.ID).Order("priority ASC, id ASC").Find(&members).Error; err != nil {
		return NumberPoolView{}, err
	}
	for _, member := range members {
		var number PhoneNumber
		if err := s.database.WithContext(ctx).Where("id = ?", member.PhoneNumberID).First(&number).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			}
			return NumberPoolView{}, err
		}
		memberView := NumberPoolMemberView{NumberPoolMember: member, Number: number}
		if number.SubscriptionProfileID != nil && strings.TrimSpace(*number.SubscriptionProfileID) != "" {
			var profile SubscriptionProfile
			if err := s.database.WithContext(ctx).Where("id = ?", *number.SubscriptionProfileID).First(&profile).Error; err == nil {
				memberView.Profile = &profile
				var card CardContainer
				if err := s.database.WithContext(ctx).Where("id = ?", profile.CardContainerID).First(&card).Error; err == nil {
					memberView.Card = &card
				}
			}
		}
		view.Members = append(view.Members, memberView)
	}
	if includeManagement {
		if err := s.database.WithContext(ctx).Where("number_pool_id = ?", pool.ID).Order("user_id ASC").Find(&view.Users).Error; err != nil {
			return NumberPoolView{}, err
		}
		if err := s.database.WithContext(ctx).Where("number_pool_id = ?", pool.ID).Order("priority ASC, id ASC").Find(&view.Policies).Error; err != nil {
			return NumberPoolView{}, err
		}
	}
	return view, nil
}

func (s *Service) CreateNumberPool(ctx context.Context, actorID string, input CreateNumberPoolInput) (NumberPool, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return NumberPool{}, errors.New("resource catalog: pool name is required")
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	now := time.Now().UTC()
	pool := NumberPool{ID: uuid.NewString(), Name: name, Description: strings.TrimSpace(input.Description), Enabled: enabled, CreatedAt: now, UpdatedAt: now}
	err := s.database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&pool).Error; err != nil {
			return err
		}
		return createAudit(tx, actorID, "number_pool.created", "number_pool", pool.ID, pool, now)
	})
	return pool, err
}

func (s *Service) UpdateNumberPool(ctx context.Context, actorID, id string, input UpdateNumberPoolInput) (NumberPool, error) {
	var pool NumberPool
	err := s.database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("id = ?", strings.TrimSpace(id)).First(&pool).Error; err != nil {
			return err
		}
		updates := map[string]any{"updated_at": time.Now().UTC()}
		if input.Name != nil {
			name := strings.TrimSpace(*input.Name)
			if name == "" {
				return errors.New("resource catalog: pool name is required")
			}
			updates["name"] = name
		}
		if input.Description != nil {
			updates["description"] = strings.TrimSpace(*input.Description)
		}
		if input.Enabled != nil {
			updates["enabled"] = *input.Enabled
		}
		if err := tx.Model(&pool).Updates(updates).Error; err != nil {
			return err
		}
		if err := tx.Where("id = ?", pool.ID).First(&pool).Error; err != nil {
			return err
		}
		return createAudit(tx, actorID, "number_pool.updated", "number_pool", pool.ID, updates, time.Now().UTC())
	})
	return pool, err
}

func (s *Service) ReplacePoolMembers(ctx context.Context, actorID, poolID string, inputs []PoolMemberInput) ([]NumberPoolMember, error) {
	poolID = strings.TrimSpace(poolID)
	result := make([]NumberPoolMember, 0, len(inputs))
	err := s.database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requirePool(tx, poolID); err != nil {
			return err
		}
		seen := make(map[string]struct{}, len(inputs))
		for index, input := range inputs {
			phoneID := strings.TrimSpace(input.PhoneNumberID)
			if phoneID == "" {
				return fmt.Errorf("resource catalog: member %d has no phone number", index)
			}
			if _, duplicate := seen[phoneID]; duplicate {
				return fmt.Errorf("resource catalog: duplicate phone number %s", phoneID)
			}
			seen[phoneID] = struct{}{}
			var count int64
			if err := tx.Model(&PhoneNumber{}).Where("id = ?", phoneID).Count(&count).Error; err != nil || count != 1 {
				return fmt.Errorf("resource catalog: phone number %s not found", phoneID)
			}
			enabled := true
			if input.Enabled != nil {
				enabled = *input.Enabled
			}
			weight := input.Weight
			if weight <= 0 {
				weight = 1
			}
			result = append(result, NumberPoolMember{ID: uuid.NewString(), NumberPoolID: poolID, PhoneNumberID: phoneID, Priority: input.Priority, Weight: weight, Enabled: enabled})
		}
		if err := tx.Where("number_pool_id = ?", poolID).Delete(&NumberPoolMember{}).Error; err != nil {
			return err
		}
		if len(result) > 0 {
			if err := tx.Create(&result).Error; err != nil {
				return err
			}
		}
		return createAudit(tx, actorID, "number_pool.members_replaced", "number_pool", poolID, inputs, time.Now().UTC())
	})
	return result, err
}

func (s *Service) ReplacePoolUsers(ctx context.Context, actorID, poolID string, inputs []PoolUserInput) ([]NumberPoolUser, error) {
	poolID = strings.TrimSpace(poolID)
	result := make([]NumberPoolUser, 0, len(inputs))
	err := s.database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requirePool(tx, poolID); err != nil {
			return err
		}
		seen := make(map[string]struct{}, len(inputs))
		for index, input := range inputs {
			userID := strings.TrimSpace(input.UserID)
			if userID == "" {
				return fmt.Errorf("resource catalog: user grant %d has no user", index)
			}
			if _, duplicate := seen[userID]; duplicate {
				return fmt.Errorf("resource catalog: duplicate user %s", userID)
			}
			seen[userID] = struct{}{}
			enabled := true
			if input.Enabled != nil {
				enabled = *input.Enabled
			}
			role := strings.TrimSpace(input.Role)
			if role == "" {
				role = "member"
			}
			result = append(result, NumberPoolUser{ID: uuid.NewString(), NumberPoolID: poolID, UserID: userID, Role: role, Enabled: enabled})
		}
		if err := tx.Where("number_pool_id = ?", poolID).Delete(&NumberPoolUser{}).Error; err != nil {
			return err
		}
		if len(result) > 0 {
			if err := tx.Create(&result).Error; err != nil {
				return err
			}
		}
		return createAudit(tx, actorID, "number_pool.users_replaced", "number_pool", poolID, inputs, time.Now().UTC())
	})
	return result, err
}

func (s *Service) ListPhoneNumbers(ctx context.Context) ([]PhoneNumber, error) {
	var numbers []PhoneNumber
	err := s.database.WithContext(ctx).Order("e164 ASC, id ASC").Find(&numbers).Error
	return numbers, err
}

func (s *Service) UpsertPhoneNumber(ctx context.Context, actorID string, input PhoneNumberInput) (PhoneNumber, error) {
	e164 := strings.TrimSpace(input.E164)
	if !e164Pattern.MatchString(e164) {
		return PhoneNumber{}, errors.New("resource catalog: number must use E.164 format")
	}
	status := strings.TrimSpace(input.Status)
	if status == "" {
		status = "available"
	}
	now := time.Now().UTC()
	number := PhoneNumber{ID: strings.TrimSpace(input.ID), E164: e164, DisplayNumber: strings.TrimSpace(input.DisplayNumber), Label: strings.TrimSpace(input.Label), Status: status, UpdatedAt: now}
	if number.ID == "" {
		number.ID, number.CreatedAt = uuid.NewString(), now
	}
	if profileID := strings.TrimSpace(input.SubscriptionProfileID); profileID != "" {
		number.SubscriptionProfileID = &profileID
	}
	err := s.database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if number.SubscriptionProfileID != nil {
			var count int64
			if err := tx.Model(&SubscriptionProfile{}).Where("id = ?", *number.SubscriptionProfileID).Count(&count).Error; err != nil || count != 1 {
				return errors.New("resource catalog: subscription profile not found")
			}
		}
		var existing PhoneNumber
		if err := tx.Where("id = ?", number.ID).First(&existing).Error; err == nil {
			number.CreatedAt = existing.CreatedAt
			if err := tx.Model(&existing).Updates(number).Error; err != nil {
				return err
			}
		} else if errors.Is(err, gorm.ErrRecordNotFound) {
			if err := tx.Create(&number).Error; err != nil {
				return err
			}
		} else {
			return err
		}
		return createAudit(tx, actorID, "phone_number.upserted", "phone_number", number.ID, number, now)
	})
	return number, err
}

func (s *Service) ListDispatchPolicies(ctx context.Context) ([]DispatchPolicy, error) {
	var policies []DispatchPolicy
	err := s.database.WithContext(ctx).Order("number_pool_id ASC, priority ASC, id ASC").Find(&policies).Error
	return policies, err
}

func (s *Service) UpsertDispatchPolicy(ctx context.Context, actorID string, input DispatchPolicyInput) (DispatchPolicy, error) {
	if strings.TrimSpace(input.NumberPoolID) == "" || strings.TrimSpace(input.Name) == "" {
		return DispatchPolicy{}, errors.New("resource catalog: policy pool and name are required")
	}
	if strings.TrimSpace(input.ConfigJSON) != "" && !json.Valid([]byte(input.ConfigJSON)) {
		return DispatchPolicy{}, errors.New("resource catalog: policy config must be valid JSON")
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	now := time.Now().UTC()
	policy := DispatchPolicy{
		ID: strings.TrimSpace(input.ID), NumberPoolID: strings.TrimSpace(input.NumberPoolID),
		Name: strings.TrimSpace(input.Name), Strategy: strings.TrimSpace(input.Strategy), Priority: input.Priority,
		Enabled: enabled, ConfigJSON: strings.TrimSpace(input.ConfigJSON), UpdatedAt: now,
	}
	if policy.ID == "" {
		policy.ID, policy.CreatedAt = uuid.NewString(), now
	}
	if policy.Strategy == "" {
		policy.Strategy = "active_registered_least_used"
	}
	err := s.database.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requirePool(tx, policy.NumberPoolID); err != nil {
			return err
		}
		var existing DispatchPolicy
		if err := tx.Where("id = ?", policy.ID).First(&existing).Error; err == nil {
			policy.CreatedAt = existing.CreatedAt
			if err := tx.Model(&existing).Updates(policy).Error; err != nil {
				return err
			}
		} else if errors.Is(err, gorm.ErrRecordNotFound) {
			if err := tx.Create(&policy).Error; err != nil {
				return err
			}
		} else {
			return err
		}
		return createAudit(tx, actorID, "dispatch_policy.upserted", "dispatch_policy", policy.ID, policy, now)
	})
	return policy, err
}

func (s *Service) ListAuditEvents(ctx context.Context, limit int) ([]AuditEvent, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var events []AuditEvent
	err := s.database.WithContext(ctx).Order("occurred_at DESC, id DESC").Limit(limit).Find(&events).Error
	return events, err
}

func requirePool(tx *gorm.DB, id string) error {
	var count int64
	if err := tx.Model(&NumberPool{}).Where("id = ?", id).Count(&count).Error; err != nil {
		return err
	}
	if count != 1 {
		return ErrNotFound
	}
	return nil
}

func createAudit(tx *gorm.DB, actorID, action, resourceType, resourceID string, payload any, at time.Time) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	event := AuditEvent{
		ID: uuid.NewString(), ActorID: strings.TrimSpace(actorID), Action: action,
		ResourceType: resourceType, ResourceID: resourceID, PayloadJSON: string(encoded),
		OccurredAt: at, CreatedAt: at,
	}
	return tx.Create(&event).Error
}

func SortPoolMembersForDispatch(members []NumberPoolMemberView) {
	sort.SliceStable(members, func(i, j int) bool {
		if members[i].Priority != members[j].Priority {
			return members[i].Priority < members[j].Priority
		}
		return members[i].ID < members[j].ID
	})
}
