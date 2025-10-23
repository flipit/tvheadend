package input

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"go.uber.org/zap"
)

const (
	ssdpMulticastAddr = "239.255.255.250:1900"
	ssdpSearchTarget  = "urn:ses-com:device:SatIPServer:1"
	ssdpMXSeconds     = 3
)

// SSDPDevice represents a discovered SSDP device
type SSDPDevice struct {
	Location    string
	Server      string
	SearchType  string
	UniqueID    string
	BootID      string
	ConfigID    string
	IPAddress   string
}

// DiscoverSSDPDevices discovers SAT>IP servers using SSDP/UPnP
func DiscoverSSDPDevices(ctx context.Context, timeout time.Duration) ([]*SSDPDevice, error) {
	// Create UDP connection for multicast
	addr, err := net.ResolveUDPAddr("udp4", ssdpMulticastAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve multicast address: %w", err)
	}

	conn, err := net.ListenPacket("udp4", ":0")
	if err != nil {
		return nil, fmt.Errorf("failed to create UDP socket: %w", err)
	}
	defer conn.Close()

	// Set deadline
	deadline := time.Now().Add(timeout)
	conn.SetDeadline(deadline)

	// Build M-SEARCH request
	request := fmt.Sprintf(
		"M-SEARCH * HTTP/1.1\r\n"+
			"HOST: %s\r\n"+
			"MAN: \"ssdp:discover\"\r\n"+
			"MX: %d\r\n"+
			"ST: %s\r\n"+
			"\r\n",
		ssdpMulticastAddr,
		ssdpMXSeconds,
		ssdpSearchTarget,
	)

	// Send M-SEARCH request
	_, err = conn.WriteTo([]byte(request), addr)
	if err != nil {
		return nil, fmt.Errorf("failed to send M-SEARCH: %w", err)
	}

	// Collect responses
	devices := make(map[string]*SSDPDevice)
	buffer := make([]byte, 8192)

	for {
		select {
		case <-ctx.Done():
			return deviceMapToSlice(devices), ctx.Err()
		default:
		}

		// Check if timeout reached
		if time.Now().After(deadline) {
			break
		}

		n, remoteAddr, err := conn.ReadFrom(buffer)
		if err != nil {
			// Timeout or other error, break
			break
		}

		response := string(buffer[:n])

		// Parse SSDP response
		device := parseSSDPResponse(response, remoteAddr)
		if device != nil && device.UniqueID != "" {
			devices[device.UniqueID] = device
		}
	}

	return deviceMapToSlice(devices), nil
}

// parseSSDPResponse parses an SSDP response
func parseSSDPResponse(response string, remoteAddr net.Addr) *SSDPDevice {
	lines := strings.Split(response, "\r\n")

	// Check if it's a valid response
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "HTTP/1.1 200 OK") {
		return nil
	}

	device := &SSDPDevice{}

	// Extract IP address from remote address
	if udpAddr, ok := remoteAddr.(*net.UDPAddr); ok {
		device.IPAddress = udpAddr.IP.String()
	}

	// Parse headers
	for _, line := range lines[1:] {
		if line == "" {
			continue
		}

		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.ToLower(strings.TrimSpace(parts[0]))
		value := strings.TrimSpace(parts[1])

		switch key {
		case "location":
			device.Location = value
		case "server":
			device.Server = value
		case "st":
			device.SearchType = value
		case "usn":
			device.UniqueID = value
		case "bootid.upnp.org":
			device.BootID = value
		case "configid.upnp.org":
			device.ConfigID = value
		}
	}

	return device
}

// deviceMapToSlice converts device map to slice
func deviceMapToSlice(devices map[string]*SSDPDevice) []*SSDPDevice {
	result := make([]*SSDPDevice, 0, len(devices))
	for _, device := range devices {
		result = append(result, device)
	}
	return result
}

// DiscoverSATIPServersWithSSDP discovers SAT>IP servers and registers them
func (s *Service) DiscoverSATIPServersWithSSDP(ctx context.Context) error {
	s.logger.Info("discovering SAT>IP servers via SSDP...")

	devices, err := DiscoverSSDPDevices(ctx, 5*time.Second)
	if err != nil && err != context.DeadlineExceeded {
		return fmt.Errorf("SSDP discovery failed: %w", err)
	}

	s.logger.Info("SSDP discovery complete",
		zap.Int("devices_found", len(devices)),
	)

	discovered := 0
	for _, device := range devices {
		// Verify it's a SAT>IP server
		if !strings.Contains(device.SearchType, "SatIPServer") {
			continue
		}

		// Extract server URL from location or use IP
		serverURL := device.IPAddress
		if device.Location != "" {
			// Parse location to extract host
			if strings.HasPrefix(device.Location, "http://") {
				parts := strings.Split(strings.TrimPrefix(device.Location, "http://"), "/")
				if len(parts) > 0 {
					serverURL = parts[0]
				}
			}
		}

		// Create adapter ID from unique ID or IP
		adapterID := fmt.Sprintf("satip-%s", device.UniqueID)
		if adapterID == "satip-" {
			adapterID = fmt.Sprintf("satip-%s", device.IPAddress)
		}

		// Check if already exists
		existing, err := s.repo.GetAdapter(ctx, adapterID)
		if err == nil && existing != nil {
			s.logger.Debug("SAT>IP server already registered",
				zap.String("adapter_id", adapterID),
			)
			continue
		}

		// Create adapter record
		adapter := &Adapter{
			ID:           adapterID,
			Name:         fmt.Sprintf("SAT>IP Server (%s)", device.IPAddress),
			Type:         InputTypeSATIP,
			DevicePath:   fmt.Sprintf("rtsp://%s:554", serverURL),
			Status:       InputStatusIdle,
			ActiveInputs: 0,
			CreatedAt:    time.Now(),
			UpdatedAt:    time.Now(),
		}

		if err := s.repo.CreateAdapter(ctx, adapter); err != nil {
			s.logger.Error("failed to register SAT>IP server",
				zap.String("adapter_id", adapterID),
				zap.Error(err),
			)
			continue
		}

		s.logger.Info("discovered SAT>IP server",
			zap.String("adapter_id", adapterID),
			zap.String("server", device.Server),
			zap.String("ip", device.IPAddress),
		)
		discovered++
	}

	s.logger.Info("SAT>IP discovery complete",
		zap.Int("discovered", discovered),
	)

	return nil
}
