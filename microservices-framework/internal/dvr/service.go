package dvr

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/robfig/cron/v3"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	dvrv1 "github.com/tvheadend/api/dvr/v1"
)

var tracer = otel.Tracer("dvr-service")

// Service implements the DVR service
type Service struct {
	dvrv1.UnimplementedDVRServiceServer

	repo      Repository
	nats      nats.JetStreamContext
	logger    *zap.Logger
	scheduler *cron.Cron
	events    chan *RecordingEvent
}

// NewService creates a new DVR service
func NewService(repo Repository, js nats.JetStreamContext, logger *zap.Logger) *Service {
	return &Service{
		repo:      repo,
		nats:      js,
		logger:    logger,
		scheduler: cron.New(cron.WithSeconds()),
		events:    make(chan *RecordingEvent, 100),
	}
}

// ScheduleRecording schedules a new recording
func (s *Service) ScheduleRecording(ctx context.Context, req *dvrv1.ScheduleRecordingRequest) (*dvrv1.Recording, error) {
	ctx, span := tracer.Start(ctx, "ScheduleRecording")
	defer span.End()

	// Validate request
	if req.ChannelId == "" {
		return nil, status.Error(codes.InvalidArgument, "channel_id is required")
	}

	// Create recording entity
	rec := &Recording{
		ID:           uuid.New().String(),
		ChannelID:    req.ChannelId,
		ProgrammeID:  req.ProgrammeId,
		StartTime:    req.StartTime.AsTime(),
		EndTime:      req.EndTime.AsTime(),
		PaddingStart: int(req.PaddingStart),
		PaddingStop:  int(req.PaddingStop),
		Priority:     int(req.Priority),
		Status:       RecordingStatusScheduled,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}

	// Save to database
	if err := s.repo.CreateRecording(ctx, rec); err != nil {
		s.logger.Error("failed to create recording", zap.Error(err))
		return nil, status.Error(codes.Internal, "failed to create recording")
	}

	// Schedule the recording
	if err := s.scheduleRecording(rec); err != nil {
		s.logger.Error("failed to schedule recording", zap.Error(err))
		// Recording is saved, but scheduling failed - will be picked up by scheduler
	}

	// Publish event
	if err := s.publishEvent(ctx, "recording.scheduled", rec); err != nil {
		s.logger.Warn("failed to publish event", zap.Error(err))
	}

	s.logger.Info("recording scheduled",
		zap.String("recording_id", rec.ID),
		zap.String("channel_id", rec.ChannelID),
		zap.Time("start_time", rec.StartTime),
	)

	span.SetAttributes(
		attribute.String("recording.id", rec.ID),
		attribute.String("channel.id", rec.ChannelID),
	)

	return rec.ToProto(), nil
}

// ListRecordings lists recordings with filters
func (s *Service) ListRecordings(ctx context.Context, req *dvrv1.ListRecordingsRequest) (*dvrv1.ListRecordingsResponse, error) {
	ctx, span := tracer.Start(ctx, "ListRecordings")
	defer span.End()

	// Build filter
	filter := &RecordingFilter{
		Status:          statusFromProto(req.Status),
		ChannelID:       req.ChannelId,
		StartTimeAfter:  timeFromProto(req.StartTimeAfter),
		StartTimeBefore: timeFromProto(req.StartTimeBefore),
		PageSize:        int(req.PageSize),
		PageToken:       req.PageToken,
		OrderBy:         req.OrderBy,
	}

	// Query database
	recordings, nextToken, total, err := s.repo.ListRecordings(ctx, filter)
	if err != nil {
		s.logger.Error("failed to list recordings", zap.Error(err))
		return nil, status.Error(codes.Internal, "failed to list recordings")
	}

	// Convert to proto
	pbRecordings := make([]*dvrv1.Recording, len(recordings))
	for i, rec := range recordings {
		pbRecordings[i] = rec.ToProto()
	}

	return &dvrv1.ListRecordingsResponse{
		Recordings:    pbRecordings,
		NextPageToken: nextToken,
		TotalCount:    int32(total),
	}, nil
}

