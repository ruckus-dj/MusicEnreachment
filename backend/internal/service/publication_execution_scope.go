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

type publicationSyncReservation struct {
	owner    *PublicationExecutionScope
	active   bool
	borrowed bool
	borrow   *publicationOptionalLyricsBorrow
}

type publicationOptionalLyricsBorrow struct {
	scope       *PublicationExecutionScope
	reservation *publicationSyncReservation
}

// privateManualLyricsWork is issued only for one stage-bound initial-copy
// proof. Its pointer identity is the authority; callers cannot reconstruct it.
type privateManualLyricsWork struct {
	scope       *PublicationExecutionScope
	reservation *publicationSyncReservation
	stage       *preparedPublicationStage
	proof       *publicationInitialOutputCopy
	borrow      *publicationOptionalLyricsBorrow
}

// privateManualLyricsReplacementInstalled is an opaque acknowledgement that
// the private manual-lyrics replacement was installed for this exact work.
type privateManualLyricsReplacementInstalled struct {
	work  *privateManualLyricsWork
	stage *preparedPublicationStage
	guard persistence.PublicationDeliveryGuard
}

// PublicationExecutionScope privately owns both the local resource lock and
// the durable delivery guard until finalization is acknowledged. Unknown
// execution results permanently fence this scope from further launches.
type PublicationExecutionScope struct {
	mu                      sync.Mutex
	guard                   persistence.PublicationDeliveryGuard
	commands                PublicationCommandRunner
	repository              publicationQuiescenceRepository
	release                 func()
	unresolved              int
	syncWork                int
	unknown                 bool
	closeTried              bool
	closed                  bool
	stageClose              func() error
	ownedFiles              []*os.File
	finalizing              bool
	finalized               bool
	firstAbsent             bool
	replacement             *replacementCompletionToken
	optionalLyrics          *publicationOptionalLyricsBorrow
	privateManualLyricsWork *privateManualLyricsWork
	privateLyricsInstalled  *privateManualLyricsReplacementInstalled
	initialCopy             bool
	preInputCleanup         *publicationPreInputCleanupCapability
}

type publicationPreInputCleanupCapability struct {
	scope  *PublicationExecutionScope
	active bool
}

