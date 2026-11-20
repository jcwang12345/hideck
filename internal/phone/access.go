package phone

import (
	"context"
	"strings"
)

// scopedRecordStore is optional for legacy/internal stores. User-facing
// scoped operations fail closed when a configured store cannot enforce it.
type scopedHistoryStore interface {
	ListByUser(context.Context, string, int) ([]CallRecord, error)
	DeleteByUser(context.Context, string, []uint64) ([]CallRecord, error)
}

type scopedRecordingStore interface {
	FindRecording(context.Context, string, string) (CallRecord, bool, error)
}

func (scope AccessScope) validate() error {
	if strings.TrimSpace(scope.UserID) == "" {
		return ErrAccessScopeRequired
	}
	return nil
}

func (s *Service) CreateMediaScoped(ctx context.Context, scope AccessScope, offer, accessHost string) (MediaAnswer, error) {
	if err := scope.validate(); err != nil {
		return MediaAnswer{}, err
	}
	return s.CreateMedia(ctx, strings.TrimSpace(scope.UserID), offer, accessHost)
}

func (s *Service) CancelMediaScoped(scope AccessScope, mediaID, lease string) error {
	if err := scope.validate(); err != nil {
		return err
	}
	owner := strings.TrimSpace(scope.UserID)
	if scope.Administrator {
		if media := s.media.Get(strings.TrimSpace(mediaID)); media != nil {
			owner = media.Owner
		}
	}
	return s.CancelMedia(owner, mediaID, lease)
}

func (s *Service) StartCallScoped(scope AccessScope, request StartCallRequest) (CallView, error) {
	if err := scope.validate(); err != nil {
		return CallView{}, err
	}
	request.Owner = strings.TrimSpace(scope.UserID)
	return s.StartCall(request)
}

func (s *Service) ActiveScoped(scope AccessScope, lease string) ([]CallView, error) {
	if err := scope.validate(); err != nil {
		return nil, err
	}
	userID := strings.TrimSpace(scope.UserID)
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]CallView, 0, len(s.calls))
	for _, call := range s.calls {
		if call.terminal || (!scope.Administrator && call.view.UserID != userID) {
			continue
		}
		result = append(result, call.snapshot(lease))
	}
	return result, nil
}

func (s *Service) AnswerScoped(ctx context.Context, scope AccessScope, request ControlRequest) (CallView, error) {
	if err := s.authorizeCall(scope, request.CallID); err != nil {
		return CallView{}, err
	}
	request.Owner = strings.TrimSpace(scope.UserID)
	return s.Answer(ctx, request)
}

func (s *Service) RejectScoped(scope AccessScope, request ControlRequest) error {
	if err := s.authorizeCall(scope, request.CallID); err != nil {
		return err
	}
	request.Owner = strings.TrimSpace(scope.UserID)
	return s.Reject(request)
}

func (s *Service) DTMFScoped(scope AccessScope, callID, lease, digit string) error {
	owner, err := s.controlOwner(scope, callID)
	if err != nil {
		return err
	}
	return s.DTMF(owner, callID, lease, digit)
}

func (s *Service) HoldScoped(ctx context.Context, scope AccessScope, callID, lease string) error {
	owner, err := s.controlOwner(scope, callID)
	if err != nil {
		return err
	}
	return s.Hold(ctx, owner, callID, lease)
}

func (s *Service) ResumeScoped(ctx context.Context, scope AccessScope, callID, lease string) error {
	owner, err := s.controlOwner(scope, callID)
	if err != nil {
		return err
	}
	return s.Resume(ctx, owner, callID, lease)
}

func (s *Service) SwitchScoped(scope AccessScope, callID, lease string) error {
	owner, err := s.controlOwner(scope, callID)
	if err != nil {
		return err
	}
	return s.Switch(owner, callID, lease)
}

func (s *Service) HangupScoped(ctx context.Context, scope AccessScope, callID, lease string) error {
	owner, err := s.controlOwner(scope, callID)
	if err != nil {
		return err
	}
	return s.Hangup(ctx, owner, callID, lease)
}

