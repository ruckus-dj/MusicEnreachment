package persistence

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/tools"
	"github.com/uptrace/bun"
)

// PublicationNamespaceNode is a caller-planned namespace reservation. CanonicalKey
// must use the caller's probed filesystem-equivalence rules; no path policy is
// inferred here.
type PublicationNamespaceNode struct {
	CanonicalKey   string `json:"canonical_key"`
	RelativePath   string `json:"relative_path"`
	NodeKind       string `json:"node_kind"`
	DirectoryScope string `json:"directory_scope,omitempty"`
}

type PublicationAdmission struct {
	PositionID          uuid.UUID
	ExpectedEpoch       uuid.UUID
	ExpectedGeneration  *uuid.UUID
	CandidateGeneration uuid.UUID
	IntentKind          string
	ToolsReadRequired   bool
	Tools               []SourceAnalysisToolSelection
	Source              *PublicationSourceSelection
	FrozenInputs        json.RawMessage
	NamespaceManifest   []PublicationNamespaceNode
}

// PublicationSourceSelection is the caller's exact inventory selector. The
// persisted hold is derived from the strict frozen contract, never these fields.
type PublicationSourceSelection struct {
	RootID         uuid.UUID
	LocationID     uuid.UUID
	WorkID         uuid.UUID
	MediaVariantID uuid.UUID
	ConfiguredPath string
	InventoryPath  string
	RelativePath   string
	SizeBytes      int64
	MTime          time.Time
	HoldUUID       uuid.UUID
}

type publicationTrackArgs struct {
	OperationID uuid.UUID `json:"operation_id"`
}

func (publicationTrackArgs) Kind() string { return "publish_track_v1" }

