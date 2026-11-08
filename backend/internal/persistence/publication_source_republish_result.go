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

func validateSourceRepublishResultTx(
	ctx context.Context,
	tx bun.Tx,
	attempt *TrackPublicationAttempt,
	input PublicationInputs,
	observation PublicationStageObservation,
	installed PublicationCurrentOwnership,
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
	if err := validateSourceRepublishTagsAndAssets(actualMetadata.Tags, assets, input); err != nil {
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
	coverProjection, err := sourceRepublishCoverProjectionTx(ctx, tx, attempt, republish.Cover, assets.Cover)
	if err != nil {
		return nil, nil, err
	}
	lyricsProjection, err := sourceRepublishLyricsProjection(republish, attempt.CandidateGeneration)
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

func validateSourceRepublishTagsAndAssets(tags map[string][]string, assets PublicationAssetOutcomes, input PublicationInputs) error {
	republish := input.SourceRepublish
	wantTags := make(map[string][]string, len(input.DesiredTags)+1)
	for key, values := range input.DesiredTags {
		if !publicationTagIsLyrics(key) {
			wantTags[key] = slices.Clone(values)
		}
	}
	if republish.Lyrics.State == "value" {
		wantTags["LYRICS"] = []string{republish.Lyrics.Text}
		if assets.Lyrics.State != PublicationAssetEmbedded || assets.Lyrics.SHA256 != republish.Lyrics.SHA256 || assets.Lyrics.Format != "lrc" {
			return errors.New("source-republish selected lyrics were not embedded exactly")
		}
	} else if assets.Lyrics.State != PublicationAssetAbsent {
		return errors.New("source-republish result contains unselected lyrics")
	}
	if !sameJSONValue(mustPublicationJSON(tags), mustPublicationJSON(wantTags)) {
		return errors.New("source-republish result mandatory or selected lyrics tags differ from frozen inputs")
	}
	switch republish.Cover.Derivation {
	case "absent":
		if assets.Cover.State != PublicationAssetAbsent {
			return errors.New("source-republish result contains an unselected cover")
		}
	case "ready":
		cover := republish.Cover
		if assets.Cover.State != PublicationAssetEmbedded || assets.Cover.SHA256 != cover.SHA256 || assets.Cover.MIME != "image/png" || assets.Cover.SizeBytes == nil || *assets.Cover.SizeBytes != cover.SizeBytes {
			return errors.New("source-republish selected cover derivative was not embedded exactly")
		}
	default:
		return errors.New("source-republish cover outcome is not supported for result commit")
	}
	return nil
}

func publicationTagIsLyrics(key string) bool {
	return strings.EqualFold(strings.TrimSpace(key), "LYRICS")
}

func sourceRepublishLyricsProjection(input *PublicationSourceRepublishInput, generation uuid.UUID) (*PublicationInitialLyricsProjection, error) {
	if input == nil || input.Lyrics.State != "value" || input.Lyrics.Origin != "source" && input.Lyrics.Origin != "lrclib" {
		return nil, nil
	}
	projection := &PublicationInitialLyricsProjection{
		Version: PublicationInitialLyricsVersion, Generation: generation, Source: input.Lyrics.Origin,
		SHA256: input.Lyrics.SHA256, Outcome: PublicationOptionalLyricsExactRoundTrip,
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
) (*PublicationCoverEmbeddingProjection, error) {
	var attemptReleaseID uuid.UUID
	if err := tx.NewRaw(`SELECT release_id FROM release_track WHERE id=? FOR UPDATE`, attempt.PositionID).Scan(ctx, &attemptReleaseID); err != nil {
		return nil, fmt.Errorf("lock source-republish release identity: %w", err)
	}
	if attemptReleaseID != frozen.ReleaseID {
		return nil, fmt.Errorf("source-republish cover release differs from attempt position: %w", ErrPublicationAdmissionConflict)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO release_publication_cover(release_id) VALUES (?) ON CONFLICT (release_id) DO NOTHING`, frozen.ReleaseID); err != nil {
		return nil, fmt.Errorf("materialize source-republish cover authority baseline: %w", err)
	}
	var lockedRelease uuid.UUID
	if err := tx.NewRaw(`SELECT release_id FROM release_publication_cover WHERE release_id=? FOR UPDATE`, frozen.ReleaseID).Scan(ctx, &lockedRelease); err != nil {
		return nil, fmt.Errorf("lock source-republish cover authority: %w", err)
	}
	cover, err := readReleaseCover(ctx, tx, frozen.ReleaseID)
	if err != nil {
		return nil, fmt.Errorf("read source-republish cover authority: %w", err)
	}
	if cover.PendingGeneration != nil || cover.AcquisitionOwner != nil || cover.Revision != frozen.AuthorityRevision || cover.Mode != frozen.AuthorityMode {
		return nil, fmt.Errorf("source-republish cover authority changed or is pending: %w", ErrPublicationAdmissionConflict)
	}
	if frozen.Derivation == "absent" {
		if cover.SelectedGeneration != nil || actual.State != PublicationAssetAbsent {
			return nil, errors.New("source-republish absent cover does not match selected authority or installed assets")
		}
		return nil, nil
	}
	if frozen.Derivation != "ready" || cover.SelectedGeneration == nil || *cover.SelectedGeneration != frozen.SelectedGeneration ||
		cover.SelectedMIME == nil || *cover.SelectedMIME != frozen.OriginalMIME || cover.SelectedSize == nil || *cover.SelectedSize != *frozen.OriginalSizeBytes ||
		hex.EncodeToString(cover.SelectedSHA256) != frozen.OriginalSHA256 {
		return nil, fmt.Errorf("source-republish selected cover authority changed: %w", ErrPublicationAdmissionConflict)
	}
	if actual.State != PublicationAssetEmbedded || actual.SHA256 != frozen.SHA256 || actual.MIME != frozen.MIME || actual.SizeBytes == nil || *actual.SizeBytes != frozen.SizeBytes {
		return nil, errors.New("source-republish cover derivative outcome is not verified")
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

func recordSourceRepublishManualLyricsTx(
	ctx context.Context,
	tx bun.Tx,
	repository *SetupManagerRepository,
	attempt *TrackPublicationAttempt,
	input *PublicationSourceRepublishInput,
	observation PublicationStageObservation,
) error {
	if input.LyricsAuthority.Desired == nil || input.LyricsAuthority.Desired.Authority != librarymetadata.AuthorityManual {
		return nil
	}
	manual := PublicationManualLyricsInput{
		ExpectedMetadataRevision:   input.LyricsAuthority.ExpectedMetadataRevision,
		ConfirmedCurrentGeneration: input.Current.Generation,
	}
	desired := input.LyricsAuthority.Desired
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