func (scope *PublicationExecutionScope) beginPreInputCleanup() *publicationPreInputCleanupCapability {
	if scope == nil {
		return nil
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	if scope.unknown || scope.finalized || scope.finalizing || scope.initialCopy || scope.preInputCleanup != nil || scope.unresolved != 0 || scope.syncWork != 0 || scope.privateManualLyricsWork != nil || scope.release == nil {
		return nil
	}
	capability := &publicationPreInputCleanupCapability{scope: scope, active: true}
	scope.preInputCleanup = capability
	return capability
}

func (scope *PublicationExecutionScope) validPreInputCleanup(capability *publicationPreInputCleanupCapability) bool {
	if scope == nil || capability == nil {
		return false
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	return scope.validPreInputCleanupLocked(capability)
}

func (scope *PublicationExecutionScope) validPreInputCleanupLocked(capability *publicationPreInputCleanupCapability) bool {
	return capability.scope == scope && capability.active && scope.preInputCleanup == capability && !scope.unknown && !scope.finalized && !scope.finalizing && scope.unresolved == 0 && scope.syncWork == 0
}

func (scope *PublicationExecutionScope) finishPreInputCleanup(capability *publicationPreInputCleanupCapability, settled bool) bool {
	if scope == nil || capability == nil {
		return false
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	if !scope.validPreInputCleanupLocked(capability) {
		return false
	}
	capability.active = false
	scope.preInputCleanup = nil
	if settled {
		scope.finalized = true
		scope.closed = true
		if scope.release != nil {
			scope.release()
			scope.release = nil
		}
	}
	return true
}

func (scope *PublicationExecutionScope) failureSettled() bool {
	if scope == nil {
		return false
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	return scope.finalized && scope.release == nil
}

// replacementCompletionToken is an unforgeable, scope-bound capability for
// the post-install rewrite transaction. It is created only when the sole
// synchronous reservation is transferred into the completion fence.
type replacementCompletionToken struct {
	scope  *PublicationExecutionScope
	active bool
}

// quarantineUncertain permanently fences this local scope after an operation
// whose outcome cannot safely be retried or finalized.
func (scope *PublicationExecutionScope) quarantineUncertain() {
	if scope == nil {
		return
	}
	scope.mu.Lock()
	scope.unknown = true
	scope.quarantineLocked()
	scope.mu.Unlock()
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
	return scope.runAuthorized(ctx, path, args, func() bool {
		return !scope.finalized && !scope.finalizing && !scope.unknown && !scope.initialCopy && scope.preInputCleanup == nil && scope.privateManualLyricsWork == nil
	})
}

func (scope *PublicationExecutionScope) runAuthorized(ctx context.Context, path string, args []string, authorize func() bool) (PublicationCommandResult, error) {
	if scope == nil {
		return unavailablePublicationCommand()
	}
	scope.mu.Lock()
	if authorize == nil || !authorize() {
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
	return scope.runFileAuthorized(ctx, executable, args, input, func() bool {
		return !scope.finalized && !scope.finalizing && !scope.unknown && !scope.initialCopy && scope.preInputCleanup == nil && scope.privateManualLyricsWork == nil
	})
}

func (scope *PublicationExecutionScope) runFileAuthorized(ctx context.Context, executable string, args []string, input sourcefs.RegularFile, authorize func() bool) (PublicationCommandResult, error) {
	if scope == nil {
		return unavailablePublicationCommand()
	}
	scope.mu.Lock()
	if authorize == nil || !authorize() {
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

// beginInitialOutputCopy excludes new command launches while a narrowly-scoped
// prepared-stage copy runs. It is not a general reservation mutex.
func (scope *PublicationExecutionScope) beginInitialOutputCopy(reservation *publicationSyncReservation) bool {
	if scope == nil || reservation == nil {
		return false
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	if reservation.owner != scope || !reservation.active || reservation.borrowed || scope.syncWork != 1 || scope.unresolved != 0 || scope.initialCopy || scope.preInputCleanup != nil || scope.unknown || scope.finalized || scope.finalizing || scope.optionalLyrics != nil || scope.privateManualLyricsWork != nil || scope.privateLyricsInstalled != nil {
		return false
	}
	reservation.borrowed = true
	scope.initialCopy = true
	return true
}

func (scope *PublicationExecutionScope) endInitialOutputCopy(reservation *publicationSyncReservation, uncertain bool) {
	if scope == nil || reservation == nil {
		return
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	valid := reservation.owner == scope && reservation.active && reservation.borrowed && scope.initialCopy
	if valid {
		scope.initialCopy = false
		reservation.borrowed = false
	} else {
		// An unexpected state means the copy's execution boundary is no longer
		// trustworthy. Keep the reservation intact and fail closed.
		uncertain = true
	}
	if uncertain {
		scope.unknown = true
		scope.quarantineLocked()
	}
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
	return scope.commands != nil && !scope.finalized && !scope.finalizing && !scope.unknown && !scope.initialCopy && scope.preInputCleanup == nil
}

// reserveSynchronousWork protects non-command staging work from concurrent
// finalization. The token makes releasing or transferring this reservation
// explicitly owned by its caller.
func (scope *PublicationExecutionScope) reserveSynchronousWork() *publicationSyncReservation {
	if scope == nil {
		return nil
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	if scope.finalized || scope.finalizing || scope.unknown || scope.commands == nil || scope.optionalLyrics != nil || scope.privateManualLyricsWork != nil || scope.initialCopy || scope.preInputCleanup != nil {
		return nil
	}
	scope.syncWork++
	return &publicationSyncReservation{owner: scope, active: true}
}

// reserveReplacementWork borrows the caller's live staging reservation when
// supplied; otherwise it reserves an idle scope atomically. It deliberately
// refuses to borrow an unrelated reservation or a scope being finalized.
func (scope *PublicationExecutionScope) reserveReplacementWork(outer *publicationSyncReservation) (*publicationSyncReservation, bool) {
	if scope == nil {
		return nil, false
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	if scope.finalized || scope.finalizing || scope.unknown || scope.commands == nil || scope.initialCopy || scope.preInputCleanup != nil {
		return nil, false
	}
	if outer != nil {
		if outer.owner != scope || !outer.active || outer.borrowed || scope.syncWork != 1 {
			return nil, false
		}
		outer.borrowed = true
		return outer, false
	}
	if scope.syncWork != 0 {
		return nil, false
	}
	scope.syncWork++
	return &publicationSyncReservation{owner: scope, active: true}, true
}

func (scope *PublicationExecutionScope) releaseReplacementWork(reservation *publicationSyncReservation, owned bool) {
	if scope == nil || reservation == nil {
		return
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	if reservation.owner != scope || !reservation.active || reservation.borrow != nil {
		return
	}
	if owned {
		reservation.active = false
		scope.syncWork--
		return
	}
	reservation.borrowed = false
}

func (scope *PublicationExecutionScope) releaseSynchronousWork(reservation *publicationSyncReservation) {
	scope.mu.Lock()
	defer scope.mu.Unlock()
	if reservation != nil && reservation.owner == scope && reservation.active && !reservation.borrowed {
		reservation.active = false
		scope.syncWork--
	}
}

func (scope *PublicationExecutionScope) borrowOptionalLyricsWork(reservation *publicationSyncReservation) *publicationOptionalLyricsBorrow {
	if scope == nil || reservation == nil {
		return nil
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	if reservation.owner != scope || !reservation.active || reservation.borrowed || scope.syncWork != 1 || scope.unknown || scope.finalized || scope.finalizing || scope.optionalLyrics != nil || scope.privateManualLyricsWork != nil || scope.initialCopy || scope.preInputCleanup != nil {
		return nil
	}
	borrow := &publicationOptionalLyricsBorrow{scope: scope, reservation: reservation}
	reservation.borrowed = true
	reservation.borrow = borrow
	scope.optionalLyrics = borrow
	return borrow
}

func (scope *PublicationExecutionScope) releaseOptionalLyricsWork(borrow *publicationOptionalLyricsBorrow) {
	if scope == nil || borrow == nil {
		return
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	reservation := borrow.reservation
	if !scope.unknown && (scope.privateManualLyricsWork == nil || scope.privateManualLyricsWork.borrow != borrow) && borrow.scope == scope && reservation != nil && reservation.owner == scope && reservation.active && reservation.borrowed && reservation.borrow == borrow && scope.optionalLyrics == borrow {
		reservation.borrowed = false
		reservation.borrow = nil
		scope.optionalLyrics = nil
	}
}

func (scope *PublicationExecutionScope) acquirePrivateManualLyricsWork(
	reservation *publicationSyncReservation,
	stage *preparedPublicationStage,
	proof *publicationInitialOutputCopy,
) *privateManualLyricsWork {
	if scope == nil || reservation == nil || stage == nil || proof == nil || proof.stage != stage ||
		!stage.privatePrepMode || !stage.rewrite || stage.guard != scope.guard {
		return nil
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	if reservation.owner != scope || !reservation.active || reservation.borrowed || scope.syncWork != 1 || scope.unresolved != 0 || scope.unknown || scope.finalized || scope.finalizing || scope.optionalLyrics != nil || scope.privateManualLyricsWork != nil || scope.initialCopy || scope.preInputCleanup != nil {
		return nil
	}
	borrow := &publicationOptionalLyricsBorrow{scope: scope, reservation: reservation}
	work := &privateManualLyricsWork{scope: scope, reservation: reservation, stage: stage, proof: proof, borrow: borrow}
	reservation.borrowed = true
	reservation.borrow = borrow
	scope.optionalLyrics = borrow
	scope.privateManualLyricsWork = work
	return work
}

func (scope *PublicationExecutionScope) validPrivateManualLyricsWork(work *privateManualLyricsWork, stage *preparedPublicationStage) bool {
	if scope == nil || work == nil {
		return false
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	return scope.validPrivateManualLyricsWorkLocked(work, stage)
}

// finishPrivateManualLyricsWork retains the reservation until the caller has
// received an accepted durable ACK. Any other result permanently fences scope.
func (scope *PublicationExecutionScope) finishPrivateManualLyricsWork(work *privateManualLyricsWork, acknowledged bool) bool {
	if scope == nil || work == nil {
		return false
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	if !scope.validPrivateManualLyricsWorkLocked(work, workStage(work)) {
		return false
	}
	// Once installation has been acknowledged, only the replacement completion
	// fence may consume this private work. The ordinary optional-lyrics finish
	// path must not drop the reservation or permit legacy finalization.
	if scope.privateLyricsInstalled != nil {
		return false
	}
	if !acknowledged {
		scope.unknown = true
		scope.quarantineLocked()
		return false
	}
	r := work.reservation
	r.borrowed = false
	r.borrow = nil
	scope.optionalLyrics = nil
	scope.privateManualLyricsWork = nil
	return true
}

func (scope *PublicationExecutionScope) validPrivateManualLyricsWorkLocked(work *privateManualLyricsWork, stage *preparedPublicationStage) bool {
	if scope == nil || work == nil || scope.privateManualLyricsWork != work || work.scope != scope || stage == nil || work.stage != stage || work.proof == nil || work.proof.stage != stage || !stage.privatePrepMode || !stage.rewrite || stage.guard != scope.guard || work.borrow == nil {
		return false
	}
	r := work.reservation
	return work.borrow.scope == scope && work.borrow.reservation == r && r != nil && r.owner == scope && r.active && r.borrowed && r.borrow == work.borrow && scope.optionalLyrics == work.borrow && scope.syncWork == 1 && scope.unresolved == 0 && !scope.unknown && !scope.finalized && !scope.finalizing
}

func (scope *PublicationExecutionScope) validPrivateManualLyricsLaunch(work *privateManualLyricsWork, stage *preparedPublicationStage) bool {
	if scope == nil || work == nil {
		return false
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	return scope.validPrivateManualLyricsWorkLocked(work, stage) && !scope.initialCopy && scope.preInputCleanup == nil
}

// recordPrivateManualLyricsReplacementInstalled binds the replacer's installed
// acknowledgement to the live private work. The capability cannot be reused
// for another work item, stage, or delivery guard.
func (scope *PublicationExecutionScope) recordPrivateManualLyricsReplacementInstalled(work *privateManualLyricsWork) *privateManualLyricsReplacementInstalled {
	if scope == nil || work == nil {
		return nil
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	if !scope.validPrivateManualLyricsWorkLocked(work, work.stage) || scope.privateLyricsInstalled != nil {
		return nil
	}
	proof := &privateManualLyricsReplacementInstalled{work: work, stage: work.stage, guard: scope.guard}
	scope.privateLyricsInstalled = proof
	return proof
}

func workStage(work *privateManualLyricsWork) *preparedPublicationStage {
	if work == nil {
		return nil
	}
	return work.stage
}

// beginFirstAbsentFinalization seals this scope against any further command
// launches while retaining its coordinator locks and durable delivery claim.
func (scope *PublicationExecutionScope) beginFirstAbsentFinalization(reservation *publicationSyncReservation) error {
	if scope == nil {
		return errors.New("publication execution scope is unavailable")
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	if scope.unknown || scope.finalized || scope.finalizing || scope.initialCopy || scope.preInputCleanup != nil || scope.unresolved != 0 || scope.syncWork != 1 || reservation == nil || reservation.owner != scope || !reservation.active || reservation.borrowed {
		return errors.New("publication execution scope is not quiescent")
	}
	// Transfer the staging reservation to first-absent cleanup atomically. The
	// cleanup fence then owns the scope until settlement is acknowledged.
	reservation.active = false
	scope.syncWork--
	scope.finalizing = true
	scope.firstAbsent = true
	return nil
}

// sealReplacementCompletion transfers the only live synchronous reservation
// to replacement completion and fences both commands and legacy Finalize.
func (scope *PublicationExecutionScope) sealReplacementCompletion(reservation *publicationSyncReservation) (*replacementCompletionToken, error) {
	if scope == nil {
		return nil, errors.New("publication execution scope is unavailable")
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	if scope.unknown || scope.finalized || scope.finalizing || scope.initialCopy || scope.preInputCleanup != nil || scope.unresolved != 0 || scope.syncWork != 1 || reservation == nil || reservation.owner != scope || !reservation.active || reservation.borrow != nil {
		return nil, errors.New("publication execution scope is not exclusively quiescent")
	}
	reservation.borrowed = false
	reservation.active = false
	scope.syncWork--
	token := &replacementCompletionToken{scope: scope, active: true}
	scope.replacement = token
	scope.finalizing = true
	return token, nil
}

// sealPrivateManualLyricsReplacement atomically transfers the active private
// work and its sole borrowed synchronous reservation into the ordinary
// replacement-completion fence after the replacer acknowledges installation.
func (scope *PublicationExecutionScope) sealPrivateManualLyricsReplacement(
	work *privateManualLyricsWork,
	installed *privateManualLyricsReplacementInstalled,
) (*replacementCompletionToken, error) {
	if scope == nil {
		return nil, errors.New("publication execution scope is unavailable")
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	if !scope.validPrivateManualLyricsWorkLocked(work, workStage(work)) || installed == nil ||
		scope.privateLyricsInstalled != installed || installed.work != work || installed.stage != work.stage || installed.guard != scope.guard ||
		scope.unknown || scope.finalized || scope.finalizing || scope.initialCopy || scope.preInputCleanup != nil || scope.unresolved != 0 || scope.syncWork != 1 {
		return nil, errors.New("private manual lyrics replacement is not exclusively installed")
	}
	r := work.reservation
	// Consume every private borrow before publishing the regular completion
	// token. All changes occur under the same lock, so no scope observer can see
	// an unowned interval between the two capabilities.
	r.borrowed = false
	r.borrow = nil
	r.active = false
	scope.syncWork--
	scope.optionalLyrics = nil
	scope.privateManualLyricsWork = nil
	scope.privateLyricsInstalled = nil
	token := &replacementCompletionToken{scope: scope, active: true}
	scope.replacement = token
	scope.finalizing = true
	return token, nil
}

// acknowledgeReplacementCompletion releases local ownership only after the
// replacement settlement transaction is acknowledged.
func (scope *PublicationExecutionScope) acknowledgeReplacementCompletion(token *replacementCompletionToken) error {
	if scope == nil || token == nil {
		return errors.New("replacement completion token is unavailable")
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	if token.scope != scope || scope.replacement != token || !token.active || !scope.finalizing || scope.unknown || scope.unresolved != 0 || scope.syncWork != 0 || scope.finalized {
		return errors.New("replacement completion token is not active")
	}
	token.active = false
	scope.finalized = true
	scope.finalizing = false
	scope.replacement = nil
	if scope.release != nil {
		scope.release()
		scope.release = nil
	}
	return nil
}

// acknowledgeFirstAbsentFinalization releases local ownership only after the
// guarded terminal transaction has acknowledged cleanup and settlement.
func (scope *PublicationExecutionScope) acknowledgeFirstAbsentFinalization() {
	scope.mu.Lock()
	defer scope.mu.Unlock()
	if scope.firstAbsent && scope.finalizing && !scope.unknown && scope.unresolved == 0 && !scope.finalized {
		scope.finalized = true
		scope.finalizing = false
		scope.release()
		scope.release = nil
	}
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
	if scope.replacement != nil || scope.firstAbsent || scope.initialCopy || scope.preInputCleanup != nil || scope.privateManualLyricsWork != nil || scope.unknown || scope.unresolved != 0 || scope.syncWork != 0 {
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
