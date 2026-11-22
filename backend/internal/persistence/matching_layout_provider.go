package persistence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

// MatchingProviderLayoutEvidenceVersion is the format of accepted inputs
// bound to a MusicBrainz layout decision.
const MatchingProviderLayoutEvidenceVersion = 1

// MatchingProviderLayoutEvidence freezes the accepted MusicBrainz evidence a
// Plan 14 layout stage used. QueryKeys, PageKeys, and HydrationKeys are each the
// canonical union across every stage that contributed evidence, so a later
// stage carries forward earlier accepted searches and pages, including stages
// whose accepted result was negative, without those keys being lost.
//
// AcceptedProjectionRevision is the direct digest of accepted response payloads
// and provider projections (including accepted cache revisions); it deliberately
// excludes reservation generation and fetch order. A refresh reservation for a
// key that was never accepted has neither payload nor revision, and the snapshot
// seeds absent keys identically, so both contribute the same nil revision and
// empty payload digest. The revision is therefore not proof of acceptance:
// validation separately requires payload for every listed key and treats a nil
// or empty payload as ErrIncomingMatchConflict. A new stage must seal only keys
// that already have accepted payloads, never a reservation or an absent key
// merely because the stage requested it. Freshness alone does not prove that
// the resulting layouts are complete or authorize a selected decision.
type MatchingProviderLayoutEvidence struct {
	SchemaVersion              int       `json:"schema_version"`
	ProviderSourceID           uuid.UUID `json:"provider_source_id"`
	ProviderID                 uuid.UUID `json:"provider_id"`
	Namespace                  string    `json:"namespace"`
	Endpoint                   string    `json:"endpoint"`
	ConfigurationIdentity      string    `json:"configuration_identity"`
	ReleaseKeys                []string  `json:"release_keys"`
	QueryKeys                  []string  `json:"query_keys"`
	PageKeys                   []string  `json:"page_keys"`
	HydrationKeys              []string  `json:"hydration_keys"`
	AcceptedProjectionRevision string    `json:"accepted_projection_revision"`
}

// ValidateMatchingProviderLayoutEvidenceTx re-reads the exact accepted provider
// evidence under the writer's lock order: the MusicBrainz configuration
// advisory lock, then provider_source FOR SHARE. ApplySuccessfulResponse takes
// that source row FOR UPDATE before changing response cache or provider graph
// rows, so holding the shared source lock freezes accepted payloads and
// projections for this transaction.
//
// Materialization is not wired yet. A future transaction that also validates
// Local evidence must call this helper before
// ValidateMatchingLocalLayoutEvidenceTx: Local validation takes SHARE locks on
// provider graph tables. Reversing the order could deadlock a provider writer
// that already holds provider_source FOR UPDATE while waiting for those tables.
func ValidateMatchingProviderLayoutEvidenceTx(ctx context.Context, tx bun.Tx, evidence MatchingProviderLayoutEvidence) error {
	return validateMatchingProviderLayoutEvidenceTx(ctx, tx, evidence, false)
}

func validateMatchingLocalRecheckProviderEvidenceTx(ctx context.Context, tx bun.Tx, evidence MatchingProviderLayoutEvidence) error {
	return validateMatchingProviderLayoutEvidenceTx(ctx, tx, evidence, true)
}

