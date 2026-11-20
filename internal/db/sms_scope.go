package db

import (
	"context"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
)

// SMSAccessIdentity is one subscription identity granted to a user through a
// number pool. Real ICCID takes precedence over IMSI/local-number fallbacks.
type SMSAccessIdentity struct {
	ICCID      string
	IMSI       string
	LocalPhone string
}

// SMSAccessScope is immutable after construction and safe to reuse in an SSE
// subscription. The zero value denies every row.
type SMSAccessScope struct {
	unrestricted bool
	iccids       map[string]struct{}
	imsis        map[string]struct{}
	localPhones  map[string]struct{}
}

func UnrestrictedSMSAccessScope() SMSAccessScope {
	return SMSAccessScope{unrestricted: true}
}

func NewSMSAccessScope(identities []SMSAccessIdentity) SMSAccessScope {
	scope := SMSAccessScope{
		iccids: make(map[string]struct{}), imsis: make(map[string]struct{}), localPhones: make(map[string]struct{}),
	}
	for _, identity := range identities {
		if iccid := CanonicalICCID(identity.ICCID); iccid != "" && !isSMSFallbackICCID(iccid) {
			scope.iccids[iccid] = struct{}{}
		}
		if imsi := strings.TrimSpace(identity.IMSI); imsi != "" {
			scope.imsis[imsi] = struct{}{}
		}
		if phone := strings.TrimSpace(identity.LocalPhone); phone != "" {
			scope.localPhones[phone] = struct{}{}
		}
	}
	return scope
}

func (scope SMSAccessScope) Unrestricted() bool { return scope.unrestricted }

func (scope SMSAccessScope) AllowsICCID(iccid string) bool {
	if scope.unrestricted {
		return true
	}
	iccid = CanonicalICCID(iccid)
	if !isSMSFallbackICCID(iccid) {
		_, allowed := scope.iccids[iccid]
		return allowed
	}
	if imsi, ok := strings.CutPrefix(strings.ToLower(iccid), "imsi:"); ok {
		for allowed := range scope.imsis {
			if strings.EqualFold(allowed, imsi) {
				return true
			}
		}
	}
	return false
}

func (scope SMSAccessScope) AllowsSMS(message SMS) bool {
	return scope.allowsIdentity(message.ICCID, message.IMSI, message.LocalPhone)
}

func (scope SMSAccessScope) allowsIdentity(iccid, imsi, localPhone string) bool {
	if scope.unrestricted {
		return true
	}
	iccid = CanonicalICCID(iccid)
	if !isSMSFallbackICCID(iccid) {
		_, allowed := scope.iccids[iccid]
		return allowed
	}
	if imsi = strings.TrimSpace(imsi); imsi != "" {
		if _, allowed := scope.imsis[imsi]; allowed {
			return true
		}
	}
	if localPhone = strings.TrimSpace(localPhone); localPhone != "" {
		_, allowed := scope.localPhones[localPhone]
		return allowed
	}
	return false
}

func isSMSFallbackICCID(iccid string) bool {
	iccid = strings.ToLower(strings.TrimSpace(iccid))
	return iccid == "" || strings.HasPrefix(iccid, "imsi:") || strings.HasPrefix(iccid, "reader-imsi-")
}

func (scope SMSAccessScope) applySMS(query *gorm.DB) *gorm.DB {
	return scope.applyIdentityQuery(query, true)
}

func (scope SMSAccessScope) applyContact(query *gorm.DB) *gorm.DB {
	return scope.applyIdentityQuery(query, false)
}

func (scope SMSAccessScope) applyDelivery(query *gorm.DB) *gorm.DB {
	return scope.applyIdentityQuery(query, false)
}

func (scope SMSAccessScope) applyIdentityQuery(query *gorm.DB, includeLocalPhone bool) *gorm.DB {
	if scope.unrestricted {
		return query
	}
	clauses := make([]string, 0, 3)
	args := make([]any, 0, 3)
	if values := smsAccessKeys(scope.iccids); len(values) > 0 {
		clauses = append(clauses, "iccid IN ?")
		args = append(args, values)
	}
	fallback := "(TRIM(COALESCE(iccid, '')) = '' OR LOWER(iccid) LIKE 'imsi:%' OR LOWER(iccid) LIKE 'reader-imsi-%')"
	if values := smsAccessKeys(scope.imsis); len(values) > 0 {
		clauses = append(clauses, fallback+" AND imsi IN ?")
		args = append(args, values)
	}
	if includeLocalPhone {
		if values := smsAccessKeys(scope.localPhones); len(values) > 0 {
			clauses = append(clauses, fallback+" AND local_phone IN ?")
			args = append(args, values)
		}
	}
	if len(clauses) == 0 {
		return query.Where("1 = 0")
	}
	return query.Where("("+strings.Join(clauses, ") OR (")+")", args...)
}

