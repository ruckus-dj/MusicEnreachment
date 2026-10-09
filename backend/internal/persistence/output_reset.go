package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun/dialect/pgdialect"
)

const outputResetSafeError = "The output directory was reset before this operation could run."

type OutputResetJournal struct {
	Token              uuid.UUID       `bun:"token"`
	State              string          `bun:"state"`
	OldRoot            string          `bun:"old_root"`
	NewRoot            string          `bun:"new_root"`
	CreatedDirectories []string        `bun:"created_directories,array"`
	DirectoryManifest  json.RawMessage `bun:"directory_manifest,type:jsonb"`
	CleanupOutcomes    json.RawMessage `bun:"cleanup_outcomes,type:jsonb"`
	CreatedAt          time.Time       `bun:"created_at"`
	UpdatedAt          time.Time       `bun:"updated_at"`
}

// OutputResetDirectory is a durable journal record for one exclusive mkdir.
// Identity is opaque to persistence and is verified by the filesystem adapter.
type OutputResetDirectory struct {
	Path     string `json:"path"`
	Phase    string `json:"phase"`
	Identity string `json:"identity,omitempty"`
}

// OutputResetRequest contains the full transaction callbacks. RunOutputReset
// remains as a compatibility wrapper for callers that only record paths.
type OutputResetRequest struct {
	ExpectedOldRoot            string
	ExpectedToolsRoot          string
	NewRoot                    string
	RuntimeValues              map[string]string
	RuntimeValuesAfterPrepare  func(context.Context) (map[string]string, error)
	AllowedDirectories         []string
	OutputCaseSensitive        *bool
	OutputUnicodeNormalization *string
	Prepare                    func(context.Context, func(OutputResetDirectory) error) error
	Finish                     func(context.Context, OutputResetJournal) error
}

type OutputResetSession struct{ conn *sql.Conn }

type outputResetOutcomes struct {
	InvalidatedArtifactIDs       []uuid.UUID `json:"invalidated_artifact_ids"`
	RemovedCandidateOperationIDs []uuid.UUID `json:"removed_candidate_operation_ids"`
}

// RunOutputReset holds the exclusive output admission gate from journal creation
// through preparation, the database reset, and post-commit filesystem work. The
// preparation callback records directories as they are created so startup
// recovery can remove only directories owned by this reset.
func (repository *SetupManagerRepository) RunOutputReset(
	ctx context.Context,
	expectedOldRoot, newRoot string,
	prepare func(context.Context, func(string) error) error,
	finish func(context.Context, OutputResetJournal) error,
) (uuid.UUID, error) {
	var expectedToolsRoot string
	if err := repository.db.NewRaw(`SELECT COALESCE(MAX(setting_value),'') FROM app_setting WHERE setting_name='tools_directory'`).Scan(ctx, &expectedToolsRoot); err != nil {
		return uuid.Nil, fmt.Errorf("read expected tools directory: %w", err)
	}
	var durablePrepare func(context.Context, func(OutputResetDirectory) error) error
	if prepare != nil {
		durablePrepare = func(ctx context.Context, record func(OutputResetDirectory) error) error {
			return prepare(ctx, func(path string) error { return record(OutputResetDirectory{Path: path, Phase: "intent"}) })
		}
	}
	return repository.RunOutputResetRequest(ctx, OutputResetRequest{
		ExpectedOldRoot:   expectedOldRoot,
		ExpectedToolsRoot: expectedToolsRoot,
		NewRoot:           newRoot,
		Prepare:           durablePrepare,
		Finish:            finish,
	})
}

