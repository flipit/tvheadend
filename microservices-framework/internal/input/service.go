package input

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	inputv1 "github.com/tvheadend/api/input/v1"
	"github.com/tvheadend/internal/input/adapter"
)

var tracer = otel.Tracer("input-service")

// Service implements the Input service
type Service struct {
	inputv1.UnimplementedInputServiceServer

	repo      Repository
	nats      nats.JetStreamContext
	logger    *zap.Logger
	adapters  map[string]adapter.Adapter // active adapters by input ID
	events    chan *InputEvent
}

// NewService creates a new Input service
func NewService(repo Repository, js nats.JetStreamContext, logger *zap.Logger) *Service {
	return &Service{
		repo:     repo,
		nats:     js,
		logger:   logger,
		adapters: make(map[string]adapter.Adapter),
		events:   make(chan *InputEvent, 100),
	}
}

// ListAdapters lists all available adapters
func (s *Service) ListAdapters(ctx context.Context, req *inputv1.ListAdaptersRequest) (*inputv1.ListAdaptersResponse, error) {
	ctx, span := tracer.Start(ctx, "ListAdapters")
	defer span.End()

	filter := &AdapterFilter{
		PageSize:  int(req.PageSize),
		PageToken: req.PageToken,
	}

	if req.TypeFilter != inputv1.InputType_INPUT_TYPE_UNSPECIFIED {
		inputType := fromProtoInputType(req.TypeFilter)
		filter.Type = &inputType
	}

	adapters, nextToken, total, err := s.repo.ListAdapters(ctx, filter)
	if err != nil {
		s.logger.Error("failed to list adapters", zap.Error(err))
		return nil, status.Error(codes.Internal, "failed to list adapters")
	}

	pbAdapters := make([]*inputv1.Adapter, len(adapters))
	for i, adapter := range adapters {
		pbAdapters[i] = adapter.ToProto()
	}

	return &inputv1.ListAdaptersResponse{
		Adapters:      pbAdapters,
		NextPageToken: nextToken,
		TotalCount:    int32(total),
	}, nil
}

// GetAdapter retrieves a specific adapter
func (s *Service) GetAdapter(ctx context.Context, req *inputv1.GetAdapterRequest) (*inputv1.Adapter, error) {
	ctx, span := tracer.Start(ctx, "GetAdapter")
	defer span.End()

	if req.Id == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}

	adapter, err := s.repo.GetAdapter(ctx, req.Id)
	if err != nil {
		if err == ErrAdapterNotFound {
			return nil, status.Error(codes.NotFound, "adapter not found")
		}
		s.logger.Error("failed to get adapter", zap.Error(err))
		return nil, status.Error(codes.Internal, "failed to get adapter")
	}

	span.SetAttributes(attribute.String("adapter.id", adapter.ID))

	return adapter.ToProto(), nil
}

