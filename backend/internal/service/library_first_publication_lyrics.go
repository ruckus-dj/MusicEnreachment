package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/lrclib"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/metadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/librarymetadata"
	"github.com/ruckus/MusicEnreachment/backend/internal/persistence"
)

// FirstPublicationLyricsPlan is bounded planning evidence only. It does not
// authorize a CURRENT rewrite or expose the captured private authority object.
type FirstPublicationLyricsPlan struct {
	State                      string              `json:"state"`
	Origin                     string              `json:"origin"`
	Lyrics                     string              `json:"lyrics,omitempty"`
	SHA256                     string              `json:"sha256,omitempty"`
	WarningCode                string              `json:"warning_code,omitempty"`
	CurrentStatus              string              `json:"current_status"`
	CurrentPresent             bool                `json:"current_present"`
	CurrentLyrics              []string            `json:"current_lyrics"`
	CurrentLyricsFields        map[string][]string `json:"current_lyrics_fields"`
	CurrentComparisonAvailable bool                `json:"current_comparison_available"`
	SemanticAuthority          string              `json:"-"`
}

func planFirstPublicationLyrics(ctx context.Context, resolver *PublicationLyricsResolver, selected *persistence.FirstPublicationSource, authority *persistence.PublicationInitialLyricsInput, enabled bool, current *persistence.LibraryPublicationCurrent) (FirstPublicationLyricsPlan, error) {
	plan := FirstPublicationLyricsPlan{State: "absent", Origin: "none", CurrentStatus: "unavailable"}
	if current != nil {
		plan.CurrentComparisonAvailable = current.Metadata.Tags != nil
		plan.CurrentLyricsFields = make(map[string][]string)
		for key, values := range current.Metadata.Tags {
			if !strings.EqualFold(strings.TrimSpace(key), "LYRICS") {
				continue
			}
			plan.CurrentLyricsFields[key] = cloneTagValues(values)
			if plan.CurrentPresent {
				plan.CurrentComparisonAvailable = false
				continue
			}
			plan.CurrentPresent = true
			plan.CurrentLyrics = cloneTagValues(values)
		}
	}
	if !plan.CurrentPresent {
		plan.CurrentLyrics = []string{}
	}
	if plan.CurrentLyricsFields == nil {
		plan.CurrentLyricsFields = map[string][]string{}
	}
	if err := ctx.Err(); err != nil {
		return FirstPublicationLyricsPlan{}, err
	}
	var metadata *persistence.PublicationAnalysedMetadata
	var lookupMetadata lrclib.Metadata
	var selectedAssociation string
	if selected != nil {
		selectedAssociation = selected.AssociationID.String()
		metadata = selected.AnalysedMetadata
	}
	lookupPerformed := false
	manualProtected := authority != nil && authority.Desired != nil &&
		(authority.Desired.Authority == librarymetadata.AuthorityManual || authority.Desired.Provenance.Kind == librarymetadata.ProvenanceManual)
	if manualProtected {
		field := authority.Desired
		if field.Authority != librarymetadata.AuthorityManual || field.Provenance.Kind != librarymetadata.ProvenanceManual {
			plan.State, plan.Origin, plan.WarningCode = "unprocessable", "manual", "lyrics_manual_value_invalid"
		} else if field.State == librarymetadata.StateAbsent {
			if field.Value != nil {
				plan.State, plan.Origin, plan.WarningCode = "unprocessable", "manual", "lyrics_manual_value_invalid"
			} else {
				plan.State, plan.Origin = "absent", "manual"
			}
		} else {
			lyrics, state, err := desiredManualLyrics(*field)
			if err != nil {
				plan.State, plan.Origin, plan.WarningCode = "unprocessable", "manual", "lyrics_manual_value_invalid"
			} else if state == "explicit_empty" {
				plan.State, plan.Origin = "absent", "manual"
			} else {
				plan.State, plan.Origin, plan.Lyrics = "value", "manual", lyrics
				plan.SHA256 = lyricsSHA256(lyrics)
			}
		}
	} else if selected == nil || metadata == nil {
		plan.State, plan.WarningCode = "unknown", "lyrics_source_metadata_unavailable"
	} else {
		request := PublicationLyricsResolutionRequest{SelectedSourceTags: metadata.Tags}
		lookupTags := cloneTagMap(metadata.Tags)
		for key := range lookupTags {
			if strings.EqualFold(strings.TrimSpace(key), "LYRICS") {
				delete(lookupTags, key)
			}
		}
		lookupMetadata = initialLyricsLookupMetadata(metadataCapture(lookupTags))
		if metadata.DurationSeconds != nil {
			lookupMetadata.Duration = *metadata.DurationSeconds
		}
		request.Metadata = lookupMetadata
		if resolver == nil {
			plan.State, plan.WarningCode = "unknown", "lyrics_provider_unavailable"
		} else {
			lookupPerformed = true
			resolution, err := resolver.ResolveWithEnabled(ctx, request, enabled)
			if err != nil {
				if ctx.Err() != nil {
					return FirstPublicationLyricsPlan{}, ctx.Err()
				}
				return FirstPublicationLyricsPlan{}, err
			}
			switch {
			case resolution.Source == "source" || resolution.Source == "lrclib":
				plan.State, plan.Origin, plan.Lyrics, plan.SHA256 = "value", resolution.Source, resolution.Lyrics, resolution.SHA256
			case resolution.WarningCode != "":
				plan.State, plan.WarningCode = "unknown", resolution.WarningCode
			default:
				plan.State, plan.Origin = "absent", "none"
			}
		}
	}
	var metadataTags map[string][]string
	var metadataResultID, winningResultID string
	var duration *float64
	if metadata != nil {
		metadataTags = metadata.Tags
		metadataResultID, winningResultID = metadata.MetadataResultID.String(), metadata.WinningResultID.String()
		duration = metadata.DurationSeconds
	}
	var authoritySnapshot any
	if authority != nil {
		authoritySnapshot = authority
	}
	encoded, err := json.Marshal(struct {
		Version                                    string
		Authority                                  any
		Association, MetadataResult, WinningResult string
		Tags                                       map[string][]string
		Duration                                   *float64
		Lookup                                     lrclib.Metadata
		LookupPerformed                            bool
		Enabled                                    bool
		State, Origin, Lyrics, SHA256              string
	}{"first-publication-lyrics:v1", authoritySnapshot, selectedAssociation, metadataResultID, winningResultID, metadataTags, duration, lookupMetadata, lookupPerformed, enabled, plan.State, plan.Origin, plan.Lyrics, plan.SHA256})
	if err != nil {
		return FirstPublicationLyricsPlan{}, err
	}
	plan.SemanticAuthority = string(encoded)
	plan.compareCurrentLyrics()
	return plan, nil
}

