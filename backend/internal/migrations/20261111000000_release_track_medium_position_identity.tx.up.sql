-- Track positions are scoped to a medium. Preserve the existing medium values
-- while allowing the same position on different media of a release.
ALTER TABLE release_track DROP CONSTRAINT release_track_pkey;
ALTER TABLE release_track ADD PRIMARY KEY (release_id, medium, position);
