-- Minimal saved technical analysis result of one source file, plus the operation
-- target and resource holds the analyze_source kind needs. This is the physical
-- slice of media_variant the current consumer reads: the container and every
-- audio stream stay in ffprobe_json and are projected by the typed service
-- read-model, so no parameter is duplicated into a SQL column before a query
-- consumer exists. A variant is immutable once written; a new analysis inserts
-- a new row. quality_score, lossless, chromaprint_hash and source digests are
-- deliberately absent: the DBML is conceptual, and this slice must not store
-- invented values for them.
CREATE TABLE media_variant (
    id uuid PRIMARY KEY,
    size_bytes bigint NOT NULL,
    analysis_policy_version integer NOT NULL DEFAULT 1,
    ffprobe_version text NOT NULL,
    ffprobe_json jsonb NOT NULL,
    observed_tags jsonb NOT NULL DEFAULT '{}'::jsonb,
    inspected_at timestamptz NOT NULL,
    -- Provenance, not a retention guard. Deliberately not a foreign key, for the
    -- same reason source_root.last_applied_operation_id is not one: deleting a
    -- succeeded operation later must not delete or block the durable result.
    -- Step 2 removes orphan variants explicitly inside a persistence transaction.
    applied_operation_id uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT media_variant_size_not_negative CHECK (size_bytes >= 0),
    CONSTRAINT media_variant_analysis_policy_version_positive CHECK (analysis_policy_version >= 1),
    CONSTRAINT media_variant_ffprobe_version_not_empty CHECK (ffprobe_version <> '')
);

-- A location references the variant of its last successful analysis. NULL is the
-- normal state of a location that was never analyzed, and it is the state of
-- every location this migration upgrades: nothing is backfilled, so an existing
-- location never gains an artificial variant. RESTRICT keeps a variant a
-- location still references from disappearing under it; unlinking is an explicit
-- write.
ALTER TABLE source_location
    ADD COLUMN media_variant_id uuid;

ALTER TABLE source_location
    ADD CONSTRAINT source_location_media_variant_id_fkey
    FOREIGN KEY (media_variant_id) REFERENCES media_variant(id) ON DELETE RESTRICT;

CREATE INDEX source_location_media_variant_idx
    ON source_location (media_variant_id);

ALTER TABLE operation
    ADD COLUMN target_source_location_id uuid,
    -- The managed FFmpeg installation an analysis holds for its whole life. Named
    -- by the plan. It is a read hold, never a mutation target, so it is not part
    -- of operation_one_active_installation nor of the exclusion constraint's
    -- installation branch; RESTRICT refuses deleting a held installation.
    ADD COLUMN analysis_installation_id uuid,
    -- The previous variant an active analysis keeps alive until it reaches a
    -- terminal state, then the hold is cleared.
    ADD COLUMN analysis_media_variant_id uuid;

-- A terminal operation must survive the deletion of the location it named: the
-- snapshot keeps its identity, the live target may become NULL. An active
-- operation, however, must never lose its target this way, which the
-- active-shape CHECK below enforces on top of this SET NULL.
ALTER TABLE operation
    ADD CONSTRAINT operation_target_source_location_id_fkey
    FOREIGN KEY (target_source_location_id) REFERENCES source_location(id) ON DELETE SET NULL;

ALTER TABLE operation
    ADD CONSTRAINT operation_analysis_installation_id_fkey
    FOREIGN KEY (analysis_installation_id) REFERENCES tool_installation(id) ON DELETE RESTRICT;

ALTER TABLE operation
    ADD CONSTRAINT operation_analysis_media_variant_id_fkey
    FOREIGN KEY (analysis_media_variant_id) REFERENCES media_variant(id) ON DELETE RESTRICT;

ALTER TABLE operation
    DROP CONSTRAINT operation_kind_check,
    ADD CONSTRAINT operation_kind_check CHECK (
        kind IN ('install', 'activate', 'delete', 'move_tools_root', 'scan_source', 'analyze_source')
    );

