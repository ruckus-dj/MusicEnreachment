package persistence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/ruckus/MusicEnreachment/backend/internal/integrations/matchingpolicy"
	"github.com/uptrace/bun"
)

// AutomaticArtistMatchingAdmission contains already-prepared provider work. The
// persistence boundary performs no filesystem, metadata, or provider I/O.
type AutomaticArtistMatchingAdmission struct {
	Parent                 *Operation
	ParentArgs             river.JobArgs
	Provider               MatchingProviderRequest
	GroupID                uuid.UUID
	ExpectedGroupRevision  string
	GroupingEpoch          int64 // informational only; not semantic identity
	PolicyVersion          string
	ArtistQueryPlanVersion int
	QueryDescriptor        json.RawMessage
	EventID                uuid.UUID
	EventRiverJobID        int64
	EventRiverAttempt      int
	EventDeliveryEpoch     int64
	// RequestGeneration binds current-state admissions to the coalesced request
	// they durably satisfy. Zero retains the legacy event-driven admission path.
	RequestGeneration int64
}

// AutomaticArtistMatchingAdmissionResult keeps the permanent receipt identity
// separate from its operation row, which may be removed by normal cleanup.
type AutomaticArtistMatchingAdmissionResult struct {
	AlreadyAdmitted     bool
	OriginalRunID       uuid.UUID
	OriginalOperationID uuid.UUID
	Operation           *Operation
}

type automaticArtistTargetCapture struct {
	SchemaVersion     int                           `json:"schema_version"`
	GroupID           uuid.UUID                     `json:"group_id"`
	GroupRevision     string                        `json:"group_revision"`
	AuthorityRevision int64                         `json:"authority_revision"`
	Members           []automaticArtistTargetMember `json:"members"`
}

type automaticArtistTargetMember struct {
	VariantID      uuid.UUID                        `json:"variant_id"`
	Ready          bool                             `json:"ready"`
	Locations      []IncomingGroupingMemberLocation `json:"locations"`
	Captures       []IncomingGroupingCapture        `json:"captures"`
	AnalysisInputs []automaticArtistAnalysisInput   `json:"analysis_inputs"`
}

// automaticArtistAnalysisInput records the selected durable intent and result
// for each analysis step of a captured work. Analysis steps use a composite
// (work_id, step) key, so that pair is the record identity.
type automaticArtistAnalysisInput struct {
	AnalysisRecordID string          `json:"analysis_record_id"`
	WorkID           uuid.UUID       `json:"work_id"`
	Step             string          `json:"step"`
	State            string          `json:"state"`
	InputSnapshot    json.RawMessage `json:"input_snapshot"`
	InputDigest      *string         `json:"input_digest"`
	ResultReference  *uuid.UUID      `json:"result_reference"`
}

