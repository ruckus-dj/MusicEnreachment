package persistence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
	"github.com/uptrace/bun"
)

type PublicationSourceRepublishLyricsOutcome string

const (
	PublicationSourceRepublishLyricsNotRequested         PublicationSourceRepublishLyricsOutcome = "not_requested"
	PublicationSourceRepublishLyricsExactRoundTrip       PublicationSourceRepublishLyricsOutcome = "exact_roundtrip"
	PublicationSourceRepublishLyricsRebuiltWithoutLyrics PublicationSourceRepublishLyricsOutcome = "rebuilt_without_lyrics"
)

type PublicationSourceRepublishCoverOutcome string

const (
	PublicationSourceRepublishCoverNotRequested      PublicationSourceRepublishCoverOutcome = "not_requested"
	PublicationSourceRepublishCoverVerified          PublicationSourceRepublishCoverOutcome = "verified"
	PublicationSourceRepublishCoverPreparationFailed PublicationSourceRepublishCoverOutcome = "preparation_failed"
	PublicationSourceRepublishCoverWriteFailed       PublicationSourceRepublishCoverOutcome = "write_failed"
	PublicationSourceRepublishCoverReadFailed        PublicationSourceRepublishCoverOutcome = "read_failed"
	PublicationSourceRepublishCoverMismatch          PublicationSourceRepublishCoverOutcome = "mismatch"
)

// PublicationSourceRepublishOptionalResult is the transient worker-to-persistence
// receipt for optional outcomes; it is not stored as a durable journal.
type PublicationSourceRepublishOptionalResult struct {
	LyricsOutcome PublicationSourceRepublishLyricsOutcome `json:"lyrics_outcome"`
	CoverOutcome  PublicationSourceRepublishCoverOutcome  `json:"cover_outcome"`
}

func validateSourceRepublishResultTx(
	ctx context.Context,
	tx bun.Tx,
	attempt *TrackPublicationAttempt,
	input PublicationInputs,
	observation PublicationStageObservation,
	installed PublicationCurrentOwnership,
	receipt PublicationSourceRepublishOptionalResult,
) (*PublicationInitialLyricsProjection, *PublicationCoverEmbeddingProjection, error) {
	republish := input.SourceRepublish
	if !validSourceRepublishPreparation(input) || attempt.ExpectedGeneration == nil ||
		*attempt.ExpectedGeneration != republish.Current.Generation || attempt.CandidateGeneration == uuid.Nil {
		return nil, nil, errors.New("source-republish result has no valid CURRENT baseline")
	}
	if observation.PayloadSHA256 == "" || installed.Identity.SHA256 != observation.PayloadSHA256 ||
		observation.Ownership.ExpectedSize == nil || installed.Identity.SizeBytes == nil || *observation.Ownership.ExpectedSize != *installed.Identity.SizeBytes ||
		observation.Ownership.ExpectedMTime == nil || installed.Identity.MTime == nil || *observation.Ownership.ExpectedMTime != *installed.Identity.MTime {
		return nil, nil, errors.New("source-republish installed ownership does not match actual candidate evidence")
	}
	if observation.QualityPolicyVersion != input.QualityPolicyVersion {
		return nil, nil, errors.New("source-republish quality policy differs from frozen inputs")
	}
	if err := validateSourceRepublishAudio(observation.ActualAudio, input); err != nil {
		return nil, nil, err
	}
	var actualMetadata PublicationCommitMetadata
	if err := strictMetadata(observation.ActualMetadata, &actualMetadata); err != nil || actualMetadata.Version != 1 {
		return nil, nil, errors.New("source-republish result metadata is invalid")
	}
	assets, err := DecodePublicationAssetOutcomes(observation.ActualAssets)
	if err != nil {
		return nil, nil, fmt.Errorf("decode source-republish result assets: %w", err)
	}
	if err := validateSourceRepublishTagsAndAssets(actualMetadata.Tags, assets, input, receipt); err != nil {
		return nil, nil, err
	}
	lyricsCurrent, err := validatePublicationLyricsCurrentAuthority(ctx, tx, attempt.PositionID,
		republish.LyricsAuthority.ExpectedMetadataRevision, republish.LyricsAuthority.Desired)
	if err != nil {
		return nil, nil, fmt.Errorf("validate source-republish lyrics authority: %w", err)
	}
	if !lyricsCurrent {
		return nil, nil, fmt.Errorf("source-republish lyrics authority changed: %w", ErrPublicationAdmissionConflict)
	}
	coverProjection, err := sourceRepublishCoverProjectionTx(ctx, tx, attempt, republish.Cover, assets.Cover, receipt.CoverOutcome)
	if err != nil {
		return nil, nil, err
	}
	lyricsProjection, err := sourceRepublishLyricsProjection(republish, attempt.CandidateGeneration, receipt.LyricsOutcome)
	if err != nil {
		return nil, nil, err
	}
	return lyricsProjection, coverProjection, nil
}

