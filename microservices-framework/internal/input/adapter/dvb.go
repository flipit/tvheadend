package adapter

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/ziutek/dvb"
	"github.com/ziutek/dvb/linuxdvb/frontend"
)

// DVBConfig contains DVB-specific configuration
type DVBConfig struct {
	DevicePath   string
	AdapterNum   int
	FrontendNum  int
	Frequency    int64  // Hz
	Polarization string // H, V, L, R (for satellite)
	SymbolRate   int    // symbols/sec
	FEC          string // Forward Error Correction
	Modulation   string // Modulation type
	StreamID     int    // For multistream (DVB-S2)
	PLSCode      int    // PLS code for multistream
	PLSMode      string // PLS mode
	Inversion    string // Inversion setting
	Bandwidth    string // Bandwidth (for DVB-T)
}

// DVBAdapter implements the Adapter interface for DVB hardware
type DVBAdapter struct {
	mu sync.RWMutex

	config   *DVBConfig
	status   Status
	frontend frontend.Device
	demux    *os.File
	dvr      *os.File
	cancel   context.CancelFunc

	// Statistics
	bytesReceived   int64
	packetsReceived int64
	startTime       time.Time

	// Signal monitoring
	lastSignalCheck time.Time
	signalQuality   *SignalQuality
}

// NewDVBAdapter creates a new DVB adapter
func NewDVBAdapter() *DVBAdapter {
	return &DVBAdapter{
		status: Status{
			State:   StateIdle,
			Message: "Not started",
		},
		signalQuality: &SignalQuality{},
	}
}

// GetType returns the adapter type
func (a *DVBAdapter) GetType() string {
	return "DVB"
}

// Initialize prepares the adapter for use
func (a *DVBAdapter) Initialize(ctx context.Context, config interface{}) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	dvbConfig, ok := config.(*DVBConfig)
	if !ok {
		return fmt.Errorf("invalid config type, expected *DVBConfig")
	}

	a.config = dvbConfig

	// Open frontend device
	frontendPath := fmt.Sprintf("/dev/dvb/adapter%d/frontend%d", dvbConfig.AdapterNum, dvbConfig.FrontendNum)

	fe, err := frontend.Open(frontendPath)
	if err != nil {
		a.status.State = StateError
		a.status.Message = "Failed to open frontend"
		a.status.Error = err
		return fmt.Errorf("failed to open frontend %s: %w", frontendPath, err)
	}

	a.frontend = fe

	// Get frontend info
	feInfo, err := fe.Info()
	if err != nil {
		fe.Close()
		return fmt.Errorf("failed to get frontend info: %w", err)
	}

	a.status.State = StateIdle
	a.status.Message = fmt.Sprintf("Initialized: %s", feInfo.Name)

	return nil
}

