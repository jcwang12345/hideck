package resourcecatalog

import (
	"context"
	"errors"
	"strings"
	"time"
)

// RecordAudit appends an administrative or orchestration event without
// exposing the catalog's transaction internals to API callers.
func (s *Service) RecordAudit(ctx context.Context, actorID, action, resourceType, resourceID string, payload any) error {
	if s == nil || s.database == nil {
		return errors.New("resource catalog database is nil")
	}
	action = strings.TrimSpace(action)
	resourceType = strings.TrimSpace(resourceType)
	resourceID = strings.TrimSpace(resourceID)
	if action == "" || resourceType == "" || resourceID == "" {
		return errors.New("resource catalog: audit action and target are required")
	}
	return createAudit(s.database.WithContext(ctx), strings.TrimSpace(actorID), action, resourceType, resourceID, payload, time.Now().UTC())
}