ALTER TABLE operation
    DROP CONSTRAINT operation_target_identity,
    ADD CONSTRAINT operation_target_identity CHECK (
        kind IN ('move_tools_root', 'scan_source', 'analyze_source')
        OR COALESCE(input_snapshot ->> 'target_identity', '') <> ''
    );

ALTER TABLE operation
    DROP CONSTRAINT operation_mutation_has_target_installation,
    ADD CONSTRAINT operation_mutation_has_target_installation CHECK (
        kind IN ('move_tools_root', 'scan_source', 'analyze_source')
        OR state NOT IN ('queued', 'running')
        OR target_installation_id IS NOT NULL
    );

-- Shape of the new kind. An active analysis names a source root, which is what
-- the root exclusivity below keys on, and never uses the mutation target column:
-- its installation lives in analysis_installation_id. An active analysis must
-- also still name its location; a terminal snapshot may have lost it through the
-- SET NULL above.
ALTER TABLE operation
    ADD CONSTRAINT operation_analysis_target_shape CHECK (
        kind <> 'analyze_source'
        OR (
            target_installation_id IS NULL
            AND (state NOT IN ('queued', 'running') OR target_source_root_id IS NOT NULL)
        )
    );

ALTER TABLE operation
    ADD CONSTRAINT operation_active_analysis_has_target_location CHECK (
        kind <> 'analyze_source'
        OR state NOT IN ('queued', 'running')
        OR target_source_location_id IS NOT NULL
    );

ALTER TABLE operation
    ADD CONSTRAINT operation_active_analysis_has_installation CHECK (
        kind <> 'analyze_source'
        OR state NOT IN ('queued', 'running')
        OR analysis_installation_id IS NOT NULL
    );

-- Root exclusivity now covers both root-targeting kinds: an active scan and an
-- active analysis mutually exclude each other on one root, and two analyses of
-- one root exclude each other. Other roots stay independent. The install and
-- move branches are unchanged, so the existing tools-root/installation
-- constraints still hold.
ALTER TABLE operation DROP CONSTRAINT operation_active_tools_operations_exclusive;

ALTER TABLE operation ADD CONSTRAINT operation_active_tools_operations_exclusive
    EXCLUDE USING gist (
        numrange(
            CASE
                WHEN kind = 'move_tools_root' THEN NULL
                WHEN kind IN ('scan_source', 'analyze_source') THEN hashtextextended(COALESCE(target_source_root_id::text, kind), 0)::numeric
                WHEN target_installation_id IS NOT NULL THEN hashtextextended(target_installation_id::text, 0)::numeric
                ELSE hashtextextended(input_snapshot ->> 'target_identity', 0)::numeric
            END,
            CASE
                WHEN kind = 'move_tools_root' THEN NULL
                WHEN kind IN ('scan_source', 'analyze_source') THEN hashtextextended(COALESCE(target_source_root_id::text, kind), 0)::numeric
                WHEN target_installation_id IS NOT NULL THEN hashtextextended(target_installation_id::text, 0)::numeric
                ELSE hashtextextended(input_snapshot ->> 'target_identity', 0)::numeric
            END,
            CASE WHEN kind = 'move_tools_root' THEN '()' ELSE '[]' END
        ) WITH &&
    ) WHERE (
        state IN ('queued', 'running')
        AND (
            kind = 'move_tools_root'
            OR kind IN ('scan_source', 'analyze_source')
            OR target_installation_id IS NOT NULL
            OR COALESCE(input_snapshot ->> 'target_identity', '') <> ''
        )
    );

-- At most one active root-targeting operation per root. This partial unique
-- index is the durable guard (not a process mutex) and is checked while the
-- exclusion constraint above holds the conflicting row, so a scan or an
-- analysis never races a root deletion.
DROP INDEX operation_one_active_source_root_scan;

CREATE UNIQUE INDEX operation_one_active_source_root_operation
    ON operation (target_source_root_id)
    WHERE kind IN ('scan_source', 'analyze_source') AND state IN ('queued', 'running');

CREATE INDEX operation_analysis_installation_idx
    ON operation (analysis_installation_id)
    WHERE analysis_installation_id IS NOT NULL;
