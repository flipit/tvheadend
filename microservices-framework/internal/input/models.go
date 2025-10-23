package input

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"time"

	inputv1 "github.com/tvheadend/api/input/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var (
	ErrInputNotFound   = errors.New("input not found")
	ErrAdapterNotFound = errors.New("adapter not found")
	ErrServiceNotFound = errors.New("service not found")
	ErrMuxNotFound     = errors.New("mux not found")
)

// InputType represents the type of input source
type InputType string

const (
	InputTypeDVBS   InputType = "DVB-S"
	InputTypeDVBS2  InputType = "DVB-S2"
	InputTypeDVBT   InputType = "DVB-T"
	InputTypeDVBT2  InputType = "DVB-T2"
	InputTypeDVBC   InputType = "DVB-C"
	InputTypeATSC   InputType = "ATSC"
	InputTypeIPTV   InputType = "IPTV"
	InputTypeSATIP  InputType = "SAT>IP"
	InputTypeFile   InputType = "FILE"
)

// InputStatus represents the operational status
type InputStatus string

const (
	InputStatusIdle     InputStatus = "IDLE"
	InputStatusTuning   InputStatus = "TUNING"
	InputStatusLocked   InputStatus = "LOCKED"
	InputStatusError    InputStatus = "ERROR"
	InputStatusDisabled InputStatus = "DISABLED"
)

