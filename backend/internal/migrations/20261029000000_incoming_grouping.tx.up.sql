CREATE TABLE incoming_grouping_state (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    revision bigint NOT NULL DEFAULT 0 CHECK (revision >= 0),
    needs_refresh boolean NOT NULL DEFAULT true,
    updated_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO incoming_grouping_state(singleton) VALUES (true);

CREATE TABLE incoming_group (
    id uuid PRIMARY KEY,
    manual boolean NOT NULL DEFAULT false,
    revision text NOT NULL,
    diagnostics jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(diagnostics) = 'array'),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE incoming_group_member (
    group_id uuid NOT NULL REFERENCES incoming_group(id) ON DELETE CASCADE,
    variant_id uuid NOT NULL,
    ready boolean NOT NULL DEFAULT true,
    PRIMARY KEY (group_id, variant_id),
    UNIQUE (variant_id)
);

-- Deliberately copied fence data, not references: inventory/work retirement must
-- never be blocked by persisted group corrections.
CREATE TABLE incoming_group_member_location (
    group_id uuid NOT NULL,
    variant_id uuid NOT NULL,
    source_root_id uuid NOT NULL,
    work_id uuid NOT NULL,
    location_id uuid NOT NULL,
    configured_path text NOT NULL,
    inventory_path text NOT NULL,
    relative_path text NOT NULL,
    size_bytes bigint NOT NULL CHECK (size_bytes >= 0),
    mtime timestamptz NOT NULL,
    PRIMARY KEY (group_id, variant_id, source_root_id, relative_path),
    FOREIGN KEY (group_id, variant_id) REFERENCES incoming_group_member(group_id, variant_id) ON DELETE CASCADE
);
CREATE INDEX incoming_group_member_location_variant_idx
    ON incoming_group_member_location(variant_id);