func (repository *SetupManagerRepository) RunOutputResetRequest(ctx context.Context, request OutputResetRequest) (uuid.UUID, error) {
	expectedOldRoot, newRoot := request.ExpectedOldRoot, request.NewRoot
	expectedToolsRoot := request.ExpectedToolsRoot
	runtimeValues := cloneSettingValues(request.RuntimeValues)
	if value, ok := runtimeValues["output_directory"]; ok && value != newRoot {
		return uuid.Nil, fmt.Errorf("runtime output directory must match the output reset target")
	}
	if request.OutputCaseSensitive != nil {
		value := strconv.FormatBool(*request.OutputCaseSensitive)
		if existing, ok := runtimeValues["output_case_sensitive"]; ok && existing != value {
			return uuid.Nil, fmt.Errorf("runtime output case sensitivity conflicts with the supplied field")
		}
		runtimeValues["output_case_sensitive"] = value
	}
	if request.OutputUnicodeNormalization != nil {
		if existing, ok := runtimeValues["output_unicode_normalization"]; ok && existing != *request.OutputUnicodeNormalization {
			return uuid.Nil, fmt.Errorf("runtime output Unicode normalization conflicts with the supplied field")
		}
		runtimeValues["output_unicode_normalization"] = *request.OutputUnicodeNormalization
	}
	prepare, finish := request.Prepare, request.Finish
	oldRoot, err := normalizeResetRoot(expectedOldRoot)
	if err != nil {
		return uuid.Nil, fmt.Errorf("normalize old output directory: %w", err)
	}
	newRoot, err = normalizeResetRoot(newRoot)
	if err != nil {
		return uuid.Nil, fmt.Errorf("normalize new output directory: %w", err)
	}
	runtimeValues["output_directory"] = newRoot
	expectedToolsRoot, err = normalizeResetRoot(expectedToolsRoot)
	if err != nil {
		return uuid.Nil, fmt.Errorf("normalize expected tools directory: %w", err)
	}
	if err := validateRuntimeSettingValues(runtimeValues); err != nil {
		return uuid.Nil, err
	}
	if oldRoot == newRoot {
		return uuid.Nil, fmt.Errorf("output reset requires a different output directory")
	}
	if prepare == nil || finish == nil {
		return uuid.Nil, fmt.Errorf("output reset preparation and finish callbacks are required")
	}
	allowedDirectories := make(map[string]struct{}, len(request.AllowedDirectories))
	allowedAncestors := make(map[string]struct{})
	for _, path := range request.AllowedDirectories {
		normalized, err := normalizeResetRoot(path)
		if err != nil || normalized == "" || (path != newRoot && !withinRoot(newRoot, path) && !withinRoot(path, newRoot)) {
			return uuid.Nil, fmt.Errorf("invalid planned output directory")
		}
		allowedDirectories[path] = struct{}{}
		if path != newRoot && withinRoot(path, newRoot) {
			allowedAncestors[path] = struct{}{}
		}
	}
	if len(allowedAncestors) > 64 {
		return uuid.Nil, fmt.Errorf("planned output ancestor chain is oversized")
	}
	for path := filepath.Dir(newRoot); ; path = filepath.Dir(path) {
		if _, ok := allowedAncestors[path]; !ok {
			break
		}
		delete(allowedAncestors, path)
		if path == filepath.Dir(path) {
			break
		}
	}
	if len(allowedAncestors) != 0 {
		return uuid.Nil, fmt.Errorf("planned output ancestors are not a contiguous chain")
	}
	token := uuid.New()
	err = WithExclusiveOutputAdmissionSession(ctx, repository.db, func(session *OutputResetSession) error {
		if err := session.lockToolsMoveGate(ctx); err != nil {
			return err
		}
		defer session.unlockToolsMoveGate()
		if err := session.begin(ctx, token, oldRoot, newRoot, expectedToolsRoot); err != nil {
			return err
		}
		record := func(directory OutputResetDirectory) error {
			if request.AllowedDirectories != nil {
				if _, ok := allowedDirectories[directory.Path]; !ok {
					return fmt.Errorf("output directory is not in the planned creation chain")
				}
			}
			return session.recordOutputResetDirectory(ctx, token, newRoot, allowedDirectories, directory)
		}
		if err := prepare(ctx, record); err != nil {
			return fmt.Errorf("prepare output reset: %w", err)
		}
		if request.RuntimeValuesAfterPrepare != nil {
			additionalValues, err := request.RuntimeValuesAfterPrepare(ctx)
			if err != nil {
				return fmt.Errorf("prepare output reset runtime values: %w", err)
			}
			for name, value := range additionalValues {
				runtimeValues[name] = value
			}
			if err := validateRuntimeSettingValues(runtimeValues); err != nil {
				return err
			}
		}
		journal, outcomes, err := session.commitReset(ctx, token, expectedToolsRoot, runtimeValues)
		if err != nil {
			return err
		}
		journal.CleanupOutcomes, err = json.Marshal(outcomes)
		if err != nil {
			return fmt.Errorf("encode output reset cleanup outcomes: %w", err)
		}
		if err := finish(ctx, journal); err != nil {
			return fmt.Errorf("finish output reset filesystem work: %w", err)
		}
		if err := session.markFinished(ctx, token); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return token, err
	}
	return token, nil
}

func (session *OutputResetSession) lockToolsMoveGate(ctx context.Context) error {
	if _, err := session.conn.ExecContext(ctx, "SELECT pg_advisory_lock($1, $2)", toolsCoordinationNamespace, toolsMoveGateKey); err != nil {
		return fmt.Errorf("acquire tools-root update gate: %w", err)
	}
	return nil
}

func (session *OutputResetSession) unlockToolsMoveGate() {
	var unlocked bool
	_ = session.conn.QueryRowContext(context.Background(), "SELECT pg_advisory_unlock($1, $2)", toolsCoordinationNamespace, toolsMoveGateKey).Scan(&unlocked)
}

// ReadUnresolvedOutputReset returns the durable reset requiring startup recovery.
func (repository *SetupManagerRepository) ReadUnresolvedOutputReset(ctx context.Context) (*OutputResetJournal, error) {
	journal := new(OutputResetJournal)
	err := repository.db.NewRaw(`SELECT token,state,old_root,new_root,created_directories,directory_manifest,cleanup_outcomes,created_at,updated_at
		FROM output_reset_journal WHERE state <> 'finished' ORDER BY created_at LIMIT 1`).Scan(ctx, journal)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read unresolved output reset: %w", err)
	}
	return journal, nil
}

