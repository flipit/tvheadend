package adapter

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/aler9/gortsplib/v2"
	"github.com/aler9/gortsplib/v2/pkg/base"
	"github.com/aler9/gortsplib/v2/pkg/headers"
	"github.com/pion/rtp"
)

// SATIPConfig contains SAT>IP-specific configuration
type SATIPConfig struct {
	ServerURL    string
	Source       int
	Frontend     int
	Frequency    int64  // Hz
	Polarization string // h or v
	SymbolRate   int    // symbols/sec
	Modulation   string // Modulation system (dvbs, dvbs2, dvbt, dvbt2, dvbc)
	Timeout      int    // seconds
	TCPTransport bool   // Use TCP instead of UDP
}

// SATIPAdapter implements the Adapter interface for SAT>IP sources
type SATIPAdapter struct {
	mu sync.RWMutex

	config     *SATIPConfig
	status     Status
	rtspClient *gortsplib.Client
	session    string
	stream     *satipStream
	cancel     context.CancelFunc

	// Statistics
	bytesReceived   int64
	packetsReceived int64
	startTime       time.Time

	// Signal monitoring
	signalQuality *SignalQuality
}

// satipStream handles RTP packet reception
type satipStream struct {
	rtpConn     net.Conn
	rtpListener *net.UDPConn
	packets     chan []byte
	errors      chan error
	cancel      context.CancelFunc
}

// NewSATIPAdapter creates a new SAT>IP adapter
func NewSATIPAdapter() *SATIPAdapter {
	return &SATIPAdapter{
		status: Status{
			State:   StateIdle,
			Message: "Not started",
		},
		signalQuality: &SignalQuality{},
	}
}

// GetType returns the adapter type
func (a *SATIPAdapter) GetType() string {
	return "SAT>IP"
}

// Initialize prepares the adapter for use
func (a *SATIPAdapter) Initialize(ctx context.Context, config interface{}) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	satipConfig, ok := config.(*SATIPConfig)
	if !ok {
		return fmt.Errorf("invalid config type, expected *SATIPConfig")
	}

	// Validate URL
	_, err := url.Parse(satipConfig.ServerURL)
	if err != nil {
		return fmt.Errorf("invalid server URL: %w", err)
	}

	// Set defaults
	if satipConfig.Timeout == 0 {
		satipConfig.Timeout = 30
	}

	a.config = satipConfig

	// Create RTSP client
	a.rtspClient = &gortsplib.Client{
		Transport: func() *gortsplib.Transport {
			if satipConfig.TCPTransport {
				v := gortsplib.TransportTCP
				return &v
			}
			v := gortsplib.TransportUDP
			return &v
		}(),
	}

	a.status.State = StateIdle
	a.status.Message = "Initialized"

	return nil
}

// Start begins streaming from the input
func (a *SATIPAdapter) Start(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.status.State == StateStreaming {
		return fmt.Errorf("already streaming")
	}

	if a.config == nil {
		return fmt.Errorf("adapter not initialized")
	}

	a.status.State = StateTuning
	a.status.Message = "Connecting to SAT>IP server..."

	// Create cancellable context
	streamCtx, cancel := context.WithCancel(ctx)
	a.cancel = cancel

	// Build SAT>IP URL with tuning parameters
	rtspURL := a.buildSATIPURL()

	// Connect to RTSP server
	_, err := url.Parse(rtspURL)
	if err != nil {
		a.status.State = StateError
		a.status.Message = "Invalid URL"
		a.status.Error = err
		return fmt.Errorf("invalid RTSP URL: %w", err)
	}

	// Start RTSP session
	if err := a.startRTSPSession(streamCtx, rtspURL); err != nil {
		a.status.State = StateError
		a.status.Message = "RTSP session failed"
		a.status.Error = err
		return fmt.Errorf("failed to start RTSP session: %w", err)
	}

	a.status.State = StateStreaming
	a.status.Message = "Streaming"
	a.status.Error = nil
	a.startTime = time.Now()

	// Start signal quality monitoring
	go a.monitorSignal(streamCtx)

	return nil
}

