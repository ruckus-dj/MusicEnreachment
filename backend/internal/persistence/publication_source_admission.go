package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
	"github.com/uptrace/bun"
)

// validateAndLockPublicationSource takes source rows only after the global
// fingerprint mutation gate and before tools installation/epoch row locks.
func validateAndLockPublicationSource(ctx context.Context, tx bun.IDB, position uuid.UUID, source PublicationSourceSelection, frozen PublicationAudioInput) error {
	var root SourceRoot
	if err := tx.NewSelect().Model(&root).Where("id=?", source.RootID).For("UPDATE").Scan(ctx); err != nil {
		return fmt.Errorf("admit publication: lock source root: %w", err)
	}
	if !root.Enabled || root.Status != SourceRootStatusAvailable || root.ConfiguredPath != frozen.ConfiguredRootPath || root.InventoryPath == nil || *root.InventoryPath != frozen.InventoryRootPath {
		return fmt.Errorf("admit publication: source root is disabled or changed")
	}
	var location SourceLocation
	if err := tx.NewSelect().Model(&location).Where("id=? AND source_root_id=?", source.LocationID, source.RootID).For("UPDATE").Scan(ctx); err != nil {
		return fmt.Errorf("admit publication: lock source location: %w", err)
	}
	if location.RelativePath != frozen.RelativePath || location.SizeBytes != frozen.SizeBytes || !location.Mtime.Equal(frozen.MTime) || location.MediaVariantID == nil || *location.MediaVariantID != frozen.MediaVariantID {
		return fmt.Errorf("admit publication: source location changed")
	}
	var work SourceAnalysisWork
	if err := tx.NewSelect().Model(&work).Where("id=?", source.WorkID).For("UPDATE").Scan(ctx); err != nil {
		return fmt.Errorf("admit publication: lock source work: %w", err)
	}
	if work.CurrentLocationID == nil || *work.CurrentLocationID != source.LocationID || work.LocationID != source.LocationID || work.SourceRootID != source.RootID || work.ConfiguredPath != frozen.ConfiguredRootPath || work.InventoryPath != frozen.InventoryRootPath || work.RelativePath != frozen.RelativePath || work.SizeBytes != frozen.SizeBytes || !work.Mtime.Equal(frozen.MTime) {
		return fmt.Errorf("admit publication: source work changed")
	}
	var canonical uuid.UUID
	if err := tx.NewRaw(`SELECT media_variant_id FROM source_work_variant_identity WHERE work_id=? FOR UPDATE`, source.WorkID).Scan(ctx, &canonical); err != nil {
		return fmt.Errorf("admit publication: lock canonical source variant: %w", err)
	}
	if canonical != frozen.MediaVariantID {
		return fmt.Errorf("admit publication: canonical source variant changed")
	}
	var authorized bool
	if err := tx.NewRaw(`SELECT EXISTS(SELECT 1 FROM release_track position JOIN track_source association ON association.track_id=position.track_id JOIN source_work_variant_identity identity ON identity.media_variant_id=association.media_variant_id WHERE position.id=? AND identity.work_id=? AND association.media_variant_id=? AND association.selected=true)`, position, source.WorkID, canonical).Scan(ctx, &authorized); err != nil {
		return fmt.Errorf("admit publication: verify track source association: %w", err)
	}
	if !authorized {
		return fmt.Errorf("admit publication: source variant is not selected for this position")
	}
	var selectedProbe *uuid.UUID
	if err := tx.NewRaw(`SELECT success_probe_variant_id FROM source_analysis_step WHERE work_id=? AND step='probe' AND state='succeeded'`, source.WorkID).Scan(ctx, &selectedProbe); err != nil {
		return fmt.Errorf("admit publication: read selected successful probe: %w", err)
	}
	if selectedProbe == nil || *selectedProbe != frozen.ProbeResultID {
		return fmt.Errorf("admit publication: probe result is not the selected successful probe")
	}
	var probe SourceMediaVariant
	if err := tx.NewRaw(`SELECT result.* FROM source_analysis_step step JOIN media_variant result ON result.id=step.success_probe_variant_id
		WHERE step.work_id=? AND step.step='probe' AND step.state='succeeded' AND step.success_probe_variant_id=? FOR UPDATE OF step,result`, source.WorkID, *selectedProbe).Scan(ctx, &probe); err != nil {
		return fmt.Errorf("admit publication: lock selected probe result: %w", err)
	}
	if probe.SizeBytes != frozen.SizeBytes || probe.AnalysisPolicyVersion == nil || *probe.AnalysisPolicyVersion != frozen.AnalysisPolicyVersion || probe.FFProbeVersion == nil || *probe.FFProbeVersion != frozen.FFProbeVersion {
		return fmt.Errorf("admit publication: selected probe provenance does not match frozen analysis")
	}
	actual, err := publicationAudioFactsFromProbe(probe, frozen.AudioFacts.StreamIndex)
	if err != nil {
		return fmt.Errorf("admit publication: verify selected probe result: %w", err)
	}
	if !reflect.DeepEqual(actual, frozen.AudioFacts) {
		return fmt.Errorf("admit publication: frozen audio facts do not match selected probe result")
	}
	return nil
}