// Start begins streaming from the input
func (a *DVBAdapter) Start(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.status.State == StateStreaming {
		return fmt.Errorf("already streaming")
	}

	if a.config == nil || a.frontend == nil {
		return fmt.Errorf("adapter not initialized")
	}

	a.status.State = StateTuning
	a.status.Message = "Tuning..."

	// Create cancellable context
	streamCtx, cancel := context.WithCancel(ctx)
	a.cancel = cancel

	// Configure frontend based on delivery system
	if err := a.tuneFrontend(); err != nil {
		a.status.State = StateError
		a.status.Message = "Tuning failed"
		a.status.Error = err
		return fmt.Errorf("tuning failed: %w", err)
	}

	// Wait for lock
	if err := a.waitForLock(streamCtx, 10*time.Second); err != nil {
		a.status.State = StateError
		a.status.Message = "Lock failed"
		a.status.Error = err
		return fmt.Errorf("failed to lock: %w", err)
	}

	// Open demux and DVR devices
	if err := a.openDemuxAndDVR(); err != nil {
		a.status.State = StateError
		a.status.Message = "Failed to open demux/DVR"
		a.status.Error = err
		return fmt.Errorf("failed to open demux/DVR: %w", err)
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
func (a *DVBAdapter) Stop(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.cancel != nil {
		a.cancel()
		a.cancel = nil
	}

	if a.dvr != nil {
		a.dvr.Close()
		a.dvr = nil
	}

	if a.demux != nil {
		a.demux.Close()
		a.demux = nil
	}

	if a.frontend != nil {
		a.frontend.Close()
		a.frontend = nil
	}

	a.status.State = StateIdle
	a.status.Message = "Stopped"

	return nil
}

// GetStatus returns the current adapter status
func (a *DVBAdapter) GetStatus() Status {
	a.mu.RLock()
	defer a.mu.RUnlock()

	return a.status
}

// GetSignalQuality returns signal quality metrics
func (a *DVBAdapter) GetSignalQuality() (*SignalQuality, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if a.frontend == nil {
		return nil, fmt.Errorf("frontend not open")
	}

	// Read signal strength (0-65535)
	strength, err := a.frontend.ReadSignalStrength()
	if err == nil {
		a.signalQuality.SignalStrength = int((strength * 100) / 65535)
	}

	// Read SNR (0-65535)
	snr, err := a.frontend.ReadSNR()
	if err == nil {
		// Convert to dB * 10
		a.signalQuality.SNR = int((snr * 400) / 65535) // ~40dB max
	}

	// Read BER
	ber, err := a.frontend.ReadBER()
	if err == nil {
		a.signalQuality.BER = int64(ber)
	}

	// Read uncorrected blocks
	unc, err := a.frontend.ReadUncorrectedBlocks()
	if err == nil {
		a.signalQuality.UNC = int64(unc)
	}

	// Read lock status
	status, err := a.frontend.ReadStatus()
	if err == nil {
		a.signalQuality.Locked = status&frontend.FE_HAS_LOCK != 0
	}

	return a.signalQuality, nil
}

// Scan scans for available services
func (a *DVBAdapter) Scan(ctx context.Context, progressChan chan<- ScanProgress) error {
	a.mu.Lock()
	if a.frontend == nil {
		a.mu.Unlock()
		progressChan <- ScanProgress{
			Status:  "failed",
			Message: "Frontend not initialized",
		}
		return fmt.Errorf("frontend not initialized")
	}
	a.mu.Unlock()

	progressChan <- ScanProgress{
		Status:   "scanning",
		Progress: 0,
		Message:  "Starting DVB scan...",
	}

	// TODO: Implement full scanning
	// 1. Load frequency list for the delivery system
	// 2. Tune to each frequency
	// 3. Wait for lock
	// 4. Parse PAT/PMT/SDT tables
	// 5. Extract service information
	// 6. Report progress

	// For now, just scan current frequency
	if err := a.tuneFrontend(); err != nil {
		progressChan <- ScanProgress{
			Status:  "failed",
			Message: fmt.Sprintf("Tuning failed: %v", err),
		}
		return err
	}

	if err := a.waitForLock(ctx, 10*time.Second); err != nil {
		progressChan <- ScanProgress{
			Status:  "failed",
			Message: fmt.Sprintf("Lock failed: %v", err),
		}
		return err
	}

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
func (a *DVBAdapter) GetStream() (io.ReadCloser, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if a.dvr == nil {
		return nil, fmt.Errorf("DVR device not open")
	}

	// Return a tracked reader to count bytes
	return &trackedDVBReader{
		reader:  a.dvr,
		adapter: a,
	}, nil
}

// IsHealthy checks if the adapter is functioning correctly
func (a *DVBAdapter) IsHealthy() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if a.frontend == nil {
		return false
	}

	// Check if we have lock
	status, err := a.frontend.ReadStatus()
	if err != nil {
		return false
	}

	return status&frontend.FE_HAS_LOCK != 0
}

// Private methods

func (a *DVBAdapter) tuneFrontend() error {
	// Get frontend info to determine delivery system
	feInfo, err := a.frontend.Info()
	if err != nil {
		return fmt.Errorf("failed to get frontend info: %w", err)
	}

	// Configure based on delivery system type
	switch feInfo.Type {
	case frontend.FE_QPSK: // DVB-S/S2
		return a.tuneSatellite()
	case frontend.FE_QAM: // DVB-C
		return a.tuneCable()
	case frontend.FE_OFDM: // DVB-T/T2
		return a.tuneTerrestrial()
	case frontend.FE_ATSC: // ATSC
		return a.tuneATSC()
	default:
		return fmt.Errorf("unsupported frontend type: %v", feInfo.Type)
	}
}

func (a *DVBAdapter) tuneSatellite() error {
	// Set delivery system
	if err := a.frontend.SetDeliverySystem(frontend.SYS_DVBS2); err != nil {
		// Fallback to DVB-S
		if err := a.frontend.SetDeliverySystem(frontend.SYS_DVBS); err != nil {
			return fmt.Errorf("failed to set delivery system: %w", err)
		}
	}

	// Set frequency
	if err := a.frontend.SetFrequency(uint32(a.config.Frequency)); err != nil {
		return fmt.Errorf("failed to set frequency: %w", err)
	}

	// Set symbol rate
	if err := a.frontend.SetSymbolRate(uint32(a.config.SymbolRate)); err != nil {
		return fmt.Errorf("failed to set symbol rate: %w", err)
	}

	// Set modulation
	modulation := a.parseModulation(a.config.Modulation)
	if err := a.frontend.SetModulation(modulation); err != nil {
		return fmt.Errorf("failed to set modulation: %w", err)
	}

	// Set FEC
	fec := a.parseFEC(a.config.FEC)
	if err := a.frontend.SetInnerFEC(fec); err != nil {
		return fmt.Errorf("failed to set FEC: %w", err)
	}

	// Tune
	if err := a.frontend.Tune(); err != nil {
		return fmt.Errorf("failed to tune: %w", err)
	}

	return nil
}

func (a *DVBAdapter) tuneCable() error {
	// Set delivery system
	if err := a.frontend.SetDeliverySystem(frontend.SYS_DVBC_ANNEX_A); err != nil {
		return fmt.Errorf("failed to set delivery system: %w", err)
	}

	// Set frequency
	if err := a.frontend.SetFrequency(uint32(a.config.Frequency)); err != nil {
		return fmt.Errorf("failed to set frequency: %w", err)
	}

	// Set symbol rate
	if err := a.frontend.SetSymbolRate(uint32(a.config.SymbolRate)); err != nil {
		return fmt.Errorf("failed to set symbol rate: %w", err)
	}

	// Set modulation
	modulation := a.parseModulation(a.config.Modulation)
	if err := a.frontend.SetModulation(modulation); err != nil {
		return fmt.Errorf("failed to set modulation: %w", err)
	}

	// Tune
	if err := a.frontend.Tune(); err != nil {
		return fmt.Errorf("failed to tune: %w", err)
	}

	return nil
}

func (a *DVBAdapter) tuneTerrestrial() error {
	// Set delivery system
	if err := a.frontend.SetDeliverySystem(frontend.SYS_DVBT2); err != nil {
		// Fallback to DVB-T
		if err := a.frontend.SetDeliverySystem(frontend.SYS_DVBT); err != nil {
			return fmt.Errorf("failed to set delivery system: %w", err)
		}
	}

	// Set frequency
	if err := a.frontend.SetFrequency(uint32(a.config.Frequency)); err != nil {
		return fmt.Errorf("failed to set frequency: %w", err)
	}

	// Set bandwidth
	bandwidth := a.parseBandwidth(a.config.Bandwidth)
	if err := a.frontend.SetBandwidth(bandwidth); err != nil {
		return fmt.Errorf("failed to set bandwidth: %w", err)
	}

	// Tune
	if err := a.frontend.Tune(); err != nil {
		return fmt.Errorf("failed to tune: %w", err)
	}

	return nil
}

func (a *DVBAdapter) tuneATSC() error {
	// Set delivery system
	if err := a.frontend.SetDeliverySystem(frontend.SYS_ATSC); err != nil {
		return fmt.Errorf("failed to set delivery system: %w", err)
	}

	// Set frequency
	if err := a.frontend.SetFrequency(uint32(a.config.Frequency)); err != nil {
		return fmt.Errorf("failed to set frequency: %w", err)
	}

	// Set modulation
	modulation := a.parseModulation(a.config.Modulation)
	if err := a.frontend.SetModulation(modulation); err != nil {
		return fmt.Errorf("failed to set modulation: %w", err)
	}

	// Tune
	if err := a.frontend.Tune(); err != nil {
		return fmt.Errorf("failed to tune: %w", err)
	}

	return nil
}

func (a *DVBAdapter) waitForLock(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		status, err := a.frontend.ReadStatus()
		if err != nil {
			return fmt.Errorf("failed to read status: %w", err)
		}

		if status&frontend.FE_HAS_LOCK != 0 {
			a.status.State = StateLocked
			a.status.Message = "Locked"
			return nil
		}

		time.Sleep(100 * time.Millisecond)
	}

	return fmt.Errorf("timeout waiting for lock")
}

func (a *DVBAdapter) openDemuxAndDVR() error {
	// Open demux device
	demuxPath := fmt.Sprintf("/dev/dvb/adapter%d/demux0", a.config.AdapterNum)
	demux, err := os.OpenFile(demuxPath, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("failed to open demux: %w", err)
	}
	a.demux = demux

	// Set demux to pass all PIDs (full transponder)
	// This would normally use ioctl calls to configure the demux
	// For simplicity, we'll just open the DVR device

	// Open DVR device
	dvrPath := fmt.Sprintf("/dev/dvb/adapter%d/dvr0", a.config.AdapterNum)
	dvr, err := os.Open(dvrPath)
	if err != nil {
		a.demux.Close()
		a.demux = nil
		return fmt.Errorf("failed to open DVR: %w", err)
	}
	a.dvr = dvr

	return nil
}

func (a *DVBAdapter) monitorSignal(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
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

func (a *DVBAdapter) parseModulation(mod string) frontend.Modulation {
	switch mod {
	case "QPSK":
		return frontend.QPSK
	case "8PSK":
		return frontend.PSK_8
	case "16QAM":
		return frontend.QAM_16
	case "32QAM":
		return frontend.QAM_32
	case "64QAM":
		return frontend.QAM_64
	case "128QAM":
		return frontend.QAM_128
	case "256QAM":
		return frontend.QAM_256
	case "8VSB":
		return frontend.VSB_8
	case "16VSB":
		return frontend.VSB_16
	default:
		return frontend.QAM_AUTO
	}
}

func (a *DVBAdapter) parseFEC(fec string) frontend.CodeRate {
	switch fec {
	case "1/2":
		return frontend.FEC_1_2
	case "2/3":
		return frontend.FEC_2_3
	case "3/4":
		return frontend.FEC_3_4
	case "4/5":
		return frontend.FEC_4_5
	case "5/6":
		return frontend.FEC_5_6
	case "6/7":
		return frontend.FEC_6_7
	case "7/8":
		return frontend.FEC_7_8
	case "8/9":
		return frontend.FEC_8_9
	default:
		return frontend.FEC_AUTO
	}
}

func (a *DVBAdapter) parseBandwidth(bw string) uint32 {
	switch bw {
	case "8MHz":
		return 8000000
	case "7MHz":
		return 7000000
	case "6MHz":
		return 6000000
	case "5MHz":
		return 5000000
	default:
		return 8000000 // Default to 8MHz
	}
}

// GetStatistics returns adapter statistics
func (a *DVBAdapter) GetStatistics() map[string]interface{} {
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
		"ber":              a.signalQuality.BER,
		"unc":              a.signalQuality.UNC,
		"locked":           a.signalQuality.Locked,
	}
}

// trackedDVBReader wraps io.ReadCloser to track statistics
type trackedDVBReader struct {
	reader  io.ReadCloser
	adapter *DVBAdapter
}

func (tr *trackedDVBReader) Read(p []byte) (n int, err error) {
	n, err = tr.reader.Read(p)

	tr.adapter.mu.Lock()
	tr.adapter.bytesReceived += int64(n)
	if n > 0 {
		// TS packets are 188 bytes each
		tr.adapter.packetsReceived += int64(n) / 188
	}
	tr.adapter.mu.Unlock()

	return n, err
}

func (tr *trackedDVBReader) Close() error {
	return tr.reader.Close()
}