// CreateInput creates a new input source
func (s *Service) CreateInput(ctx context.Context, req *inputv1.CreateInputRequest) (*inputv1.Input, error) {
	ctx, span := tracer.Start(ctx, "CreateInput")
	defer span.End()

	if req.Name == "" {
		return nil, status.Error(codes.InvalidArgument, "name is required")
	}

	// Marshal config to JSON based on type
	var configJSON []byte
	var err error

	switch req.Type {
	case inputv1.InputType_INPUT_TYPE_IPTV:
		if req.GetIptvConfig() == nil {
			return nil, status.Error(codes.InvalidArgument, "iptv_config is required")
		}
		configJSON, err = json.Marshal(req.GetIptvConfig())
	case inputv1.InputType_INPUT_TYPE_DVB_S, inputv1.InputType_INPUT_TYPE_DVB_S2,
		inputv1.InputType_INPUT_TYPE_DVB_T, inputv1.InputType_INPUT_TYPE_DVB_T2,
		inputv1.InputType_INPUT_TYPE_DVB_C:
		if req.GetDvbConfig() == nil {
			return nil, status.Error(codes.InvalidArgument, "dvb_config is required")
		}
		configJSON, err = json.Marshal(req.GetDvbConfig())
	case inputv1.InputType_INPUT_TYPE_SATIP:
		if req.GetSatipConfig() == nil {
			return nil, status.Error(codes.InvalidArgument, "satip_config is required")
		}
		configJSON, err = json.Marshal(req.GetSatipConfig())
	case inputv1.InputType_INPUT_TYPE_FILE:
		if req.GetFileConfig() == nil {
			return nil, status.Error(codes.InvalidArgument, "file_config is required")
		}
		configJSON, err = json.Marshal(req.GetFileConfig())
	default:
		return nil, status.Error(codes.InvalidArgument, "unsupported input type")
	}

	if err != nil {
		return nil, status.Error(codes.Internal, "failed to marshal config")
	}

	input := &Input{
		ID:        uuid.New().String(),
		Name:      req.Name,
		Type:      fromProtoInputType(req.Type),
		AdapterID: req.AdapterId,
		Enabled:   req.Enabled,
		Priority:  int(req.Priority),
		Status:    InputStatusIdle,
		Config:    configJSON,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if err := s.repo.CreateInput(ctx, input); err != nil {
		s.logger.Error("failed to create input", zap.Error(err))
		return nil, status.Error(codes.Internal, "failed to create input")
	}

	// Publish event
	if err := s.publishEvent(ctx, "input.created", input, nil); err != nil {
		s.logger.Warn("failed to publish event", zap.Error(err))
	}

	s.logger.Info("input created",
		zap.String("input_id", input.ID),
		zap.String("name", input.Name),
		zap.String("type", string(input.Type)),
	)

	span.SetAttributes(
		attribute.String("input.id", input.ID),
		attribute.String("input.type", string(input.Type)),
	)

	return input.ToProto(), nil
}

// ListInputs lists inputs with filters
func (s *Service) ListInputs(ctx context.Context, req *inputv1.ListInputsRequest) (*inputv1.ListInputsResponse, error) {
	ctx, span := tracer.Start(ctx, "ListInputs")
	defer span.End()

	filter := &InputFilter{
		EnabledOnly: req.EnabledOnly,
		PageSize:    int(req.PageSize),
		PageToken:   req.PageToken,
		OrderBy:     req.OrderBy,
	}

	if req.TypeFilter != inputv1.InputType_INPUT_TYPE_UNSPECIFIED {
		inputType := fromProtoInputType(req.TypeFilter)
		filter.Type = &inputType
	}

	if req.StatusFilter != inputv1.InputStatus_INPUT_STATUS_UNSPECIFIED {
		// Convert proto status to model status
		statusMap := map[inputv1.InputStatus]InputStatus{
			inputv1.InputStatus_INPUT_STATUS_IDLE:     InputStatusIdle,
			inputv1.InputStatus_INPUT_STATUS_TUNING:   InputStatusTuning,
			inputv1.InputStatus_INPUT_STATUS_LOCKED:   InputStatusLocked,
			inputv1.InputStatus_INPUT_STATUS_ERROR:    InputStatusError,
			inputv1.InputStatus_INPUT_STATUS_DISABLED: InputStatusDisabled,
		}
		if modelStatus, ok := statusMap[req.StatusFilter]; ok {
			filter.Status = &modelStatus
		}
	}

	inputs, nextToken, total, err := s.repo.ListInputs(ctx, filter)
	if err != nil {
		s.logger.Error("failed to list inputs", zap.Error(err))
		return nil, status.Error(codes.Internal, "failed to list inputs")
	}

	pbInputs := make([]*inputv1.Input, len(inputs))
	for i, input := range inputs {
		pbInputs[i] = input.ToProto()
	}

	return &inputv1.ListInputsResponse{
		Inputs:        pbInputs,
		NextPageToken: nextToken,
		TotalCount:    int32(total),
	}, nil
}

// GetInput retrieves a specific input
func (s *Service) GetInput(ctx context.Context, req *inputv1.GetInputRequest) (*inputv1.Input, error) {
	ctx, span := tracer.Start(ctx, "GetInput")
	defer span.End()

	if req.Id == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}

	input, err := s.repo.GetInput(ctx, req.Id)
	if err != nil {
		if err == ErrInputNotFound {
			return nil, status.Error(codes.NotFound, "input not found")
		}
		s.logger.Error("failed to get input", zap.Error(err))
		return nil, status.Error(codes.Internal, "failed to get input")
	}

	span.SetAttributes(attribute.String("input.id", input.ID))

	return input.ToProto(), nil
}

// UpdateInput updates an input
func (s *Service) UpdateInput(ctx context.Context, req *inputv1.UpdateInputRequest) (*inputv1.Input, error) {
	ctx, span := tracer.Start(ctx, "UpdateInput")
	defer span.End()

	if req.Id == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}

	input, err := s.repo.GetInput(ctx, req.Id)
	if err != nil {
		if err == ErrInputNotFound {
			return nil, status.Error(codes.NotFound, "input not found")
		}
		return nil, status.Error(codes.Internal, "failed to get input")
	}

	// Update fields
	if req.Name != nil {
		input.Name = *req.Name
	}
	if req.Enabled != nil {
		input.Enabled = *req.Enabled
	}
	if req.Priority != nil {
		input.Priority = int(*req.Priority)
	}

	// Update config if provided
	// TODO: Marshal new config based on type

	input.UpdatedAt = time.Now()

	if err := s.repo.UpdateInput(ctx, input); err != nil {
		return nil, status.Error(codes.Internal, "failed to update input")
	}

	// Publish event
	if err := s.publishEvent(ctx, "input.updated", input, nil); err != nil {
		s.logger.Warn("failed to publish event", zap.Error(err))
	}

	s.logger.Info("input updated", zap.String("input_id", input.ID))

	return input.ToProto(), nil
}