type publicationFFProbeDocument struct {
	Format struct {
		FormatName string `json:"format_name"`
	} `json:"format"`
	Streams []struct {
		Index         json.Number `json:"index"`
		CodecType     string      `json:"codec_type"`
		CodecName     string      `json:"codec_name"`
		Channels      json.Number `json:"channels"`
		Bitrate       json.Number `json:"bit_rate"`
		RawSampleBits json.Number `json:"bits_per_raw_sample"`
		SampleBits    json.Number `json:"bits_per_sample"`
		SampleFormat  string      `json:"sample_fmt"`
	} `json:"streams"`
}

func publicationAudioFactsFromProbe(probe SourceMediaVariant, selectedIndex int) (PersistedAudioFacts, error) {
	if probe.AnalysisPolicyVersion == nil || *probe.AnalysisPolicyVersion <= 0 || probe.FFProbeVersion == nil || strings.TrimSpace(*probe.FFProbeVersion) == "" || len(probe.FFProbeJSON) == 0 || probe.InspectedAt == nil || probe.InspectedAt.IsZero() || probe.AppliedOperationID == nil || *probe.AppliedOperationID == uuid.Nil || probe.AudioStreamCount == nil {
		return PersistedAudioFacts{}, fmt.Errorf("probe provenance is incomplete")
	}
	decoder := json.NewDecoder(strings.NewReader(string(probe.FFProbeJSON)))
	decoder.UseNumber()
	var document publicationFFProbeDocument
	if err := decoder.Decode(&document); err != nil {
		return PersistedAudioFacts{}, fmt.Errorf("decode ffprobe JSON: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return PersistedAudioFacts{}, fmt.Errorf("ffprobe JSON has trailing data")
	}
	var selected *struct {
		Index                                        json.Number
		CodecType, CodecName                         string
		Channels, Bitrate, RawSampleBits, SampleBits json.Number
		SampleFormat                                 string
	}
	seen := make(map[int]bool)
	audioCount := 0
	for i := range document.Streams {
		stream := &document.Streams[i]
		index, ok := strictProbeNumber(stream.Index)
		if !ok || index > int64(^uint(0)>>1) || index < 0 {
			return PersistedAudioFacts{}, fmt.Errorf("ffprobe stream index is invalid")
		}
		if seen[int(index)] {
			return PersistedAudioFacts{}, fmt.Errorf("ffprobe stream indexes are not unique")
		}
		seen[int(index)] = true
		if stream.CodecType == "audio" {
			audioCount++
		}
		if stream.CodecType == "audio" && int(index) == selectedIndex {
			if selected != nil {
				return PersistedAudioFacts{}, fmt.Errorf("ffprobe result has ambiguous selected audio stream")
			}
			selected = &struct {
				Index                                        json.Number
				CodecType, CodecName                         string
				Channels, Bitrate, RawSampleBits, SampleBits json.Number
				SampleFormat                                 string
			}{stream.Index, stream.CodecType, stream.CodecName, stream.Channels, stream.Bitrate, stream.RawSampleBits, stream.SampleBits, stream.SampleFormat}
		}
	}
	if audioCount != *probe.AudioStreamCount {
		return PersistedAudioFacts{}, fmt.Errorf("ffprobe audio stream count does not match persisted result")
	}
	if selected == nil {
		return PersistedAudioFacts{}, fmt.Errorf("ffprobe result has no unique selected audio stream")
	}
	index, _ := strictProbeNumber(selected.Index)
	codec := strings.ToLower(strings.TrimSpace(selected.CodecName))
	lossless, ok := publicationCodecLossless(codec, selected.SampleFormat)
	if !ok {
		return PersistedAudioFacts{}, fmt.Errorf("ffprobe codec losslessness is ambiguous")
	}
	facts := PersistedAudioFacts{StreamIndex: int(index), Codec: codec, Lossless: &lossless}
	if n, ok := strictProbeNumber(selected.Channels); ok && n > 0 && n <= int64(^uint(0)>>1) {
		v := int(n)
		facts.Channels = &v
	} else if selected.Channels != "" {
		return PersistedAudioFacts{}, fmt.Errorf("ffprobe channels are invalid")
	}
	if selected.Bitrate != "" {
		n, ok := strictProbeNumber(selected.Bitrate)
		if !ok || n < 0 || n > int64(^uint(0)>>1) {
			return PersistedAudioFacts{}, fmt.Errorf("ffprobe bitrate is invalid")
		}
		v := int(n)
		facts.Bitrate = &v
	}
	depth, err := publicationProbeBitDepth(selected.RawSampleBits, selected.SampleBits)
	if err != nil {
		return PersistedAudioFacts{}, err
	}
	facts.BitDepth = depth
	container, err := publicationContainerFromProbe(document.Format.FormatName)
	if err != nil {
		return PersistedAudioFacts{}, err
	}
	facts.Container = container
	facts.SupportedSourceContainer = publicationSourceCopySupported(container, codec)
	return facts, facts.Validate()
}

func publicationProbeBitDepth(rawSampleBits, sampleBits json.Number) (*int, error) {
	parse := func(value json.Number) (int64, error) {
		n, ok := strictProbeNumber(value)
		if !ok || n < 0 || n > int64(^uint(0)>>1) {
			return 0, fmt.Errorf("ffprobe sample depth is invalid")
		}
		return n, nil
	}
	if rawSampleBits != "" {
		n, err := parse(rawSampleBits)
		if err != nil {
			return nil, err
		}
		if n > 0 {
			depth := int(n)
			return &depth, nil
		}
	}
	if sampleBits == "" {
		return nil, nil
	}
	n, err := parse(sampleBits)
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, nil
	}
	depth := int(n)
	return &depth, nil
}

