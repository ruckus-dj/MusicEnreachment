package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

const PublicationPreparationVersion = 1

type PublicationDirectoryIdentity struct {
	Platform string `json:"platform"`
	Volume   string `json:"volume"`
	FileID   string `json:"file_id"`
}

// PublicationPreparationRecord is journal data, not filesystem authority.
// Replaying an identical record only reports its durable presence; callers
// must independently establish ownership before performing filesystem work.
type PublicationPreparationRecord struct {
	Version           int                           `json:"version"`
	Kind              string                        `json:"kind"`
	Step              string                        `json:"step"`
	OperationID       uuid.UUID                     `json:"operation_id"`
	ClaimUUID         uuid.UUID                     `json:"claim_uuid"`
	OwnerToken        uuid.UUID                     `json:"owner_token"`
	PrivateDirRel     string                        `json:"private_dir_rel"`
	InputRel          string                        `json:"input_rel"`
	OutputRel         string                        `json:"output_rel"`
	ParentIdentity    PublicationDirectoryIdentity  `json:"parent_identity"`
	DirectoryIdentity *PublicationDirectoryIdentity `json:"directory_identity,omitempty"`
	InputSize         *int64                        `json:"input_size,omitempty"`
	InputMTime        *int64                        `json:"input_mtime,omitempty"`
}

type PublicationPreparationOutcome string

const (
	PublicationPreparationRecorded        PublicationPreparationOutcome = "recorded"
	PublicationPreparationAlreadyRecorded PublicationPreparationOutcome = "already_recorded"
	PublicationPreparationStale           PublicationPreparationOutcome = "stale"
)

// RecordPublicationPreparation appends an immutable preparation intent or
// observation under the full live-delivery guard. Idempotency is not permission
// to repeat or resume a filesystem mutation.
func (repository *SetupManagerRepository) RecordPublicationPreparation(ctx context.Context, guard PublicationDeliveryGuard, record PublicationPreparationRecord) (PublicationPreparationOutcome, error) {
	if err := validatePublicationDeliveryGuard(guard); err != nil {
		return PublicationPreparationStale, err
	}
	outcome := PublicationPreparationStale
	err := repository.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		attempt, operation, _, ok, err := lockAndValidatePublicationDelivery(ctx, tx, guard, "record publication preparation")
		if err != nil || !ok || attempt.Phase != "preparing" || operation.Stage != "preparing" {
			return err
		}
		if !validPublicationPreparationRecord(record, guard) {
			return errors.New("invalid publication preparation record")
		}
		journal, appendRecord, err := appendPublicationPreparationJournal(attempt.MutationJournal, record)
		if err != nil {
			return fmt.Errorf("record publication preparation: decode mutation journal: %w", err)
		}
		if !appendRecord {
			outcome = PublicationPreparationAlreadyRecorded
			return nil
		}
		result, err := tx.NewRaw(`UPDATE track_publication_attempt SET mutation_journal=?::jsonb WHERE operation_id=? AND phase='preparing'`, string(journal), guard.OperationID).Exec(ctx)
		if err != nil {
			return fmt.Errorf("record publication preparation: update attempt: %w", err)
		}
		rows, err := result.RowsAffected()
		if err != nil || rows != 1 {
			return errors.New("record publication preparation: attempt changed concurrently")
		}
		outcome = PublicationPreparationRecorded
		return nil
	})
	return outcome, err
}

func validPublicationPreparationRecord(record PublicationPreparationRecord, guard PublicationDeliveryGuard) bool {
	if record.Version != PublicationPreparationVersion || record.Kind != "publication_preparation" || record.OperationID != guard.OperationID || record.ClaimUUID != guard.ClaimUUID || record.OwnerToken == uuid.Nil || !validPreparationPlatformIdentity(record.ParentIdentity) {
		return false
	}
	if !portablePreparationPath(record.PrivateDirRel) || !portablePreparationPath(record.InputRel) || !portablePreparationPath(record.OutputRel) || preparationPathsOverlap(record.InputRel, record.OutputRel) || !strings.HasPrefix(record.InputRel, record.PrivateDirRel+"/") || !strings.HasPrefix(record.OutputRel, record.PrivateDirRel+"/") {
		return false
	}
	switch record.Step {
	case "intent":
		return record.DirectoryIdentity == nil && record.InputSize == nil && record.InputMTime == nil
	case "directory_created":
		return validPreparationPlatformIdentityPtr(record.DirectoryIdentity) && record.DirectoryIdentity.Platform == record.ParentIdentity.Platform && record.InputSize == nil && record.InputMTime == nil
	case "input_ready":
		return validPreparationPlatformIdentityPtr(record.DirectoryIdentity) && record.DirectoryIdentity.Platform == record.ParentIdentity.Platform && record.InputSize != nil && *record.InputSize >= 0 && record.InputMTime != nil
	default:
		return false
	}
}

func validPreparationPlatformIdentity(identity PublicationDirectoryIdentity) bool {
	switch identity.Platform {
	case "darwin", "linux", "windows":
	default:
		return false
	}
	return strings.TrimSpace(identity.Volume) != "" && strings.TrimSpace(identity.FileID) != ""
}

