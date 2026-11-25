import type { MatchingReview } from "../../api/generated/client.schemas";

/**
 * The saved solver decision is only the baseline for this exact release.
 * Candidate edges are evidence, not instructions to pick a track.
 */
export function savedAutomaticAssignments(
  review: MatchingReview | undefined,
  releaseId: string,
): Readonly<Record<string, string>> {
  const saved = review?.automatic_assignment;
  if (
    !review?.evaluated ||
    !review.proposal_available ||
    !releaseId ||
    saved?.provider_release_id !== releaseId
  )
    return {};

  const release = review.releases?.find(
    (item) => item.provider_release_id === releaseId,
  );
  if (!release) return {};
  const members = new Set(review.members);
  const tracks = new Set(release.positions?.map((item) => item.track_id));
  const assigned = new Map<string, string>();
  for (const { file_member_id, provider_track_id } of saved.assignments ?? []) {
    if (
      !members.has(file_member_id) ||
      !tracks.has(provider_track_id) ||
      assigned.has(file_member_id)
    )
      return {};
    assigned.set(file_member_id, provider_track_id);
  }
  return Object.fromEntries(assigned);
}
