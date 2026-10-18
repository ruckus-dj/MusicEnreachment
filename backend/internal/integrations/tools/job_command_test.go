//go:build !windows && !linux && !darwin

package tools

import (
	"context"
	"strings"
	"testing"
)

func TestRunJobCommandFailsClosedWhenContainmentIsUnavailable(t *testing.T) {
	result, err := RunJobCommand(context.Background(), JobCommandRequest{Executable: "/must/not/run"})
	if err == nil || result.QuiescenceEstablished {
		t.Fatalf("RunJobCommand() = (%+v, %v), want unavailable error and unknown quiescence", result, err)
	}
	if !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("RunJobCommand() error = %v, want unavailable", err)
	}
}