// Adapter represents a hardware or virtual input adapter
type Adapter struct {
	ID           string          `gorm:"primaryKey"`
	Name         string          `gorm:"index:idx_name"`
	Type         InputType       `gorm:"index:idx_type"`
	DevicePath   string          `gorm:"unique"`
	Capabilities json.RawMessage `gorm:"type:jsonb"`
	Status       InputStatus     `gorm:"index:idx_status"`
	ActiveInputs int             `gorm:"default:0"`
	HardwareInfo json.RawMessage `gorm:"type:jsonb"`
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// TableName returns the table name for GORM
func (Adapter) TableName() string {
	return "adapters"
}

// ToProto converts to protobuf message
func (a *Adapter) ToProto() *inputv1.Adapter {
	adapter := &inputv1.Adapter{
		Id:           a.ID,
		Name:         a.Name,
		Type:         toProtoInputType(a.Type),
		DevicePath:   a.DevicePath,
		Status:       toProtoInputStatus(a.Status),
		ActiveInputs: int32(a.ActiveInputs),
		CreatedAt:    timestamppb.New(a.CreatedAt),
		UpdatedAt:    timestamppb.New(a.UpdatedAt),
	}

	// TODO: Parse Capabilities and HardwareInfo from JSON

	return adapter
}

// Input represents a configured input source
type Input struct {
	ID         string          `gorm:"primaryKey"`
	Name       string          `gorm:"index:idx_name"`
	Type       InputType       `gorm:"index:idx_type"`
	AdapterID  string          `gorm:"index:idx_adapter"`
	Enabled    bool            `gorm:"default:true"`
	Priority   int             `gorm:"default:50"`
	Status     InputStatus     `gorm:"index:idx_status"`
	Config     json.RawMessage `gorm:"type:jsonb"`
	Statistics json.RawMessage `gorm:"type:jsonb"`
	Error      string
	CreatedAt  time.Time
	UpdatedAt  time.Time

	// Relations
	Adapter *Adapter `gorm:"foreignKey:AdapterID"`
}

// TableName returns the table name for GORM
func (Input) TableName() string {
	return "inputs"
}

// ToProto converts to protobuf message
func (i *Input) ToProto() *inputv1.Input {
	input := &inputv1.Input{
		Id:        i.ID,
		Name:      i.Name,
		Type:      toProtoInputType(i.Type),
		AdapterId: i.AdapterID,
		Enabled:   i.Enabled,
		Priority:  int32(i.Priority),
		Status:    toProtoInputStatus(i.Status),
		Error:     i.Error,
		CreatedAt: timestamppb.New(i.CreatedAt),
		UpdatedAt: timestamppb.New(i.UpdatedAt),
	}

	// TODO: Parse Config and Statistics from JSON based on input type

	return input
}

// Mux represents a transport stream multiplex
type Mux struct {
	ID                  string     `gorm:"primaryKey"`
	InputID             string     `gorm:"index:idx_input"`
	Frequency           int64      `gorm:"index:idx_frequency"`
	NetworkID           int        `gorm:"index:idx_network_id"`
	TransportStreamID   int        `gorm:"index:idx_tsid"`
	OriginalNetworkID   int
	Name                string
	Enabled             bool       `gorm:"default:true"`
	ScanStatus          ScanStatus `gorm:"index:idx_scan_status"`
	ServiceCount        int        `gorm:"default:0"`
	CreatedAt           time.Time
	UpdatedAt           time.Time

	// Relations
	Input    *Input     `gorm:"foreignKey:InputID"`
	Services []*Service `gorm:"foreignKey:MuxID"`
}

// TableName returns the table name for GORM
func (Mux) TableName() string {
	return "muxes"
}

// ToProto converts to protobuf message
func (m *Mux) ToProto() *inputv1.Mux {
	return &inputv1.Mux{
		Id:                  m.ID,
		InputId:             m.InputID,
		Frequency:           m.Frequency,
		NetworkId:           int32(m.NetworkID),
		TransportStreamId:   int32(m.TransportStreamID),
		OriginalNetworkId:   int32(m.OriginalNetworkID),
		Name:                m.Name,
		Enabled:             m.Enabled,
		ScanStatus:          toProtoScanStatus(m.ScanStatus),
		ServiceCount:        int32(m.ServiceCount),
		CreatedAt:           timestamppb.New(m.CreatedAt),
		UpdatedAt:           timestamppb.New(m.UpdatedAt),
	}
}

// ScanStatus represents the scanning state
type ScanStatus string

const (
	ScanStatusPending   ScanStatus = "PENDING"
	ScanStatusScanning  ScanStatus = "SCANNING"
	ScanStatusCompleted ScanStatus = "COMPLETED"
	ScanStatusFailed    ScanStatus = "FAILED"
)

// Service represents a discovered service/program
type Service struct {
	ID        string      `gorm:"primaryKey"`
	MuxID     string      `gorm:"index:idx_mux"`
	ServiceID int         `gorm:"index:idx_service_id"`
	Name      string      `gorm:"index:idx_name"`
	Provider  string
	Type      ServiceType `gorm:"index:idx_type"`
	Enabled   bool        `gorm:"default:true"`
	PIDs      json.RawMessage `gorm:"type:jsonb"`
	Encrypted bool        `gorm:"index:idx_encrypted"`
	CASystems json.RawMessage `gorm:"type:jsonb"` // Array of CA system IDs
	CreatedAt time.Time
	UpdatedAt time.Time

	// Relations
	Mux *Mux `gorm:"foreignKey:MuxID"`
}

// TableName returns the table name for GORM
func (Service) TableName() string {
	return "services"
}

// ToProto converts to protobuf message
func (s *Service) ToProto() *inputv1.Service {
	service := &inputv1.Service{
		Id:        s.ID,
		MuxId:     s.MuxID,
		ServiceId: int32(s.ServiceID),
		Name:      s.Name,
		Provider:  s.Provider,
		Type:      toProtoServiceType(s.Type),
		Enabled:   s.Enabled,
		Encrypted: s.Encrypted,
		CreatedAt: timestamppb.New(s.CreatedAt),
		UpdatedAt: timestamppb.New(s.UpdatedAt),
	}

	// TODO: Parse PIDs and CASystems from JSON

	return service
}

// ServiceType indicates the type of service
type ServiceType string

const (
	ServiceTypeTV    ServiceType = "TV"
	ServiceTypeRadio ServiceType = "RADIO"
	ServiceTypeSDTV  ServiceType = "SDTV"
	ServiceTypeHDTV  ServiceType = "HDTV"
	ServiceTypeUHDTV ServiceType = "UHDTV"
	ServiceTypeData  ServiceType = "DATA"
)

// SignalQuality represents signal quality metrics
type SignalQuality struct {
	InputID        string
	SignalStrength int
	SNR            int
	BER            int64
	UNC            int64
	Locked         bool
	Timestamp      time.Time
}

// ToProto converts to protobuf message
func (sq *SignalQuality) ToProto() *inputv1.SignalQuality {
	return &inputv1.SignalQuality{
		InputId:        sq.InputID,
		SignalStrength: int32(sq.SignalStrength),
		Snr:            int32(sq.SNR),
		Ber:            sq.BER,
		Unc:            sq.UNC,
		Locked:         sq.Locked,
		Timestamp:      timestamppb.New(sq.Timestamp),
	}
}

// InputFilter contains filter criteria for listing inputs
type InputFilter struct {
	Type        *InputType
	Status      *InputStatus
	EnabledOnly bool
	PageSize    int
	PageToken   string
	OrderBy     string
}

// AdapterFilter contains filter criteria for listing adapters
type AdapterFilter struct {
	Type      *InputType
	PageSize  int
	PageToken string
}

// ServiceFilter contains filter criteria for listing services
type ServiceFilter struct {
	InputID         string
	MuxID           string
	Type            *ServiceType
	EnabledOnly     bool
	ExcludeEncrypted bool
	PageSize        int
	PageToken       string
}

// MuxFilter contains filter criteria for listing muxes
type MuxFilter struct {
	InputID     string
	EnabledOnly bool
	PageSize    int
	PageToken   string
}

// InputEvent represents an input event for pub/sub
type InputEvent struct {
	Type          string         `json:"type"`
	InputID       string         `json:"input_id"`
	Input         *Input         `json:"input,omitempty"`
	SignalQuality *SignalQuality `json:"signal_quality,omitempty"`
	Timestamp     time.Time      `json:"timestamp"`
}

// MarshalJSON implements json.Marshaler
func (e *InputEvent) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type          string         `json:"type"`
		InputID       string         `json:"input_id"`
		Input         *Input         `json:"input,omitempty"`
		SignalQuality *SignalQuality `json:"signal_quality,omitempty"`
		Timestamp     string         `json:"timestamp"`
	}{
		Type:          e.Type,
		InputID:       e.InputID,
		Input:         e.Input,
		SignalQuality: e.SignalQuality,
		Timestamp:     e.Timestamp.Format(time.RFC3339),
	})
}