func validateSourceRepublishAudio(raw json.RawMessage, input PublicationInputs) error {
	facts, ok := publicationAudioFactsFromObservation(raw)
	if !ok {
		return errors.New("source-republish result audio facts are invalid")
	}
	plan, err := publication.PlanAudioTransform(input.Format, input.Audio.AudioFacts.AudioSourceFacts())
	if err != nil {
		return fmt.Errorf("plan source-republish result audio: %w", err)
	}
	wantCodec := input.Audio.AudioFacts.Codec
	switch plan.Action {
	case publication.AudioCodecALAC:
		wantCodec = "alac"
	case publication.AudioCodecAAC:
		wantCodec = "aac"
	}
	if facts.Codec != wantCodec || facts.Container != plan.OutputContainer {
		return errors.New("source-republish result audio does not match the frozen transform plan")
	}
	want := input.Audio.AudioFacts
	wantLossless := want.Lossless != nil && *want.Lossless
	if plan.Action == publication.AudioCodecAAC {
		wantLossless = false
	}
	if want.Channels != nil && (facts.Channels == nil || *facts.Channels != *want.Channels) ||
		want.SampleRate != nil && (facts.SampleRate == nil || *facts.SampleRate != *want.SampleRate) ||
		facts.Lossless == nil || *facts.Lossless != wantLossless ||
		(plan.Action == publication.AudioCodecCopy || plan.Action == publication.AudioCodecALAC) &&
			(want.BitDepth != nil && (facts.BitDepth == nil || *facts.BitDepth != *want.BitDepth) ||
				want.ChannelLayout != "" && facts.ChannelLayout != want.ChannelLayout) {
		return errors.New("source-republish result audio shape differs from the frozen source plan")
	}
	return nil
}

func validateSourceRepublishTagsAndAssets(tags map[string][]string, assets PublicationAssetOutcomes, input PublicationInputs, receipt PublicationSourceRepublishOptionalResult) error {
	republish := input.SourceRepublish
	wantTags := make(map[string][]string, len(input.DesiredTags)+1)
	for key, values := range input.DesiredTags {
		if !publicationTagIsLyrics(key) {
			wantTags[key] = slices.Clone(values)
		}
	}
	if republish.Lyrics.State == "value" {
		switch receipt.LyricsOutcome {
		case PublicationSourceRepublishLyricsExactRoundTrip:
			wantTags["LYRICS"] = []string{republish.Lyrics.Text}
			if assets.Lyrics.State != PublicationAssetEmbedded || assets.Lyrics.SHA256 != republish.Lyrics.SHA256 || assets.Lyrics.Format != "lrc" {
				return errors.New("source-republish selected lyrics were not embedded exactly")
			}
		case PublicationSourceRepublishLyricsRebuiltWithoutLyrics:
			if assets.Lyrics.State != PublicationAssetAbsent {
				return errors.New("source-republish fallback lyrics result must observe absence")
			}
		default:
			return errors.New("source-republish selected lyrics receipt is invalid")
		}
	} else if receipt.LyricsOutcome != PublicationSourceRepublishLyricsNotRequested || assets.Lyrics.State != PublicationAssetAbsent {
		return errors.New("source-republish absent lyrics require an absent, not-requested result")
	}
	if !sameJSONValue(mustPublicationJSON(tags), mustPublicationJSON(wantTags)) {
		return errors.New("source-republish result mandatory or selected lyrics tags differ from frozen inputs")
	}
	switch republish.Cover.Derivation {
	case "absent":
		if receipt.CoverOutcome != PublicationSourceRepublishCoverNotRequested || assets.Cover.State != PublicationAssetAbsent {
			return errors.New("source-republish absent cover requires an absent, not-requested result")
		}
	case "optional_failure":
		if receipt.CoverOutcome != PublicationSourceRepublishCoverPreparationFailed || assets.Cover.State != PublicationAssetAbsent {
			return errors.New("source-republish optional cover failure receipt does not match frozen preparation failure")
		}
	case "ready":
		cover := republish.Cover
		switch receipt.CoverOutcome {
		case PublicationSourceRepublishCoverVerified:
			if assets.Cover.State != PublicationAssetEmbedded || assets.Cover.SHA256 != cover.SHA256 || assets.Cover.MIME != "image/png" || assets.Cover.SizeBytes == nil || *assets.Cover.SizeBytes != cover.SizeBytes {
				return errors.New("source-republish selected cover derivative was not embedded exactly")
			}
		case PublicationSourceRepublishCoverWriteFailed, PublicationSourceRepublishCoverReadFailed, PublicationSourceRepublishCoverMismatch:
			if assets.Cover.State != PublicationAssetAbsent {
				return errors.New("failed source-republish cover result must observe cover absence")
			}
		default:
			return errors.New("source-republish selected cover receipt is invalid")
		}
	default:
		return errors.New("source-republish cover outcome is not supported for result commit")
	}
	return nil
}