// Stop halts streaming
func (a *SATIPAdapter) Stop(ctx context.Context) error {
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

	if a.rtspClient != nil {
		// Send TEARDOWN
		if a.session != "" {
			a.rtspClient.Teardown(a.session)
		}
		a.rtspClient.Close()
		a.rtspClient = nil
		a.session = ""
	}

	a.status.State = StateIdle
	a.status.Message = "Stopped"

	return nil
}

// GetStatus returns the current adapter status
func (a *SATIPAdapter) GetStatus() Status {
	a.mu.RLock()
	defer a.mu.RUnlock()

	return a.status
}

// GetSignalQuality returns signal quality metrics
func (a *SATIPAdapter) GetSignalQuality() (*SignalQuality, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	// SAT>IP servers may provide signal info in RTSP responses
	// For now, return based on lock status

	if a.status.State == StateStreaming {
		a.signalQuality.SignalStrength = 80
		a.signalQuality.SNR = 350 // 35dB
		a.signalQuality.Locked = true
	} else {
		a.signalQuality.SignalStrength = 0
		a.signalQuality.SNR = 0
		a.signalQuality.Locked = false
	}

	return a.signalQuality, nil
}

// Scan scans for available services
func (a *SATIPAdapter) Scan(ctx context.Context, progressChan chan<- ScanProgress) error {
	// SAT>IP scanning would involve tuning to multiple frequencies
	// and parsing PSI/SI tables from the stream

	progressChan <- ScanProgress{
		Status:   "scanning",
		Progress: 50,
		Message:  "SAT>IP scanning not fully implemented. Use manual configuration.",
	}

	time.Sleep(1 * time.Second)

	progressChan <- ScanProgress{
		Status:        "completed",
		Progress:      100,
		MuxesScanned:  1,
		ServicesFound: 0,
		Message:       "Enable stream processing for service detection.",
	}

	return nil
}

// GetStream returns the transport stream reader
func (a *SATIPAdapter) GetStream() (io.ReadCloser, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if a.stream == nil {
		return nil, fmt.Errorf("stream not available")
	}

	return &satipStreamReader{
		stream:  a.stream,
		adapter: a,
	}, nil
}

// IsHealthy checks if the adapter is functioning correctly
func (a *SATIPAdapter) IsHealthy() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()

	return a.status.State == StateStreaming && a.rtspClient != nil
}

// Private methods

func (a *SATIPAdapter) buildSATIPURL() string {
	// Build SAT>IP RTSP URL with tuning parameters
	// Format: rtsp://server:554/?src=X&freq=Y&pol=Z&sr=W&msys=S

	params := url.Values{}
	params.Set("src", fmt.Sprintf("%d", a.config.Source))
	params.Set("freq", fmt.Sprintf("%.0f", float64(a.config.Frequency)/1000000)) // Convert Hz to MHz
	params.Set("pol", strings.ToLower(a.config.Polarization))
	params.Set("sr", fmt.Sprintf("%d", a.config.SymbolRate))

	// Modulation system
	modSystem := strings.ToLower(a.config.Modulation)
	if modSystem == "" {
		modSystem = "dvbs2"
	}
	params.Set("msys", modSystem)

	// Optional frontend selection
	if a.config.Frontend > 0 {
		params.Set("fe", fmt.Sprintf("%d", a.config.Frontend))
	}

	// Build full URL
	baseURL := a.config.ServerURL
	if !strings.HasPrefix(baseURL, "rtsp://") {
		baseURL = "rtsp://" + baseURL
	}
	if !strings.Contains(baseURL, ":") {
		baseURL += ":554"
	}

	return fmt.Sprintf("%s/?%s", baseURL, params.Encode())
}

