package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/sourcefs"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

const libraryDeletionFailureMessage = "library deletion could not safely remove all captured publications"

const LibraryDeletionOperationKind = persistence.LibraryDeletionOperationKind

var ErrLibraryDeletionConflict = persistence.ErrLibraryDeletionConflict
var ErrLibraryDeletionBlocked = persistence.ErrLibraryDeletionBlocked
var ErrLibraryDeletionReleaseNotFound = persistence.ErrLibraryReleaseNotFound
var ErrLibraryDeletionConfirmationUnavailable = errors.New("library deletion confirmation is unavailable or expired")

type libraryDeletionRepository interface {
	GetOperation(context.Context, uuid.UUID) (*persistence.Operation, error)
	CaptureLibraryDeletionPreview(context.Context, string, uuid.UUID, uuid.UUID, func(context.Context, string, persistence.LibraryDeletionPreview, map[uuid.UUID]uuid.UUID) ([]json.RawMessage, error)) (persistence.LibraryDeletionPreview, []json.RawMessage, error)
	FindLibraryDeletionConfirmation(context.Context, uuid.UUID, string) (*persistence.Operation, error)
	AdmitLibraryDeletionOperation(context.Context, uuid.UUID, string, time.Time, persistence.LibraryDeletionPreview, []json.RawMessage, persistence.RiverInserter, *river.InsertOpts) (*persistence.Operation, error)
	ClaimLibraryDeletionDelivery(context.Context, persistence.LibraryDeletionDeliveryGuard) (persistence.LibraryDeletionDeliveryClaimOutcome, error)
	SettleLibraryDeletionPublication(context.Context, persistence.LibraryDeletionDeliveryGuard, uuid.UUID, func(context.Context, json.RawMessage) error) (bool, error)
	FinalizeLibraryDeletion(context.Context, persistence.LibraryDeletionDeliveryGuard) (bool, error)
	FailLibraryDeletionDelivery(context.Context, persistence.LibraryDeletionDeliveryGuard, string) (bool, error)
	RetryLibraryDeletionOperationAndEnqueue(context.Context, uuid.UUID, persistence.RiverInserter) (*persistence.Operation, error)
}

type LibraryDeletionBlocker struct {
	PositionID uuid.UUID `json:"position_id,omitempty"`
	Code       string    `json:"code"`
	Message    string    `json:"message"`
}

type LibraryDeletionOutputFact struct {
	PositionID   uuid.UUID `json:"position_id"`
	RelativePath string    `json:"relative_path"`
	Capturable   bool      `json:"capturable"`
}

type LibraryDeletionArtistRelease struct {
	Title string `json:"title"`
}

type LibraryDeletionCoartistWarning struct {
	ReleaseTitle string   `json:"release_title"`
	ArtistNames  []string `json:"artist_names"`
}

// LibraryDeletionPreview is an explicit-confirmation capability. Its frozen
// witnesses are private and cannot be replaced by a caller before admission.
type LibraryDeletionPreview struct {
	Kind               string                           `json:"kind"`
	ReleaseID          uuid.UUID                        `json:"release_id"`
	Title              string                           `json:"title"`
	ArtistReleases     []LibraryDeletionArtistRelease   `json:"artist_releases"`
	CoartistWarnings   []LibraryDeletionCoartistWarning `json:"coartist_warnings"`
	PublicationIDs     []uuid.UUID                      `json:"publication_ids"`
	RecordingIDs       []uuid.UUID                      `json:"recording_ids"`
	RecordingCount     int                              `json:"recording_count"`
	OutputFacts        []LibraryDeletionOutputFact      `json:"outputs"`
	Blockers           []LibraryDeletionBlocker         `json:"blockers"`
	ConfirmationDigest string                           `json:"confirmation_digest"`
	persistencePreview persistence.LibraryDeletionPreview
	witnesses          []json.RawMessage
	expiresAt          time.Time
}