func strictProbeNumber(value json.Number) (int64, bool) {
	if value == "" {
		return 0, false
	}
	n, err := strconv.ParseInt(string(value), 10, 64)
	return n, err == nil
}

func publicationContainerFromProbe(names string) (publication.AudioContainer, error) {
	for _, name := range strings.Split(strings.ToLower(names), ",") {
		switch strings.TrimSpace(name) {
		case "mp3":
			return publication.AudioContainerMP3, nil
		case "flac":
			return publication.AudioContainerFLAC, nil
		case "wav":
			return publication.AudioContainerWAV, nil
		case "ogg":
			return publication.AudioContainerOGG, nil
		case "opus":
			return publication.AudioContainerOpus, nil
		case "aac":
			return publication.AudioContainerAAC, nil
		case "mov":
			return publication.AudioContainerM4A, nil
		case "matroska":
			return publication.AudioContainerMKA, nil
		}
	}
	return "", fmt.Errorf("ffprobe container is unsupported or ambiguous")
}

func publicationSourceCopySupported(container publication.AudioContainer, codec string) bool {
	switch container {
	case publication.AudioContainerFLAC:
		return codec == "flac"
	case publication.AudioContainerMP3:
		return codec == "mp3"
	case publication.AudioContainerWAV:
		return strings.HasPrefix(codec, "pcm_") && (strings.HasSuffix(codec, "le") || strings.HasSuffix(codec, "be"))
	case publication.AudioContainerOGG:
		return codec == "vorbis"
	case publication.AudioContainerOpus:
		return codec == "opus"
	case publication.AudioContainerAAC:
		return codec == "aac"
	case publication.AudioContainerM4A:
		return codec == "aac" || codec == "alac"
	case publication.AudioContainerMKA:
		return codec == "flac" || codec == "opus" || codec == "vorbis" || codec == "aac" || codec == "mp3" || codec == "alac"
	default:
		return false
	}
}