// MatchingLocalRecheckProviderRevision binds the accepted payloads and
// normalized layout values without binding observation order. Another lookup
// in the same recheck can re-observe a shared recording without changing it.
func MatchingLocalRecheckProviderRevision(snapshot ProviderMatchingSnapshot) (string, error) {
	projection, err := fallbackLayoutProjectionValues(snapshot)
	if err != nil {
		return "", fmt.Errorf("normalize matching recheck provider projection: %w", err)
	}
	hash := sha256.New()
	hash.Write([]byte(acceptedProjectionRevision(nil, nil, snapshot.Responses)))
	hash.Write(projection)
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func validateMatchingProviderLayoutEvidenceTx(ctx context.Context, tx bun.Tx, evidence MatchingProviderLayoutEvidence, localRecheck bool) error {
	if err := requireMatchingLayoutEvidenceReadCommitted(ctx, tx); err != nil {
		return err
	}
	cacheKeys, err := validateMatchingProviderLayoutEvidence(evidence)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock_shared(hashtext(?))`, "musicbrainz-config"); err != nil {
		return fmt.Errorf("validate matching provider layout evidence: lock provider configuration: %w", err)
	}
	var source struct {
		ProviderID            uuid.UUID `bun:"provider_id"`
		Namespace             string    `bun:"namespace"`
		Endpoint              string    `bun:"endpoint"`
		ConfigurationIdentity string    `bun:"configuration_identity"`
	}
	err = tx.NewRaw(`SELECT source.provider_id, source.namespace, source.endpoint, source.configuration_identity
		FROM provider_source source
		JOIN app_setting setting ON setting.setting_name='musicbrainz_config_identity'
			AND setting.setting_value=source.configuration_identity
		WHERE source.id=? AND source.provider_id=? FOR SHARE OF source`,
		evidence.ProviderSourceID, evidence.ProviderID).Scan(ctx, &source)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrIncomingMatchConflict
	}
	if err != nil {
		return fmt.Errorf("validate matching provider layout evidence: read provider source: %w", err)
	}
	if source.ProviderID != evidence.ProviderID || source.Namespace != evidence.Namespace ||
		source.Endpoint != evidence.Endpoint || source.ConfigurationIdentity != evidence.ConfigurationIdentity {
		return ErrIncomingMatchConflict
	}
	return validateMatchingProviderLayoutSnapshotTx(ctx, tx, evidence, cacheKeys, localRecheck)
}

// validateMatchingProviderLayoutSnapshotTx compares evidence while the caller
// holds the provider configuration and source row locks.
func validateMatchingProviderLayoutSnapshotTx(ctx context.Context, tx bun.Tx, evidence MatchingProviderLayoutEvidence, cacheKeys []string, localRecheck bool) error {
	snapshot, err := readMusicBrainzMatchingSnapshot(ctx, tx, evidence.Endpoint, evidence.ConfigurationIdentity, evidence.ReleaseKeys, cacheKeys, false)
	if err != nil {
		return fmt.Errorf("validate matching provider layout evidence: %w", err)
	}
	if snapshot.Source.ID != evidence.ProviderSourceID || snapshot.Source.ProviderID != evidence.ProviderID ||
		snapshot.Source.Namespace != evidence.Namespace || snapshot.Source.Endpoint != evidence.Endpoint ||
		snapshot.Source.ConfigurationIdentity != evidence.ConfigurationIdentity {
		return ErrIncomingMatchConflict
	}
	for _, key := range cacheKeys {
		response := snapshot.Responses[key]
		if response == nil || len(response.Payload) == 0 {
			return fmt.Errorf("validate matching provider layout evidence: accepted cache response %q is missing: %w", key, ErrIncomingMatchConflict)
		}
	}
	revision := snapshot.AcceptedProjectionRevision
	if localRecheck {
		revision, err = MatchingLocalRecheckProviderRevision(snapshot)
		if err != nil {
			return err
		}
	}
	if revision != evidence.AcceptedProjectionRevision {
		return fmt.Errorf("validate matching provider layout evidence: accepted projection changed: %w", ErrIncomingMatchConflict)
	}
	return nil
}

func validateMatchingProviderLayoutEvidence(evidence MatchingProviderLayoutEvidence) ([]string, error) {
	if evidence.SchemaVersion != MatchingProviderLayoutEvidenceVersion {
		return nil, errors.New("matching provider layout evidence schema is unsupported")
	}
	if evidence.ProviderSourceID == uuid.Nil || evidence.ProviderID == uuid.Nil ||
		strings.TrimSpace(evidence.Namespace) == "" || strings.TrimSpace(evidence.Endpoint) == "" ||
		strings.TrimSpace(evidence.ConfigurationIdentity) == "" {
		return nil, errors.New("matching provider layout evidence identity is required")
	}
	if !canonicalReleaseKeys(evidence.ReleaseKeys) {
		return nil, errors.New("matching provider layout evidence requires sorted, distinct release keys")
	}
	for _, key := range evidence.ReleaseKeys {
		id, err := uuid.Parse(key)
		if err != nil || id == uuid.Nil || id.String() != key {
			return nil, errors.New("matching provider layout evidence contains an invalid release key")
		}
	}
	if len(mustDecodeSHA256(evidence.AcceptedProjectionRevision)) != sha256.Size {
		return nil, errors.New("matching provider layout evidence accepted projection revision is invalid")
	}
	lists := []struct {
		label string
		keys  []string
	}{
		{"query", evidence.QueryKeys},
		{"page", evidence.PageKeys},
		{"hydration", evidence.HydrationKeys},
	}
	seen := make(map[string]struct{})
	keys := make([]string, 0, len(evidence.QueryKeys)+len(evidence.PageKeys)+len(evidence.HydrationKeys))
	for _, list := range lists {
		for index, key := range list.keys {
			if key == "" || index > 0 && list.keys[index-1] >= key {
				return nil, fmt.Errorf("matching provider layout evidence %s keys must be non-empty, sorted, and unique", list.label)
			}
			if _, duplicate := seen[key]; duplicate {
				return nil, errors.New("matching provider layout evidence cache keys must be unique across stages")
			}
			seen[key] = struct{}{}
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 {
		return nil, errors.New("matching provider layout evidence requires accepted responses")
	}
	for _, key := range evidence.ReleaseKeys {
		if _, ok := seen["release-lookup:"+key]; !ok {
			return nil, fmt.Errorf("matching provider layout evidence has no lookup for release %q", key)
		}
	}
	sort.Strings(keys)
	return keys, nil
}