type libraryDeletionFrozenOutput struct {
	persistence.LibraryDeletionPublicationWitness
	OutputRoot         string                                   `json:"output_root"`
	NativePath         string                                   `json:"native_path"`
	OutputRootIdentity persistence.PublicationDirectoryIdentity `json:"output_root_identity"`
	ParentIdentity     persistence.PublicationDirectoryIdentity `json:"parent_identity"`
	FileSize           int64                                    `json:"file_size"`
	ModifiedAt         int64                                    `json:"modified_at_unix_micro"`
	SHA256             string                                   `json:"sha256"`
}

// LibraryDeletionService owns only publication cleanup and library row removal.
// The supplied coordinator must be the same instance used by publication flows.
type LibraryDeletionService struct {
	repository  libraryDeletionRepository
	coordinator *publication.Coordinator
	bootUUID    uuid.UUID
	opener      sourcefs.OutputOpener
	previewsMu  sync.Mutex
	previews    map[string]cachedLibraryDeletionPreview
}

type cachedLibraryDeletionPreview struct {
	preview LibraryDeletionPreview
	expires time.Time
}

const libraryDeletionPreviewLifetime = 10 * time.Minute
const libraryDeletionPreviewCacheLimit = 128

func NewLibraryDeletionService(repository *persistence.SetupManagerRepository, coordinator *publication.Coordinator, bootUUID uuid.UUID, opener sourcefs.OutputOpener) *LibraryDeletionService {
	var repositoryDependency libraryDeletionRepository
	if repository != nil {
		repositoryDependency = repository
	}
	if opener == nil {
		opener = sourcefs.NewOutputOpener()
	}
	return &LibraryDeletionService{repository: repositoryDependency, coordinator: coordinator, bootUUID: bootUUID, opener: opener, previews: make(map[string]cachedLibraryDeletionPreview)}
}

