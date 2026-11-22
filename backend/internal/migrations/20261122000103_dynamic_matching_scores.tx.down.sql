CREATE TABLE public.incoming_match_candidate (
    group_id uuid NOT NULL,
    file_member_id uuid NOT NULL,
    provider_id uuid NOT NULL,
    provider_source_id uuid NOT NULL,
    release_id uuid NOT NULL,
    score double precision NOT NULL,
    evidence jsonb NOT NULL,
    CONSTRAINT incoming_match_candidate_evidence_check CHECK (jsonb_typeof(evidence) = 'object'),
    CONSTRAINT incoming_match_candidate_score_check CHECK (score >= 0 AND score <= 1),
    CONSTRAINT incoming_match_candidate_pkey PRIMARY KEY (group_id, file_member_id, provider_source_id, release_id),
    CONSTRAINT incoming_match_candidate_group_id_file_member_id_fkey FOREIGN KEY (group_id, file_member_id) REFERENCES public.incoming_match_evaluation_member(group_id, file_member_id) ON DELETE CASCADE,
    CONSTRAINT incoming_match_candidate_provider_id_provider_source_id_fkey FOREIGN KEY (provider_id, provider_source_id) REFERENCES public.provider_source(provider_id, id) ON DELETE RESTRICT,
    CONSTRAINT incoming_match_candidate_provider_id_release_id_fkey FOREIGN KEY (provider_id, release_id) REFERENCES public.provider_release(provider_id, id) ON DELETE RESTRICT
);

CREATE TABLE public.incoming_match_candidate_track (
    group_id uuid NOT NULL,
    file_member_id uuid NOT NULL,
    provider_source_id uuid NOT NULL,
    provider_id uuid NOT NULL,
    release_id uuid NOT NULL,
    track_id uuid NOT NULL,
    medium integer NOT NULL,
    "position" integer NOT NULL,
    CONSTRAINT incoming_match_candidate_track_medium_check CHECK (medium >= 0),
    CONSTRAINT incoming_match_candidate_track_position_check CHECK ("position" >= 0),
    CONSTRAINT incoming_match_candidate_track_pkey PRIMARY KEY (group_id, file_member_id, provider_source_id, release_id, medium, "position"),
    CONSTRAINT incoming_match_candidate_trac_group_id_file_member_id_prov_fkey FOREIGN KEY (group_id, file_member_id, provider_source_id, release_id) REFERENCES public.incoming_match_candidate(group_id, file_member_id, provider_source_id, release_id) ON DELETE CASCADE,
    CONSTRAINT incoming_match_candidate_trac_provider_id_release_id_track_fkey FOREIGN KEY (provider_id, release_id, track_id) REFERENCES public.provider_release_track(provider_id, release_id, id) ON DELETE RESTRICT
);
