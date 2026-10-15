-- A baseline rollback is destructive: reject populated application tables before DDL.
DO $baseline_rollback$
DECLARE
    relation_name text;
    populated boolean;
BEGIN
    FOREACH relation_name IN ARRAY ARRAY['app_setting', 'operation', 'output_reset_journal', 'tool_installation', 'tools_execution_claim']
    LOOP
        EXECUTE format('SELECT EXISTS (SELECT 1 FROM public.%I)', relation_name) INTO populated;
        IF populated THEN
            RAISE EXCEPTION 'cannot roll back populated application baseline: % contains data', relation_name;
        END IF;
    END LOOP;
END;
$baseline_rollback$;

ALTER TABLE public.tools_execution_claim DROP CONSTRAINT tools_execution_claim_operation_id_fkey;
ALTER TABLE public.operation DROP CONSTRAINT operation_target_installation_id_fkey;

DROP TABLE public.tools_execution_claim;
DROP TABLE public.tool_installation;
DROP TABLE public.output_reset_journal;
DROP TABLE public.operation;
DROP TABLE public.app_setting;