// DeleteInput deletes an input
func (s *Service) DeleteInput(ctx context.Context, req *inputv1.DeleteInputRequest) (*emptypb.Empty, error) {
	ctx, span := tracer.Start(ctx, "DeleteInput")
	defer span.End()

	if req.Id == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}

	// Get input first
	input, err := s.repo.GetInput(ctx, req.Id)
	if err != nil {
		if err == ErrInputNotFound {
			return nil, status.Error(codes.NotFound, "input not found")
		}
		return nil, status.Error(codes.Internal, "failed to get input")
	}

	// Stop adapter if running
	if adapterInst, ok := s.adapters[req.Id]; ok {
		if err := adapterInst.Stop(ctx); err != nil {
			s.logger.Error("failed to stop adapter", zap.Error(err))
		}
		delete(s.adapters, req.Id)
	}

	// Delete from database
	if err := s.repo.DeleteInput(ctx, req.Id); err != nil {
		s.logger.Error("failed to delete input", zap.Error(err))
		return nil, status.Error(codes.Internal, "failed to delete input")
	}

	// Publish event
	if err := s.publishEvent(ctx, "input.deleted", input, nil); err != nil {
		s.logger.Warn("failed to publish event", zap.Error(err))
	}

	s.logger.Info("input deleted", zap.String("input_id", input.ID))

	return &emptypb.Empty{}, nil
}

// SetInputEnabled enables or disables an input
func (s *Service) SetInputEnabled(ctx context.Context, req *inputv1.SetInputEnabledRequest) (*inputv1.Input, error) {
	ctx, span := tracer.Start(ctx, "SetInputEnabled")
	defer span.End()

	if req.Id == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}

	input, err := s.repo.GetInput(ctx, req.Id)
	if err != nil {
		if err == ErrInputNotFound {
			return nil, status.Error(codes.NotFound, "input not found")
		}
		return nil, status.Error(codes.Internal, "failed to get input")
	}

	input.Enabled = req.Enabled
	input.UpdatedAt = time.Now()

	if err := s.repo.UpdateInput(ctx, input); err != nil {
		return nil, status.Error(codes.Internal, "failed to update input")
	}

	s.logger.Info("input enabled status changed",
		zap.String("input_id", input.ID),
		zap.Bool("enabled", req.Enabled),
	)

	return input.ToProto(), nil
}

// ScanInput scans for services on an input
func (s *Service) ScanInput(req *inputv1.ScanInputRequest, stream inputv1.InputService_ScanInputServer) error {
	// TODO: Implement scanning
	return status.Error(codes.Unimplemented, "not implemented yet")
}

// ListServices lists discovered services
func (s *Service) ListServices(ctx context.Context, req *inputv1.ListServicesRequest) (*inputv1.ListServicesResponse, error) {
	// TODO: Implement service listing
	return nil, status.Error(codes.Unimplemented, "not implemented yet")
}

// GetService retrieves a specific service
func (s *Service) GetService(ctx context.Context, req *inputv1.GetServiceRequest) (*inputv1.Service, error) {
	// TODO: Implement service retrieval
	return nil, status.Error(codes.Unimplemented, "not implemented yet")
}

// ListMuxes lists muxes
func (s *Service) ListMuxes(ctx context.Context, req *inputv1.ListMuxesRequest) (*inputv1.ListMuxesResponse, error) {
	// TODO: Implement mux listing
	return nil, status.Error(codes.Unimplemented, "not implemented yet")
}

// GetMux retrieves a specific mux
func (s *Service) GetMux(ctx context.Context, req *inputv1.GetMuxRequest) (*inputv1.Mux, error) {
	// TODO: Implement mux retrieval
	return nil, status.Error(codes.Unimplemented, "not implemented yet")
}

// WatchInputs streams input events
func (s *Service) WatchInputs(req *inputv1.WatchInputsRequest, stream inputv1.InputService_WatchInputsServer) error {
	// TODO: Implement event streaming
	return status.Error(codes.Unimplemented, "not implemented yet")
}

// GetSignalQuality retrieves signal quality metrics
func (s *Service) GetSignalQuality(ctx context.Context, req *inputv1.GetSignalQualityRequest) (*inputv1.SignalQuality, error) {
	ctx, span := tracer.Start(ctx, "GetSignalQuality")
	defer span.End()

	if req.InputId == "" {
		return nil, status.Error(codes.InvalidArgument, "input_id is required")
	}

	// Check if adapter is running for this input
	adapterInst, ok := s.adapters[req.InputId]
	if !ok {
		return nil, status.Error(codes.FailedPrecondition, "input not active")
	}

	quality, err := adapterInst.GetSignalQuality()
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to get signal quality")
	}

	return &inputv1.SignalQuality{
		InputId:        req.InputId,
		SignalStrength: int32(quality.SignalStrength),
		Snr:            int32(quality.SNR),
		Ber:            quality.BER,
		Unc:            quality.UNC,
		Locked:         quality.Locked,
		Timestamp:      nil, // TODO: Add timestamp
	}, nil
}

// Helper methods

func (s *Service) publishEvent(ctx context.Context, eventType string, input *Input, quality *SignalQuality) error {
	event := &InputEvent{
		Type:          eventType,
		InputID:       input.ID,
		Input:         input,
		SignalQuality: quality,
		Timestamp:     time.Now(),
	}

	subject := fmt.Sprintf("input.%s", eventType)
	data, err := event.MarshalJSON()
	if err != nil {
		return err
	}

	_, err = s.nats.Publish(subject, data)
	return err
}
