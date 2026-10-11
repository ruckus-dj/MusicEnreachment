package persistence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/uptrace/bun"
)

type ManualPickerSearchAdmission struct {
	RequestID     uuid.UUID
	GroupID       uuid.UUID
	GroupRevision string
	Kind          string
	Digest        [sha256.Size]byte
	Query         string
	Offset        int
	Limit         int
	Operation     *Operation
	Args          river.JobArgs
}

type ManualPickerSearchResult struct {
	GroupID       uuid.UUID       `json:"group_id"`
	OperationID   uuid.UUID       `json:"operation_id"`
	Kind          string          `json:"kind"`
	Payload       json.RawMessage `json:"payload"`
	PayloadSHA256 string          `json:"payload_sha256"`
	CacheRevision *string         `json:"cache_revision,omitempty"`
}

// AdmitManualPickerSearch creates the provider operation, delivery and scoped
// idempotency record together. It deliberately creates no matching evaluation.
func (repository *SetupManagerRepository) AdmitManualPickerSearch(ctx context.Context, client RiverInserter, input ManualPickerSearchAdmission) (*Operation, error) {
	if client == nil || input.RequestID == uuid.Nil || input.GroupID == uuid.Nil || input.GroupRevision == "" ||
		(input.Kind != "artist_search" && input.Kind != "release_search" && input.Kind != "artist_release_browse") ||
		input.Offset < 0 || input.Limit < 1 || input.Operation == nil || input.Args == nil {
		return nil, errors.New("manual picker search admission is incomplete")
	}
	returned := input.Operation
	err := repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtext(?), hashtext(?))", "manual-picker-search", input.RequestID.String()); err != nil {
			return err
		}
		var priorDigest []byte
		var priorOperation uuid.UUID
		err := tx.NewRaw(`SELECT canonical_digest, operation_id FROM manual_picker_search WHERE request_id=?`, input.RequestID).Scan(ctx, &priorDigest, &priorOperation)
		if err == nil {
			if !equalDigest(priorDigest, input.Digest[:]) {
				return ErrIncomingMatchConflict
			}
			returned, err = repository.GetOperationForUpdate(ctx, tx, priorOperation)
			return err
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var currentRevision string
		if err := tx.NewRaw(`SELECT revision FROM incoming_group WHERE id=? AND EXISTS (SELECT 1 FROM incoming_group_member WHERE group_id=incoming_group.id) FOR SHARE`, input.GroupID).Scan(ctx, &currentRevision); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrIncomingMatchConflict
			}
			return err
		}
		if currentRevision != input.GroupRevision {
			return ErrIncomingMatchConflict
		}
		if input.Operation.ProviderSourceID == nil || input.Operation.ProviderConfigurationIdentity == nil || input.Operation.ProviderCacheKey == nil {
			return errors.New("manual picker provider operation is incomplete")
		}
		if err := repository.createOperationAndEnqueueTx(ctx, tx, input.Operation, client, input.Args, nil); err != nil {
			return err
		}
		_, err = tx.NewRaw(`INSERT INTO manual_picker_search
			(request_id,group_id,operation_id,search_kind,canonical_digest,query_text,page_offset,page_limit,provider_source_id,configuration_identity,cache_key)
			VALUES (?,?,?,?,?,?,?,?,?,?,?)`, input.RequestID, input.GroupID, input.Operation.ID, input.Kind,
			input.Digest[:], input.Query, input.Offset, input.Limit, *input.Operation.ProviderSourceID,
			*input.Operation.ProviderConfigurationIdentity, *input.Operation.ProviderCacheKey).Exec(ctx)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("admit manual picker search: %w", err)
	}
	return returned, nil
}

func (repository *SetupManagerRepository) GetManualPickerSearchResult(ctx context.Context, groupID, operationID uuid.UUID) (*ManualPickerSearchResult, error) {
	result := new(ManualPickerSearchResult)
	err := repository.db.NewRaw(`SELECT result.group_id,result.operation_id,result.search_kind AS kind,result.payload,
		encode(result.payload_sha256,'hex') AS payload_sha256,result.cache_revision
		FROM manual_picker_search_result result JOIN manual_picker_search search USING(operation_id)
		JOIN operation ON operation.id=result.operation_id
		WHERE result.group_id=? AND search.group_id=? AND result.operation_id=? AND operation.kind IN
		('provider_artist_search','provider_release_search','provider_artist_release_browse') AND operation.state='succeeded'`, groupID, groupID, operationID).Scan(ctx, result)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read manual picker search result: %w", err)
	}
	return result, nil
}

func (repository *ProviderCacheRepository) captureManualPickerSearchResult(ctx context.Context, tx bun.Tx, fence ProviderDeliveryFence, ticket ProviderRefreshTicket, payload []byte, revision string) error {
	var groupID uuid.UUID
	var kind, configurationIdentity, cacheKey string
	err := tx.NewRaw(`SELECT group_id,search_kind,configuration_identity,cache_key FROM manual_picker_search
		WHERE operation_id=? AND provider_source_id=? FOR SHARE`, fence.OperationID, ticket.ProviderSourceID).Scan(ctx, &groupID, &kind, &configurationIdentity, &cacheKey)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if configurationIdentity != ticket.ConfigurationIdentity || cacheKey != ticket.CacheKey || len(payload) == 0 {
		return errors.New("manual picker accepted response does not match its registered search")
	}
	var payloadObject map[string]json.RawMessage
	if err := json.Unmarshal(payload, &payloadObject); err != nil || payloadObject == nil {
		return errors.New("manual picker response is not a JSON object")
	}
	// JSONB normalizes whitespace and object-key order. Hash its canonical text
	// after compacting it, matching the JSON bytes returned by the API.
	var canonicalPayload string
	if err := tx.NewRaw(`SELECT ?::jsonb::text`, string(payload)).Scan(ctx, &canonicalPayload); err != nil {
		return err
	}
	apiPayload, err := json.Marshal(json.RawMessage(canonicalPayload))
	if err != nil {
		return err
	}
	digest := sha256.Sum256(apiPayload)
	_, err = tx.NewRaw(`INSERT INTO manual_picker_search_result
		(operation_id,group_id,search_kind,provider_source_id,configuration_identity,cache_key,payload,payload_sha256,cache_revision)
		VALUES (?,?,?,?,?,?,?::jsonb,?,NULLIF(?,'')) ON CONFLICT (operation_id) DO NOTHING`,
		fence.OperationID, groupID, kind, ticket.ProviderSourceID, configurationIdentity, cacheKey, canonicalPayload, digest[:], revision).Exec(ctx)
	if err != nil {
		return err
	}
	return nil
}