// AdmitPublication atomically inserts the source-free operation snapshot, attempt,
// namespace reservations, and its River delivery.
func (repository *SetupManagerRepository) AdmitPublication(ctx context.Context, request PublicationAdmission, client RiverInserter) (uuid.UUID, error) {
	if client == nil || request.PositionID == uuid.Nil || request.ExpectedEpoch == uuid.Nil || request.CandidateGeneration == uuid.Nil {
		return uuid.Nil, fmt.Errorf("admit publication: invalid identity or missing River client")
	}
	if request.IntentKind != "audio" && request.IntentKind != "rewrite" {
		return uuid.Nil, fmt.Errorf("admit publication: unsupported intent")
	}
	input, err := DecodePublicationInputs(request.FrozenInputs)
	if err != nil {
		return uuid.Nil, fmt.Errorf("admit publication: %w", err)
	}
	if input.IntentKind != request.IntentKind || !samePublicationTools(request.Tools, input.Tools) {
		return uuid.Nil, fmt.Errorf("admit publication: request conflicts with frozen publication inputs")
	}
	if request.IntentKind == "audio" {
		if request.Source == nil || !publicationSourceMatchesInput(*request.Source, *input.Audio) {
			return uuid.Nil, fmt.Errorf("admit publication: source selection must match frozen audio input")
		}
	} else if request.Source != nil {
		return uuid.Nil, fmt.Errorf("admit publication: rewrite cannot select an original source")
	}
	if request.IntentKind == "rewrite" && !sameOptionalUUID(request.ExpectedGeneration, &input.Rewrite.CurrentGeneration) {
		return uuid.Nil, fmt.Errorf("admit publication: rewrite generation does not match frozen input")
	}
	if err := validatePublicationManifest(request.NamespaceManifest); err != nil {
		return uuid.Nil, err
	}
	operationID := uuid.New()
	err = repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return fmt.Errorf("admit publication: lock output gate: %w", err)
		}
		var pinnedInstallation *ToolInstallation
		if request.Source != nil {
			if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
				return fmt.Errorf("admit publication: lock source mutation gate: %w", err)
			}
		}
		if request.ToolsReadRequired {
			if err := lockToolsMoveReaders(ctx, tx); err != nil {
				return fmt.Errorf("admit publication: lock tools-root readers: %w", err)
			}
			moving, err := activeToolsMoveExists(ctx, tx)
			if err != nil {
				return fmt.Errorf("admit publication: check active tools-root move: %w", err)
			}
			if moving {
				return ErrToolsRootMoveActive
			}
			if err := validatePublicationToolSelections(request.Tools); err != nil {
				return err
			}
			if err := lockPackageSelections(ctx, tx, []string{"ffmpeg"}); err != nil {
				return fmt.Errorf("admit publication: lock ffmpeg selection: %w", err)
			}
		} else if len(request.Tools) != 0 {
			return fmt.Errorf("admit publication: tool selections require tools-read admission")
		}
		if request.Source != nil {
			if err := validateAndLockPublicationSource(ctx, tx, request.PositionID, *request.Source, *input.Audio); err != nil {
				return err
			}
		}
		if request.ToolsReadRequired {
			installationID := request.Tools[0].InstallationID
			if err := lockCompatibleInstallations(ctx, tx, []uuid.UUID{installationID}); err != nil {
				return fmt.Errorf("admit publication: lock pinned ffmpeg installation: %w", err)
			}
			pinnedInstallation = new(ToolInstallation)
			if err := tx.NewSelect().Model(pinnedInstallation).Where("id = ?", installationID).Scan(ctx); err != nil {
				return fmt.Errorf("admit publication: read pinned ffmpeg installation: %w", err)
			}
			if err := validatePublicationInstallation(pinnedInstallation, request.Tools); err != nil {
				return err
			}
		}
		var epoch uuid.UUID
		if err := tx.NewRaw(`SELECT output_epoch FROM publication_namespace_state WHERE singleton=true FOR UPDATE`).Scan(ctx, &epoch); err != nil {
			return fmt.Errorf("admit publication: lock output epoch: %w", err)
		}
		if epoch != request.ExpectedEpoch {
			return fmt.Errorf("admit publication: output epoch changed")
		}
		var position uuid.UUID
		if err := tx.NewRaw(`SELECT id FROM release_track WHERE id=? FOR UPDATE`, request.PositionID).Scan(ctx, &position); err != nil {
			return fmt.Errorf("admit publication: lock position: %w", err)
		}
		var generation *uuid.UUID
		err := tx.NewRaw(`SELECT generation FROM track_publication WHERE position_id=?`, request.PositionID).Scan(ctx, &generation)
		if err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("admit publication: read current generation: %w", err)
		}
		if !sameOptionalUUID(generation, request.ExpectedGeneration) {
			return fmt.Errorf("admit publication: current generation changed")
		}
		if input.Rewrite != nil {
			var current TrackPublication
			if err := tx.NewSelect().Model(&current).Where("position_id=?", request.PositionID).For("UPDATE").Scan(ctx); err != nil {
				return fmt.Errorf("admit publication: lock current publication: %w", err)
			}
			if !publicationRewriteMatchesCurrent(current, epoch, *input.Rewrite) {
				return fmt.Errorf("admit publication: frozen rewrite facts do not match current publication")
			}
		}
		var active uuid.UUID
		if err := tx.NewRaw(`SELECT operation_id FROM track_publication_attempt WHERE position_id=?`, request.PositionID).Scan(ctx, &active); err == nil {
			return fmt.Errorf("admit publication: position already has an unfinished attempt")
		} else if err != sql.ErrNoRows {
			return fmt.Errorf("admit publication: check active attempt: %w", err)
		}
		if err := reservePublicationNamespace(ctx, tx, request); err != nil {
			return err
		}
		snapshot, _ := json.Marshal(map[string]any{"version": 1, "position_id": request.PositionID.String(), "output_epoch": epoch.String(), "intent_kind": request.IntentKind})
		frozenInputs := append(json.RawMessage(nil), request.FrozenInputs...)
		result, err := client.InsertTx(ctx, tx.Tx, publicationTrackArgs{OperationID: operationID}, nil)
		if err != nil {
			return fmt.Errorf("admit publication: insert River job: %w", err)
		}
		if result == nil || result.Job == nil {
			return fmt.Errorf("admit publication: insert River job returned no job")
		}
		op := &Operation{ID: operationID, Kind: "publish_track_v1", State: "queued", Stage: "queued", InputSnapshot: snapshot, ToolsReadRequired: request.ToolsReadRequired, Attempt: 1, RiverJobID: &result.Job.ID}
		if err := repository.CreateOperationWith(ctx, tx, op); err != nil {
			return fmt.Errorf("admit publication: create operation: %w", err)
		}
		manifest, _ := json.Marshal(struct {
			Version int                        `json:"version"`
			Nodes   []PublicationNamespaceNode `json:"nodes"`
		}{Version: 1, Nodes: request.NamespaceManifest})
		attempt := &TrackPublicationAttempt{OperationID: operationID, PositionID: request.PositionID, Epoch: epoch, ExpectedGeneration: request.ExpectedGeneration, CandidateGeneration: request.CandidateGeneration, IntentKind: request.IntentKind, Phase: "queued", FrozenInputs: frozenInputs, NamespaceManifest: manifest, MutationJournal: json.RawMessage("[]")}
		if _, err := tx.NewInsert().Model(attempt).Exec(ctx); err != nil {
			return fmt.Errorf("admit publication: create attempt: %w", err)
		}
		if pinnedInstallation != nil {
			if _, err := tx.NewRaw(`INSERT INTO operation_tool_read_hold(operation_id, installation_id) VALUES (?, ?)`, operationID, pinnedInstallation.ID).Exec(ctx); err != nil {
				return fmt.Errorf("admit publication: hold pinned ffmpeg installation: %w", err)
			}
		}
		if request.Source != nil {
			source := request.Source
			if source.HoldUUID == uuid.Nil {
				return fmt.Errorf("admit publication: source hold UUID is required")
			}
			if _, err := tx.NewRaw(`INSERT INTO publication_source_hold(operation_id,application_attempt,river_job_id,epoch,hold_uuid,source_root_id,source_location_id,work_id,media_variant_id,configured_path,inventory_path,relative_path,size_bytes,mtime) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, operationID, 1, result.Job.ID, epoch, source.HoldUUID, source.RootID, source.LocationID, source.WorkID, source.MediaVariantID, source.ConfiguredPath, source.InventoryPath, source.RelativePath, source.SizeBytes, source.MTime).Exec(ctx); err != nil {
				return fmt.Errorf("admit publication: create source hold: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return uuid.Nil, err
	}
	return operationID, nil
}

func publicationRewriteMatchesCurrent(current TrackPublication, epoch uuid.UUID, rewrite PublicationRewriteInput) bool {
	if current.Epoch != epoch || current.Generation != rewrite.CurrentGeneration || current.RelativePath != rewrite.RelativePath {
		return false
	}
	var ownership PublicationCurrentOwnership
	if decodePublicationCurrentContract(current.Ownership, &ownership) != nil || ownership.Version != 1 || !reflect.DeepEqual(ownership.Identity, rewrite.Ownership) {
		return false
	}
	var audio PublicationCurrentAudio
	return decodePublicationCurrentContract(current.ActualAudio, &audio) == nil && audio.Version == 1 && audio.Facts.Validate() == nil && reflect.DeepEqual(audio.Facts, rewrite.AudioFacts)
}

func decodePublicationCurrentContract(raw json.RawMessage, destination any) error {
	if len(raw) == 0 || string(raw) == "null" || string(raw) == "{}" {
		return fmt.Errorf("current publication contract is empty")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("current publication contract has trailing data")
	}
	switch contract := destination.(type) {
	case *PublicationCurrentAudio:
		if contract.Version != 1 {
			return fmt.Errorf("unsupported current publication audio version %d", contract.Version)
		}
		if err := contract.Facts.Validate(); err != nil {
			return fmt.Errorf("invalid current publication audio facts: %w", err)
		}
	case *PublicationCurrentOwnership:
		if contract.Version != 1 {
			return fmt.Errorf("unsupported current publication ownership version %d", contract.Version)
		}
		identity := contract.Identity
		if identity.SizeBytes == nil && identity.MTime == nil && identity.SHA256 == "" {
			return fmt.Errorf("current publication ownership identity is required")
		}
		if identity.SizeBytes != nil && *identity.SizeBytes < 0 {
			return fmt.Errorf("current publication ownership size is invalid")
		}
		if identity.SHA256 != "" && !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(identity.SHA256) {
			return fmt.Errorf("current publication ownership digest is invalid")
		}
	}
	return nil
}

func publicationSourceMatchesInput(source PublicationSourceSelection, input PublicationAudioInput) bool {
	return source.RootID == input.RootID && source.LocationID == input.LocationID && source.WorkID == input.WorkID &&
		source.MediaVariantID == input.MediaVariantID && source.ConfiguredPath == input.ConfiguredRootPath &&
		source.InventoryPath == input.InventoryRootPath && source.RelativePath == input.RelativePath &&
		source.SizeBytes == input.SizeBytes && source.MTime.Equal(input.MTime)
}

func validatePublicationToolSelections(selections []SourceAnalysisToolSelection) error {
	if len(selections) != 2 {
		return fmt.Errorf("admit publication: ffmpeg and ffprobe selections are required")
	}
	seen := map[string]bool{}
	var installationID uuid.UUID
	for _, selection := range selections {
		if selection.PackageKind != "ffmpeg" || selection.InstallationID == uuid.Nil || selection.RelativePath == "" || selection.Version == "" || selection.VersionBanner == "" {
			return fmt.Errorf("admit publication: invalid pinned ffmpeg executable selection")
		}
		if selection.Executable != "ffmpeg" && selection.Executable != "ffprobe" || seen[selection.Executable] {
			return fmt.Errorf("admit publication: ffmpeg and ffprobe selections must be unique")
		}
		if installationID != uuid.Nil && installationID != selection.InstallationID {
			return fmt.Errorf("admit publication: ffmpeg executables must use the same installation")
		}
		installationID = selection.InstallationID
		seen[selection.Executable] = true
	}
	if !seen["ffmpeg"] || !seen["ffprobe"] {
		return fmt.Errorf("admit publication: ffmpeg and ffprobe selections are required")
	}
	return nil
}

func validatePublicationInstallation(installation *ToolInstallation, selections []SourceAnalysisToolSelection) error {
	if installation == nil || installation.PackageKind != "ffmpeg" || installation.State != "ready" || installation.VerifiedAt == nil ||
		installation.PlatformGOOS != runtime.GOOS || installation.PlatformGOARCH != runtime.GOARCH ||
		!(tools.Platform{GOOS: installation.PlatformGOOS, GOARCH: installation.PlatformGOARCH}).Supported() || !validInstallationRelativePath(installation) {
		return fmt.Errorf("admit publication: pinned ffmpeg installation is not ready and verified for this platform")
	}
	var versions map[string]string
	if json.Unmarshal(installation.ExecutableVersions, &versions) != nil {
		return fmt.Errorf("admit publication: invalid verified executable versions")
	}
	for _, selection := range selections {
		banner, ok := tools.VerifiedExecutableVersion(versions, tools.PackageFFmpeg, selection.Executable, installation.PlatformGOOS)
		if !ok || filepath.ToSlash(selection.RelativePath) != filepath.ToSlash(installation.RelativePath) ||
			banner != strings.TrimSpace(selection.VersionBanner) || !publicationBannerMatchesVersion(banner, selection.Executable, selection.Version) {
			return fmt.Errorf("admit publication: pinned executable version does not match verified installation metadata")
		}
	}
	return nil
}

func publicationBannerMatchesVersion(banner, executable, version string) bool {
	if strings.TrimSpace(version) == "" {
		return false
	}
	firstLine, _, _ := strings.Cut(banner, "\n")
	fields := strings.Fields(firstLine)
	return len(fields) >= 3 && fields[0] == executable && fields[1] == "version" && fields[2] == version
}

func samePublicationTools(left, right []SourceAnalysisToolSelection) bool {
	a, _ := json.Marshal(left)
	b, _ := json.Marshal(right)
	return string(a) == string(b)
}

func reservePublicationNamespace(ctx context.Context, tx bun.Tx, request PublicationAdmission) error {
	var existing []PublicationNamespaceClaim
	if err := tx.NewSelect().Model(&existing).Where("epoch=?", request.ExpectedEpoch).For("UPDATE").Scan(ctx); err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("admit publication: inspect namespace: %w", err)
	}
	manifest := append([]PublicationNamespaceNode(nil), request.NamespaceManifest...)
	sort.Slice(manifest, func(i, j int) bool { return manifest[i].CanonicalKey < manifest[j].CanonicalKey })
	for _, n := range manifest {
		digest := sha256.Sum256([]byte(n.CanonicalKey))
		var reusable *PublicationNamespaceClaim
		for _, old := range existing {
			if string(old.KeyDigest) == string(digest[:]) && old.CanonicalKey != n.CanonicalKey {
				return fmt.Errorf("admit publication: namespace digest collision")
			}
			if old.CanonicalKey == n.CanonicalKey {
				if n.NodeKind == "dir" && old.NodeKind == "dir" && old.DirectoryScopeKey != nil && *old.DirectoryScopeKey == n.DirectoryScope {
					reusable = &old
					continue
				}
				if n.NodeKind == "file" && old.NodeKind == "file" && old.OwnerPositionID != nil && *old.OwnerPositionID == request.PositionID {
					reusable = &old
					continue
				}
				return fmt.Errorf("admit publication: namespace conflict at %q", n.RelativePath)
			}
			if publicationPrefixConflict(old.CanonicalKey, old.NodeKind, n.CanonicalKey, n.NodeKind) {
				return fmt.Errorf("admit publication: file/directory namespace conflict")
			}
		}
		var owner *uuid.UUID
		var scope *string
		if n.NodeKind == "file" {
			owner = &request.PositionID
		} else {
			scope = &n.DirectoryScope
		}
		claim := reusable
		if claim == nil {
			claim = &PublicationNamespaceClaim{ID: uuid.New(), Epoch: request.ExpectedEpoch, KeyDigest: digest[:], CanonicalKey: n.CanonicalKey, RelativePath: n.RelativePath, NodeKind: n.NodeKind, OwnerPositionID: owner, DirectoryScopeKey: scope}
			if _, err := tx.NewInsert().Model(claim).Exec(ctx); err != nil {
				return fmt.Errorf("admit publication: reserve namespace: %w", err)
			}
			existing = append(existing, *claim)
		}
		reservation := &PublicationNamespaceReservation{NamespaceClaimID: claim.ID, PositionID: request.PositionID, Generation: request.CandidateGeneration}
		if _, err := tx.NewInsert().Model(reservation).Exec(ctx); err != nil {
			return fmt.Errorf("admit publication: reserve generation: %w", err)
		}
	}
	return nil
}

func validatePublicationManifest(nodes []PublicationNamespaceNode) error {
	if len(nodes) == 0 {
		return fmt.Errorf("admit publication: namespace manifest is required")
	}
	ordered := append([]PublicationNamespaceNode(nil), nodes...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].CanonicalKey < ordered[j].CanonicalKey })
	seen := map[string]bool{}
	byKey := make(map[string]PublicationNamespaceNode, len(ordered))
	for _, n := range ordered {
		if !portableRelativePath(n.CanonicalKey) || !portableRelativePath(n.RelativePath) || (n.NodeKind != "file" && n.NodeKind != "dir") || seen[n.CanonicalKey] {
			return fmt.Errorf("admit publication: invalid or duplicate namespace entry")
		}
		if n.NodeKind == "dir" && n.DirectoryScope == "" {
			return fmt.Errorf("admit publication: directory scope is required")
		}
		seen[n.CanonicalKey] = true
		byKey[n.CanonicalKey] = n
	}
	for _, n := range ordered {
		for parent := parentPath(n.CanonicalKey); parent != ""; parent = parentPath(parent) {
			parentNode, ok := byKey[parent]
			if !ok || parentNode.NodeKind != "dir" {
				return fmt.Errorf("admit publication: parent directory %q missing from manifest", parent)
			}
		}
		canonicalParent, relativeParent := parentPath(n.CanonicalKey), parentPath(n.RelativePath)
		if canonicalParent != "" {
			parentNode := byKey[canonicalParent]
			if relativeParent == "" || parentNode.RelativePath != relativeParent {
				return fmt.Errorf("admit publication: namespace parent paths do not correspond")
			}
		} else if relativeParent != "" {
			return fmt.Errorf("admit publication: namespace parent paths do not correspond")
		}
	}
	return nil
}

func portableRelativePath(path string) bool {
	if path == "" || strings.ContainsAny(path, "\\:\x00") || strings.HasPrefix(path, "/") {
		return false
	}
	for _, segment := range strings.Split(path, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

func publicationPrefixConflict(a, aKind, b, bKind string) bool {
	return aKind == "file" && strings.HasPrefix(b, a+"/") || bKind == "file" && strings.HasPrefix(a, b+"/")
}
func parentPath(s string) string {
	i := strings.LastIndexByte(s, '/')
	if i < 0 {
		return ""
	}
	return s[:i]
}
func publicationJSONObject(raw json.RawMessage) bool {
	var value map[string]json.RawMessage
	return json.Unmarshal(raw, &value) == nil && value != nil
}

var _ river.JobArgs = publicationTrackArgs{}