func (s *Service) RefreshMediaScoped(scope AccessScope, request RefreshRequest) (CallView, string, error) {
	if err := s.authorizeCall(scope, request.CallID); err != nil {
		return CallView{}, "", err
	}
	request.Owner = strings.TrimSpace(scope.UserID)
	return s.RefreshMedia(request)
}

func (s *Service) HistoryScoped(ctx context.Context, scope AccessScope, limit int) ([]CallRecord, error) {
	if err := scope.validate(); err != nil {
		return nil, err
	}
	if scope.Administrator {
		return s.History(ctx, limit)
	}
	if s.store == nil {
		return []CallRecord{}, nil
	}
	store, ok := s.store.(scopedHistoryStore)
	if !ok {
		return nil, ErrScopedStoreUnavailable
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	return store.ListByUser(ctx, strings.TrimSpace(scope.UserID), limit)
}

func (s *Service) DeleteHistoryScoped(ctx context.Context, scope AccessScope, ids []uint64) (int64, error) {
	if err := scope.validate(); err != nil {
		return 0, err
	}
	if scope.Administrator {
		return s.DeleteHistory(ctx, ids)
	}
	ids = uniqueHistoryIDs(ids)
	if len(ids) == 0 {
		return 0, errEmptyHistorySelection
	}
	if s.store == nil {
		return 0, errHistoryStoreUnavailable
	}
	store, ok := s.store.(scopedHistoryStore)
	if !ok {
		return 0, ErrScopedStoreUnavailable
	}

	s.recordMu.Lock()
	defer s.recordMu.Unlock()
	records, err := store.DeleteByUser(ctx, strings.TrimSpace(scope.UserID), ids)
	if err != nil {
		return 0, err
	}
	s.rememberDeletedRecords(records)
	return int64(len(records)), nil
}

func (s *Service) AuthorizeRecording(ctx context.Context, scope AccessScope, recordingName string) error {
	if err := scope.validate(); err != nil {
		return err
	}
	recordingName = strings.TrimSpace(recordingName)
	if recordingName == "" || s.store == nil {
		return ErrRecordingNotFound
	}
	store, ok := s.store.(scopedRecordingStore)
	if !ok {
		return ErrScopedStoreUnavailable
	}
	userID := strings.TrimSpace(scope.UserID)
	if scope.Administrator {
		userID = ""
	}
	_, found, err := store.FindRecording(ctx, recordingName, userID)
	if err != nil {
		return err
	}
	if !found {
		return ErrRecordingNotFound
	}
	return nil
}

func (s *Service) SubscribeScoped(scope AccessScope, afterID uint64) ([]Event, <-chan Event, func(), error) {
	if err := scope.validate(); err != nil {
		return nil, nil, nil, err
	}
	userID := strings.TrimSpace(scope.UserID)
	backlog, stream, cancel := s.events.subscribeFiltered(afterID, func(event Event) bool {
		return scope.Administrator || event.Call.UserID == userID
	})
	return backlog, stream, cancel, nil
}

func (s *Service) authorizeCall(scope AccessScope, callID string) error {
	if err := scope.validate(); err != nil {
		return err
	}
	s.mu.RLock()
	call := s.calls[strings.TrimSpace(callID)]
	allowed := call != nil && !call.terminal && (scope.Administrator || call.view.UserID == strings.TrimSpace(scope.UserID))
	s.mu.RUnlock()
	if !allowed {
		return ErrAccessDenied
	}
	return nil
}

func (s *Service) controlOwner(scope AccessScope, callID string) (string, error) {
	if err := scope.validate(); err != nil {
		return "", err
	}
	userID := strings.TrimSpace(scope.UserID)
	s.mu.RLock()
	call := s.calls[strings.TrimSpace(callID)]
	if call == nil || call.terminal || (!scope.Administrator && call.view.UserID != userID) {
		s.mu.RUnlock()
		return "", ErrAccessDenied
	}
	owner := userID
	if scope.Administrator && strings.TrimSpace(call.owner) != "" {
		owner = call.owner
	}
	s.mu.RUnlock()
	return owner, nil
}