func smsAccessKeys(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	return result
}

func GetSMSContactsScoped(scope SMSAccessScope, limit int, beforeTs *time.Time, beforePeer string) ([]SMSContact, error) {
	if DB == nil {
		return []SMSContact{}, nil
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	query := scope.applyContact(DB.Model(&SMSContact{}))
	if beforeTs != nil && !beforeTs.IsZero() {
		if beforePeer != "" {
			query = query.Where("last_timestamp < ? OR (last_timestamp = ? AND peer < ?)", *beforeTs, *beforeTs, beforePeer)
		} else {
			query = query.Where("last_timestamp < ?", *beforeTs)
		}
	}
	var result []SMSContact
	err := query.Order("last_timestamp desc, peer desc").Limit(limit).Find(&result).Error
	return result, err
}

func GetSMSByICCIDAndPeerScoped(scope SMSAccessScope, iccid, peer string, limit int, beforeTs *time.Time, beforeID uint) ([]SMS, error) {
	if scope.Unrestricted() {
		return GetSMSByICCIDAndPeer(iccid, peer, limit, beforeTs, beforeID)
	}
	if DB == nil {
		return []SMS{}, nil
	}
	iccid = CanonicalICCID(iccid)
	peer = strings.TrimSpace(peer)
	if iccid == "" || peer == "" || !scope.AllowsICCID(iccid) {
		return []SMS{}, nil
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	query := scope.applySMS(DB.Model(&SMS{}).Where("iccid = ? AND peer = ?", iccid, peer))
	if beforeTs != nil && !beforeTs.IsZero() && beforeID > 0 {
		query = query.Where("timestamp < ? OR (timestamp = ? AND id < ?)", *beforeTs, *beforeTs, beforeID)
	} else if beforeTs != nil && !beforeTs.IsZero() {
		query = query.Where("timestamp < ?", *beforeTs)
	} else if beforeID > 0 {
		query = query.Where("id < ?", beforeID)
	}
	var result []SMS
	err := query.Order("timestamp desc, id desc").Limit(limit).Find(&result).Error
	return result, err
}

func GetSMSNotificationsScoped(ctx context.Context, scope SMSAccessScope, afterID *uint) (SMSNotifications, error) {
	if scope.Unrestricted() {
		return GetSMSNotifications(ctx, afterID)
	}
	var result SMSNotifications
	if DB == nil {
		return result, errors.New("database is not initialized")
	}
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		incoming := scope.applySMS(tx.Model(&SMS{}).Where("type = ?", smsTypeIncoming))
		if err := incoming.Select("COALESCE(MAX(id), 0)").Scan(&result.Cursor).Error; err != nil {
			return err
		}
		unread := scope.applySMS(tx.Model(&SMS{}).Where("type = ? AND status = ?", smsTypeIncoming, smsStatusUnread))
		if err := unread.Count(&result.UnreadCount).Error; err != nil {
			return err
		}
		if afterID == nil {
			return nil
		}
		if result.Cursor < *afterID {
			result.Cursor = *afterID
		}
		query := scope.applySMS(tx.Model(&SMS{}).Where(
			"type = ? AND status = ? AND id > ? AND id <= ?", smsTypeIncoming, smsStatusUnread, *afterID, result.Cursor,
		))
		if err := query.Count(&result.NewCount).Error; err != nil || result.NewCount == 0 {
			return err
		}
		var latest SMS
		if err := query.Order("id DESC").First(&latest).Error; err != nil {
			return err
		}
		result.Latest = &latest
		return nil
	})
	return result, err
}

func IncomingSMSCursorScoped(ctx context.Context, scope SMSAccessScope) (uint, error) {
	if scope.Unrestricted() {
		return IncomingSMSCursor(ctx)
	}
	if DB == nil {
		return 0, errors.New("database is not initialized")
	}
	var cursor uint
	err := scope.applySMS(DB.WithContext(ctx).Model(&SMS{}).Where("type = ?", smsTypeIncoming)).
		Select("COALESCE(MAX(id), 0)").Scan(&cursor).Error
	return cursor, err
}

func ListIncomingUnreadSMSScoped(ctx context.Context, scope SMSAccessScope, query IncomingSMSBacklogQuery) ([]SMS, error) {
	if scope.Unrestricted() {
		return ListIncomingUnreadSMS(ctx, query)
	}
	if DB == nil {
		return nil, errors.New("database is not initialized")
	}
	limit := query.Limit
	if limit <= 0 || limit > incomingSMSBacklogLimit {
		limit = incomingSMSBacklogLimit
	}
	statement := scope.applySMS(DB.WithContext(ctx).Where(
		"type = ? AND status = ? AND id > ?", smsTypeIncoming, smsStatusUnread, query.AfterID,
	))
	if query.ThroughID != nil {
		statement = statement.Where("id <= ?", *query.ThroughID)
	}
	var rows []SMS
	err := statement.Order("id ASC").Limit(limit).Find(&rows).Error
	return rows, err
}