func publicationTagIsLyrics(key string) bool {
	return strings.EqualFold(strings.TrimSpace(key), "LYRICS")
}

func sourceRepublishLyricsProjection(input *PublicationSourceRepublishInput, generation uuid.UUID, outcome PublicationSourceRepublishLyricsOutcome) (*PublicationInitialLyricsProjection, error) {
	if input == nil || input.Lyrics.State != "value" || input.Lyrics.Origin == "none" {
		return nil, nil
	}
	projectionOutcome := PublicationOptionalLyricsExactRoundTrip
	warning := ""
	if outcome == PublicationSourceRepublishLyricsRebuiltWithoutLyrics {
		projectionOutcome = PublicationOptionalLyricsRebuiltWithoutLyrics
		warning = "readback_mismatch"
	}
	projection := &PublicationInitialLyricsProjection{
		Version: PublicationInitialLyricsVersion, Generation: generation, Source: input.Lyrics.Origin,
		SHA256: input.Lyrics.SHA256, Outcome: string(projectionOutcome), OutcomeWarningCode: warning,
		AuthorityMetadataRevision: input.LyricsAuthority.ExpectedMetadataRevision,
		AuthorityState:            string(librarymetadata.StateAbsent), Authority: string(librarymetadata.AuthorityAutomatic),
		AuthorityProvenance: string(librarymetadata.ProvenanceSourceMetadata),
	}
	if desired := input.LyricsAuthority.Desired; desired != nil {
		projection.AuthorityState = string(desired.State)
		projection.Authority = string(desired.Authority)
		projection.AuthorityProvenance = string(desired.Provenance.Kind)
		frozen, err := json.Marshal(desired)
		if err != nil {
			return nil, fmt.Errorf("encode source-republish lyrics authority projection: %w", err)
		}
		digest := sha256.Sum256(frozen)
		projection.AuthorityFingerprintSHA256 = hex.EncodeToString(digest[:])
	}
	return projection, nil
}

func sourceRepublishCoverProjectionTx(
	ctx context.Context,
	tx bun.Tx,
	attempt *TrackPublicationAttempt,
	frozen PublicationRepublishCover,
	actual PublicationCoverOutcome,
	outcome PublicationSourceRepublishCoverOutcome,
) (*PublicationCoverEmbeddingProjection, error) {
	coverCurrent, err := validateSourceRepublishCoverAuthorityTx(ctx, tx, attempt.PositionID, frozen, true)
	if err != nil {
		return nil, fmt.Errorf("validate source-republish cover authority: %w", err)
	}
	if !coverCurrent {
		return nil, fmt.Errorf("source-republish cover authority changed or is pending: %w", ErrPublicationAdmissionConflict)
	}
	if frozen.Derivation == "absent" {
		if actual.State != PublicationAssetAbsent || outcome != PublicationSourceRepublishCoverNotRequested {
			return nil, errors.New("source-republish absent cover does not match selected authority or installed assets")
		}
		return nil, nil
	}
	if frozen.Derivation == "optional_failure" {
		if actual.State != PublicationAssetAbsent || outcome != PublicationSourceRepublishCoverPreparationFailed {
			return nil, errors.New("source-republish preparation failure does not match selected cover result")
		}
		projection := &PublicationCoverEmbeddingProjection{
			Version: PublicationCoverEmbeddingDerivativeProjectionVersion, Generation: attempt.CandidateGeneration,
			ReceiptOperationID: attempt.OperationID, ReceiptReleaseID: frozen.ReleaseID, ReceiptRevision: frozen.AuthorityRevision,
			ReceiptMode: frozen.AuthorityMode, RequestedGeneration: frozen.SelectedGeneration,
			RequestedSHA256: frozen.OriginalSHA256, Outcome: string(outcome), WarningCode: frozen.Failure,
		}
		if err := projection.Validate(); err != nil {
			return nil, fmt.Errorf("validate source-republish preparation failure projection: %w", err)
		}
		return projection, nil
	}
	if outcome != PublicationSourceRepublishCoverVerified {
		if actual.State != PublicationAssetAbsent || outcome != PublicationSourceRepublishCoverWriteFailed && outcome != PublicationSourceRepublishCoverReadFailed && outcome != PublicationSourceRepublishCoverMismatch {
			return nil, errors.New("source-republish cover failure receipt does not match actual absence")
		}
		projection := &PublicationCoverEmbeddingProjection{
			Version: PublicationCoverEmbeddingProjectionVersion, Generation: attempt.CandidateGeneration,
			ReceiptOperationID: attempt.OperationID, ReceiptReleaseID: frozen.ReleaseID, ReceiptRevision: frozen.AuthorityRevision,
			ReceiptMode: frozen.AuthorityMode, RequestedGeneration: frozen.SelectedGeneration,
			RequestedSHA256: frozen.OriginalSHA256, Outcome: string(outcome), WarningCode: coverOutcomeWarning(string(outcome)),
		}
		if err := projection.Validate(); err != nil {
			return nil, fmt.Errorf("validate source-republish failed cover projection: %w", err)
		}
		return projection, nil
	}
	if actual.State != PublicationAssetEmbedded || actual.SHA256 != frozen.SHA256 || actual.MIME != frozen.MIME || actual.SizeBytes == nil || *actual.SizeBytes != frozen.SizeBytes {
		return nil, errors.New("source-republish verified cover receipt does not match actual derivative")
	}
	projection := &PublicationCoverEmbeddingProjection{
		Version: PublicationCoverEmbeddingDerivativeProjectionVersion, Generation: attempt.CandidateGeneration,
		ReceiptOperationID: attempt.OperationID, ReceiptReleaseID: frozen.ReleaseID, ReceiptRevision: frozen.AuthorityRevision,
		ReceiptMode: frozen.AuthorityMode, RequestedGeneration: frozen.SelectedGeneration, RequestedSHA256: frozen.OriginalSHA256,
		EmbeddedSHA256: frozen.SHA256, EmbeddedMIME: frozen.MIME, EmbeddedSize: int64Pointer(frozen.SizeBytes),
		Outcome: "verified",
	}
	if err := projection.Validate(); err != nil {
		return nil, fmt.Errorf("validate source-republish cover projection: %w", err)
	}
	return projection, nil
}

