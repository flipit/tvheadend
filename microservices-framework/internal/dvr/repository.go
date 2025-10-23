package dvr

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"gorm.io/gorm"
)

// Repository interface for DVR data access
type Repository interface {
	// Recordings
	CreateRecording(ctx context.Context, rec *Recording) error
	GetRecording(ctx context.Context, id string) (*Recording, error)
	UpdateRecording(ctx context.Context, rec *Recording) error
	DeleteRecording(ctx context.Context, id string) error
	ListRecordings(ctx context.Context, filter *RecordingFilter) ([]*Recording, string, int, error)

	// AutoRec Rules
	CreateAutoRecRule(ctx context.Context, rule *AutoRecRule) error
	GetAutoRecRule(ctx context.Context, id string) (*AutoRecRule, error)
	UpdateAutoRecRule(ctx context.Context, rule *AutoRecRule) error
	DeleteAutoRecRule(ctx context.Context, id string) error
	ListAutoRecRules(ctx context.Context, enabledOnly bool, pageSize int, pageToken string) ([]*AutoRecRule, string, int, error)
}

// repository implements Repository using GORM
type repository struct {
	db *gorm.DB
}

// NewRepository creates a new repository
func NewRepository(db *gorm.DB) Repository {
	return &repository{db: db}
}

// CreateRecording creates a new recording
func (r *repository) CreateRecording(ctx context.Context, rec *Recording) error {
	return r.db.WithContext(ctx).Create(rec).Error
}

// GetRecording retrieves a recording by ID
func (r *repository) GetRecording(ctx context.Context, id string) (*Recording, error) {
	var rec Recording
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&rec).Error
	if err == gorm.ErrRecordNotFound {
		return nil, ErrRecordingNotFound
	}
	return &rec, err
}

// UpdateRecording updates a recording
func (r *repository) UpdateRecording(ctx context.Context, rec *Recording) error {
	return r.db.WithContext(ctx).Save(rec).Error
}

// DeleteRecording deletes a recording
func (r *repository) DeleteRecording(ctx context.Context, id string) error {
	result := r.db.WithContext(ctx).Where("id = ?", id).Delete(&Recording{})
	if result.RowsAffected == 0 {
		return ErrRecordingNotFound
	}
	return result.Error
}

// ListRecordings lists recordings with filters and pagination
func (r *repository) ListRecordings(ctx context.Context, filter *RecordingFilter) ([]*Recording, string, int, error) {
	query := r.db.WithContext(ctx).Model(&Recording{})

	// Apply filters
	if len(filter.Status) > 0 {
		query = query.Where("status IN ?", filter.Status)
	}
	if filter.ChannelID != "" {
		query = query.Where("channel_id = ?", filter.ChannelID)
	}
	if filter.StartTimeAfter != nil {
		query = query.Where("start_time >= ?", *filter.StartTimeAfter)
	}
	if filter.StartTimeBefore != nil {
		query = query.Where("start_time <= ?", *filter.StartTimeBefore)
	}

	// Count total
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, "", 0, err
	}

	// Apply ordering
	orderBy := "start_time DESC"
	if filter.OrderBy != "" {
		orderBy = filter.OrderBy
	}
	query = query.Order(orderBy)

	// Apply pagination
	pageSize := filter.PageSize
	if pageSize == 0 {
		pageSize = 50
	}
	if pageSize > 100 {
		pageSize = 100
	}

	// Decode page token
	offset := 0
	if filter.PageToken != "" {
		offset = decodePageToken(filter.PageToken)
	}

	query = query.Limit(pageSize).Offset(offset)

	// Execute query
	var recordings []*Recording
	if err := query.Find(&recordings).Error; err != nil {
		return nil, "", 0, err
	}

	// Generate next page token
	nextToken := ""
	if offset+len(recordings) < int(total) {
		nextToken = encodePageToken(offset + pageSize)
	}

	return recordings, nextToken, int(total), nil
}

// CreateAutoRecRule creates a new autorec rule
func (r *repository) CreateAutoRecRule(ctx context.Context, rule *AutoRecRule) error {
	return r.db.WithContext(ctx).Create(rule).Error
}

// GetAutoRecRule retrieves an autorec rule by ID
func (r *repository) GetAutoRecRule(ctx context.Context, id string) (*AutoRecRule, error) {
	var rule AutoRecRule
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&rule).Error
	if err == gorm.ErrRecordNotFound {
		return nil, fmt.Errorf("autorec rule not found")
	}
	return &rule, err
}

// UpdateAutoRecRule updates an autorec rule
func (r *repository) UpdateAutoRecRule(ctx context.Context, rule *AutoRecRule) error {
	return r.db.WithContext(ctx).Save(rule).Error
}

// DeleteAutoRecRule deletes an autorec rule
func (r *repository) DeleteAutoRecRule(ctx context.Context, id string) error {
	result := r.db.WithContext(ctx).Where("id = ?", id).Delete(&AutoRecRule{})
	if result.RowsAffected == 0 {
		return fmt.Errorf("autorec rule not found")
	}
	return result.Error
}

// ListAutoRecRules lists autorec rules
func (r *repository) ListAutoRecRules(ctx context.Context, enabledOnly bool, pageSize int, pageToken string) ([]*AutoRecRule, string, int, error) {
	query := r.db.WithContext(ctx).Model(&AutoRecRule{})

	if enabledOnly {
		query = query.Where("enabled = ?", true)
	}

	// Count total
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, "", 0, err
	}

	// Pagination
	if pageSize == 0 {
		pageSize = 50
	}
	if pageSize > 100 {
		pageSize = 100
	}

	offset := 0
	if pageToken != "" {
		offset = decodePageToken(pageToken)
	}

	query = query.Order("created_at DESC").Limit(pageSize).Offset(offset)

	var rules []*AutoRecRule
	if err := query.Find(&rules).Error; err != nil {
		return nil, "", 0, err
	}

	// Next page token
	nextToken := ""
	if offset+len(rules) < int(total) {
		nextToken = encodePageToken(offset + pageSize)
	}

	return rules, nextToken, int(total), nil
}

// AutoMigrate runs database migrations
func AutoMigrate(db *gorm.DB) error {
	return db.AutoMigrate(
		&Recording{},
		&AutoRecRule{},
	)
}

// Pagination helpers

type pageToken struct {
	Offset int `json:"offset"`
}

func encodePageToken(offset int) string {
	token := pageToken{Offset: offset}
	data, _ := json.Marshal(token)
	return base64.StdEncoding.EncodeToString(data)
}

func decodePageToken(token string) int {
	data, err := base64.StdEncoding.DecodeString(token)
	if err != nil {
		return 0
	}

	var pt pageToken
	if err := json.Unmarshal(data, &pt); err != nil {
		return 0
	}

	return pt.Offset
}
