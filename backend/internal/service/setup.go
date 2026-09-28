package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

type SetupState struct {
	Completed           bool
	ConfigurationHealth bool
	Problems            []string
}
type SetupService struct {
	store    settings.Store
	registry *settings.Registry
	platform settings.PlatformState
}

func NewSetup(store settings.Store, registry *settings.Registry, platform settings.PlatformState) *SetupService {
	return &SetupService{store: store, registry: registry, platform: platform}
}
func (s *SetupService) State(ctx context.Context) (SetupState, error) {
	completed, err := s.registry.SetupCompleted(ctx)
	if err != nil {
		return SetupState{}, err
	}
	state := SetupState{Completed: completed, ConfigurationHealth: !s.platform.Diagnostic}
	if s.platform.Diagnostic {
		state.Problems = append(state.Problems, s.platform.Reason)
	}
	for _, key := range []string{settings.ToolsDirectoryKey, settings.OutputDirectoryKey, settings.PublicationFormatKey} {
		if value, ok, err := s.store.Get(ctx, key); err != nil {
			return SetupState{}, err
		} else if !ok || value == "" {
			state.ConfigurationHealth = false
			state.Problems = append(state.Problems, "missing "+key)
		}
	}
	return state, nil
}
func (s *SetupService) SaveRuntime(ctx context.Context, toolsDirectory, outputDirectory, publicationFormat string) error {
	if strings.TrimSpace(toolsDirectory) != "" {
		if _, err := settings.NormalizePath(toolsDirectory); err != nil {
			return fmt.Errorf("tools directory: %w", err)
		}
		if err := s.store.Set(ctx, settings.ToolsDirectoryKey, toolsDirectory); err != nil {
			return err
		}
	}
	if strings.TrimSpace(outputDirectory) != "" {
		if _, err := settings.NormalizePath(outputDirectory); err != nil {
			return fmt.Errorf("output directory: %w", err)
		}
		if err := s.store.Set(ctx, settings.OutputDirectoryKey, outputDirectory); err != nil {
			return err
		}
	}
	if publicationFormat != "" && publicationFormat != "source" && publicationFormat != "mka" {
		return fmt.Errorf("invalid publication format")
	}
	if publicationFormat != "" {
		return s.store.Set(ctx, settings.PublicationFormatKey, publicationFormat)
	}
	return nil
}
func (s *SetupService) Complete(ctx context.Context) error {
	state, err := s.State(ctx)
	if err != nil {
		return err
	}
	if !state.ConfigurationHealth {
		return fmt.Errorf("setup requirements are not met: %s", strings.Join(state.Problems, ", "))
	}
	return s.registry.CompleteSetup(ctx)
}
