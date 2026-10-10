CREATE TABLE source_work_variant_identity (
	work_id uuid PRIMARY KEY REFERENCES source_analysis_work(id) ON DELETE CASCADE,
	media_variant_id uuid NOT NULL REFERENCES media_variant(id) ON DELETE RESTRICT,
	created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX source_work_variant_identity_media_variant_id_idx
	ON source_work_variant_identity(media_variant_id);

CREATE FUNCTION prevent_source_work_variant_identity_rekey() RETURNS trigger AS $$
BEGIN
	IF NEW.work_id IS DISTINCT FROM OLD.work_id OR NEW.media_variant_id IS DISTINCT FROM OLD.media_variant_id THEN
		RAISE EXCEPTION 'source work variant identity is immutable';
	END IF;
	RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER source_work_variant_identity_no_rekey
	BEFORE UPDATE ON source_work_variant_identity
	FOR EACH ROW EXECUTE FUNCTION prevent_source_work_variant_identity_rekey();
