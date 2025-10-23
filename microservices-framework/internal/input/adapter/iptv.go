package adapter

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// IPTVConfig contains IPTV-specific configuration
type IPTVConfig struct {
	URL         string
	Interface   string
	BufferSize  int
	Timeout     int
	UserAgent   string
	Headers     map[string]string
	Username    string
	Password    string
}

// IPTVAdapter implements the Adapter interface for IPTV sources
type IPTVAdapter struct {
	mu sync.RWMutex

	config  *IPTVConfig
	client  *http.Client
	status  Status
	stream  io.ReadCloser
	cancel  context.CancelFunc

	bytesReceived int64
	packetsReceived int64
	startTime     time.Time
}

// NewIPTVAdapter creates a new IPTV adapter
func NewIPTVAdapter() *IPTVAdapter {
	return &IPTVAdapter{
		status: Status{
			State:   StateIdle,
			Message: "Not started",
		},
	}
}

// GetType returns the adapter type
func (a *IPTVAdapter) GetType() string {
	return "IPTV"
}

// Initialize prepares the adapter for use
func (a *IPTVAdapter) Initialize(ctx context.Context, config interface{}) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	iptvConfig, ok := config.(*IPTVConfig)
	if !ok {
		return fmt.Errorf("invalid config type, expected *IPTVConfig")
	}

	// Validate URL
	parsedURL, err := url.Parse(iptvConfig.URL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" &&
	   parsedURL.Scheme != "udp" && parsedURL.Scheme != "rtp" {
		return fmt.Errorf("unsupported URL scheme: %s", parsedURL.Scheme)
	}

	a.config = iptvConfig

	// Create HTTP client
	timeout := time.Duration(iptvConfig.Timeout) * time.Second
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	a.client = &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			MaxIdleConns:        10,
			IdleConnTimeout:     90 * time.Second,
			TLSHandshakeTimeout: 10 * time.Second,
		},
	}

	a.status.State = StateIdle
	a.status.Message = "Initialized"

	return nil
}

// Start begins streaming from the input
func (a *IPTVAdapter) Start(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.status.State == StateStreaming {
		return fmt.Errorf("already streaming")
	}

	if a.config == nil {
		return fmt.Errorf("adapter not initialized")
	}

	// Create request
	req, err := http.NewRequest("GET", a.config.URL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Set user agent
	userAgent := a.config.UserAgent
	if userAgent == "" {
		userAgent = "tvheadend/2.0"
	}
	req.Header.Set("User-Agent", userAgent)

	// Set custom headers
	for key, value := range a.config.Headers {
		req.Header.Set(key, value)
	}

	// Set authentication
	if a.config.Username != "" {
		req.SetBasicAuth(a.config.Username, a.config.Password)
	}

	// Create cancellable context
	streamCtx, cancel := context.WithCancel(ctx)
	a.cancel = cancel

	req = req.WithContext(streamCtx)

	// Make request
	a.status.State = StateTuning
	a.status.Message = "Connecting..."

	resp, err := a.client.Do(req)
	if err != nil {
		a.status.State = StateError
		a.status.Message = "Connection failed"
		a.status.Error = err
		return fmt.Errorf("failed to connect: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		a.status.State = StateError
		a.status.Message = fmt.Sprintf("HTTP error: %d", resp.StatusCode)
		return fmt.Errorf("HTTP error: %d %s", resp.StatusCode, resp.Status)
	}

	a.stream = resp.Body
	a.status.State = StateStreaming
	a.status.Message = "Streaming"
	a.status.Error = nil
	a.startTime = time.Now()

	return nil
}

// Stop halts streaming
func (a *IPTVAdapter) Stop(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.cancel != nil {
		a.cancel()
		a.cancel = nil
	}

	if a.stream != nil {
		a.stream.Close()
		a.stream = nil
	}

	a.status.State = StateIdle
	a.status.Message = "Stopped"

	return nil
}

// GetStatus returns the current adapter status
func (a *IPTVAdapter) GetStatus() Status {
	a.mu.RLock()
	defer a.mu.RUnlock()

	return a.status
}

// GetSignalQuality returns signal quality metrics
func (a *IPTVAdapter) GetSignalQuality() (*SignalQuality, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	// IPTV doesn't have traditional signal quality metrics
	// We report based on stream health
	quality := &SignalQuality{
		Locked: a.status.State == StateStreaming,
	}

	if a.status.State == StateStreaming {
		quality.SignalStrength = 100
		quality.SNR = 400 // 40 dB
	}

	return quality, nil
}

// Scan scans for available services
func (a *IPTVAdapter) Scan(ctx context.Context, progressChan chan<- ScanProgress) error {
	// IPTV sources typically don't support scanning
	// The URL points to a specific stream

	progressChan <- ScanProgress{
		Status:   "completed",
		Progress: 100,
		Message:  "IPTV sources do not support scanning",
	}

	return nil
}

// GetStream returns the transport stream reader
func (a *IPTVAdapter) GetStream() (io.ReadCloser, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if a.stream == nil {
		return nil, fmt.Errorf("stream not available")
	}

	// Return a tracked reader to count bytes
	return &trackedReader{
		reader: a.stream,
		adapter: a,
	}, nil
}

// IsHealthy checks if the adapter is functioning correctly
func (a *IPTVAdapter) IsHealthy() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()

	return a.status.State == StateStreaming || a.status.State == StateIdle
}

// GetStatistics returns adapter statistics
func (a *IPTVAdapter) GetStatistics() map[string]interface{} {
	a.mu.RLock()
	defer a.mu.RUnlock()

	uptime := int64(0)
	if !a.startTime.IsZero() {
		uptime = int64(time.Since(a.startTime).Seconds())
	}

	bitrate := int64(0)
	if uptime > 0 {
		bitrate = (a.bytesReceived * 8) / uptime
	}

	return map[string]interface{}{
		"bytes_received":   a.bytesReceived,
		"packets_received": a.packetsReceived,
		"uptime":          uptime,
		"bitrate":         bitrate,
	}
}

// trackedReader wraps io.ReadCloser to track statistics
type trackedReader struct {
	reader  io.ReadCloser
	adapter *IPTVAdapter
}

func (tr *trackedReader) Read(p []byte) (n int, err error) {
	n, err = tr.reader.Read(p)

	tr.adapter.mu.Lock()
	tr.adapter.bytesReceived += int64(n)
	if n > 0 {
		// Assume TS packets (188 bytes each)
		tr.adapter.packetsReceived += int64(n) / 188
	}
	tr.adapter.mu.Unlock()

	return n, err
}

func (tr *trackedReader) Close() error {
	return tr.reader.Close()
}