func publicationCodecLossless(codec, sampleFormat string) (bool, bool) {
	switch codec {
	case "flac", "alac":
		return true, true
	case "mp3", "aac", "vorbis", "opus", "ac3", "eac3":
		return false, true
	case "pcm_s16le", "pcm_s24le", "pcm_s32le":
		return true, true
	default:
		return false, false
	}
}

// CheckPublicationSourceHoldForDelivery validates a source hold before any
// epoch/output locks are taken. A false result means the delivery is stale.
func (repository *SetupManagerRepository) CheckPublicationSourceHoldForDelivery(ctx context.Context, guard PublicationDeliveryGuard, holdUUID uuid.UUID) (PublicationSourceHold, bool, error) {
	var result PublicationSourceHold
	valid := false
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return fmt.Errorf("check publication source hold: acquire output admission: %w", err)
		}
		if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
			return fmt.Errorf("check publication source hold: acquire source mutation gate: %w", err)
		}
		var hold PublicationSourceHold
		if err := tx.NewSelect().Model(&hold).Where("operation_id=? AND application_attempt=? AND river_job_id=? AND hold_uuid=?", guard.OperationID, guard.ApplicationAttempt, guard.RiverJobID, holdUUID).Scan(ctx); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("check publication source hold: read hold: %w", err)
		}
		var publicationAttempt TrackPublicationAttempt
		if err := tx.NewSelect().Model(&publicationAttempt).Where("operation_id=?", guard.OperationID).Scan(ctx); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("check publication source hold: read attempt: %w", err)
		}
		input, err := DecodePublicationInputs(publicationAttempt.FrozenInputs)
		if err != nil {
			return fmt.Errorf("check publication source hold: decode frozen inputs: %w", err)
		}
		if input.IntentKind != "audio" || input.Audio == nil {
			return nil
		}
		source := PublicationSourceSelection{
			RootID: hold.SourceRootID, LocationID: hold.SourceLocationID, WorkID: hold.WorkID,
			MediaVariantID: hold.MediaVariantID, ConfiguredPath: hold.ConfiguredPath,
			InventoryPath: hold.InventoryPath, RelativePath: hold.RelativePath,
			SizeBytes: hold.SizeBytes, MTime: hold.Mtime,
		}
		if hold.Epoch != guard.Epoch || publicationAttempt.Epoch != guard.Epoch || publicationAttempt.PositionID != guard.ExpectedPositionID || publicationAttempt.CandidateGeneration != guard.CandidateGeneration || !publicationSourceMatchesInput(source, *input.Audio) {
			return nil
		}
		if err := validateAndLockPublicationSource(ctx, tx, publicationAttempt.PositionID, source, *input.Audio); err != nil {
			return err
		}
		_, _, _, guarded, err := lockAndValidatePublicationDelivery(ctx, tx, guard, "check publication source hold")
		if err != nil {
			return err
		}
		if !guarded {
			return nil
		}
		result, valid = hold, true
		return nil
	})
	if err != nil {
		return PublicationSourceHold{}, false, err
	}
	return result, valid, nil
}
