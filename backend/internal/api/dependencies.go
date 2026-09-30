package api

import (
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/service"
)

type Dependencies struct {
	Setup             *service.SetupService
	Catalog           *service.CatalogService
	InstallOperations *service.InstallOperations
	Installations     *service.Installations
	MoveTools         *service.MoveTools
	Operations        *service.Operations
	SourceRoots       *service.SourceRoots
	SourceLocations   *service.SourceLocations
	SourceScan        *service.SourceScanOperations
}

type preflightEntry struct {
	createdAt time.Time
	install   *service.InstallPreflight
	move      *service.MovePreflight
}

type preflightTokens struct {
	mu      sync.Mutex
	entries map[string]preflightEntry
	now     func() time.Time
}

func newPreflightTokens() *preflightTokens {
	return &preflightTokens{entries: make(map[string]preflightEntry), now: time.Now}
}

func (tokens *preflightTokens) issueInstall(plan service.InstallPreflight) string {
	return tokens.issue(preflightEntry{createdAt: tokens.now(), install: &plan})
}

func (tokens *preflightTokens) issueMove(plan service.MovePreflight) string {
	return tokens.issue(preflightEntry{createdAt: tokens.now(), move: &plan})
}

func (tokens *preflightTokens) issue(entry preflightEntry) string {
	token := uuid.NewString()
	tokens.mu.Lock()
	defer tokens.mu.Unlock()
	tokens.expireLocked()
	tokens.entries[token] = entry
	return token
}

func (tokens *preflightTokens) consume(token string) (preflightEntry, bool) {
	tokens.mu.Lock()
	defer tokens.mu.Unlock()
	tokens.expireLocked()
	entry, exists := tokens.entries[token]
	delete(tokens.entries, token)
	return entry, exists
}

func (tokens *preflightTokens) expireLocked() {
	cutoff := tokens.now().Add(-5 * time.Minute)
	for token, entry := range tokens.entries {
		if entry.createdAt.Before(cutoff) {
			delete(tokens.entries, token)
		}
	}
}