// AdmitAutomaticArtistMatchingRun fences and captures one current group and
// atomically records its permanent semantic admission receipt and durable work.
// The event delivery fence is mandatory; the immutable run carries the
// versioned target fence consumed by delivery persistence.
func (repository *SetupManagerRepository) AdmitAutomaticArtistMatchingRun(ctx context.Context, client RiverInserter, admission AutomaticArtistMatchingAdmission) (AutomaticArtistMatchingAdmissionResult, error) {
	if client == nil || admission.Parent == nil || admission.GroupID == uuid.Nil || admission.ExpectedGroupRevision == "" ||
		admission.EventID == uuid.Nil || admission.EventRiverJobID < 1 || admission.EventRiverAttempt < 1 || admission.EventDeliveryEpoch < 1 ||
		admission.RequestGeneration < 0 ||
		admission.Provider.Operation == nil || admission.Provider.Operation.Kind != "provider_artist_search" ||
		admission.Provider.Operation.ProviderCacheKey == nil || admission.ArtistQueryPlanVersion < 1 ||
		!matchingpolicy.Supported(admission.PolicyVersion) || len(admission.QueryDescriptor) == 0 {
		return AutomaticArtistMatchingAdmissionResult{}, fmt.Errorf("automatic artist matching admission is incomplete")
	}
	if admission.Parent.Kind != "matching_evaluation_v1" {
		return AutomaticArtistMatchingAdmissionResult{}, fmt.Errorf("automatic artist matching parent operation kind is invalid")
	}
	descriptor, err := canonicalMatchingJSON(admission.QueryDescriptor)
	if err != nil {
		return AutomaticArtistMatchingAdmissionResult{}, fmt.Errorf("automatic artist matching query descriptor is invalid: %w", err)
	}
	preparedInput, err := canonicalMatchingJSON(admission.Provider.Operation.InputSnapshot)
	if err != nil || !equalDigest(preparedInput, descriptor) {
		return AutomaticArtistMatchingAdmissionResult{}, fmt.Errorf("automatic artist matching query descriptor differs from the prepared provider request")
	}
	var result AutomaticArtistMatchingAdmissionResult
	err = repository.db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if err := AcquireOutputAdmissionGate(ctx, tx); err != nil {
			return fmt.Errorf("automatic artist matching admission: lock output gate: %w", err)
		}
		if err := lockSourceFingerprintMutations(ctx, tx); err != nil {
			return fmt.Errorf("automatic artist matching admission: lock source mutation gate: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock_shared(hashtext(?))", "musicbrainz-config"); err != nil {
			return err
		}
		if err := validateAutomaticArtistProviderFence(ctx, tx, admission.Provider.Operation); err != nil {
			return err
		}
		if err := validateAutomaticArtistEventFence(ctx, tx, admission); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext(?),hashtext(?))`, "automatic-artist-admission", admission.GroupID.String()); err != nil {
			return err
		}
		var grouping struct {
			Revision     int64 `bun:"revision"`
			NeedsRefresh bool  `bun:"needs_refresh"`
		}
		if err := tx.NewRaw(`SELECT revision,needs_refresh FROM incoming_grouping_state WHERE singleton=true FOR UPDATE`).Scan(ctx, &grouping); err != nil {
			return err
		}
		if grouping.NeedsRefresh {
			return ErrIncomingMatchConflict
		}
		if admission.RequestGeneration > 0 {
			var requested int64
			if err := tx.NewRaw(`SELECT requested_generation FROM incoming_matching_generation WHERE group_id=? FOR UPDATE`, admission.GroupID).Scan(ctx, &requested); err != nil || requested != admission.RequestGeneration {
				return ErrIncomingMatchConflict
			}
		}
		var group struct {
			Revision string `bun:"revision"`
		}
		if err := tx.NewRaw(`SELECT revision FROM incoming_group WHERE id=? FOR UPDATE`, admission.GroupID).Scan(ctx, &group); err != nil || group.Revision != admission.ExpectedGroupRevision {
			return ErrIncomingMatchConflict
		}
		authorityRevision, err := requireAutomaticMatchingAuthority(ctx, tx, admission.GroupID)
		if err != nil {
			return err
		}
		capture, err := captureAutomaticArtistTarget(ctx, tx, admission.GroupID, group.Revision, authorityRevision)
		if err != nil {
			return err
		}
		metadataCapture, err := captureMatchingAdmissionMetadata(ctx, tx, []MatchingTarget{{GroupID: admission.GroupID, Revision: group.Revision}})
		if err != nil {
			return err
		}
		metadataInputDigests := make(map[uuid.UUID]string)
		for _, member := range capture.Members {
			for _, input := range member.AnalysisInputs {
				if input.Step == "metadata" && input.InputDigest != nil {
					metadataInputDigests[input.WorkID] = *input.InputDigest
				}
			}
		}
		for index := range metadataCapture {
			metadataCapture[index].AnalysisInputSHA256 = metadataInputDigests[metadataCapture[index].WorkID]
		}
		metadataDigest, err := matchingMetadataCaptureDigest(metadataCapture)
		if err != nil {
			return err
		}
		captureBytes, err := json.Marshal(capture)
		if err != nil {
			return err
		}
		captureBytes, captureDigest, err := automaticArtistTargetCaptureDigest(captureBytes)
		if err != nil {
			return err
		}
		semantic := struct {
			Version           int             `json:"version"`
			Capture           json.RawMessage `json:"target_capture"`
			CaptureDigest     string          `json:"target_capture_digest"`
			Policy            string          `json:"policy_version"`
			QueryVersion      int             `json:"artist_query_plan_version"`
			Query             json.RawMessage `json:"query_descriptor"`
			SourceID          uuid.UUID       `json:"provider_source_id"`
			ProviderID        uuid.UUID       `json:"provider_id"`
			Endpoint          string          `json:"provider_endpoint"`
			Configuration     string          `json:"provider_configuration_identity"`
			RequestGeneration int64           `json:"request_generation,omitempty"`
		}{2, captureBytes, hex.EncodeToString(captureDigest[:]), admission.PolicyVersion, admission.ArtistQueryPlanVersion, descriptor,
			*admission.Provider.Operation.ProviderSourceID, *admission.Provider.Operation.ProviderID, admission.Provider.Operation.ProviderEndpoint,
			*admission.Provider.Operation.ProviderConfigurationIdentity, admission.RequestGeneration}
		semanticBytes, err := json.Marshal(semantic)
		if err != nil {
			return err
		}
		fingerprint := sha256.Sum256(semanticBytes)
		var prior struct {
			RunID       uuid.UUID `bun:"original_run_id"`
			OperationID uuid.UUID `bun:"original_operation_id"`
		}
		err = tx.NewRaw(`SELECT original_run_id,original_operation_id FROM incoming_matching_automatic_admission WHERE fingerprint=?`, fingerprint[:]).Scan(ctx, &prior)
		if err == nil {
			result = AutomaticArtistMatchingAdmissionResult{AlreadyAdmitted: true, OriginalRunID: prior.RunID, OriginalOperationID: prior.OperationID}
			if op, getErr := repository.GetOperationForUpdate(ctx, tx, prior.OperationID); getErr == nil {
				result.Operation = op
			} else if !errors.Is(getErr, sql.ErrNoRows) {
				return getErr
			}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}

		// IDs are allocated only after the authoritative semantic receipt lookup.
		if admission.Parent.ID == uuid.Nil {
			admission.Parent.ID = uuid.New()
		}
		if admission.Provider.Operation.ID == uuid.Nil {
			admission.Provider.Operation.ID = uuid.New()
		}
		plan := struct {
			SchemaVersion          int             `json:"schema_version"`
			Intent                 string          `json:"intent"`
			Mode                   string          `json:"mode"`
			TargetCaptureDigest    string          `json:"target_capture_digest"`
			PolicyVersion          string          `json:"policy_version"`
			ArtistQueryPlanVersion int             `json:"artist_query_plan_version"`
			QueryDescriptor        json.RawMessage `json:"query_descriptor"`
		}{4, "automatic_artist_acquisition", "automatic", hex.EncodeToString(captureDigest[:]), admission.PolicyVersion, admission.ArtistQueryPlanVersion, descriptor}
		planBytes, err := json.Marshal(plan)
		if err != nil {
			return err
		}
		planBytes, err = canonicalMatchingJSON(planBytes)
		if err != nil {
			return fmt.Errorf("canonicalize automatic artist matching plan: %w", err)
		}
		parentSnapshot, err := json.Marshal(struct {
			SchemaVersion int             `json:"schema_version"`
			Plan          json.RawMessage `json:"plan"`
		}{1, planBytes})
		if err != nil {
			return err
		}
		admission.Parent.InputSnapshot, err = canonicalMatchingJSON(parentSnapshot)
		if err != nil {
			return fmt.Errorf("canonicalize automatic artist matching parent snapshot: %w", err)
		}
		planDigest := sha256.Sum256(planBytes)
		if err := repository.createOperationAndEnqueueTx(ctx, tx, admission.Parent, client, admission.ParentArgs, nil); err != nil {
			return err
		}
		provider := admission.Provider.Operation
		if err := repository.createOperationAndEnqueueTx(ctx, tx, provider, client, admission.Provider.Args, nil); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`INSERT INTO matching_run(id,operation_id,plan_admission_key,plan_digest,immutable_plan,grouping_revision,grouping_epoch,provider_source_id,provider_configuration_identity,policy_version,state,fence_version) VALUES (?,?,?,?,?::jsonb,?,?,?,? ,?,'queued',2)`, admission.Parent.ID, admission.Parent.ID, "automatic-artist:"+hex.EncodeToString(fingerprint[:]), planDigest[:], string(planBytes), grouping.Revision, grouping.Revision, *provider.ProviderSourceID, *provider.ProviderConfigurationIdentity, admission.PolicyVersion).Exec(ctx); err != nil {
			return err
		}
		if admission.Parent.RiverJobID == nil || *admission.Parent.RiverJobID < 1 {
			return fmt.Errorf("automatic artist matching parent delivery identity is missing")
		}
		if _, err := tx.NewRaw(`INSERT INTO matching_delivery(run_id,river_job_id,operation_attempt) VALUES (?,?,?)`, admission.Parent.ID, *admission.Parent.RiverJobID, admission.Parent.Attempt).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`INSERT INTO incoming_matching_generation(group_id,high_water) VALUES (?,0) ON CONFLICT (group_id) DO NOTHING`, admission.GroupID).Exec(ctx); err != nil {
			return err
		}
		var generation int64
		if err := tx.NewRaw(`UPDATE incoming_matching_generation SET high_water=high_water+1,current_run_id=?,updated_at=now() WHERE group_id=? RETURNING high_water`, admission.Parent.ID, admission.GroupID).Scan(ctx, &generation); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`INSERT INTO matching_run_target(run_id,group_id,captured_group_revision,target_generation,authority_revision,target_capture,target_capture_digest,reconciliation_generation) VALUES (?,?,?,?,?,?::jsonb,?,NULLIF(?,0))`, admission.Parent.ID, admission.GroupID, group.Revision, generation, authorityRevision, string(captureBytes), captureDigest[:], admission.RequestGeneration).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`INSERT INTO matching_provider_dependency(parent_run_id,request_id,cache_key) VALUES (?,?,?)`, admission.Parent.ID, provider.ID, *provider.ProviderCacheKey).Exec(ctx); err != nil {
			return err
		}
		intent := MatchingAcquisitionIntent{
			SchemaVersion: matchingAutomaticAcquisitionIntentVersion, FenceVersion: 2,
			GroupingEpoch: grouping.Revision, PlanDigest: hex.EncodeToString(planDigest[:]),
			CaptureDigest: metadataDigest, CapturedMembers: metadataCapture,
			PolicyVersion: admission.PolicyVersion, TargetCaptureDigest: hex.EncodeToString(captureDigest[:]),
			QueryDescriptor: descriptor, ArtistQueryPlanVersion: admission.ArtistQueryPlanVersion,
			Targets: []MatchingTarget{{GroupID: admission.GroupID, Revision: group.Revision}},
			InitialProviderInputs: []MatchingAcquisitionProviderRequest{{
				OperationID: provider.ID, Kind: provider.Kind, CacheKey: *provider.ProviderCacheKey,
				Input: append(json.RawMessage(nil), provider.InputSnapshot...),
			}},
		}
		intentBytes, err := canonicalMatchingAcquisitionIntentBytes(intent)
		if err != nil {
			return err
		}
		intentDigest := sha256.Sum256(intentBytes)
		if _, err := tx.NewRaw(`INSERT INTO matching_acquisition(run_id,intent_version,intent,intent_digest,current_phase) VALUES (?,2,?::jsonb,?,'admitted')`, admission.Parent.ID, string(intentBytes), intentDigest[:]).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`INSERT INTO matching_acquisition_phase(run_id,revision,phase,input,output,response_provenance) VALUES (?,0,'admitted','{}'::jsonb,'{"state":"admitted"}'::jsonb,'[]'::jsonb)`, admission.Parent.ID).Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewRaw(`INSERT INTO incoming_matching_automatic_admission(fingerprint,original_run_id,original_operation_id) VALUES (?,?,?)`, fingerprint[:], admission.Parent.ID, admission.Parent.ID).Exec(ctx); err != nil {
			return err
		}
		result = AutomaticArtistMatchingAdmissionResult{OriginalRunID: admission.Parent.ID, OriginalOperationID: admission.Parent.ID, Operation: admission.Parent}
		return nil
	})
	return result, err
}

func validateAutomaticArtistProviderFence(ctx context.Context, tx bun.Tx, operation *Operation) error {
	if operation.ProviderSourceID == nil || operation.ProviderID == nil || operation.ProviderConfigurationIdentity == nil || operation.ProviderEndpoint == "" {
		return fmt.Errorf("automatic artist matching provider fence is incomplete")
	}
	var config string
	if err := tx.NewRaw(`SELECT setting_value FROM app_setting WHERE setting_name='musicbrainz_config_identity'`).Scan(ctx, &config); err != nil || config != *operation.ProviderConfigurationIdentity {
		return fmt.Errorf("automatic artist matching provider configuration changed")
	}
	var source struct {
		ProviderID uuid.UUID `bun:"provider_id"`
		Endpoint   string    `bun:"endpoint"`
		Identity   string    `bun:"configuration_identity"`
	}
	if err := tx.NewRaw(`SELECT provider_id,endpoint,configuration_identity FROM provider_source WHERE id=?`, *operation.ProviderSourceID).Scan(ctx, &source); err != nil || source.ProviderID != *operation.ProviderID || source.Endpoint != operation.ProviderEndpoint || source.Identity != config {
		return fmt.Errorf("automatic artist matching provider source changed")
	}
	return nil
}

func validateAutomaticArtistEventFence(ctx context.Context, tx bun.Tx, admission AutomaticArtistMatchingAdmission) error {
	var valid bool
	err := tx.NewRaw(`SELECT state='dispatched' AND registered_job_id=? AND highest_delivery_attempt=? AND delivery_claimed AND delivery_epoch=? FROM incoming_matching_reconcile_event WHERE id=? FOR UPDATE`, admission.EventRiverJobID, admission.EventRiverAttempt, admission.EventDeliveryEpoch, admission.EventID).Scan(ctx, &valid)
	if err != nil || !valid {
		return fmt.Errorf("automatic artist matching reconciliation delivery fence is stale")
	}
	return nil
}

func captureAutomaticArtistTarget(ctx context.Context, tx bun.Tx, groupID uuid.UUID, revision string, authorityRevision int64) (automaticArtistTargetCapture, error) {
	capture := automaticArtistTargetCapture{SchemaVersion: 2, GroupID: groupID, GroupRevision: revision, AuthorityRevision: authorityRevision, Members: make([]automaticArtistTargetMember, 0)}
	var members []struct {
		ID    uuid.UUID `bun:"variant_id"`
		Ready bool      `bun:"ready"`
	}
	if err := tx.NewRaw(`SELECT variant_id,ready FROM incoming_group_member WHERE group_id=? ORDER BY variant_id FOR SHARE`, groupID).Scan(ctx, &members); err != nil {
		return capture, err
	}
	var locations []IncomingGroupingMemberLocation
	if err := tx.NewRaw(`SELECT variant_id, source_root_id AS root_id, work_id, location_id, configured_path, inventory_path, relative_path, size_bytes, mtime
		FROM incoming_group_member_location WHERE group_id=? ORDER BY variant_id, source_root_id, relative_path`, groupID).Scan(ctx, &locations); err != nil {
		return capture, fmt.Errorf("load automatic artist target locations: %w", err)
	}
	locationIDs := make([]uuid.UUID, 0, len(locations))
	for _, location := range locations {
		locationIDs = append(locationIDs, location.LocationID)
	}
	if len(locationIDs) == 0 {
		return capture, fmt.Errorf("automatic artist matching target capture has no persisted location")
	}
	captures, err := loadIncomingGroupingCapturesForLocations(ctx, tx, locationIDs)
	if err != nil {
		return capture, err
	}
	analysisInputs, err := loadAutomaticArtistAnalysisInputs(ctx, tx, captures)
	if err != nil {
		return capture, err
	}
	for _, member := range members {
		entry := automaticArtistTargetMember{VariantID: member.ID, Ready: member.Ready, Locations: make([]IncomingGroupingMemberLocation, 0), Captures: make([]IncomingGroupingCapture, 0), AnalysisInputs: make([]automaticArtistAnalysisInput, 0)}
		for _, location := range locations {
			if location.VariantID == member.ID {
				entry.Locations = append(entry.Locations, location)
			}
		}
		for _, source := range captures {
			if source.VariantID == member.ID {
				entry.Captures = append(entry.Captures, source)
				entry.AnalysisInputs = append(entry.AnalysisInputs, analysisInputs[source.WorkID]...)
			}
		}
		if len(entry.Locations) == 0 {
			return capture, fmt.Errorf("automatic artist matching target capture has no persisted location")
		}
		capture.Members = append(capture.Members, entry)
	}
	sort.Slice(capture.Members, func(i, j int) bool {
		return capture.Members[i].VariantID.String() < capture.Members[j].VariantID.String()
	})
	return capture, nil
}

func loadAutomaticArtistAnalysisInputs(ctx context.Context, tx bun.Tx, captures []IncomingGroupingCapture) (map[uuid.UUID][]automaticArtistAnalysisInput, error) {
	workSet := make(map[uuid.UUID]struct{})
	for _, capture := range captures {
		if capture.WorkID != uuid.Nil {
			workSet[capture.WorkID] = struct{}{}
		}
	}
	workIDs := make([]uuid.UUID, 0, len(workSet))
	for workID := range workSet {
		workIDs = append(workIDs, workID)
	}
	sort.Slice(workIDs, func(i, j int) bool { return workIDs[i].String() < workIDs[j].String() })
	inputs := make(map[uuid.UUID][]automaticArtistAnalysisInput, len(workIDs))
	if len(workIDs) == 0 {
		return inputs, nil
	}
	type analysisStep struct {
		WorkID                  uuid.UUID       `bun:"work_id"`
		Step                    string          `bun:"step"`
		State                   string          `bun:"state"`
		InputSnapshot           json.RawMessage `bun:"input_snapshot"`
		SuccessSHAVariantID     *uuid.UUID      `bun:"success_sha_variant_id"`
		SuccessProbeVariantID   *uuid.UUID      `bun:"success_probe_variant_id"`
		SuccessFingerprintID    *uuid.UUID      `bun:"success_fingerprint_result_id"`
		SuccessMetadataResultID *uuid.UUID      `bun:"success_metadata_result_id"`
	}
	var rows []analysisStep
	if err := tx.NewRaw(`SELECT work_id,step,state,input_snapshot,success_sha_variant_id,success_probe_variant_id,success_fingerprint_result_id,success_metadata_result_id
		FROM source_analysis_step WHERE work_id IN (?) ORDER BY work_id,step`, bun.List(workIDs)).Scan(ctx, &rows); err != nil {
		return nil, fmt.Errorf("capture automatic artist analysis inputs: %w", err)
	}
	byKey := make(map[string]analysisStep, len(rows))
	for _, row := range rows {
		byKey[row.WorkID.String()+":"+row.Step] = row
	}
	steps := []string{"fingerprint", "metadata", "probe", "sha256"}
	for _, workID := range workIDs {
		for _, step := range steps {
			row, found := byKey[workID.String()+":"+step]
			input := automaticArtistAnalysisInput{
				AnalysisRecordID: workID.String() + ":" + step,
				WorkID:           workID,
				Step:             step,
				State:            "missing",
				InputSnapshot:    json.RawMessage("null"),
			}
			if found {
				input.State = row.State
				input.ResultReference = automaticArtistAnalysisResultReference(row, step)
				if len(row.InputSnapshot) != 0 {
					canonical, err := canonicalMatchingJSON(row.InputSnapshot)
					if err != nil {
						return nil, fmt.Errorf("canonicalize automatic artist %s analysis input for work %s: %w", step, workID, err)
					}
					input.InputSnapshot = canonical
					digest := sha256.Sum256(canonical)
					digestText := hex.EncodeToString(digest[:])
					input.InputDigest = &digestText
				}
			}
			inputs[workID] = append(inputs[workID], input)
		}
	}
	return inputs, nil
}

func automaticArtistTargetCaptureDigest(raw json.RawMessage) (json.RawMessage, [32]byte, error) {
	canonical, err := canonicalMatchingJSON(raw)
	if err != nil {
		return nil, [32]byte{}, fmt.Errorf("canonicalize automatic artist target capture: %w", err)
	}
	return canonical, sha256.Sum256(canonical), nil
}

func automaticArtistAnalysisResultReference(row struct {
	WorkID                  uuid.UUID       `bun:"work_id"`
	Step                    string          `bun:"step"`
	State                   string          `bun:"state"`
	InputSnapshot           json.RawMessage `bun:"input_snapshot"`
	SuccessSHAVariantID     *uuid.UUID      `bun:"success_sha_variant_id"`
	SuccessProbeVariantID   *uuid.UUID      `bun:"success_probe_variant_id"`
	SuccessFingerprintID    *uuid.UUID      `bun:"success_fingerprint_result_id"`
	SuccessMetadataResultID *uuid.UUID      `bun:"success_metadata_result_id"`
}, step string) *uuid.UUID {
	switch step {
	case "metadata":
		return row.SuccessMetadataResultID
	case "probe":
		return row.SuccessProbeVariantID
	case "fingerprint":
		return row.SuccessFingerprintID
	case "sha256":
		return row.SuccessSHAVariantID
	default:
		return nil
	}
}