func int64Pointer(value int64) *int64 { return &value }

func coverOutcomeWarning(outcome string) string {
	switch outcome {
	case "write_failed":
		return "cover_write_failed"
	case "read_failed":
		return "cover_read_failed"
	case "mismatch":
		return "cover_mismatch"
	default:
		return ""
	}
}

func recordSourceRepublishManualLyricsTx(
	ctx context.Context,
	tx bun.Tx,
	repository *SetupManagerRepository,
	attempt *TrackPublicationAttempt,
	input *PublicationSourceRepublishInput,
	observation PublicationStageObservation,
	receipt PublicationSourceRepublishOptionalResult,
) error {
	if input.LyricsAuthority.Desired == nil || input.LyricsAuthority.Desired.Authority != librarymetadata.AuthorityManual {
		return nil
	}
	manual := PublicationManualLyricsInput{
		ExpectedMetadataRevision:   input.LyricsAuthority.ExpectedMetadataRevision,
		ConfirmedCurrentGeneration: input.Current.Generation,
	}
	desired := input.LyricsAuthority.Desired
	if input.Lyrics.State == "value" && receipt.LyricsOutcome == PublicationSourceRepublishLyricsRebuiltWithoutLyrics {
		return nil // The desired manual receipt remains pending after a read-back fallback.
	}
	if desired.State == librarymetadata.StateAbsent {
		assets, err := DecodePublicationAssetOutcomes(observation.ActualAssets)
		if err != nil {
			return fmt.Errorf("decode source-republish manual-absent assets: %w", err)
		}
		if input.Lyrics.State != "absent" || input.Lyrics.Origin != "none" ||
			receipt.LyricsOutcome != PublicationSourceRepublishLyricsNotRequested || assets.Lyrics.State != PublicationAssetAbsent {
			return errors.New("source-republish saved-absent manual lyrics require validated actual absence")
		}
		return nil // A saved-absent manual choice is a no-op, not explicit-empty.
	}
	switch desired.State {
	case librarymetadata.StateValue:
		value, ok := desired.Value.(string)
		if !ok || input.Lyrics.Origin != "manual" || input.Lyrics.State != "value" || value != input.Lyrics.Text {
			return errors.New("source-republish manual lyrics value does not match frozen authority")
		}
		manual.State, manual.Value = "value", value
	case librarymetadata.StateExplicitEmpty:
		if input.Lyrics.State != "absent" || input.Lyrics.Origin != "none" {
			return errors.New("source-republish explicit-empty lyrics conflict with frozen selection")
		}
		manual.State = "explicit_empty"
	default:
		return errors.New("source-republish manual lyrics authority state is unsupported")
	}
	return repository.recordPublicationResultLyricsTx(ctx, tx, attempt, observation, manual)
}