func metadataCapture(tags map[string][]string) metadata.Capture {
	return metadata.Capture{Tags: tags}
}

func (plan *FirstPublicationLyricsPlan) compareCurrentLyrics() {
	if !plan.CurrentComparisonAvailable || plan.State == "unknown" || plan.State == "unprocessable" {
		plan.CurrentStatus = "unavailable"
		return
	}
	if plan.State == "absent" {
		if !plan.CurrentPresent {
			plan.CurrentStatus = "matches"
		} else if len(plan.CurrentLyrics) == 0 {
			plan.CurrentStatus = "unavailable"
		} else {
			plan.CurrentStatus = "differs"
		}
		return
	}
	if plan.CurrentPresent && len(plan.CurrentLyrics) == 0 {
		plan.CurrentStatus = "unavailable"
		return
	}
	if plan.CurrentPresent && len(plan.CurrentLyrics) == 1 && plan.CurrentLyrics[0] == plan.Lyrics {
		plan.CurrentStatus = "matches"
	} else if !plan.CurrentPresent || len(plan.CurrentLyrics) > 0 {
		plan.CurrentStatus = "differs"
	} else {
		plan.CurrentStatus = "unavailable"
	}
}

func lyricsSHA256(lyrics string) string {
	digest := sha256.Sum256([]byte(lyrics))
	return hex.EncodeToString(digest[:])
}

func foldFirstPublicationLyricsPlanDigest(preview *FirstPublicationPreview, plan FirstPublicationLyricsPlan) error {
	encoded, err := json.Marshal(struct {
		Version   string
		Previous  string
		Authority string
	}{"melo:first-publication-lyrics:v1", preview.ContextDigest, plan.SemanticAuthority})
	if err != nil {
		return err
	}
	digest := sha256.Sum256(encoded)
	preview.ContextDigest = hex.EncodeToString(digest[:])
	return nil
}
