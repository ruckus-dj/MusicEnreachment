CREATE TABLE tools_execution_claim (
    operation_id uuid PRIMARY KEY REFERENCES operation(id) ON DELETE RESTRICT,
    attempt integer NOT NULL CHECK (attempt > 0),
    river_job_id bigint NOT NULL,
    borrower text NOT NULL CHECK (borrower IN ('install', 'move_tools_root')),
    claimed_at timestamptz NOT NULL DEFAULT now(),
    borrowed boolean NOT NULL DEFAULT false
);
