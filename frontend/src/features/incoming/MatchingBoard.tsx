import type { ReactNode } from "react";
import type {
  MatchingReviewCandidate,
  MatchingReviewLayoutPosition,
  MatchingReviewRelease,
} from "../../api/generated/client.schemas";
import { useI18n } from "../../i18n/I18nProvider";
import {
  MatchingBoard as MatchingBoardView,
  type MatchingBoardProps as MatchingBoardViewProps,
  type MatchingWorkspacePositionContext,
  type MatchingWorkspaceSavedReference,
  type MatchingWorkspaceSource,
} from "../matchingWorkspace";

export {
  matchingBoardAutomaticForRelease,
  matchingBoardPercent,
  matchingBoardSavedScore,
  matchingBoardSavedTrack,
  matchingBoardScoresForRelease,
} from "../matchingWorkspace/scoring";
export type {
  MatchingWorkspaceAutomaticAssignments as MatchingBoardAutomaticAssignments,
  MatchingWorkspaceSavedReference as MatchingBoardSavedReference,
  MatchingWorkspaceSavedScores as MatchingBoardSavedScores,
  MatchingWorkspaceSource as MatchingBoardSource,
} from "../matchingWorkspace/types";

export type MatchingBoardPositionContext = {
  position: MatchingReviewLayoutPosition;
  assigned: readonly MatchingWorkspaceSource[];
  saved: readonly MatchingWorkspaceSavedReference[];
};

export type MatchingBoardProps = Omit<
  MatchingBoardViewProps,
  "release" | "locale" | "renderPositionMeta"
> & {
  release: MatchingReviewRelease;
  renderPositionMeta?: (context: MatchingBoardPositionContext) => ReactNode;
};

/**
 * Project saved candidate edges for exactly one release into the board's
 * member-to-track score map. Scores are real saved [0, 1] values; missing or
 * out-of-range values are omitted rather than inferred.
 */
export function matchingBoardScoresFromCandidates(
  candidates: readonly MatchingReviewCandidate[] | null | undefined,
  release: MatchingReviewRelease,
): Readonly<Record<string, Readonly<Record<string, number>>>> {
  const tracks = new Set(
    (release.positions ?? []).map((position) => position.track_id),
  );
  const releaseIds = new Set([release.provider_release_id, release.release_id]);
  const scores: Record<string, Record<string, number>> = {};
  for (const candidate of candidates ?? []) {
    if (!releaseIds.has(candidate.release_id)) continue;
    for (const position of candidate.positions ?? []) {
      if (!tracks.has(position.track_id)) continue;
      const confidence = position.assignment_confidence;
      if (
        typeof confidence !== "number" ||
        !Number.isFinite(confidence) ||
        confidence < 0 ||
        confidence > 1
      ) {
        continue;
      }
      let memberScores = scores[candidate.file_member_id];
      if (!memberScores) {
        memberScores = {};
        scores[candidate.file_member_id] = memberScores;
      }
      const existing = memberScores[position.track_id];
      memberScores[position.track_id] =
        existing === undefined ? confidence : Math.max(existing, confidence);
    }
  }
  return scores;
}

/**
 * Generated-API adapter for the portable matching board. Locale still comes
 * from the application i18n provider here; the portable view receives it as an
 * explicit prop.
 */
export function MatchingBoard({
  release,
  renderPositionMeta,
  ...props
}: MatchingBoardProps) {
  const { locale } = useI18n();
  return (
    <MatchingBoardView
      {...props}
      release={release}
      locale={locale}
      renderPositionMeta={
        renderPositionMeta
          ? (context: MatchingWorkspacePositionContext) =>
              renderPositionMeta({
                position: context.position as MatchingReviewLayoutPosition,
                assigned: context.assigned,
                saved: context.saved,
              })
          : undefined
      }
    />
  );
}