// RecoverOutputReset serializes startup recovery with all output activity and
// keeps the admission gate until the service callback has resolved filesystem
// work recorded in the journal.
func (repository *SetupManagerRepository) RecoverOutputReset(ctx context.Context, recoverFilesystem func(context.Context, OutputResetJournal) error) error {
	if recoverFilesystem == nil {
		return fmt.Errorf("output reset recovery callback is required")
	}
	return WithExclusiveOutputAdmissionSession(ctx, repository.db, func(session *OutputResetSession) error {
		journal, err := session.readUnresolved(ctx)
		if err != nil || journal == nil {
			return err
		}
		if err := recoverFilesystem(ctx, *journal); err != nil {
			return fmt.Errorf("recover output reset filesystem work: %w", err)
		}
		return session.markFinished(ctx, journal.Token)
	})
}

func (session *OutputResetSession) begin(ctx context.Context, token uuid.UUID, oldRoot, newRoot, expectedToolsRoot string) error {
	if err := checkResetAllowed(ctx, session.conn, oldRoot, newRoot, expectedToolsRoot); err != nil {
		return err
	}
	_, err := session.conn.ExecContext(ctx, `INSERT INTO output_reset_journal(token,state,old_root,new_root)
		VALUES ($1,'preparing',$2,$3)`, token, oldRoot, newRoot)
	if err != nil {
		return fmt.Errorf("write preparing output reset journal: %w", err)
	}
	return nil
}

