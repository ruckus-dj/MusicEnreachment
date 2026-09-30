CREATE TABLE source_root (
    id uuid PRIMARY KEY,
    configured_path text NOT NULL,
    display_name text NOT NULL,
    enabled boolean NOT NULL DEFAULT true,
    scan_generation bigint NOT NULL DEFAULT 0,
    inventory_path text,
    last_successful_scan_at timestamptz,
    last_applied_operation_id uuid,
    status text NOT NULL DEFAULT 'unknown',
    safe_error text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT source_root_configured_path_absolute CHECK (configured_path LIKE '/%'),
    CONSTRAINT source_root_display_name_not_empty CHECK (display_name <> ''),
    CONSTRAINT source_root_scan_generation_not_negative CHECK (scan_generation >= 0),
    CONSTRAINT source_root_status_allowed CHECK (status IN ('unknown', 'available', 'unavailable')),
    CONSTRAINT source_root_unavailable_has_error CHECK (status <> 'unavailable' OR safe_error IS NOT NULL),
    CONSTRAINT source_root_inventory_path_matches_generation CHECK (
        inventory_path IS NULL OR scan_generation > 0
    ),
    CONSTRAINT source_root_configured_path_unique UNIQUE (configured_path)
);

CREATE TABLE source_location (
    id uuid PRIMARY KEY,
    source_root_id uuid NOT NULL REFERENCES source_root(id) ON DELETE CASCADE,
    relative_path text NOT NULL,
    size_bytes bigint NOT NULL,
    mtime timestamptz NOT NULL,
    last_seen_scan_generation bigint NOT NULL,
    probe_status text NOT NULL,
    safe_error text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT source_location_size_not_negative CHECK (size_bytes >= 0),
    CONSTRAINT source_location_generation_positive CHECK (last_seen_scan_generation > 0),
    CONSTRAINT source_location_relative_path_not_empty CHECK (relative_path <> ''),
    CONSTRAINT source_location_probe_status_allowed CHECK (probe_status IN ('audio', 'no_audio', 'probe_error')),
    CONSTRAINT source_location_probe_error_has_error CHECK (probe_status <> 'probe_error' OR safe_error IS NOT NULL),
    CONSTRAINT source_location_probe_status_has_no_error CHECK (probe_status = 'probe_error' OR safe_error IS NULL),
    CONSTRAINT source_location_root_path_unique UNIQUE (source_root_id, relative_path)
);

CREATE INDEX source_location_root_id_idx
    ON source_location (source_root_id);

CREATE TABLE source_scan_candidate (
    id uuid PRIMARY KEY,
    operation_id uuid NOT NULL REFERENCES operation(id) ON DELETE CASCADE,
    relative_path text NOT NULL,
    size_bytes bigint NOT NULL,
    mtime timestamptz NOT NULL,
    probe_status text NOT NULL,
    safe_error text,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT source_scan_candidate_operation_path_unique UNIQUE (operation_id, relative_path),
    CONSTRAINT source_scan_candidate_relative_path_not_empty CHECK (relative_path <> ''),
    CONSTRAINT source_scan_candidate_size_not_negative CHECK (size_bytes >= 0),
    CONSTRAINT source_scan_candidate_probe_status_allowed CHECK (probe_status IN ('audio', 'no_audio', 'probe_error')),
    CONSTRAINT source_scan_candidate_probe_error_has_error CHECK (probe_status <> 'probe_error' OR safe_error IS NOT NULL),
    CONSTRAINT source_scan_candidate_probe_status_has_no_error CHECK (probe_status = 'probe_error' OR safe_error IS NULL)
);

CREATE INDEX source_scan_candidate_operation_idx
    ON source_scan_candidate (operation_id);

ALTER TABLE operation
    DROP CONSTRAINT operation_mutation_has_target_installation,
    ADD CONSTRAINT operation_mutation_has_target_installation
    CHECK (
        kind = 'move_tools_root'
        OR kind = 'scan_source'
        OR state NOT IN ('queued', 'running')
        OR target_installation_id IS NOT NULL
    );

ALTER TABLE operation
    DROP CONSTRAINT operation_target_identity,
    ADD CONSTRAINT operation_target_identity CHECK (
        kind IN ('move_tools_root', 'scan_source')
        OR COALESCE(input_snapshot ->> 'target_identity', '') <> ''
    );

ALTER TABLE operation
    ADD COLUMN target_source_root_id uuid REFERENCES source_root(id) ON DELETE SET NULL;

ALTER TABLE operation
    DROP CONSTRAINT operation_kind_check,
    ADD CONSTRAINT operation_kind_check CHECK (kind IN ('install', 'activate', 'delete', 'move_tools_root', 'scan_source'));

-- A tools-root move has no target row, so it must keep the unbounded interval it
-- had before scan_source existed: any other active operation, including an active
-- scan, collides with it. The interval must be a numrange: int8range cannot
-- represent a canonical empty range over the whole bigint domain (Postgres
-- overflows converting the exclusive -infinity sentinel), so an int8range here
-- makes every move insert fail with "bigint out of range" under the GiST index.
ALTER TABLE operation DROP CONSTRAINT operation_active_tools_operations_exclusive;

ALTER TABLE operation ADD CONSTRAINT operation_active_tools_operations_exclusive
    EXCLUDE USING gist (
        numrange(
            CASE
                WHEN kind = 'move_tools_root' THEN NULL
                WHEN kind = 'scan_source' THEN hashtextextended(COALESCE(target_source_root_id::text, kind), 0)::numeric
                WHEN target_installation_id IS NOT NULL THEN hashtextextended(target_installation_id::text, 0)::numeric
                ELSE hashtextextended(input_snapshot ->> 'target_identity', 0)::numeric
            END,
            CASE
                WHEN kind = 'move_tools_root' THEN NULL
                WHEN kind = 'scan_source' THEN hashtextextended(COALESCE(target_source_root_id::text, kind), 0)::numeric
                WHEN target_installation_id IS NOT NULL THEN hashtextextended(target_installation_id::text, 0)::numeric
                ELSE hashtextextended(input_snapshot ->> 'target_identity', 0)::numeric
            END,
            CASE WHEN kind = 'move_tools_root' THEN '()' ELSE '[]' END
        ) WITH &&
    ) WHERE (
        state IN ('queued', 'running')
        AND (
            kind = 'move_tools_root'
            OR kind = 'scan_source'
            OR target_installation_id IS NOT NULL
            OR COALESCE(input_snapshot ->> 'target_identity', '') <> ''
        )
    );

-- At most one active scan per root. This partial unique index is the durable
-- guard (not a process mutex) and is checked while the exclusion constraint
-- above holds the conflicting row, so a scan never races a root deletion.
CREATE UNIQUE INDEX operation_one_active_source_root_scan
    ON operation (target_source_root_id)
    WHERE kind = 'scan_source' AND state IN ('queued', 'running');

CREATE INDEX operation_active_source_root_idx
    ON operation (target_source_root_id)
    WHERE kind = 'scan_source' AND state IN ('queued', 'running');

CREATE INDEX operation_scan_source_updated_at_idx
    ON operation (updated_at)
    WHERE kind = 'scan_source' AND state IN ('queued', 'running');

CREATE INDEX operation_target_source_root_idx
    ON operation (target_source_root_id)
    WHERE target_source_root_id IS NOT NULL;
