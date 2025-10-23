package adapter

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// FileConfig contains file input configuration
type FileConfig struct {
	Path  string
	Loop  bool
	Speed float64 // Playback speed multiplier (1.0 = normal)
}

// FileAdapter implements the Adapter interface for file sources
type FileAdapter struct {
	mu sync.RWMutex

	config *FileConfig
	status Status
	file   *os.File
	cancel context.CancelFunc

	bytesRead int64
	startTime time.Time
}

// NewFileAdapter creates a new file adapter
func NewFileAdapter() *FileAdapter {
	return &FileAdapter{
		status: Status{
			State:   StateIdle,
			Message: "Not started",
		},
	}
}

// GetType returns the adapter type
func (a *FileAdapter) GetType() string {
	return "FILE"
}

// Initialize prepares the adapter for use
func (a *FileAdapter) Initialize(ctx context.Context, config interface{}) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	fileConfig, ok := config.(*FileConfig)
	if !ok {
		return fmt.Errorf("invalid config type, expected *FileConfig")
	}

	// Check if file exists
	if _, err := os.Stat(fileConfig.Path); err != nil {
		return fmt.Errorf("file not found: %w", err)
	}

	// Set default speed
	if fileConfig.Speed == 0 {
		fileConfig.Speed = 1.0
	}

	a.config = fileConfig
	a.status.State = StateIdle
	a.status.Message = "Initialized"

	return nil
}

// Start begins streaming from the input
func (a *FileAdapter) Start(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.status.State == StateStreaming {
		return fmt.Errorf("already streaming")
	}

	if a.config == nil {
		return fmt.Errorf("adapter not initialized")
	}

	// Open file
	file, err := os.Open(a.config.Path)
	if err != nil {
		a.status.State = StateError
		a.status.Message = "Failed to open file"
		a.status.Error = err
		return fmt.Errorf("failed to open file: %w", err)
	}

	// Create cancellable context
	streamCtx, cancel := context.WithCancel(ctx)
	a.cancel = cancel

	a.file = file
	a.status.State = StateStreaming
	a.status.Message = "Streaming"
	a.status.Error = nil
	a.startTime = time.Now()

	// If looping is enabled, start a goroutine to handle it
	if a.config.Loop {
		go a.loopPlayback(streamCtx)
	}

	return nil
}

// Stop halts streaming
func (a *FileAdapter) Stop(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.cancel != nil {
		a.cancel()
		a.cancel = nil
	}

	if a.file != nil {
		a.file.Close()
		a.file = nil
	}

	a.status.State = StateIdle
	a.status.Message = "Stopped"

	return nil
}

// GetStatus returns the current adapter status
func (a *FileAdapter) GetStatus() Status {
	a.mu.RLock()
	defer a.mu.RUnlock()

	return a.status
}

// GetSignalQuality returns signal quality metrics
func (a *FileAdapter) GetSignalQuality() (*SignalQuality, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	// File sources always have "perfect" signal
	quality := &SignalQuality{
		Locked:         a.status.State == StateStreaming,
		SignalStrength: 100,
		SNR:            500, // 50 dB
		BER:            0,
		UNC:            0,
	}

	return quality, nil
}

// Scan scans for available services
func (a *FileAdapter) Scan(ctx context.Context, progressChan chan<- ScanProgress) error {
	// File sources typically contain a single mux
	// Would need to parse TS to extract service information
	// For now, just report success

	progressChan <- ScanProgress{
		Status:        "scanning",
		Progress:      50,
		MuxesScanned:  0,
		ServicesFound: 0,
		Message:       "Analyzing transport stream...",
	}

	// Simulate scan time
	time.Sleep(1 * time.Second)

	progressChan <- ScanProgress{
		Status:        "completed",
		Progress:      100,
		MuxesScanned:  1,
		ServicesFound: 0,
		Message:       "Scan complete. Enable stream processing for service detection.",
	}

	return nil
}

// GetStream returns the transport stream reader
func (a *FileAdapter) GetStream() (io.ReadCloser, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if a.file == nil {
		return nil, fmt.Errorf("stream not available")
	}

	// Return a rate-limited reader based on speed
	return &ratedReader{
		reader:  a.file,
		adapter: a,
		rate:    int64(float64(1500000) * a.config.Speed), // ~12 Mbps typical TS bitrate
	}, nil
}

// IsHealthy checks if the adapter is functioning correctly
func (a *FileAdapter) IsHealthy() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()

	return a.status.State == StateStreaming || a.status.State == StateIdle
}

// loopPlayback handles file looping
func (a *FileAdapter) loopPlayback(ctx context.Context) {
	<-ctx.Done()

	// When context is cancelled, check if we should loop
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.file != nil && a.config.Loop {
		// Seek back to beginning
		a.file.Seek(0, io.SeekStart)
		a.bytesRead = 0
	}
}

// GetStatistics returns adapter statistics
func (a *FileAdapter) GetStatistics() map[string]interface{} {
	a.mu.RLock()
	defer a.mu.RUnlock()

	uptime := int64(0)
	if !a.startTime.IsZero() {
		uptime = int64(time.Since(a.startTime).Seconds())
	}

	bitrate := int64(0)
	if uptime > 0 {
		bitrate = (a.bytesRead * 8) / uptime
	}

	return map[string]interface{}{
		"bytes_read": a.bytesRead,
		"uptime":     uptime,
		"bitrate":    bitrate,
	}
}

// ratedReader implements rate-limited reading
type ratedReader struct {
	reader   *os.File
	adapter  *FileAdapter
	rate     int64
	lastRead time.Time
}

func (rr *ratedReader) Read(p []byte) (n int, err error) {
	// Calculate delay to maintain target bitrate
	if !rr.lastRead.IsZero() && rr.rate > 0 {
		elapsed := time.Since(rr.lastRead)
		expectedBytes := int64(elapsed.Seconds() * float64(rr.rate) / 8)

		if rr.adapter.bytesRead > expectedBytes {
			// We're ahead, sleep to slow down
			delay := time.Duration((rr.adapter.bytesRead-expectedBytes)*8*1000/rr.rate) * time.Millisecond
			time.Sleep(delay)
		}
	}

	n, err = rr.reader.Read(p)
	rr.lastRead = time.Now()

	rr.adapter.mu.Lock()
	rr.adapter.bytesRead += int64(n)
	rr.adapter.mu.Unlock()

	// Handle EOF for looping
	if err == io.EOF && rr.adapter.config.Loop {
		rr.reader.Seek(0, io.SeekStart)
		rr.adapter.mu.Lock()
		rr.adapter.bytesRead = 0
		rr.adapter.mu.Unlock()
		// Try reading again
		return rr.reader.Read(p)
	}

	return n, err
}

func (rr *ratedReader) Close() error {
	// Don't close the underlying file, let Stop() handle it
	return nil
}
