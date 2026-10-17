package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

type publicationQuiescenceRepository interface {
	CheckPublicationDelivery(context.Context, persistence.PublicationDeliveryGuard) (bool, error)
	MarkPublicationQuiescent(context.Context, persistence.PublicationDeliveryGuard) (bool, error)
}

// PublicationExecutionScope privately owns both the local resource lock and
// the durable delivery guard until finalization is acknowledged. Unknown
// execution results permanently fence this scope from further launches.
type PublicationExecutionScope struct {
	mu         sync.Mutex
	guard      persistence.PublicationDeliveryGuard
	commands   PublicationCommandRunner
	repository publicationQuiescenceRepository
	release    func()
	unresolved int
	unknown    bool
	closeTried bool
	closed     bool
	stageClose func() error
	ownedFiles []*os.File
	finalizing bool
	finalized  bool
}

func NewPublicationExecutionScope(
	ctx context.Context,
	coordinator *publication.Coordinator,
	resources []publication.ResourceLock,
	guard persistence.PublicationDeliveryGuard,
	claim func(context.Context, persistence.PublicationDeliveryGuard) (persistence.PublicationDeliveryClaimOutcome, error),
	commands PublicationCommandRunner,
	repository publicationQuiescenceRepository,
) (*PublicationExecutionScope, error) {
	if coordinator == nil || repository == nil || claim == nil {
		return nil, errors.New("publication execution scope dependencies are unavailable")
	}
	release, err := coordinator.Acquire(ctx, resources)
	if err != nil {
		return nil, fmt.Errorf("acquire publication execution resources: %w", err)
	}
	outcome, err := claim(ctx, guard)
	if err != nil {
		release()
		return nil, fmt.Errorf("claim publication delivery under coordinator resources: %w", err)
	}
	if outcome != persistence.PublicationDeliveryAcquired {
		release()
		return nil, errors.New("publication execution scope requires a newly acquired delivery claim")
	}
	return &PublicationExecutionScope{guard: guard, commands: commands, repository: repository, release: release}, nil
}

func (scope *PublicationExecutionScope) Run(ctx context.Context, path string, args ...string) (PublicationCommandResult, error) {
	if scope == nil {
		return unavailablePublicationCommand()
	}
	scope.mu.Lock()
	if scope.finalized || scope.finalizing || scope.unknown {
		scope.mu.Unlock()
		return PublicationCommandResult{}, errors.New("publication execution scope is not launchable")
	}
	if scope.commands == nil {
		scope.mu.Unlock()
		return unavailablePublicationCommand()
	}
	scope.unresolved++
	scope.mu.Unlock()

	var result PublicationCommandResult
	var err error
	panicked := true
	defer func() {
		if panicked {
			scope.mu.Lock()
			scope.unresolved--
			scope.unknown = true
			scope.quarantineLocked()
			scope.mu.Unlock()
		}
	}()
	result, err = scope.commands.Run(ctx, path, args...)
	panicked = false
	scope.mu.Lock()
	defer scope.mu.Unlock()
	scope.unresolved--
	if result.QuiescenceEstablished {
		return result, err
	}
	scope.unknown = true
	scope.quarantineLocked()
	return result, errors.Join(err, errors.New("publication command quiescence is unknown"))
}