func checkResetAllowed(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, expectedRoot, newRoot, expectedToolsRoot string) error {
	var outputRoot, toolsRoot string
	err := queryer.QueryRowContext(ctx, `SELECT
		COALESCE(MAX(setting_value) FILTER (WHERE setting_name='output_directory'), ''),
		COALESCE(MAX(setting_value) FILTER (WHERE setting_name='tools_directory'), '')
		FROM app_setting WHERE setting_name IN ('output_directory','tools_directory')`).Scan(&outputRoot, &toolsRoot)
	if err != nil && err != sql.ErrNoRows {
		return fmt.Errorf("read current output directory: %w", err)
	}
	canonical, err := canonicalizeOptionalPersistedPath(outputRoot)
	if err != nil {
		return fmt.Errorf("read current output directory: %w", err)
	}
	if canonical != expectedRoot {
		return fmt.Errorf("output directory changed since reset was prepared")
	}
	canonicalTools, err := canonicalizeOptionalPersistedPath(toolsRoot)
	if err != nil {
		return fmt.Errorf("read current tools directory: %w", err)
	}
	if canonicalTools != expectedToolsRoot {
		return fmt.Errorf("tools directory changed since reset was prepared")
	}
	if err := validateOutputResetRoot(ctx, queryer, newRoot); err != nil {
		return err
	}
	var blocked bool
	err = queryer.QueryRowContext(ctx, `SELECT
		EXISTS (SELECT 1 FROM operation WHERE state='running') OR
		EXISTS (SELECT 1 FROM tools_execution_claim) OR
		EXISTS (
			SELECT 1 FROM source_analysis_artifact_cleanup_item item
			JOIN source_analysis_artifact artifact ON artifact.id=item.artifact_id
			JOIN source_analysis_work work ON work.id=artifact.work_id
			WHERE item.state='claimed'
		) OR
		EXISTS (
			SELECT 1 FROM source_analysis_work_artifact_binding binding
			JOIN source_analysis_work work ON work.id=binding.work_id
			WHERE binding.borrower_operation_id IS NOT NULL
		) OR
		EXISTS (
			SELECT 1 FROM source_analysis_artifact artifact
			JOIN source_analysis_work work ON work.id=artifact.work_id
			JOIN operation operation ON operation.id=artifact.owner_operation_id
			JOIN river_job job ON job.id=operation.river_job_id
			WHERE artifact.state='acquiring' AND operation.state='queued'
			AND job.state IN ('available','pending','running','retryable','scheduled')
		)`).Scan(&blocked)
	if err != nil {
		return fmt.Errorf("check output reset blockers: %w", err)
	}
	if blocked {
		return fmt.Errorf("output directory cannot be reset while operations or filesystem claims are active")
	}
	return nil
}