// Preview captures output ownership while both process publication mutation
// coordination and the database output-configuration gate are held.
func (service *LibraryDeletionService) Preview(ctx context.Context, kind string, releaseID, positionID uuid.UUID) (*LibraryDeletionPreview, error) {
	if err := service.validateDependencies(ctx); err != nil {
		return nil, err
	}
	unlock, err := service.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	var facts []LibraryDeletionOutputFact
	blockers := make([]LibraryDeletionBlocker, 0)
	preview, witnesses, err := service.repository.CaptureLibraryDeletionPreview(ctx, kind, releaseID, positionID,
		func(ctx context.Context, outputRoot string, preview persistence.LibraryDeletionPreview, claimIDs map[uuid.UUID]uuid.UUID) ([]json.RawMessage, error) {
			facts = make([]LibraryDeletionOutputFact, 0, len(preview.Publications))
			witnesses := make([]json.RawMessage, 0, len(preview.Publications))
			for _, current := range preview.Publications {
				fact := LibraryDeletionOutputFact{PositionID: current.PositionID, RelativePath: current.RelativePath}
				if slices.Contains(preview.Blockers, current.PositionID.String()) {
					facts = append(facts, fact)
					continue
				}
				claimID := claimIDs[current.PositionID]
				if claimID == uuid.Nil {
					fact.Capturable = false
					facts = append(facts, fact)
					continue
				}
				root, openErr := service.opener.OpenRoot(ctx, outputRoot)
				if openErr != nil {
					facts = append(facts, fact)
					blockers = append(blockers, LibraryDeletionBlocker{PositionID: current.PositionID, Code: "output_root_unavailable", Message: "output root is unavailable for safe verification"})
					continue
				}
				captured, captureErr := captureObsoletePublicationOutput(ctx, root, outputRoot, current, claimID)
				closeErr := root.Close()
				if captureErr != nil || closeErr != nil {
					facts = append(facts, fact)
					blockers = append(blockers, LibraryDeletionBlocker{PositionID: current.PositionID, Code: "output_not_capturable", Message: "current output does not match its registered ownership"})
					continue
				}
				fact.Capturable = true
				facts = append(facts, fact)
				witness := libraryDeletionFrozenOutput{
					LibraryDeletionPublicationWitness: persistence.LibraryDeletionPublicationWitness{
						PositionID: current.PositionID, Epoch: current.Epoch, Generation: current.Generation,
						RelativePath: current.RelativePath, Ownership: append(json.RawMessage(nil), current.Ownership...),
					},
					OutputRoot: outputRoot, NativePath: captured.NativePath,
					OutputRootIdentity: captured.OutputRootIdentity, ParentIdentity: captured.ParentIdentity,
					FileSize: captured.FileSize, ModifiedAt: captured.ModifiedAt.UnixMicro(), SHA256: hex.EncodeToString(captured.SHA256),
				}
				raw, marshalErr := json.Marshal(witness)
				if marshalErr != nil {
					return nil, fmt.Errorf("encode frozen library deletion output: %w", marshalErr)
				}
				witnesses = append(witnesses, raw)
			}
			for _, id := range preview.Blockers {
				positionID, parseErr := uuid.Parse(id)
				if parseErr != nil {
					return nil, fmt.Errorf("decode library deletion blocker identity: %w", parseErr)
				}
				blockers = append(blockers, LibraryDeletionBlocker{PositionID: positionID, Code: "publication_blocked", Message: "publication cannot be safely deleted"})
			}
			return witnesses, nil
		})
	if err != nil {
		return nil, fmt.Errorf("capture library deletion preview: %w", err)
	}
	confirmationDigest, err := libraryDeletionConfirmationDigest(preview.ScopeDigest, witnesses, uuid.NewString())
	if err != nil {
		return nil, err
	}
	artistReleases := make([]LibraryDeletionArtistRelease, 0, len(preview.ArtistReleases))
	for _, release := range preview.ArtistReleases {
		artistReleases = append(artistReleases, LibraryDeletionArtistRelease{Title: release.Title})
	}
	coartistWarnings := make([]LibraryDeletionCoartistWarning, 0, len(preview.CoartistWarnings))
	for _, warning := range preview.CoartistWarnings {
		coartistWarnings = append(coartistWarnings, LibraryDeletionCoartistWarning{
			ReleaseTitle: warning.ReleaseTitle, ArtistNames: slices.Clone(warning.ArtistNames),
		})
	}
	result := &LibraryDeletionPreview{
		Kind: kind, ReleaseID: releaseID, Title: preview.Title,
		ArtistReleases: artistReleases, CoartistWarnings: coartistWarnings,
		PublicationIDs: slices.Clone(preview.PositionIDs),
		RecordingIDs:   slices.Clone(preview.RecordingIDs), RecordingCount: preview.RecordingCount,
		OutputFacts: facts, Blockers: blockers, ConfirmationDigest: confirmationDigest,
		persistencePreview: preview, witnesses: witnesses, expiresAt: time.Now().Add(libraryDeletionPreviewLifetime),
	}
	service.cachePreview(*result)
	return result, nil
}

// Confirm admits exactly the preview capability supplied by the service and
// requires the caller to repeat its digest explicitly.
func (service *LibraryDeletionService) Confirm(ctx context.Context, confirmationDigest string, client persistence.RiverInserter, options *river.InsertOpts) (*persistence.Operation, error) {
	if err := service.validateDependencies(ctx); err != nil {
		return nil, err
	}
	unlock, err := service.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	operationID, err := persistence.LibraryDeletionOperationID(confirmationDigest)
	if err != nil {
		return nil, fmt.Errorf("confirm library deletion: %w", err)
	}
	existing, err := service.repository.FindLibraryDeletionConfirmation(ctx, operationID, confirmationDigest)
	if err != nil {
		return nil, fmt.Errorf("resolve library deletion confirmation: %w", err)
	}
	if existing != nil {
		service.removePreview(confirmationDigest)
		return existing, nil
	}
	preview, ok := service.previewForConfirmation(confirmationDigest)
	if !ok || !preview.expiresAt.After(time.Now()) || len(preview.Blockers) != 0 || len(preview.witnesses) != len(preview.persistencePreview.Publications) {
		return nil, fmt.Errorf("confirm library deletion: %w", ErrLibraryDeletionConfirmationUnavailable)
	}
	operation, err := service.repository.AdmitLibraryDeletionOperation(ctx, operationID, confirmationDigest, preview.expiresAt,
		preview.persistencePreview, preview.witnesses, client, options)
	if err != nil {
		return nil, err
	}
	service.removePreview(confirmationDigest)
	return operation, nil
}

