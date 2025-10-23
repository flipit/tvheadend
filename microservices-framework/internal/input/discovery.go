package input

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ziutek/dvb/linuxdvb/frontend"
	"go.uber.org/zap"
)

// DiscoverDVBAdapters scans for DVB adapters on the system
func (s *Service) DiscoverDVBAdapters(ctx context.Context) error {
	s.logger.Info("discovering DVB adapters...")

	// Scan /dev/dvb directory
	dvbPath := "/dev/dvb"
	if _, err := os.Stat(dvbPath); os.IsNotExist(err) {
		s.logger.Info("no DVB devices found (no /dev/dvb directory)")
		return nil
	}

	adapters, err := os.ReadDir(dvbPath)
	if err != nil {
		return fmt.Errorf("failed to read DVB directory: %w", err)
	}

	discovered := 0
	for _, adapter := range adapters {
		if !adapter.IsDir() || !strings.HasPrefix(adapter.Name(), "adapter") {
			continue
		}

		adapterNum := strings.TrimPrefix(adapter.Name(), "adapter")
		adapterPath := filepath.Join(dvbPath, adapter.Name())

		// Check for frontends
		frontends, err := os.ReadDir(adapterPath)
		if err != nil {
			s.logger.Warn("failed to read adapter directory",
				zap.String("path", adapterPath),
				zap.Error(err),
			)
			continue
		}

		for _, fe := range frontends {
			if !strings.HasPrefix(fe.Name(), "frontend") {
				continue
			}

			frontendPath := filepath.Join(adapterPath, fe.Name())

			// Try to open and get frontend info
			feDevice, err := frontend.Open(frontendPath)
			if err != nil {
				s.logger.Warn("failed to open frontend",
					zap.String("path", frontendPath),
					zap.Error(err),
				)
				continue
			}

			feInfo, err := feDevice.Info()
			feDevice.Close()

			if err != nil {
				s.logger.Warn("failed to get frontend info",
					zap.String("path", frontendPath),
					zap.Error(err),
				)
				continue
			}

			// Determine input type
			inputType := s.frontendTypeToInputType(feInfo.Type)

			// Create adapter record
			adapterID := fmt.Sprintf("dvb-%s-%s", adapterNum, fe.Name())

			// Check if already exists
			existing, err := s.repo.GetAdapter(ctx, adapterID)
			if err == nil && existing != nil {
				s.logger.Debug("DVB adapter already registered",
					zap.String("adapter_id", adapterID),
				)
				continue
			}

			adapter := &Adapter{
				ID:           adapterID,
				Name:         feInfo.Name,
				Type:         inputType,
				DevicePath:   frontendPath,
				Status:       InputStatusIdle,
				ActiveInputs: 0,
				CreatedAt:    time.Now(),
				UpdatedAt:    time.Now(),
			}

			// TODO: Set capabilities from feInfo

			if err := s.repo.CreateAdapter(ctx, adapter); err != nil {
				s.logger.Error("failed to register DVB adapter",
					zap.String("adapter_id", adapterID),
					zap.Error(err),
				)
				continue
			}

			s.logger.Info("discovered DVB adapter",
				zap.String("adapter_id", adapterID),
				zap.String("name", feInfo.Name),
				zap.String("type", string(inputType)),
			)
			discovered++
		}
	}

	s.logger.Info("DVB adapter discovery complete",
		zap.Int("discovered", discovered),
	)

	return nil
}

// DiscoverSATIPServers discovers SAT>IP servers using SSDP
func (s *Service) DiscoverSATIPServers(ctx context.Context) error {
	return s.DiscoverSATIPServersWithSSDP(ctx)
}

// frontendTypeToInputType converts DVB frontend type to our input type
func (s *Service) frontendTypeToInputType(feType frontend.FrontendType) InputType {
	switch feType {
	case frontend.FE_QPSK:
		return InputTypeDVBS2 // Default to DVB-S2, can detect S vs S2 later
	case frontend.FE_QAM:
		return InputTypeDVBC
	case frontend.FE_OFDM:
		return InputTypeDVBT2 // Default to DVB-T2, can detect T vs T2 later
	case frontend.FE_ATSC:
		return InputTypeATSC
	default:
		return InputType("DVB-UNKNOWN")
	}
}

// StartAdapterDiscovery starts periodic adapter discovery
func (s *Service) StartAdapterDiscovery(ctx context.Context) {
	s.logger.Info("starting adapter discovery")

	// Run initial discovery
	if err := s.DiscoverDVBAdapters(ctx); err != nil {
		s.logger.Error("DVB discovery failed", zap.Error(err))
	}

	if err := s.DiscoverSATIPServers(ctx); err != nil {
		s.logger.Error("SAT>IP discovery failed", zap.Error(err))
	}

	// Periodic rediscovery every 5 minutes
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.logger.Info("adapter discovery stopped")
			return
		case <-ticker.C:
			if err := s.DiscoverDVBAdapters(ctx); err != nil {
				s.logger.Error("DVB discovery failed", zap.Error(err))
			}
			if err := s.DiscoverSATIPServers(ctx); err != nil {
				s.logger.Error("SAT>IP discovery failed", zap.Error(err))
			}
		}
	}
}
