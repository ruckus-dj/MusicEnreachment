-- Evaluations are replaced per group only after all persistence fences pass.
-- These rows are snapshots: group refreshes and source retirement never cascade
-- into matching evidence or create references to local media entities.
CREATE TABLE incoming_match_evaluation (
    group_id uuid PRIMARY KEY,
    group_revision text NOT NULL,
    groups_revision text NOT NULL,
    grouping_epoch bigint NOT NULL CHECK (grouping_epoch >= 0),
    policy_version text NOT NULL CHECK (length(btrim(policy_version)) > 0),
    provider_source_id uuid NOT NULL,
    provider_id uuid NOT NULL,
    provider_configuration_identity text NOT NULL,
    provider_namespace text NOT NULL,
    provider_endpoint text NOT NULL,
    cache_key text NOT NULL,
    cache_revision text,
    provider_snapshot_revision text NOT NULL CHECK (length(btrim(provider_snapshot_revision)) > 0),
    provider_fences jsonb NOT NULL CHECK (jsonb_typeof(provider_fences) = 'object'),
    evaluated_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (provider_id, provider_source_id)
        REFERENCES provider_source(provider_id, id) ON DELETE RESTRICT
);

-- A header with no candidate rows represents an evaluated-empty result; no
-- header represents not-yet-evaluated. Members and locations are copied fences.
CREATE TABLE incoming_match_evaluation_member (
    group_id uuid NOT NULL,
    file_member_id uuid NOT NULL,
    work_id uuid NOT NULL,
    source_sha256 bytea CHECK (source_sha256 IS NULL OR octet_length(source_sha256) = 32),
    PRIMARY KEY (group_id, file_member_id),
    FOREIGN KEY (group_id) REFERENCES incoming_match_evaluation(group_id) ON DELETE CASCADE
);

CREATE TABLE incoming_match_evaluation_location (
    group_id uuid NOT NULL,
    file_member_id uuid NOT NULL,
    source_root_id uuid NOT NULL,
    location_id uuid NOT NULL,
    configured_path text NOT NULL,
    inventory_path text NOT NULL,
    relative_path text NOT NULL,
    size_bytes bigint NOT NULL CHECK (size_bytes >= 0),
    mtime timestamptz NOT NULL,
    PRIMARY KEY (group_id, file_member_id, source_root_id, relative_path),
    FOREIGN KEY (group_id, file_member_id)
        REFERENCES incoming_match_evaluation_member(group_id, file_member_id) ON DELETE CASCADE
);

CREATE TABLE incoming_match_candidate (
    group_id uuid NOT NULL,
    file_member_id uuid NOT NULL,
    provider_id uuid NOT NULL,
    provider_source_id uuid NOT NULL,
    release_id uuid NOT NULL,
    score double precision NOT NULL CHECK (score >= 0 AND score <= 1),
    evidence jsonb NOT NULL CHECK (jsonb_typeof(evidence) = 'object'),
    PRIMARY KEY (group_id, file_member_id, provider_source_id, release_id),
    FOREIGN KEY (group_id, file_member_id)
        REFERENCES incoming_match_evaluation_member(group_id, file_member_id) ON DELETE CASCADE,
    FOREIGN KEY (provider_id, provider_source_id)
        REFERENCES provider_source(provider_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (provider_id, release_id)
        REFERENCES provider_release(provider_id, id) ON DELETE RESTRICT
);

-- Position assignments reference the real source-scoped track and are scoped
-- to one file/release candidate. The same provider position may be assigned to
-- different file members (for alternate folders/variants).
-- Track identity is provider-global and survives replacement of a source's
-- mutable ordered projection. Candidate positions must not prevent refreshes.
ALTER TABLE provider_release_track
    ADD CONSTRAINT provider_release_track_match_target
    UNIQUE (provider_id, release_id, id);

CREATE TABLE incoming_match_candidate_track (
    group_id uuid NOT NULL,
    file_member_id uuid NOT NULL,
    provider_source_id uuid NOT NULL,
    provider_id uuid NOT NULL,
    release_id uuid NOT NULL,
    track_id uuid NOT NULL,
    medium integer NOT NULL CHECK (medium >= 0),
    position integer NOT NULL CHECK (position >= 0),
    PRIMARY KEY (group_id, file_member_id, provider_source_id, release_id, medium, position),
    FOREIGN KEY (group_id, file_member_id, provider_source_id, release_id)
        REFERENCES incoming_match_candidate(group_id, file_member_id, provider_source_id, release_id) ON DELETE CASCADE,
    FOREIGN KEY (provider_id, release_id, track_id)
        REFERENCES provider_release_track(provider_id, release_id, id) ON DELETE RESTRICT
);
