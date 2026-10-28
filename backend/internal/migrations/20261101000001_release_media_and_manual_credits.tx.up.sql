CREATE TABLE release_medium (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    release_id uuid NOT NULL REFERENCES local_release(id) ON DELETE CASCADE,
    medium integer NOT NULL CHECK (medium >= 0),
    title text NOT NULL DEFAULT '',
    format text NOT NULL DEFAULT '',
    UNIQUE (release_id, medium)
);

INSERT INTO release_medium (release_id, medium)
SELECT DISTINCT release_id, medium FROM release_track;

ALTER TABLE release_track
    ADD CONSTRAINT release_track_medium_fk
    FOREIGN KEY (release_id, medium) REFERENCES release_medium(release_id, medium);

CREATE FUNCTION ensure_release_medium_for_track() RETURNS trigger
LANGUAGE plpgsql AS $function$
BEGIN
    INSERT INTO release_medium (release_id, medium)
    VALUES (NEW.release_id, NEW.medium)
    ON CONFLICT (release_id, medium) DO NOTHING;
    RETURN NEW;
END;
$function$;

CREATE TRIGGER release_track_ensure_medium
BEFORE INSERT OR UPDATE OF release_id, medium ON release_track
FOR EACH ROW EXECUTE FUNCTION ensure_release_medium_for_track();

ALTER TABLE release_artist ADD COLUMN role text NOT NULL DEFAULT '';
ALTER TABLE track_artist ADD COLUMN role text NOT NULL DEFAULT '';
ALTER TABLE recording_artist ADD COLUMN role text NOT NULL DEFAULT '';
ALTER TABLE release_track
    ADD COLUMN duration_ms bigint CHECK (duration_ms IS NULL OR duration_ms >= 0);
