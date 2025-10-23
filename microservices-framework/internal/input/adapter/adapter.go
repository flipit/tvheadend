package adapter

import (
	"context"
	"io"
)

// Adapter is the interface that all input adapters must implement
type Adapter interface {
	// GetType returns the adapter type
	GetType() string

	// Initialize prepares the adapter for use
	Initialize(ctx context.Context, config interface{}) error

	// Start begins streaming from the input
	Start(ctx context.Context) error

	// Stop halts streaming
	Stop(ctx context.Context) error

	// GetStatus returns the current adapter status
	GetStatus() Status

	// GetSignalQuality returns signal quality metrics
	GetSignalQuality() (*SignalQuality, error)

	// Scan scans for available services
	Scan(ctx context.Context, progressChan chan<- ScanProgress) error

	// GetStream returns the transport stream reader
	GetStream() (io.ReadCloser, error)

	// IsHealthy checks if the adapter is functioning correctly
	IsHealthy() bool
}

// Status represents the current state of an adapter
type Status struct {
	State   State
	Message string
	Error   error
}

// State represents adapter state
type State string

const (
	StateIdle     State = "IDLE"
	StateTuning   State = "TUNING"
	StateLocked   State = "LOCKED"
	StateStreaming State = "STREAMING"
	StateError    State = "ERROR"
)

// SignalQuality contains signal quality metrics
type SignalQuality struct {
	SignalStrength int   // 0-100%
	SNR            int   // Signal-to-noise ratio (dB * 10)
	BER            int64 // Bit error rate
	UNC            int64 // Uncorrected blocks
	Locked         bool  // Lock status
}

// ScanProgress reports scan progress
type ScanProgress struct {
	Status         string
	Progress       int // 0-100
	MuxesScanned   int
	ServicesFound  int
	Message        string
}

// Service represents a discovered service
type Service struct {
	ID          int
	Name        string
	Provider    string
	Type        string
	PCR_PID     int
	PMT_PID     int
	VideoPIDs   []int
	AudioPIDs   []int
	SubtitlePIDs []int
	TeletextPIDs []int
	Encrypted   bool
	CASystems   []int
}

// Mux represents a discovered mux
type Mux struct {
	Frequency         int64
	NetworkID         int
	TransportStreamID int
	OriginalNetworkID int
	Services          []Service
}
