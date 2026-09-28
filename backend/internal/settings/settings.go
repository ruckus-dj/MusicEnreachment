// Package settings owns typed runtime settings and instance platform policy.
package settings

import (
	"context"
	"fmt"
	"log/slog"
	"runtime"
	"strings"
	"time"
)

const (
	PlatformGOOSKey             = "instance.goos"
	PlatformGOARCHKey           = "instance.goarch"
	ToolsDirectoryKey           = "tools_directory"
	OutputDirectoryKey          = "output_directory"
	PublicationFormatKey        = "publication_format"
	LogLevelKey                 = "log_level"
	ActiveFFmpegInstallationKey = "active_ffmpeg_installation_id"
	ActiveFPCalcInstallationKey = "active_fpcalc_installation_id"
	SetupCompletedAtKey         = "setup_completed_at"
)

type Store interface {
	Get(context.Context, string) (string, bool, error)
	Set(context.Context, string, string) error
	SetIfAbsent(context.Context, string, string) (string, error)
}

type Platform struct{ GOOS, GOARCH string }

func (p Platform) Supported() bool {
	return (p.GOOS == "linux" || p.GOOS == "darwin") && (p.GOARCH == "amd64" || p.GOARCH == "arm64") || p.GOOS == "windows" && p.GOARCH == "amd64"
}

type PlatformState struct {
	Platform   Platform
	Diagnostic bool
	Reason     string
}

type Registry struct {
	store Store
	level *slog.LevelVar
	now   func() time.Time
}

func New(store Store, level *slog.LevelVar) *Registry {
	if level == nil {
		level = new(slog.LevelVar)
		level.Set(slog.LevelInfo)
	}
	return &Registry{store: store, level: level, now: time.Now}
}

func (r *Registry) InitializePlatform(ctx context.Context, current Platform) (PlatformState, error) {
	if !current.Supported() {
		return PlatformState{Platform: current, Diagnostic: true, Reason: "unsupported platform"}, nil
	}
	goos, err := r.store.SetIfAbsent(ctx, PlatformGOOSKey, current.GOOS)
	if err != nil {
		return PlatformState{}, err
	}
	arch, err := r.store.SetIfAbsent(ctx, PlatformGOARCHKey, current.GOARCH)
	if err != nil {
		return PlatformState{}, err
	}
	persisted := Platform{GOOS: goos, GOARCH: arch}
	if persisted != current {
		return PlatformState{Platform: persisted, Diagnostic: true, Reason: "instance platform differs from current process"}, nil
	}
	return PlatformState{Platform: persisted}, nil
}

func CurrentPlatform() Platform { return Platform{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH} }

func (r *Registry) SetLogLevel(ctx context.Context, value string) error {
	level, ok := parseLogLevel(value)
	if !ok {
		return fmt.Errorf("invalid log level %q", value)
	}
	if err := r.store.Set(ctx, LogLevelKey, value); err != nil {
		return err
	}
	r.level.Set(level)
	return nil
}

func (r *Registry) LoadLogLevel(ctx context.Context) error {
	value, exists, err := r.store.Get(ctx, LogLevelKey)
	if err != nil || !exists {
		return err
	}
	level, ok := parseLogLevel(value)
	if !ok {
		return fmt.Errorf("stored invalid log level %q", value)
	}
	r.level.Set(level)
	return nil
}

func (r *Registry) CompleteSetup(ctx context.Context) error {
	return r.store.Set(ctx, SetupCompletedAtKey, r.now().UTC().Format(time.RFC3339Nano))
}
func (r *Registry) SetupCompleted(ctx context.Context) (bool, error) {
	_, ok, err := r.store.Get(ctx, SetupCompletedAtKey)
	return ok, err
}

func parseLogLevel(value string) (slog.Level, bool) {
	switch strings.ToLower(value) {
	case "debug":
		return slog.LevelDebug, true
	case "info":
		return slog.LevelInfo, true
	case "warn":
		return slog.LevelWarn, true
	case "error":
		return slog.LevelError, true
	}
	return 0, false
}