// RunFile runs with a descriptor duplicated during a synchronous borrow. The
// original source descriptor is never passed to the command backend.
func (scope *PublicationExecutionScope) RunFile(ctx context.Context, executable string, args []string, input sourcefs.RegularFile) (PublicationCommandResult, error) {
	if scope == nil {
		return unavailablePublicationCommand()
	}
	scope.mu.Lock()
	if scope.finalized || scope.finalizing || scope.unknown {
		scope.mu.Unlock()
		return PublicationCommandResult{}, errors.New("publication execution scope is not launchable")
	}
	runner, ok := scope.commands.(PublicationFileCommandRunner)
	if scope.commands == nil || !ok || input == nil {
		scope.mu.Unlock()
		return unavailablePublicationCommand()
	}
	scope.unresolved++
	scope.mu.Unlock()
	reservationResolved := false
	defer func() {
		if reservationResolved {
			return
		}
		scope.mu.Lock()
		scope.unresolved--
		scope.unknown = true
		scope.quarantineLocked()
		scope.mu.Unlock()
	}()

	var duplicate *os.File
	err := input.Borrow(ctx, func(file *os.File) error {
		var duplicateErr error
		duplicate, duplicateErr = tools.DuplicateCommandInput(ctx, file)
		if duplicateErr != nil {
			return duplicateErr
		}
		scope.mu.Lock()
		scope.ownedFiles = append(scope.ownedFiles, duplicate)
		scope.mu.Unlock()
		return nil
	})
	if err != nil {
		if duplicate != nil {
			_ = duplicate.Close()
			scope.mu.Lock()
			scope.removeOwnedFileLocked(duplicate)
			scope.mu.Unlock()
		}
		scope.mu.Lock()
		scope.unresolved--
		reservationResolved = true
		scope.mu.Unlock()
		return PublicationCommandResult{QuiescenceEstablished: true}, err
	}

	result, runErr := runner.RunFile(ctx, tools.FileCommandRequest{Executable: executable, Args: args, Input: duplicate})
	scope.mu.Lock()
	scope.unresolved--
	reservationResolved = true
	if result.QuiescenceEstablished {
		if closeErr := duplicate.Close(); closeErr != nil {
			scope.unknown = true
			scope.quarantineLocked()
			scope.mu.Unlock()
			return result, errors.Join(runErr, fmt.Errorf("close publication command input: %w", closeErr), errors.New("publication command quiescence is unknown"))
		}
		scope.removeOwnedFileLocked(duplicate)
		scope.mu.Unlock()
		return result, runErr
	}
	scope.unknown = true
	scope.quarantineLocked()
	scope.mu.Unlock()
	return result, errors.Join(runErr, errors.New("publication command quiescence is unknown"))
}

func (scope *PublicationExecutionScope) removeOwnedFileLocked(file *os.File) {
	for i, owned := range scope.ownedFiles {
		if owned == file {
			scope.ownedFiles = append(scope.ownedFiles[:i], scope.ownedFiles[i+1:]...)
			return
		}
	}
}

// CanExecute reports whether the scope still admits work. The final command
// launch must still go through Run, which checks and reserves atomically.
func (scope *PublicationExecutionScope) CanExecute() bool {
	if scope == nil {
		return false
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	return scope.commands != nil && !scope.finalized && !scope.finalizing && !scope.unknown
}

// Finalize acknowledges a stopped scope only after stage cleanup and a
// successful guarded quiescence transition. Close is never retried after an
// error; DB uncertainty can be retried after a successful close without
// repeating the close callback.
func (scope *PublicationExecutionScope) Finalize(ctx context.Context, closeStage func() error) error {
	if scope == nil {
		return errors.New("publication execution scope is unavailable")
	}
	if closeStage == nil {
		return errors.New("publication stage cleanup callback is required")
	}
	scope.mu.Lock()
	if scope.stageClose == nil {
		scope.stageClose = closeStage
	}
	if scope.finalized {
		scope.mu.Unlock()
		return nil
	}
	if scope.unknown || scope.unresolved != 0 {
		if scope.unknown {
			scope.quarantineLocked()
		}
		scope.mu.Unlock()
		return errors.New("publication execution may still be active")
	}
	scope.finalizing = true
	if !scope.closeTried {
		scope.closeTried = true
		scope.mu.Unlock()
		if scope.stageClose != nil {
			if err := scope.stageClose(); err != nil {
				return fmt.Errorf("close publication stage: %w", err)
			}
		}
		scope.mu.Lock()
		scope.closed = true
	}
	if !scope.closed {
		scope.mu.Unlock()
		return errors.New("publication stage cleanup did not complete")
	}
	scope.mu.Unlock()

	cleanupCtx := context.WithoutCancel(ctx)
	marked, err := scope.repository.MarkPublicationQuiescent(cleanupCtx, scope.guard)
	if err != nil {
		return fmt.Errorf("quiesce publication execution: %w", err)
	}
	if !marked {
		return errors.New("publication quiescence was not acknowledged")
	}

	scope.mu.Lock()
	if !scope.finalized {
		scope.finalized = true
		scope.release()
		scope.release = nil
	}
	scope.mu.Unlock()
	return nil
}

func (scope *PublicationExecutionScope) CommandRunner() tools.CommandRunner {
	return publicationCommandAdapter{scope: scope}
}
