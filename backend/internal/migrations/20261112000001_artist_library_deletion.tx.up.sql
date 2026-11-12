ALTER TABLE public.local_artist
    ADD COLUMN catalog_visible boolean NOT NULL DEFAULT true;

ALTER TABLE public.operation
    DROP CONSTRAINT operation_library_deletion_target_shape;

ALTER TABLE public.operation
    ADD CONSTRAINT operation_library_deletion_target_shape CHECK (
        kind <> 'delete_library_v1'
        OR (
            jsonb_typeof(input_snapshot->'scope') = 'object'
            AND jsonb_typeof(input_snapshot->'scope'->'publication_ids') = 'array'
            AND jsonb_typeof(input_snapshot->'scope'->'manual_receipt_request_ids') = 'array'
            AND public.valid_library_deletion_uuid_array(input_snapshot->'scope'->'publication_ids') IS TRUE
            AND public.valid_library_deletion_uuid_array(input_snapshot->'scope'->'manual_receipt_request_ids') IS TRUE
            AND length(input_snapshot->'scope'->>'dependency_digest') = 64
            AND input_snapshot->'scope'->>'dependency_digest' ~ '^[0-9a-f]{64}$'
            AND (
                ((input_snapshot->'scope')->>'kind' IN ('publication','release')
                    AND input_snapshot->'scope' ?& ARRAY['release_id','kind','publication_ids','manual_receipt_request_ids','dependency_digest']
                    AND (input_snapshot->'scope') - ARRAY['release_id','kind','publication_ids','manual_receipt_request_ids','dependency_digest'] = '{}'::jsonb
                    AND input_snapshot->'scope'->>'release_id' ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
                    AND ((input_snapshot->'scope'->>'kind'='release') OR
                        (jsonb_array_length(input_snapshot->'scope'->'publication_ids')=1 AND jsonb_array_length(input_snapshot->'scope'->'manual_receipt_request_ids')=0)))
                OR
                ((input_snapshot->'scope')->>'kind'='artist'
                    AND input_snapshot->'scope' ?& ARRAY['artist_id','artist_release_ids','artist_reference_release_ids','warning_release_ids','kind','publication_ids','manual_receipt_request_ids','dependency_digest']
                    AND (input_snapshot->'scope') - ARRAY['artist_id','artist_release_ids','artist_reference_release_ids','warning_release_ids','kind','publication_ids','manual_receipt_request_ids','dependency_digest'] = '{}'::jsonb
                    AND input_snapshot->'scope'->>'artist_id' ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
                    AND jsonb_typeof(input_snapshot->'scope'->'artist_release_ids')='array'
                    AND jsonb_typeof(input_snapshot->'scope'->'artist_reference_release_ids')='array'
                    AND jsonb_typeof(input_snapshot->'scope'->'warning_release_ids')='array'
                    AND public.valid_library_deletion_uuid_array(input_snapshot->'scope'->'artist_release_ids') IS TRUE
                    AND public.valid_library_deletion_uuid_array(input_snapshot->'scope'->'artist_reference_release_ids') IS TRUE
                    AND public.valid_library_deletion_uuid_array(input_snapshot->'scope'->'warning_release_ids') IS TRUE)
            )
        ) IS TRUE
    );

CREATE OR REPLACE FUNCTION public.guard_manual_matching_receipt_retirement() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    owner_operation public.operation%ROWTYPE;
    authorized_release boolean;
BEGIN
    IF TG_OP = 'INSERT' THEN
        IF NEW.local_release_id IS NULL THEN
            RAISE EXCEPTION 'manual matching receipt must initially reference a live release';
        END IF;
        NEW.original_local_release_id := NEW.local_release_id;
        NEW.retired_by_operation_id := NULL;
        RETURN NEW;
    END IF;

    IF (to_jsonb(NEW) - ARRAY['local_release_id','retired_by_operation_id','updated_at'])
       IS DISTINCT FROM (to_jsonb(OLD) - ARRAY['local_release_id','retired_by_operation_id','updated_at']) THEN
        RAISE EXCEPTION 'manual matching receipt proof is immutable';
    END IF;
    IF OLD.local_release_id IS NULL OR OLD.retired_by_operation_id IS NOT NULL
       OR NEW.local_release_id IS NOT NULL OR NEW.retired_by_operation_id IS NULL THEN
        RAISE EXCEPTION 'manual matching receipt can only be retired once';
    END IF;

    SELECT * INTO owner_operation
    FROM public.operation
    WHERE id = NEW.retired_by_operation_id
    FOR UPDATE;
    IF NOT FOUND OR owner_operation.kind <> 'delete_library_v1' OR owner_operation.state <> 'running'
       OR owner_operation.river_job_id IS NULL
       OR jsonb_array_length(owner_operation.input_snapshot->'residual_publication_ids') <> 0 THEN
        RAISE EXCEPTION 'manual matching receipt retirement lacks current library-deletion authority';
    END IF;
    authorized_release :=
        (owner_operation.input_snapshot->'scope'->>'kind'='release'
         AND owner_operation.input_snapshot->'scope'->>'release_id'=OLD.original_local_release_id::text)
        OR (owner_operation.input_snapshot->'scope'->>'kind'='artist'
            AND owner_operation.input_snapshot->'scope'->'artist_release_ids' @> jsonb_build_array(OLD.original_local_release_id::text));
    IF authorized_release IS NOT TRUE
       OR NOT (owner_operation.input_snapshot->'scope'->'manual_receipt_request_ids' @> jsonb_build_array(OLD.request_id::text)) THEN
        RAISE EXCEPTION 'manual matching receipt retirement lacks current library-deletion authority';
    END IF;
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION public.check_manual_matching_receipt_retirement() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    receipt public.manual_matching_receipt%ROWTYPE;
    owner_operation public.operation%ROWTYPE;
    authorized_release boolean;
BEGIN
    SELECT * INTO receipt FROM public.manual_matching_receipt WHERE request_id = NEW.request_id;
    IF NOT FOUND OR receipt.retired_by_operation_id IS NULL THEN
        RETURN NULL;
    END IF;
    SELECT * INTO owner_operation FROM public.operation WHERE id = receipt.retired_by_operation_id;
    authorized_release :=
        (owner_operation.input_snapshot->'scope'->>'kind'='release'
         AND owner_operation.input_snapshot->'scope'->>'release_id'=receipt.original_local_release_id::text)
        OR (owner_operation.input_snapshot->'scope'->>'kind'='artist'
            AND owner_operation.input_snapshot->'scope'->'artist_release_ids' @> jsonb_build_array(receipt.original_local_release_id::text));
    IF NOT FOUND OR owner_operation.kind <> 'delete_library_v1' OR owner_operation.state <> 'succeeded'
       OR receipt.local_release_id IS NOT NULL OR EXISTS (SELECT 1 FROM public.local_release WHERE id = receipt.original_local_release_id)
       OR authorized_release IS NOT TRUE
       OR NOT (owner_operation.input_snapshot->'scope'->'manual_receipt_request_ids' @> jsonb_build_array(receipt.request_id::text))
       OR jsonb_array_length(owner_operation.input_snapshot->'residual_publication_ids') <> 0 THEN
        RAISE EXCEPTION 'manual matching receipt retirement did not settle with its library deletion';
    END IF;
    RETURN NULL;
END;
$$;
