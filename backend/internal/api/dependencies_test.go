package api

import (
	"testing"
	"time"

	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

func TestPreflightTokensExpireAndCannotBeReused(t *testing.T) {
	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	tokens := newPreflightTokens()
	tokens.now = func() time.Time { return now }

	atBoundary := tokens.issueInstall(service.InstallPreflight{})
	now = now.Add(5 * time.Minute)
	entry, ok := tokens.consume(atBoundary)
	if !ok || entry.install == nil || entry.move != nil {
		t.Fatalf("install preflight expired at its valid boundary: %#v, %t", entry, ok)
	}
	if _, ok := tokens.consume(atBoundary); ok {
		t.Fatal("one-use preflight token was consumed twice")
	}

	expired := tokens.issueMove(service.MovePreflight{})
	now = now.Add(5*time.Minute + time.Nanosecond)
	if _, ok := tokens.consume(expired); ok {
		t.Fatal("expired move preflight token was accepted")
	}
	if len(tokens.entries) != 0 {
		t.Fatalf("expired token remains in registry: %d entries", len(tokens.entries))
	}
}

func TestPreflightTokenDoesNotChangePlanKind(t *testing.T) {
	tokens := newPreflightTokens()
	install := tokens.issueInstall(service.InstallPreflight{})
	move := tokens.issueMove(service.MovePreflight{})
	installEntry, ok := tokens.consume(install)
	if !ok || installEntry.install == nil || installEntry.move != nil {
		t.Fatalf("install token has wrong plan kind: %#v, %t", installEntry, ok)
	}
	moveEntry, ok := tokens.consume(move)
	if !ok || moveEntry.move == nil || moveEntry.install != nil {
		t.Fatalf("move token has wrong plan kind: %#v, %t", moveEntry, ok)
	}
	if _, ok := tokens.consume("not-issued"); ok {
		t.Fatal("unknown token was accepted")
	}
}
