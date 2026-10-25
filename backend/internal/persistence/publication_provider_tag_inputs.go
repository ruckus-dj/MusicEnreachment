package persistence

import (
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"strings"

	"github.com/google/uuid"
	"github.com/ruckus/MusicEnreachment/backend/internal/publication"
)

// PublicationProviderTagInput freezes provider authority for a source-free
// CURRENT metadata rewrite.
type PublicationProviderTagInput struct {
	Current               PublicationCurrentWitness `json:"current"`
	AudioPayloadSHA256    string                    `json:"audio_payload_sha256"`
	ProviderSourceID      uuid.UUID                 `json:"provider_source_id"`
	Endpoint              string                    `json:"endpoint"`
	ConfigurationIdentity string                    `json:"configuration_identity"`
	AuthorityRevision     string                    `json:"authority_revision"`
	Patch                 map[string][]string       `json:"patch"`
	ProtectedKeys         []string                  `json:"protected_keys"`
}

func (input PublicationProviderTagInput) Validate(rewrite PublicationRewriteInput, desiredTags map[string][]string) error {
	if err := input.Current.Validate(); err != nil {
		return fmt.Errorf("invalid current witness: %w", err)
	}
	if !validSHA256(input.AudioPayloadSHA256) {
		return errors.New("current encoded audio payload digest must be a lowercase SHA-256 digest")
	}
	if rewrite.CurrentGeneration != input.Current.Generation || rewrite.RelativePath != input.Current.RelativePath ||
		!reflect.DeepEqual(rewrite.Ownership, input.Current.Ownership) || !reflect.DeepEqual(rewrite.AudioFacts, input.Current.AudioFacts) {
		return errors.New("rewrite must exactly match the current publication witness")
	}
	if input.ProviderSourceID == uuid.Nil {
		return errors.New("provider source identity is required")
	}
	endpoint, err := url.Parse(input.Endpoint)
	if err != nil || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.Host == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.ForceQuery || endpoint.Fragment != "" || strings.Contains(input.Endpoint, "#") {
		return errors.New("provider endpoint must be an absolute HTTP(S) URL without user info, query, or fragment")
	}
	if strings.TrimSpace(input.ConfigurationIdentity) == "" || input.ConfigurationIdentity != strings.TrimSpace(input.ConfigurationIdentity) {
		return errors.New("provider configuration identity must be non-empty and canonical")
	}
	if !validSHA256(input.AuthorityRevision) {
		return errors.New("provider authority revision must be a lowercase SHA-256 digest")
	}
	if len(input.Patch) == 0 || input.ProtectedKeys == nil {
		return errors.New("provider patch must be non-empty and protected keys must be explicitly frozen")
	}
	for key, values := range input.Patch {
		if strings.TrimSpace(key) != key || strings.EqualFold(strings.TrimSpace(key), "LYRICS") || values == nil {
			return errors.New("provider patch must contain canonical non-lyrics keys and non-nil value lists")
		}
	}
	desired, err := publication.ApplyProviderTagPatch(input.Current.Tags, input.Patch, input.ProtectedKeys)
	if err != nil {
		return fmt.Errorf("invalid provider tag patch: %w", err)
	}
	if reflect.DeepEqual(desired, input.Current.Tags) {
		return errors.New("provider tag patch must change current tags")
	}
	if !reflect.DeepEqual(desired, desiredTags) {
		return errors.New("desired tags must match the frozen provider patch")
	}
	return nil
}
