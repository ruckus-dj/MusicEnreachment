-- These are only derived review projections; accepted decisions and their
-- source/provider authority remain on incoming_match_evaluation.
DROP TABLE public.incoming_match_candidate_track;
DROP TABLE public.incoming_match_candidate;

UPDATE public.incoming_match_evaluation
SET evaluation_evidence = evaluation_evidence - 'release_selection'
WHERE evaluation_evidence ? 'release_selection';
