import { expect, it } from "vitest";
import type { MatchingReview } from "../../api/generated/client.schemas";
import { savedAutomaticAssignments } from "./savedAutomaticAssignments";

it("uses only the saved member-to-track pairs for the exact selected release", () => {
  const review: MatchingReview = {
    group_id: "group",
    evaluated: true,
    proposal_available: true,
    authority_mode: "automatic",
    authority_revision: 1,
    members: ["file"],
    candidates: [],
    current_linked_manual_media_source_ids: [],
    releases: [
      {
        provider_release_id: "release",
        release_id: "provider:release",
        title: "Album",
        tracklist_complete: true,
        positions: [
          {
            track_id: "track",
            displayed_number: "1",
            medium: 0,
            position: 0,
            recording_id: "recording",
            title: "Track",
          },
        ],
      },
    ],
    automatic_assignment: {
      provider_release_id: "release",
      assignments: [{ file_member_id: "file", provider_track_id: "track" }],
    },
  };
  expect(savedAutomaticAssignments(review, "release")).toEqual({
    file: "track",
  });
  expect(savedAutomaticAssignments(review, "different")).toEqual({});
  expect(
    savedAutomaticAssignments(
      { ...review, proposal_available: false },
      "release",
    ),
  ).toEqual({});
  expect(
    savedAutomaticAssignments(
      {
        ...review,
        automatic_assignment: {
          provider_release_id: "release",
          assignments: [{ file_member_id: "file", provider_track_id: "stale" }],
        },
      },
      "release",
    ),
  ).toEqual({});
});