func validPreparationPlatformIdentityPtr(identity *PublicationDirectoryIdentity) bool {
	return identity != nil && validPreparationPlatformIdentity(*identity)
}

func portablePreparationPath(value string) bool {
	if value == "" || strings.ContainsAny(value, "\\\x00") || path.IsAbs(value) || path.Clean(value) != value || strings.Contains(value, ":") {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func appendPublicationPreparationJournal(raw json.RawMessage, record PublicationPreparationRecord) (json.RawMessage, bool, error) {
	var entries []json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil || entries == nil {
		return nil, false, errors.New("journal is not an array")
	}
	var plan *PublicationPreparationRecord
	var steps = map[string]PublicationPreparationRecord{}
	for _, entry := range entries {
		var header struct {
			Kind string `json:"kind"`
		}
		if len(entry) == 0 || entry[0] != '{' || json.Unmarshal(entry, &header) != nil || header.Kind == "" {
			return nil, false, errors.New("journal entry is malformed")
		}
		if header.Kind != "publication_preparation" {
			continue
		}
		var prior PublicationPreparationRecord
		if err := json.Unmarshal(entry, &prior); err != nil || !validPreparationJournalRecord(prior) {
			return nil, false, errors.New("preparation journal entry is malformed")
		}
		if plan == nil {
			if prior.Step != "intent" {
				return nil, false, errors.New("preparation journal has no intent")
			}
			copy := prior
			plan = &copy
		} else if !samePreparationPlan(*plan, prior) {
			return nil, false, errors.New("preparation journal conflicts with immutable plan")
		}
		if old, exists := steps[prior.Step]; exists {
			if !samePreparationRecord(old, prior) {
				return nil, false, errors.New("preparation journal has conflicting step")
			}
			continue
		}
		if prior.Step == "directory_created" && steps["intent"].Step == "" || prior.Step == "input_ready" && steps["directory_created"].Step == "" {
			return nil, false, errors.New("preparation journal steps are out of order")
		}
		if prior.Step == "input_ready" && *prior.DirectoryIdentity != *steps["directory_created"].DirectoryIdentity {
			return nil, false, errors.New("preparation journal input identity conflicts with created directory")
		}
		steps[prior.Step] = prior
	}
	if plan != nil && !samePreparationPlan(*plan, record) {
		return nil, false, errors.New("preparation record conflicts with immutable plan")
	}
	if existing, ok := steps[record.Step]; ok {
		if samePreparationRecord(existing, record) {
			journal, err := json.Marshal(entries)
			return journal, false, err
		}
		return nil, false, errors.New("preparation step conflicts with prior record")
	}
	switch record.Step {
	case "intent":
		if plan != nil {
			return nil, false, errors.New("preparation intent already exists")
		}
	case "directory_created":
		if plan == nil || steps["intent"].Step == "" {
			return nil, false, errors.New("directory creation requires intent")
		}
	case "input_ready":
		if plan == nil || steps["directory_created"].Step == "" || *steps["directory_created"].DirectoryIdentity != *record.DirectoryIdentity {
			return nil, false, errors.New("input readiness requires matching created directory")
		}
	}
	entry, err := json.Marshal(record)
	if err != nil {
		return nil, false, err
	}
	entries = append(entries, entry)
	journal, err := json.Marshal(entries)
	return journal, true, err
}

func validPreparationJournalRecord(record PublicationPreparationRecord) bool {
	if record.Version != PublicationPreparationVersion || record.Kind != "publication_preparation" || record.OperationID == uuid.Nil || record.ClaimUUID == uuid.Nil || record.OwnerToken == uuid.Nil || !portablePreparationPath(record.PrivateDirRel) || !portablePreparationPath(record.InputRel) || !portablePreparationPath(record.OutputRel) || preparationPathsOverlap(record.InputRel, record.OutputRel) || !strings.HasPrefix(record.InputRel, record.PrivateDirRel+"/") || !strings.HasPrefix(record.OutputRel, record.PrivateDirRel+"/") || !validPreparationPlatformIdentity(record.ParentIdentity) {
		return false
	}
	switch record.Step {
	case "intent":
		return record.DirectoryIdentity == nil && record.InputSize == nil && record.InputMTime == nil
	case "directory_created":
		return validPreparationPlatformIdentityPtr(record.DirectoryIdentity) && record.DirectoryIdentity.Platform == record.ParentIdentity.Platform && record.InputSize == nil && record.InputMTime == nil
	case "input_ready":
		return validPreparationPlatformIdentityPtr(record.DirectoryIdentity) && record.DirectoryIdentity.Platform == record.ParentIdentity.Platform && record.InputSize != nil && *record.InputSize >= 0 && record.InputMTime != nil
	default:
		return false
	}
}

func preparationPathsOverlap(a, b string) bool {
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

func samePreparationPlan(a, b PublicationPreparationRecord) bool {
	return a.OperationID == b.OperationID && a.ClaimUUID == b.ClaimUUID && a.OwnerToken == b.OwnerToken && a.PrivateDirRel == b.PrivateDirRel && a.InputRel == b.InputRel && a.OutputRel == b.OutputRel && a.ParentIdentity == b.ParentIdentity
}

func samePreparationRecord(a, b PublicationPreparationRecord) bool {
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return string(left) == string(right)
}