// validateOutputResetRoot checks the proposed root against every runtime path
// that a reset can invalidate. Queued tools moves are allowed unless their
// source or target roots overlap the proposed output root.
func validateOutputResetRoot(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, newRoot string) error {
	var toolsRoot, outputCase string
	if err := queryer.QueryRowContext(ctx, `SELECT
		COALESCE(MAX(setting_value) FILTER (WHERE setting_name='tools_directory'), ''),
		COALESCE(MAX(setting_value) FILTER (WHERE setting_name='output_case_sensitive'), 'true')
		FROM app_setting WHERE setting_name IN ('tools_directory','output_case_sensitive')`).Scan(&toolsRoot, &outputCase); err != nil {
		return fmt.Errorf("read runtime roots for output reset: %w", err)
	}
	caseSensitive := outputCase != "false"
	if err := validateRuntimeRootPair(toolsRoot, newRoot, caseSensitive); err != nil {
		return err
	}

	rows, err := queryer.QueryContext(ctx, "SELECT configured_path FROM source_root ORDER BY id")
	if err != nil {
		return fmt.Errorf("read source roots for output reset: %w", err)
	}
	for rows.Next() {
		var sourceRoot string
		if err := rows.Scan(&sourceRoot); err != nil {
			_ = rows.Close()
			return fmt.Errorf("read source root for output reset: %w", err)
		}
		if persistedPathsOverlap(newRoot, sourceRoot, caseSensitive) {
			_ = rows.Close()
			return fmt.Errorf("output directory overlaps with a source directory")
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("read source roots for output reset: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("read source roots for output reset: %w", err)
	}

	rows, err = queryer.QueryContext(ctx, `SELECT input_snapshot FROM operation
		WHERE kind='move_tools_root' AND state IN ('queued','running') ORDER BY id`)
	if err != nil {
		return fmt.Errorf("read active tools root moves: %w", err)
	}
	for rows.Next() {
		var snapshotJSON []byte
		if err := rows.Scan(&snapshotJSON); err != nil {
			_ = rows.Close()
			return fmt.Errorf("read active tools root move: %w", err)
		}
		var snapshot struct {
			OldRoot string `json:"old_root"`
			NewRoot string `json:"new_root"`
		}
		if err := json.Unmarshal(snapshotJSON, &snapshot); err != nil || snapshot.OldRoot == "" || snapshot.NewRoot == "" {
			_ = rows.Close()
			return fmt.Errorf("active tools root move has an invalid snapshot")
		}
		if persistedPathsOverlap(newRoot, snapshot.OldRoot, caseSensitive) || persistedPathsOverlap(newRoot, snapshot.NewRoot, caseSensitive) {
			_ = rows.Close()
			return fmt.Errorf("output directory overlaps with an active tools root move")
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("read active tools root moves: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("read active tools root moves: %w", err)
	}
	return nil
}

func (session *OutputResetSession) recordOutputResetDirectory(ctx context.Context, token uuid.UUID, newRoot string, allowedDirectories map[string]struct{}, directory OutputResetDirectory) error {
	path, err := normalizeResetRoot(directory.Path)
	if err != nil {
		return fmt.Errorf("output directory record must use a normalized absolute path")
	}
	if path != newRoot && !withinRoot(newRoot, path) {
		if _, planned := allowedDirectories[path]; !planned || !withinRoot(path, newRoot) {
			return fmt.Errorf("output directory record must be a normalized descendant of the new output root or a planned ancestor")
		}
	}
	if directory.Phase != "intent" && directory.Phase != "created" {
		return fmt.Errorf("invalid output directory journal phase")
	}
	encoded, err := json.Marshal(directory)
	if err != nil {
		return fmt.Errorf("encode output directory journal record: %w", err)
	}
	var result sql.Result
	if directory.Phase == "intent" {
		result, err = session.conn.ExecContext(ctx, `UPDATE output_reset_journal
			SET created_directories=array_append(created_directories,$2),directory_manifest=directory_manifest || jsonb_build_array($3::jsonb),updated_at=now()
			WHERE token=$1 AND state='preparing' AND NOT ($2=ANY(created_directories))`, token, path, string(encoded))
	} else {
		result, err = session.conn.ExecContext(ctx, `UPDATE output_reset_journal
			SET directory_manifest=(SELECT jsonb_agg(CASE WHEN item->>'path'=$2 THEN $3::jsonb ELSE item END ORDER BY ord) FROM jsonb_array_elements(directory_manifest) WITH ORDINALITY AS entries(item,ord)),updated_at=now()
			WHERE token=$1 AND state='preparing' AND EXISTS (SELECT 1 FROM jsonb_array_elements(directory_manifest) item WHERE item->>'path'=$2 AND item->>'phase'='intent')`, token, path, string(encoded))
	}
	if err != nil {
		return fmt.Errorf("record output directory: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil || changed != 1 {
		return fmt.Errorf("record output directory: journal is not preparing or intent is missing")
	}
	return nil
}

func (session *OutputResetSession) commitReset(ctx context.Context, token uuid.UUID, expectedToolsRoot string, runtimeValues map[string]string) (OutputResetJournal, outputResetOutcomes, error) {
	var journal OutputResetJournal
	outcomes := outputResetOutcomes{InvalidatedArtifactIDs: []uuid.UUID{}, RemovedCandidateOperationIDs: []uuid.UUID{}}
	tx, err := session.conn.BeginTx(ctx, nil)
	if err != nil {
		return journal, outcomes, fmt.Errorf("begin output reset transaction: %w", err)
	}
	rollback := func(err error) (OutputResetJournal, outputResetOutcomes, error) {
		_ = tx.Rollback()
		return journal, outcomes, err
	}
	if err := scanJournal(ctx, tx, token, &journal); err != nil {
		return rollback(fmt.Errorf("read preparing output reset journal: %w", err))
	}
	if journal.State != "preparing" {
		return rollback(fmt.Errorf("output reset journal is not preparing"))
	}
	if err := resetDomainState(ctx, tx, journal.OldRoot, journal.NewRoot, expectedToolsRoot, runtimeValues, &outcomes); err != nil {
		return rollback(err)
	}
	encoded, err := json.Marshal(outcomes)
	if err != nil {
		return rollback(fmt.Errorf("encode output reset outcomes: %w", err))
	}
	if _, err := tx.ExecContext(ctx, `UPDATE output_reset_journal SET state='committed',cleanup_outcomes=$2,updated_at=now()
			WHERE token=$1 AND state='preparing'`, token, string(encoded)); err != nil {
		return rollback(fmt.Errorf("commit output reset journal: %w", err))
	}
	if err := tx.Commit(); err != nil {
		return journal, outcomes, fmt.Errorf("commit output reset transaction: %w", err)
	}
	journal.State = "committed"
	journal.CleanupOutcomes = encoded
	return journal, outcomes, nil
}

func resetDomainState(ctx context.Context, tx *sql.Tx, oldRoot, newRoot, expectedToolsRoot string, runtimeValues map[string]string, outcomes *outputResetOutcomes) error {
	// Recheck blockers under the exclusive session gate, before taking domain locks.
	if err := checkResetAllowed(ctx, tx, oldRoot, newRoot, expectedToolsRoot); err != nil {
		return fmt.Errorf("recheck output reset admission: %w", err)
	}
	if err := validateRuntimeUpdateSQL(ctx, tx, expectedToolsRoot, oldRoot, newRoot, runtimeValues); err != nil {
		return fmt.Errorf("validate output reset runtime settings: %w", err)
	}
	var ids []uuid.UUID
	rows, err := tx.QueryContext(ctx, `SELECT id FROM source_analysis_artifact ORDER BY id`)
	if err != nil {
		return fmt.Errorf("list invalidated output artifacts: %w", err)
	}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	outcomes.InvalidatedArtifactIDs = ids
	rows, err = tx.QueryContext(ctx, `SELECT DISTINCT candidate.operation_id FROM source_scan_candidate candidate
		JOIN operation operation ON operation.id=candidate.operation_id
		WHERE operation.state='queued' ORDER BY candidate.operation_id`)
	if err != nil {
		return fmt.Errorf("list queued scan candidates: %w", err)
	}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		outcomes.RemovedCandidateOperationIDs = append(outcomes.RemovedCandidateOperationIDs, id)
	}
	if err := rows.Close(); err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, `UPDATE river_job job SET state='cancelled',finalized_at=now()
			FROM operation operation WHERE operation.river_job_id=job.id AND operation.state='queued'
			AND job.state IN ('available','pending','retryable','scheduled') AND job.attempted_at IS NULL`); err != nil {
		return fmt.Errorf("cancel queued application jobs: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE source_analysis_step SET
			state=CASE WHEN state IN ('pending','queued') THEN 'failed' ELSE state END,
			safe_error=CASE WHEN state IN ('pending','queued') THEN $1 ELSE safe_error END,
			skip_reason=CASE WHEN state IN ('pending','queued') THEN NULL ELSE skip_reason END,
			execution_operation_id=NULL,execution_operation_attempt=NULL,execution_job_id=NULL,updated_at=now()
			WHERE state IN ('pending','queued') OR execution_operation_id IN (SELECT id FROM operation WHERE state='queued')`, outputResetSafeError); err != nil {
		return fmt.Errorf("clear cancelled source analysis step executions: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM operation_source_work_hold WHERE operation_id IN (SELECT id FROM operation WHERE state='queued')`); err != nil {
		return fmt.Errorf("release queued source-work holds: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM operation_tool_read_hold WHERE operation_id IN (SELECT id FROM operation WHERE state='queued')`); err != nil {
		return fmt.Errorf("release queued tool holds: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM source_scan_candidate WHERE operation_id IN (SELECT id FROM operation WHERE state='queued')`); err != nil {
		return fmt.Errorf("remove queued scan candidates: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE operation SET state='failed',stage='finished',safe_error=$1,
			target_source_root_id=CASE WHEN source_analysis_mode IS NOT NULL THEN NULL ELSE target_source_root_id END,
			target_source_location_id=CASE WHEN source_analysis_mode IS NOT NULL THEN NULL ELSE target_source_location_id END,
			target_work_id=CASE WHEN source_analysis_mode IS NOT NULL THEN NULL ELSE target_work_id END,
			target_step=CASE WHEN source_analysis_mode IS NOT NULL THEN NULL ELSE target_step END,
			tools_read_required=false,rerun_target=CASE WHEN source_analysis_mode IS NOT NULL THEN false ELSE rerun_target END,
			finished_at=now(),updated_at=now()
			WHERE state='queued'`, outputResetSafeError); err != nil {
		return fmt.Errorf("fail queued operations: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM source_analysis_work_artifact_binding`); err != nil {
		return fmt.Errorf("remove invalidated artifact bindings: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM source_analysis_artifact`); err != nil {
		return fmt.Errorf("remove invalidated artifact ownership: %w", err)
	}
	if err := setRuntimeValuesSQL(ctx, tx, runtimeValues); err != nil {
		return err
	}
	return nil
}

func (session *OutputResetSession) readUnresolved(ctx context.Context) (*OutputResetJournal, error) {
	journal := new(OutputResetJournal)
	err := scanUnresolvedJournal(ctx, session.conn, journal)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read unresolved output reset: %w", err)
	}
	return journal, nil
}

func (session *OutputResetSession) markFinished(ctx context.Context, token uuid.UUID) error {
	result, err := session.conn.ExecContext(ctx, `UPDATE output_reset_journal SET state='finished',updated_at=now()
		WHERE token=$1 AND state IN ('preparing','committed')`, token)
	if err != nil {
		return fmt.Errorf("finish output reset journal: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil || changed != 1 {
		return fmt.Errorf("finish output reset journal: unresolved journal not found")
	}
	return nil
}

func scanJournal(ctx context.Context, tx *sql.Tx, token uuid.UUID, journal *OutputResetJournal) error {
	return tx.QueryRowContext(ctx, `SELECT token,state,old_root,new_root,created_directories,directory_manifest,cleanup_outcomes,created_at,updated_at
		FROM output_reset_journal WHERE token=$1 FOR UPDATE`, token).Scan(&journal.Token, &journal.State, &journal.OldRoot, &journal.NewRoot, pgdialect.Array(&journal.CreatedDirectories), &journal.DirectoryManifest, &journal.CleanupOutcomes, &journal.CreatedAt, &journal.UpdatedAt)
}

func scanUnresolvedJournal(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, journal *OutputResetJournal) error {
	return queryer.QueryRowContext(ctx, `SELECT token,state,old_root,new_root,created_directories,directory_manifest,cleanup_outcomes,created_at,updated_at
		FROM output_reset_journal WHERE state <> 'finished' ORDER BY created_at LIMIT 1`).Scan(&journal.Token, &journal.State, &journal.OldRoot, &journal.NewRoot, pgdialect.Array(&journal.CreatedDirectories), &journal.DirectoryManifest, &journal.CleanupOutcomes, &journal.CreatedAt, &journal.UpdatedAt)
}

func normalizeResetRoot(root string) (string, error) {
	if root == "" {
		return "", nil
	}
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return "", fmt.Errorf("path must be a normalized absolute path")
	}
	return root, nil
}

func withinRoot(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != "." && rel != ".." && !filepath.IsAbs(rel) && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
