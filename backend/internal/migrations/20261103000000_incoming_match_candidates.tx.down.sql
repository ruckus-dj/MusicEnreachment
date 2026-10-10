DROP TABLE incoming_match_candidate_track;
DROP TABLE incoming_match_candidate;
DROP TABLE incoming_match_evaluation_location;
DROP TABLE incoming_match_evaluation_member;
DROP TABLE incoming_match_evaluation;
ALTER TABLE provider_release_track
    DROP CONSTRAINT provider_release_track_match_target;
