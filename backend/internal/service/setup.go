package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/ruckus/MusicEnreachment/backend/internal/settings"
)

type SetupState struct {
	Completed           bool
	ConfigurationHealth settings.ConfigurationHealth
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
	health := s.registry.ComputeConfigurationHealth(ctx, s.platform)
	return SetupState{Completed: completed, ConfigurationHealth: health}, nil
}
func (s *SetupService) SaveRuntime(ctx context.Context, toolsDirectory, outputDirectory, publicationFormat string) error {
	if strings.TrimSpace(toolsDirectory) != "" {
		currentOutput, _, _ := s.registry.GetOutputDirectory(ctx)
		if err := s.registry.SetToolsDirectory(ctx, toolsDirectory, currentOutput); err != nil {
			return err
		}
	}
	if strings.TrimSpace(outputDirectory) != "" {
		currentTools, _, _ := s.registry.GetToolsDirectory(ctx)
		if err := s.registry.SetOutputDirectory(ctx, outputDirectory, currentTools); err != nil {
			return err
		}
	}
	if publicationFormat != "" {
		return s.registry.SetPublicationFormat(ctx, publicationFormat)
	}
	return nil
}
func (s *SetupService) Complete(ctx context.Context) error {
	state, err := s.State(ctx)
	if err != nil {
		return err
	}
	if !state.ConfigurationHealth.Healthy {
		return fmt.Errorf("setup requirements are not met: %s", strings.Join(state.ConfigurationHealth.Problems, ", "))
	}
	return s.registry.CompleteSetup(ctx)
}