func GetSMSMessageTargetScoped(ctx context.Context, scope SMSAccessScope, id uint) (SMSMessageTarget, error) {
	if scope.Unrestricted() {
		return GetSMSMessageTarget(ctx, id)
	}
	var result SMSMessageTarget
	if DB == nil {
		return result, errors.New("database is not initialized")
	}
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := scope.applySMS(tx.Model(&SMS{}).Where("id = ?", id)).First(&result.Message).Error; err != nil {
			return err
		}
		message := result.Message
		if err := scope.applyContact(tx.Model(&SMSContact{}).Where("iccid = ? AND peer = ?", message.ICCID, message.Peer)).First(&result.Contact).Error; err != nil {
			return err
		}
		query := scope.applySMS(tx.Model(&SMS{}).Where("iccid = ? AND peer = ?", message.ICCID, message.Peer)).
			Where("timestamp < ? OR (timestamp = ? AND id <= ?)", message.Timestamp, message.Timestamp, message.ID)
		if err := query.Order("timestamp DESC, id DESC").Limit(smsTargetHistoryLimit + 1).Find(&result.Messages).Error; err != nil {
			return err
		}
		result.HasMore = len(result.Messages) > smsTargetHistoryLimit
		if result.HasMore {
			result.Messages = result.Messages[:smsTargetHistoryLimit]
		}
		return nil
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return result, ErrSMSNotFound
	}
	return result, err
}

func MarkSMSThreadReadByICCIDScoped(scope SMSAccessScope, iccid, peer string, throughID uint) (SMSReadResult, error) {
	if !scope.AllowsICCID(iccid) {
		return SMSReadResult{}, ErrSMSNotFound
	}
	return MarkSMSThreadReadByICCID(iccid, peer, throughID)
}

func MarkSMSMessagesReadByICCIDScoped(scope SMSAccessScope, iccid, peer string, messageIDs []uint) (SMSReadResult, error) {
	if !scope.AllowsICCID(iccid) {
		return SMSReadResult{}, ErrSMSNotFound
	}
	return MarkSMSMessagesReadByICCID(iccid, peer, messageIDs)
}

func DeleteSMSByIDScoped(scope SMSAccessScope, id uint) (bool, string, string, error) {
	if scope.Unrestricted() {
		return DeleteSMSByID(id)
	}
	if DB == nil {
		return true, "", "", ErrSMSNotFound
	}
	var threadEmpty bool
	var message SMS
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := scope.applySMS(tx.Model(&SMS{}).Where("id = ?", id)).First(&message).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrSMSNotFound
			}
			return err
		}
		if err := tx.Delete(&SMS{}, message.ID).Error; err != nil {
			return err
		}
		var err error
		threadEmpty, err = rebuildSMSContactTx(tx, CanonicalICCID(message.ICCID), strings.TrimSpace(message.Peer))
		return err
	})
	return threadEmpty, strings.TrimSpace(message.IMSI), strings.TrimSpace(message.Peer), err
}

func DeleteSMSByICCIDAndPeerScoped(scope SMSAccessScope, iccid, peer string) (int64, error) {
	if !scope.AllowsICCID(iccid) {
		return 0, ErrSMSNotFound
	}
	return DeleteSMSByICCIDAndPeer(iccid, peer)
}

func GetSMSDeliveryStatusScoped(scope SMSAccessScope, messageID string) (*SMSDeliveryStatus, error) {
	if scope.Unrestricted() {
		return GetSMSDeliveryStatus(messageID)
	}
	if DB == nil {
		return nil, gorm.ErrRecordNotFound
	}
	messageID = strings.TrimSpace(messageID)
	var delivery SMSDelivery
	if err := scope.applyDelivery(DB.Model(&SMSDelivery{}).Where("message_id = ?", messageID)).First(&delivery).Error; err != nil {
		return nil, err
	}
	var parts []SMSDeliveryPart
	if err := DB.Where("message_id = ?", messageID).Order("part_no asc").Find(&parts).Error; err != nil {
		return nil, err
	}
	return &SMSDeliveryStatus{
		MessageID: delivery.MessageID, IMSI: delivery.IMSI, ICCID: delivery.ICCID, DeviceID: delivery.DeviceID,
		Peer: delivery.Peer, Content: delivery.Content, PartsTotal: delivery.PartsTotal, Acks: delivery.Acks,
		State: delivery.State, LastError: delivery.LastError, CreatedAt: delivery.CreatedAt, UpdatedAt: delivery.UpdatedAt,
		Parts: parts,
	}, nil
}
