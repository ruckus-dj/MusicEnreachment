ALTER TABLE source_analysis_artifact
    ADD COLUMN requested_steps text[] NOT NULL DEFAULT '{}'
        CHECK (requested_steps <@ ARRAY['sha256','probe','fingerprint']::text[]),
    ADD COLUMN requested_steps_known boolean NOT NULL DEFAULT false,
    ADD CONSTRAINT source_analysis_artifact_requested_steps_unique
        CHECK (cardinality(array_positions(requested_steps, 'sha256')) <= 1
            AND cardinality(array_positions(requested_steps, 'probe')) <= 1
            AND cardinality(array_positions(requested_steps, 'fingerprint')) <= 1),
    ADD CONSTRAINT source_analysis_artifact_requested_steps_order
        CHECK ((array_position(requested_steps, 'sha256') IS NULL
                OR array_position(requested_steps, 'probe') IS NULL
                OR array_position(requested_steps, 'sha256') < array_position(requested_steps, 'probe'))
            AND (array_position(requested_steps, 'probe') IS NULL
                OR array_position(requested_steps, 'fingerprint') IS NULL
                OR array_position(requested_steps, 'probe') < array_position(requested_steps, 'fingerprint'))),
    ADD CONSTRAINT source_analysis_artifact_unknown_requested_steps_empty
        CHECK (requested_steps_known OR cardinality(requested_steps) = 0);

UPDATE source_analysis_artifact artifact
SET requested_steps = ARRAY(
        SELECT step
        FROM unnest(binding.requested_steps) AS steps(step)
        WHERE step = ANY (ARRAY['sha256','probe','fingerprint']::text[])
        GROUP BY step
        ORDER BY array_position(ARRAY['sha256','probe','fingerprint']::text[], step)
    ),
    requested_steps_known = true
FROM source_analysis_work_artifact_binding binding
WHERE binding.artifact_id = artifact.id;

-- An unbound artifact's creator execution can prove only its initial request;
-- it cannot prove the cumulative union from later borrowers that may have been
-- released or deleted. Keep that history explicitly unknown rather than
-- attributing every execution for the work item to every artifact.