// Helper functions for type conversion

func toProtoInputType(t InputType) inputv1.InputType {
	switch t {
	case InputTypeDVBS:
		return inputv1.InputType_INPUT_TYPE_DVB_S
	case InputTypeDVBS2:
		return inputv1.InputType_INPUT_TYPE_DVB_S2
	case InputTypeDVBT:
		return inputv1.InputType_INPUT_TYPE_DVB_T
	case InputTypeDVBT2:
		return inputv1.InputType_INPUT_TYPE_DVB_T2
	case InputTypeDVBC:
		return inputv1.InputType_INPUT_TYPE_DVB_C
	case InputTypeATSC:
		return inputv1.InputType_INPUT_TYPE_ATSC
	case InputTypeIPTV:
		return inputv1.InputType_INPUT_TYPE_IPTV
	case InputTypeSATIP:
		return inputv1.InputType_INPUT_TYPE_SATIP
	case InputTypeFile:
		return inputv1.InputType_INPUT_TYPE_FILE
	default:
		return inputv1.InputType_INPUT_TYPE_UNSPECIFIED
	}
}

func fromProtoInputType(t inputv1.InputType) InputType {
	switch t {
	case inputv1.InputType_INPUT_TYPE_DVB_S:
		return InputTypeDVBS
	case inputv1.InputType_INPUT_TYPE_DVB_S2:
		return InputTypeDVBS2
	case inputv1.InputType_INPUT_TYPE_DVB_T:
		return InputTypeDVBT
	case inputv1.InputType_INPUT_TYPE_DVB_T2:
		return InputTypeDVBT2
	case inputv1.InputType_INPUT_TYPE_DVB_C:
		return InputTypeDVBC
	case inputv1.InputType_INPUT_TYPE_ATSC:
		return InputTypeATSC
	case inputv1.InputType_INPUT_TYPE_IPTV:
		return InputTypeIPTV
	case inputv1.InputType_INPUT_TYPE_SATIP:
		return InputTypeSATIP
	case inputv1.InputType_INPUT_TYPE_FILE:
		return InputTypeFile
	default:
		return ""
	}
}

func toProtoInputStatus(s InputStatus) inputv1.InputStatus {
	switch s {
	case InputStatusIdle:
		return inputv1.InputStatus_INPUT_STATUS_IDLE
	case InputStatusTuning:
		return inputv1.InputStatus_INPUT_STATUS_TUNING
	case InputStatusLocked:
		return inputv1.InputStatus_INPUT_STATUS_LOCKED
	case InputStatusError:
		return inputv1.InputStatus_INPUT_STATUS_ERROR
	case InputStatusDisabled:
		return inputv1.InputStatus_INPUT_STATUS_DISABLED
	default:
		return inputv1.InputStatus_INPUT_STATUS_UNSPECIFIED
	}
}

func toProtoScanStatus(s ScanStatus) inputv1.ScanStatus {
	switch s {
	case ScanStatusPending:
		return inputv1.ScanStatus_SCAN_STATUS_PENDING
	case ScanStatusScanning:
		return inputv1.ScanStatus_SCAN_STATUS_SCANNING
	case ScanStatusCompleted:
		return inputv1.ScanStatus_SCAN_STATUS_COMPLETED
	case ScanStatusFailed:
		return inputv1.ScanStatus_SCAN_STATUS_FAILED
	default:
		return inputv1.ScanStatus_SCAN_STATUS_UNSPECIFIED
	}
}

func toProtoServiceType(t ServiceType) inputv1.ServiceType {
	switch t {
	case ServiceTypeTV:
		return inputv1.ServiceType_SERVICE_TYPE_TV
	case ServiceTypeRadio:
		return inputv1.ServiceType_SERVICE_TYPE_RADIO
	case ServiceTypeSDTV:
		return inputv1.ServiceType_SERVICE_TYPE_SDTV
	case ServiceTypeHDTV:
		return inputv1.ServiceType_SERVICE_TYPE_HDTV
	case ServiceTypeUHDTV:
		return inputv1.ServiceType_SERVICE_TYPE_UHDTV
	case ServiceTypeData:
		return inputv1.ServiceType_SERVICE_TYPE_DATA
	default:
		return inputv1.ServiceType_SERVICE_TYPE_UNSPECIFIED
	}
}

// Ensure json.RawMessage implements driver.Valuer and sql.Scanner
func (j json.RawMessage) Value() (driver.Value, error) {
	if j == nil {
		return nil, nil
	}
	return string(j), nil
}

func (j *json.RawMessage) Scan(value interface{}) error {
	if value == nil {
		*j = nil
		return nil
	}

	bytes, ok := value.([]byte)
	if !ok {
		return errors.New("failed to unmarshal JSONB value")
	}

	*j = bytes
	return nil
}