func (a *SATIPAdapter) startRTSPSession(ctx context.Context, rtspURL string) error {
	u, err := base.ParseURL(rtspURL)
	if err != nil {
		return fmt.Errorf("failed to parse URL: %w", err)
	}

	// Connect
	err = a.rtspClient.Start(u.Scheme, u.Host)
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}

	// Send OPTIONS request
	_, err = a.rtspClient.Options(u)
	if err != nil {
		return fmt.Errorf("OPTIONS failed: %w", err)
	}

	// Send DESCRIBE request
	res, tracks, err := a.rtspClient.Describe(u)
	if err != nil {
		return fmt.Errorf("DESCRIBE failed: %w", err)
	}

	// Extract session info from response if present
	if sess := res.Header.Get("Session"); sess != "" {
		a.session = sess
	}

	// Setup for each track (usually just one for SAT>IP)
	for _, track := range tracks {
		_, err = a.rtspClient.Setup(track, u, 0, 0)
		if err != nil {
			return fmt.Errorf("SETUP failed: %w", err)
		}
	}

	// Extract session from SETUP response
	// The library handles this automatically

	// Create stream for receiving RTP packets
	a.stream = &satipStream{
		packets: make(chan []byte, 100),
		errors:  make(chan error, 10),
	}

	// Start receiving packets
	streamCtx, streamCancel := context.WithCancel(ctx)
	a.stream.cancel = streamCancel

	// Set up packet handler
	a.rtspClient.OnPacketRTP = func(track int, pkt *rtp.Packet) {
		// Extract TS data from RTP payload
		select {
		case a.stream.packets <- pkt.Payload:
		default:
			// Drop packet if buffer full
		}

		// Update statistics
		a.mu.Lock()
		a.bytesReceived += int64(len(pkt.Payload))
		a.packetsReceived++
		a.mu.Unlock()
	}

	// Send PLAY request
	_, err = a.rtspClient.Play(nil)
	if err != nil {
		streamCancel()
		return fmt.Errorf("PLAY failed: %w", err)
	}

	// Start keepalive
	go a.sendKeepalive(streamCtx)

	return nil
}

func (a *SATIPAdapter) sendKeepalive(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Send OPTIONS as keepalive
			if a.rtspClient != nil {
				a.rtspClient.Options(nil)
			}
		}
	}
}

func (a *SATIPAdapter) monitorSignal(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.GetSignalQuality()
		}
	}
}

// GetStatistics returns adapter statistics
func (a *SATIPAdapter) GetStatistics() map[string]interface{} {
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
		"uptime":           uptime,
		"bitrate":          bitrate,
		"signal_strength":  a.signalQuality.SignalStrength,
		"snr":              a.signalQuality.SNR,
		"locked":           a.signalQuality.Locked,
	}
}

// satipStreamReader implements io.ReadCloser for the SAT>IP stream
type satipStreamReader struct {
	stream  *satipStream
	adapter *SATIPAdapter
	buffer  []byte
	offset  int
}

func (sr *satipStreamReader) Read(p []byte) (n int, err error) {
	for {
		// If we have buffered data, return it
		if sr.offset < len(sr.buffer) {
			n = copy(p, sr.buffer[sr.offset:])
			sr.offset += n
			if sr.offset >= len(sr.buffer) {
				sr.buffer = nil
				sr.offset = 0
			}
			return n, nil
		}

		// Wait for next packet
		select {
		case data := <-sr.stream.packets:
			if len(data) == 0 {
				return 0, io.EOF
			}
			// Buffer the packet data
			sr.buffer = data
			sr.offset = 0
		case err := <-sr.stream.errors:
			return 0, err
		}
	}
}

func (sr *satipStreamReader) Close() error {
	// Stream will be closed by Stop()
	return nil
}

// Close closes the SAT>IP stream
func (s *satipStream) Close() {
	if s.cancel != nil {
		s.cancel()
	}
	if s.rtpListener != nil {
		s.rtpListener.Close()
	}
	if s.rtpConn != nil {
		s.rtpConn.Close()
	}
	close(s.packets)
	close(s.errors)
}
