ALTER TABLE public.source_cleanup_item
    ADD CONSTRAINT source_cleanup_item_id_operation_key UNIQUE (id, operation_id);

CREATE TABLE public.source_cleanup_owner (
    item_id uuid NOT NULL,
    operation_id uuid NOT NULL,
    application_attempt integer NOT NULL,
    river_job_id bigint NOT NULL,
    owner_uuid uuid NOT NULL,
    source_root_id uuid NOT NULL,
    source_location_id uuid NOT NULL,
    work_id uuid NOT NULL,
    CONSTRAINT source_cleanup_owner_pkey PRIMARY KEY (item_id),
    CONSTRAINT source_cleanup_owner_item_fkey FOREIGN KEY (item_id, operation_id)
        REFERENCES public.source_cleanup_item(id, operation_id) ON DELETE RESTRICT,
    CONSTRAINT source_cleanup_owner_operation_fkey FOREIGN KEY (operation_id, application_attempt, river_job_id)
        REFERENCES public.operation(id, attempt, river_job_id) ON DELETE RESTRICT,
    CONSTRAINT source_cleanup_owner_application_attempt_check CHECK (application_attempt > 0),
    CONSTRAINT source_cleanup_owner_river_job_id_check CHECK (river_job_id > 0),
    CONSTRAINT source_cleanup_owner_owner_uuid_key UNIQUE (owner_uuid),
    CONSTRAINT source_cleanup_owner_source_location_id_key UNIQUE (source_location_id),
    CONSTRAINT source_cleanup_owner_work_id_key UNIQUE (work_id)
);