// ExecuteDelivery executes one actual River delivery. Interrupted, completed,
// stale, or already-owned deliveries never enter the filesystem callback.
func (service *LibraryDeletionService) ExecuteDelivery(ctx context.Context, operationID uuid.UUID, riverJobID int64, riverAttempt int) error {
	if err := service.validateDependencies(ctx); err != nil {
		return err
	}
	if operationID == uuid.Nil || riverJobID <= 0 || riverAttempt <= 0 {
		return errors.New("library deletion delivery identity is invalid")
	}
	unlock, err := service.acquire(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	operation, err := service.repository.GetOperation(ctx, operationID)
	if err != nil {
		return fmt.Errorf("read library deletion operation: %w", err)
	}
	if operation == nil || operation.Kind != persistence.LibraryDeletionOperationKind || operation.RiverJobID == nil || *operation.RiverJobID != riverJobID {
		return nil
	}
	if operation.State == "succeeded" || operation.State == "dismissed" || operation.State == "failed" {
		return nil
	}
	guard := persistence.LibraryDeletionDeliveryGuard{
		OperationID: operationID, ApplicationAttempt: operation.Attempt, RiverJobID: riverJobID,
		RiverAttempt: riverAttempt, ClaimUUID: uuid.New(), BootUUID: service.bootUUID, DeliveryToken: uuid.NewString(),
	}
	outcome, err := service.repository.ClaimLibraryDeletionDelivery(ctx, guard)
	if err != nil {
		return fmt.Errorf("claim library deletion delivery: %w", err)
	}
	if outcome != persistence.LibraryDeletionDeliveryAcquired {
		switch outcome {
		case persistence.LibraryDeletionDeliveryRecoveryRequired, persistence.LibraryDeletionDeliveryInterrupted:
			return fmt.Errorf("library deletion delivery requires explicit recovery")
		case persistence.LibraryDeletionDeliveryAlreadyOwned, persistence.LibraryDeletionDeliveryAlreadyTerminal, persistence.LibraryDeletionDeliveryStale:
			return nil
		case persistence.LibraryDeletionDeliveryBlocked:
			return errors.New("library deletion delivery was blocked without a durable recovery outcome")
		default:
			return fmt.Errorf("library deletion delivery returned unknown claim outcome %q", outcome)
		}
	}
	operation, err = service.repository.GetOperation(ctx, operationID)
	if err != nil {
		return fmt.Errorf("read claimed library deletion snapshot: %w", err)
	}
	var snapshot persistence.LibraryDeletionOperationSnapshot
	if operation == nil || json.Unmarshal(operation.InputSnapshot, &snapshot) != nil {
		return service.fail(ctx, guard, errors.New("claimed deletion snapshot is unavailable"))
	}
	for _, positionID := range slices.Clone(snapshot.ResidualPublicationIDs) {
		settled, settleErr := service.repository.SettleLibraryDeletionPublication(ctx, guard, positionID,
			func(callbackCtx context.Context, raw json.RawMessage) error {
				witness, err := decodeLibraryDeletionFrozenOutput(raw)
				if err != nil {
					return fmt.Errorf("decode frozen output authority: %w", err)
				}
				return service.removeFrozenOutput(callbackCtx, witness)
			})
		if settleErr != nil {
			return service.fail(ctx, guard, settleErr)
		}
		if !settled {
			return errors.New("library deletion delivery no longer owns publication settlement")
		}
	}
	finalized, err := service.repository.FinalizeLibraryDeletion(ctx, guard)
	if err != nil {
		return service.fail(ctx, guard, err)
	}
	if !finalized {
		return errors.New("library deletion delivery could not finalize its frozen scope")
	}
	return nil
}

func (service *LibraryDeletionService) Retry(ctx context.Context, operationID uuid.UUID, client persistence.RiverInserter) (*persistence.Operation, error) {
	if err := service.validateDependencies(ctx); err != nil {
		return nil, err
	}
	unlock, err := service.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	return service.repository.RetryLibraryDeletionOperationAndEnqueue(ctx, operationID, client)
}

// DeliveryFailureWasRecorded reports whether ExecuteDelivery has already
// transitioned this exact River delivery to the durable explicit-recovery
// state. Workers use it to ACK handled failures without asking River to retry.
func (service *LibraryDeletionService) DeliveryFailureWasRecorded(ctx context.Context, operationID uuid.UUID, riverJobID int64) (bool, error) {
	if err := service.validateDependencies(ctx); err != nil {
		return false, err
	}
	if operationID == uuid.Nil || riverJobID < 1 {
		return false, errors.New("library deletion delivery identity is invalid")
	}
	operation, err := service.repository.GetOperation(ctx, operationID)
	if err != nil {
		return false, fmt.Errorf("read failed library deletion delivery: %w", err)
	}
	return operation != nil && operation.Kind == persistence.LibraryDeletionOperationKind &&
		operation.State == "failed" && operation.Stage == "deletion_recovery_required" &&
		operation.SafeError != nil && strings.TrimSpace(*operation.SafeError) != "" &&
		operation.RiverJobID != nil && *operation.RiverJobID == riverJobID, nil
}

func (service *LibraryDeletionService) fail(ctx context.Context, guard persistence.LibraryDeletionDeliveryGuard, cause error) error {
	if ctx.Err() == nil {
		if _, err := service.repository.FailLibraryDeletionDelivery(ctx, guard, libraryDeletionFailureMessage); err != nil {
			return errors.Join(fmt.Errorf("library deletion failed: %w", cause), fmt.Errorf("record deletion failure: %w", err))
		}
	}
	return fmt.Errorf("library deletion failed: %w", cause)
}

func (service *LibraryDeletionService) removeFrozenOutput(ctx context.Context, witness libraryDeletionFrozenOutput) (resultErr error) {
	rootPath := filepath.Clean(witness.OutputRoot)
	relativePath := filepath.FromSlash(witness.RelativePath)
	if !filepath.IsAbs(rootPath) || !persistencePathPortable(witness.RelativePath) ||
		filepath.Clean(witness.NativePath) != filepath.Join(rootPath, filepath.Clean(relativePath)) {
		return errors.New("frozen library deletion path is invalid")
	}
	root, err := service.opener.OpenRoot(ctx, rootPath)
	if err != nil {
		return fmt.Errorf("open frozen output root: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, root.Close()) }()
	if err := root.Verify(ctx); err != nil {
		return err
	}
	rootID, err := root.Identity(ctx)
	if err != nil {
		return err
	}
	if !sameCleanupIdentity(rootID, witness.OutputRootIdentity) {
		return errors.New("frozen output root identity changed")
	}
	components := strings.Split(filepath.Clean(relativePath), string(filepath.Separator))
	parent := root
	opened := make([]sourcefs.OutputDirectory, 0, len(components)-1)
	type pinnedChild struct {
		name     string
		identity sourcefs.DirectoryIdentity
	}
	chain := make([]pinnedChild, 0, len(components)-1)
	defer func() {
		for index := len(opened) - 1; index >= 0; index-- {
			resultErr = errors.Join(resultErr, opened[index].Close())
		}
	}()
	var traversal sourcefs.OutputDirectoryTraversal
	if traversal, _ = root.(sourcefs.OutputDirectoryTraversal); traversal == nil {
		return errors.New("output filesystem does not support safe traversal")
	}
	for _, component := range components[:len(components)-1] {
		child, openErr := traversal.OpenExistingDir(ctx, component)
		if openErr != nil {
			return fmt.Errorf("open frozen output parent: %w", openErr)
		}
		opened = append(opened, child)
		identity, identityErr := child.Identity(ctx)
		if identityErr != nil {
			return identityErr
		}
		if err := parent.VerifyChild(ctx, component, identity); err != nil {
			return err
		}
		chain = append(chain, pinnedChild{name: component, identity: identity})
		parent = child
		traversal, _ = child.(sourcefs.OutputDirectoryTraversal)
		if traversal == nil {
			return errors.New("output filesystem does not support safe traversal")
		}
	}
	parentID, err := parent.Identity(ctx)
	if err != nil {
		return err
	}
	if !sameCleanupIdentity(parentID, witness.ParentIdentity) {
		return errors.New("frozen output parent identity changed")
	}
	verifyNamespace := func() error {
		if err := root.Verify(ctx); err != nil {
			return err
		}
		ancestor := root
		for index, item := range chain {
			if err := ancestor.VerifyChild(ctx, item.name, item.identity); err != nil {
				return err
			}
			ancestor = opened[index]
		}
		return nil
	}
	if err := verifyNamespace(); err != nil {
		return err
	}
	mutation, ok := parent.(sourcefs.OutputMutation)
	if !ok {
		return errors.New("output filesystem does not support safe removal")
	}
	leaf := components[len(components)-1]
	file, err := mutation.OpenExistingRegular(ctx, leaf)
	if errors.Is(err, fs.ErrNotExist) {
		return verifyNamespace()
	}
	if err != nil {
		return fmt.Errorf("open frozen publication output: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, file.Close()) }()
	info, err := file.Stat(ctx)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() != witness.FileSize || info.ModTime().UnixMicro() != witness.ModifiedAt {
		return errors.New("frozen publication output metadata changed")
	}
	hash := sha256.New()
	if err := file.Borrow(ctx, func(handle *os.File) error { _, copyErr := io.Copy(hash, handle); return copyErr }); err != nil {
		return fmt.Errorf("hash frozen publication output: %w", err)
	}
	if hex.EncodeToString(hash.Sum(nil)) != witness.SHA256 {
		return errors.New("frozen publication output hash changed")
	}
	after, err := file.Stat(ctx)
	if err != nil {
		return err
	}
	if !os.SameFile(info, after) || info.Size() != after.Size() || info.ModTime().UnixMicro() != after.ModTime().UnixMicro() {
		return errors.New("frozen publication output changed while hashing")
	}
	if err := verifyNamespace(); err != nil {
		return err
	}
	cleanup, ok := parent.(sourcefs.OutputCleanup)
	if !ok {
		return errors.New("output filesystem does not support owned removal")
	}
	return cleanup.RemoveOwnedRegular(ctx, leaf, parentID, after)
}

func decodeLibraryDeletionFrozenOutput(raw json.RawMessage) (libraryDeletionFrozenOutput, error) {
	var witness libraryDeletionFrozenOutput
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&witness); err != nil {
		return witness, err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return witness, errors.New("frozen output authority has trailing data")
	}
	var ownership persistence.PublicationCurrentOwnership
	if err := json.Unmarshal(witness.Ownership, &ownership); err != nil {
		return witness, fmt.Errorf("decode frozen CURRENT ownership: %w", err)
	}
	if witness.PositionID == uuid.Nil || witness.Epoch == uuid.Nil || witness.Generation == uuid.Nil ||
		witness.RelativePath == "" || witness.OutputRoot == "" || witness.NativePath == "" ||
		witness.FileSize < 0 || !validReplacementDigest(witness.SHA256) ||
		witness.OutputRootIdentity.Platform == "" || witness.OutputRootIdentity.Volume == "" || witness.OutputRootIdentity.FileID == "" ||
		witness.ParentIdentity.Platform != witness.OutputRootIdentity.Platform || witness.ParentIdentity.Volume == "" || witness.ParentIdentity.FileID == "" ||
		ownership.Version != persistence.PublicationCurrentOwnershipVersion || ownership.Identity.SizeBytes == nil ||
		*ownership.Identity.SizeBytes != witness.FileSize || ownership.Identity.MTime == nil || *ownership.Identity.MTime != witness.ModifiedAt ||
		!strings.EqualFold(ownership.Identity.SHA256, witness.SHA256) {
		return witness, errors.New("frozen output authority is incomplete or inconsistent")
	}
	return witness, nil
}

func (service *LibraryDeletionService) validateDependencies(ctx context.Context) error {
	if service == nil || service.repository == nil || service.coordinator == nil || service.bootUUID == uuid.Nil || service.opener == nil {
		return errors.New("library deletion service dependencies are unavailable")
	}
	if ctx == nil {
		return errors.New("library deletion context is required")
	}
	return nil
}

func (service *LibraryDeletionService) acquire(ctx context.Context) (func(), error) {
	unlock, err := service.coordinator.Acquire(ctx, []publication.ResourceLock{{Key: GlobalPublicationMutationResource, Mode: publication.Exclusive}})
	if err != nil {
		return nil, fmt.Errorf("acquire publication mutation coordinator for library deletion: %w", err)
	}
	return unlock, nil
}

func (service *LibraryDeletionService) cachePreview(preview LibraryDeletionPreview) {
	service.previewsMu.Lock()
	defer service.previewsMu.Unlock()
	now := time.Now()
	for digest, cached := range service.previews {
		if !cached.expires.After(now) {
			delete(service.previews, digest)
		}
	}
	if len(service.previews) >= libraryDeletionPreviewCacheLimit {
		var oldestDigest string
		var oldestExpiry time.Time
		for digest, cached := range service.previews {
			if oldestDigest == "" || cached.expires.Before(oldestExpiry) {
				oldestDigest, oldestExpiry = digest, cached.expires
			}
		}
		delete(service.previews, oldestDigest)
	}
	service.previews[preview.ConfirmationDigest] = cachedLibraryDeletionPreview{preview: preview, expires: preview.expiresAt}
}

func (service *LibraryDeletionService) previewForConfirmation(digest string) (LibraryDeletionPreview, bool) {
	if digest == "" {
		return LibraryDeletionPreview{}, false
	}
	service.previewsMu.Lock()
	defer service.previewsMu.Unlock()
	cached, ok := service.previews[digest]
	if !ok || !cached.expires.After(time.Now()) {
		return LibraryDeletionPreview{}, false
	}
	return cached.preview, true
}

func (service *LibraryDeletionService) removePreview(digest string) {
	service.previewsMu.Lock()
	delete(service.previews, digest)
	service.previewsMu.Unlock()
}

func libraryDeletionConfirmationDigest(scopeDigest string, witnesses []json.RawMessage, nonce string) (string, error) {
	input, err := json.Marshal(struct {
		ScopeDigest string            `json:"scope_digest"`
		Witnesses   []json.RawMessage `json:"witnesses"`
		Nonce       string            `json:"nonce"`
	}{scopeDigest, witnesses, nonce})
	if err != nil {
		return "", fmt.Errorf("encode library deletion confirmation: %w", err)
	}
	digest := sha256.Sum256(input)
	return hex.EncodeToString(digest[:]), nil
}