// GetRecording retrieves a specific recording
func (s *Service) GetRecording(ctx context.Context, req *dvrv1.GetRecordingRequest) (*dvrv1.Recording, error) {
	ctx, span := tracer.Start(ctx, "GetRecording")
	defer span.End()

	if req.Id == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}

	rec, err := s.repo.GetRecording(ctx, req.Id)
	if err != nil {
		if err == ErrRecordingNotFound {
			return nil, status.Error(codes.NotFound, "recording not found")
		}
		s.logger.Error("failed to get recording", zap.Error(err))
		return nil, status.Error(codes.Internal, "failed to get recording")
	}

	span.SetAttributes(attribute.String("recording.id", rec.ID))

	return rec.ToProto(), nil
}

// DeleteRecording deletes a recording
func (s *Service) DeleteRecording(ctx context.Context, req *dvrv1.DeleteRecordingRequest) (*emptypb.Empty, error) {
	ctx, span := tracer.Start(ctx, "DeleteRecording")
	defer span.End()

	if req.Id == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}

	// Get recording first
	rec, err := s.repo.GetRecording(ctx, req.Id)
	if err != nil {
		if err == ErrRecordingNotFound {
			return nil, status.Error(codes.NotFound, "recording not found")
		}
		return nil, status.Error(codes.Internal, "failed to get recording")
	}

	// Cancel if recording is in progress
	if rec.Status == RecordingStatusRecording {
		if err := s.stopRecording(ctx, rec); err != nil {
			s.logger.Error("failed to stop recording", zap.Error(err))
		}
	}

	// Delete from database
	if err := s.repo.DeleteRecording(ctx, req.Id); err != nil {
		s.logger.Error("failed to delete recording", zap.Error(err))
		return nil, status.Error(codes.Internal, "failed to delete recording")
	}

	// Delete file if requested
	if req.DeleteFile && rec.FilePath != "" {
		if err := s.deleteRecordingFile(rec.FilePath); err != nil {
			s.logger.Warn("failed to delete recording file",
				zap.String("path", rec.FilePath),
				zap.Error(err),
			)
		}
	}

	// Publish event
	if err := s.publishEvent(ctx, "recording.deleted", rec); err != nil {
		s.logger.Warn("failed to publish event", zap.Error(err))
	}

	s.logger.Info("recording deleted", zap.String("recording_id", rec.ID))

	return &emptypb.Empty{}, nil
}

// CancelRecording cancels a scheduled/in-progress recording
func (s *Service) CancelRecording(ctx context.Context, req *dvrv1.CancelRecordingRequest) (*dvrv1.Recording, error) {
	ctx, span := tracer.Start(ctx, "CancelRecording")
	defer span.End()

	if req.Id == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}

	rec, err := s.repo.GetRecording(ctx, req.Id)
	if err != nil {
		if err == ErrRecordingNotFound {
			return nil, status.Error(codes.NotFound, "recording not found")
		}
		return nil, status.Error(codes.Internal, "failed to get recording")
	}

	// Check if cancellable
	if rec.Status != RecordingStatusScheduled && rec.Status != RecordingStatusRecording {
		return nil, status.Error(codes.FailedPrecondition, "recording cannot be cancelled")
	}

	// Stop if recording
	if rec.Status == RecordingStatusRecording {
		if err := s.stopRecording(ctx, rec); err != nil {
			s.logger.Error("failed to stop recording", zap.Error(err))
		}
	}

	// Update status
	rec.Status = RecordingStatusCancelled
	rec.UpdatedAt = time.Now()

	if err := s.repo.UpdateRecording(ctx, rec); err != nil {
		return nil, status.Error(codes.Internal, "failed to update recording")
	}

	// Publish event
	if err := s.publishEvent(ctx, "recording.cancelled", rec); err != nil {
		s.logger.Warn("failed to publish event", zap.Error(err))
	}

	s.logger.Info("recording cancelled", zap.String("recording_id", rec.ID))

	return rec.ToProto(), nil
}

// CreateAutoRecRule creates an automatic recording rule
func (s *Service) CreateAutoRecRule(ctx context.Context, req *dvrv1.CreateAutoRecRuleRequest) (*dvrv1.AutoRecRule, error) {
	// Implementation similar to ScheduleRecording
	return nil, status.Error(codes.Unimplemented, "not implemented yet")
}

