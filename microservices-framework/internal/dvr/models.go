package dvr

import (
	"encoding/json"
	"errors"
	"time"

	dvrv1 "github.com/tvheadend/api/dvr/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var (
	ErrRecordingNotFound = errors.New("recording not found")
	ErrInvalidStatus     = errors.New("invalid recording status")
)

// RecordingStatus represents the state of a recording
type RecordingStatus int

const (
	RecordingStatusScheduled RecordingStatus = iota + 1
	RecordingStatusWaiting
	RecordingStatusRecording
	RecordingStatusPostProcessing
	RecordingStatusCompleted
	RecordingStatusFailed
	RecordingStatusCancelled
	RecordingStatusMissed
)

// Recording represents a DVR recording
type Recording struct {
	ID              string          `gorm:"primaryKey"`
	ChannelID       string          `gorm:"index:idx_channel"`
	ProgrammeID     string          `gorm:"index:idx_programme"`
	Title           string
	Subtitle        string
	Description     string
	StartTime       time.Time       `gorm:"index:idx_start_time"`
	EndTime         time.Time
	ActualStartTime time.Time
	ActualEndTime   time.Time
	Status          RecordingStatus `gorm:"index:idx_status"`
	FilePath        string
	FileSize        int64
	Priority        int
	PaddingStart    int
	PaddingStop     int
	AutoRecID       string
	Error           string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// TableName returns the table name for GORM
func (Recording) TableName() string {
	return "recordings"
}

// ToProto converts to protobuf message
func (r *Recording) ToProto() *dvrv1.Recording {
	rec := &dvrv1.Recording{
		Id:           r.ID,
		ChannelId:    r.ChannelID,
		ProgrammeId:  r.ProgrammeID,
		Title:        r.Title,
		Subtitle:     r.Subtitle,
		Description:  r.Description,
		StartTime:    timestamppb.New(r.StartTime),
		EndTime:      timestamppb.New(r.EndTime),
		Status:       dvrv1.RecordingStatus(r.Status),
		FilePath:     r.FilePath,
		FileSize:     r.FileSize,
		Priority:     int32(r.Priority),
		PaddingStart: int32(r.PaddingStart),
		PaddingStop:  int32(r.PaddingStop),
		AutorecId:    r.AutoRecID,
		Error:        r.Error,
		CreatedAt:    timestamppb.New(r.CreatedAt),
		UpdatedAt:    timestamppb.New(r.UpdatedAt),
	}

	if !r.ActualStartTime.IsZero() {
		rec.ActualStartTime = timestamppb.New(r.ActualStartTime)
	}
	if !r.ActualEndTime.IsZero() {
		rec.ActualEndTime = timestamppb.New(r.ActualEndTime)
	}

	return rec
}

// AutoRecRule represents an automatic recording rule
type AutoRecRule struct {
	ID                  string                   `gorm:"primaryKey"`
	Name                string                   `gorm:"index:idx_name"`
	TitlePattern        string
	ChannelID           string
	TagID               string
	Genre               string
	Enabled             bool                     `gorm:"default:true"`
	Priority            int
	PaddingStart        int
	PaddingStop         int
	DuplicateDetection  dvrv1.DuplicateDetection
	DVRConfigID         string
	RecordingsCount     int
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// TableName returns the table name for GORM
func (AutoRecRule) TableName() string {
	return "autorec_rules"
}

// ToProto converts to protobuf message
func (a *AutoRecRule) ToProto() *dvrv1.AutoRecRule {
	return &dvrv1.AutoRecRule{
		Id:                 a.ID,
		Name:               a.Name,
		TitlePattern:       a.TitlePattern,
		ChannelId:          a.ChannelID,
		TagId:              a.TagID,
		Genre:              a.Genre,
		Enabled:            a.Enabled,
		Priority:           int32(a.Priority),
		PaddingStart:       int32(a.PaddingStart),
		PaddingStop:        int32(a.PaddingStop),
		DuplicateDetection: a.DuplicateDetection,
		DvrConfigId:        a.DVRConfigID,
		CreatedAt:          timestamppb.New(a.CreatedAt),
		RecordingsCount:    int32(a.RecordingsCount),
	}
}

// RecordingFilter contains filter criteria for listing recordings
type RecordingFilter struct {
	Status          []RecordingStatus
	ChannelID       string
	StartTimeAfter  *time.Time
	StartTimeBefore *time.Time
	PageSize        int
	PageToken       string
	OrderBy         string
}

// RecordingEvent represents a recording event for pub/sub
type RecordingEvent struct {
	Type        string     `json:"type"`
	RecordingID string     `json:"recording_id"`
	Recording   *Recording `json:"recording"`
	Timestamp   time.Time  `json:"timestamp"`
}

// MarshalJSON implements json.Marshaler
func (e *RecordingEvent) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type        string     `json:"type"`
		RecordingID string     `json:"recording_id"`
		Recording   *Recording `json:"recording"`
		Timestamp   string     `json:"timestamp"`
	}{
		Type:        e.Type,
		RecordingID: e.RecordingID,
		Recording:   e.Recording,
		Timestamp:   e.Timestamp.Format(time.RFC3339),
	})
}
