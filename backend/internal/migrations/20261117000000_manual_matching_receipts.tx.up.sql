CREATE TABLE manual_matching_receipt (
    request_id uuid PRIMARY KEY,
    group_id uuid NOT NULL,
    local_release_id uuid NOT NULL REFERENCES local_release(id) ON DELETE RESTRICT,
    intent_hash text NOT NULL CHECK (intent_hash <> ''),
    preview_digest text NOT NULL CHECK (preview_digest <> ''),
    response jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE FUNCTION reject_manual_matching_receipt_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'manual matching receipts are immutable';
END;
$$;

CREATE TRIGGER manual_matching_receipt_write_once
    BEFORE UPDATE OR DELETE ON manual_matching_receipt
    FOR EACH ROW EXECUTE FUNCTION reject_manual_matching_receipt_mutation();