// ListAutoRecRules lists automatic recording rules
func (s *Service) ListAutoRecRules(ctx context.Context, req *dvrv1.ListAutoRecRulesRequest) (*dvrv1.ListAutoRecRulesResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented yet")
}

// DeleteAutoRecRule deletes an automatic recording rule
func (s *Service) DeleteAutoRecRule(ctx context.Context, req *dvrv1.DeleteAutoRecRuleRequest) (*emptypb.Empty, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented yet")
}

// WatchRecordings streams recording events
func (s *Service) WatchRecordings(req *dvrv1.WatchRecordingsRequest, stream dvrv1.DVRService_WatchRecordingsServer) error {
	// Subscribe to events and stream to client
	return status.Error(codes.Unimplemented, "not implemented yet")
}

// StartScheduler starts the recording scheduler
func (s *Service) StartScheduler(ctx context.Context) {
	s.logger.Info("starting recording scheduler")

	// Schedule check every minute
	s.scheduler.AddFunc("@every 1m", func() {
		s.checkScheduledRecordings(ctx)
	})

	s.scheduler.Start()

	<-ctx.Done()
	s.scheduler.Stop()
	s.logger.Info("recording scheduler stopped")
}

// ProcessRecordings processes active recordings
func (s *Service) ProcessRecordings(ctx context.Context) {
	s.logger.Info("starting recording processor")

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.logger.Info("recording processor stopped")
			return
		case <-ticker.C:
			s.checkActiveRecordings(ctx)
		}
	}
}

// Helper methods

func (s *Service) scheduleRecording(rec *Recording) error {
	// Schedule recording start
	_, err := s.scheduler.AddFunc(rec.StartTime.Format("05 04 15 02 01 *"), func() {
		s.startRecording(context.Background(), rec)
	})
	return err
}

func (s *Service) startRecording(ctx context.Context, rec *Recording) {
	s.logger.Info("starting recording", zap.String("recording_id", rec.ID))

	rec.Status = RecordingStatusRecording
	rec.ActualStartTime = time.Now()
	rec.UpdatedAt = time.Now()

	if err := s.repo.UpdateRecording(ctx, rec); err != nil {
		s.logger.Error("failed to update recording status", zap.Error(err))
		return
	}

	// Publish event
	if err := s.publishEvent(ctx, "recording.started", rec); err != nil {
		s.logger.Warn("failed to publish event", zap.Error(err))
	}

	// TODO: Start actual recording process
}

func (s *Service) stopRecording(ctx context.Context, rec *Recording) error {
	s.logger.Info("stopping recording", zap.String("recording_id", rec.ID))

	// TODO: Stop actual recording process

	return nil
}

func (s *Service) checkScheduledRecordings(ctx context.Context) {
	// Check for recordings that should start soon
	// Implementation...
}

func (s *Service) checkActiveRecordings(ctx context.Context) {
	// Check recordings that should stop
	// Implementation...
}

func (s *Service) deleteRecordingFile(path string) error {
	// Delete file from storage
	// Implementation...
	return nil
}

func (s *Service) publishEvent(ctx context.Context, eventType string, rec *Recording) error {
	event := &RecordingEvent{
		Type:        eventType,
		RecordingID: rec.ID,
		Recording:   rec,
		Timestamp:   time.Now(),
	}

	// Publish to NATS
	subject := fmt.Sprintf("dvr.%s", eventType)
	data, err := event.MarshalJSON()
	if err != nil {
		return err
	}

	_, err = s.nats.Publish(subject, data)
	return err
}

// Utility functions

func statusFromProto(statuses []dvrv1.RecordingStatus) []RecordingStatus {
	result := make([]RecordingStatus, len(statuses))
	for i, s := range statuses {
		result[i] = RecordingStatus(s)
	}
	return result
}

func timeFromProto(t *timestamppb.Timestamp) *time.Time {
	if t == nil {
		return nil
	}
	ts := t.AsTime()
	return &ts
}
