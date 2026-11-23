-- Local matching receipts are written once per request: a retry may read the
-- recorded receipt but must never revise or remove it. The table is INSERT-only
-- in the application, so any UPDATE or DELETE indicates rewritten authority.
CREATE FUNCTION public.reject_local_matching_receipt_mutation() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    RAISE EXCEPTION 'local matching receipts are immutable';
END;
$$;

CREATE TRIGGER local_matching_receipt_immutable
    BEFORE UPDATE OR DELETE ON public.local_matching_receipt
    FOR EACH ROW EXECUTE FUNCTION public.reject_local_matching_receipt_mutation();
