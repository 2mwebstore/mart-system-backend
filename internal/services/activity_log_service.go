package services

import (
	"encoding/json"

	"com-mart/backend/internal/models"
	"com-mart/backend/internal/repositories"
)

type ActivityLogService struct {
	repo *repositories.ActivityLogRepository
}

func NewActivityLogService(repo *repositories.ActivityLogRepository) *ActivityLogService {
	return &ActivityLogService{repo: repo}
}

// LogEntry describes one audit-trail write. Before/After are arbitrary
// structs/maps marshalled to JSON; nil is fine for create-only actions.
type LogEntry struct {
	UserID     uint64
	Role       string
	BranchID   *uint64
	Module     string
	Action     string
	EntityType string
	EntityID   *uint64
	Before     interface{}
	After      interface{}
	IP         string
	Device     string
}

// Log writes an activity_logs row. Called explicitly from services/
// handlers that mutate sensitive state (users, roles, settings, inventory,
// sales voids/refunds, ...) rather than from generic middleware, because a
// meaningful before/after diff needs to know the entity's shape.
func (s *ActivityLogService) Log(entry LogEntry) error {
	beforeJSON, err := marshalOrEmpty(entry.Before)
	if err != nil {
		return err
	}
	afterJSON, err := marshalOrEmpty(entry.After)
	if err != nil {
		return err
	}

	return s.repo.Create(&models.ActivityLog{
		UserID:     entry.UserID,
		Role:       entry.Role,
		BranchID:   entry.BranchID,
		Module:     entry.Module,
		Action:     entry.Action,
		EntityType: entry.EntityType,
		EntityID:   entry.EntityID,
		BeforeJSON: beforeJSON,
		AfterJSON:  afterJSON,
		IP:         entry.IP,
		Device:     entry.Device,
	})
}

func marshalOrEmpty(v interface{}) (string, error) {
	if v == nil {
		return "", nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
