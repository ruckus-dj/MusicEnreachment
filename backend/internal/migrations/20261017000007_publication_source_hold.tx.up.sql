ALTER TABLE public.track_publication_attempt
    ADD CONSTRAINT track_publication_attempt_operation_epoch_unique UNIQUE (operation_id, epoch);

CREATE TABLE public.publication_source_hold (
    operation_id uuid NOT NULL,
    application_attempt integer NOT NULL,
    river_job_id bigint NOT NULL,
    epoch uuid NOT NULL,
    hold_uuid uuid NOT NULL,
    source_root_id uuid NOT NULL,
    source_location_id uuid NOT NULL,
    work_id uuid NOT NULL,
    media_variant_id uuid NOT NULL,
    configured_path text NOT NULL,
    inventory_path text NOT NULL,
    relative_path text NOT NULL,
    size_bytes bigint NOT NULL,
    mtime timestamp with time zone NOT NULL,
    CONSTRAINT publication_source_hold_pkey PRIMARY KEY (operation_id),
    CONSTRAINT publication_source_hold_operation_fkey FOREIGN KEY (operation_id, application_attempt, river_job_id)
        REFERENCES public.operation(id, attempt, river_job_id) ON DELETE RESTRICT,
    CONSTRAINT publication_source_hold_attempt_fkey FOREIGN KEY (operation_id, epoch)
        REFERENCES public.track_publication_attempt(operation_id, epoch) ON DELETE RESTRICT,
    CONSTRAINT publication_source_hold_root_fkey FOREIGN KEY (source_root_id)
        REFERENCES public.source_root(id) ON DELETE RESTRICT,
    CONSTRAINT publication_source_hold_location_fkey FOREIGN KEY (source_location_id)
        REFERENCES public.source_location(id) ON DELETE RESTRICT,
    CONSTRAINT publication_source_hold_work_fkey FOREIGN KEY (work_id)
        REFERENCES public.source_analysis_work(id) ON DELETE RESTRICT,
    CONSTRAINT publication_source_hold_variant_fkey FOREIGN KEY (media_variant_id)
        REFERENCES public.media_variant(id) ON DELETE RESTRICT,
    CONSTRAINT publication_source_hold_uuid_key UNIQUE (hold_uuid),
    CONSTRAINT publication_source_hold_attempt_check CHECK (application_attempt > 0),
    CONSTRAINT publication_source_hold_job_check CHECK (river_job_id > 0),
    CONSTRAINT publication_source_hold_configured_path_check CHECK (configured_path <> ''),
    CONSTRAINT publication_source_hold_inventory_path_check CHECK (inventory_path <> ''),
    CONSTRAINT publication_source_hold_relative_path_check CHECK (relative_path <> ''),
    CONSTRAINT publication_source_hold_size_check CHECK (size_bytes >= 0)
);

CREATE INDEX publication_source_hold_source_idx
    ON public.publication_source_hold(source_root_id, source_location_id, work_id, media_variant_id);
