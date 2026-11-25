import type {
  MatchingWorkspaceAutomaticAssignments,
  MatchingWorkspaceRelease,
  MatchingWorkspaceSavedScores,
} from "./types";

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

/**
 * Resolve the release-scoped portion of an automatic assignment map. When the
 * map is nested, the provider release id is the primary key and the readable
 * release id is the fallback.
 */
export function matchingBoardAutomaticForRelease(
  automaticAssignments:
    | MatchingWorkspaceAutomaticAssignments
    | null
    | undefined,
  release: MatchingWorkspaceRelease,
): Readonly<Record<string, string>> {
  if (!automaticAssignments) return {};
  const nested =
    automaticAssignments[release.provider_release_id] ??
    automaticAssignments[release.release_id];
  return isRecord(nested)
    ? (nested as Readonly<Record<string, string>>)
    : (automaticAssignments as Readonly<Record<string, string>>);
}

/**
 * Resolve the release-scoped portion of a score map. A nested map is
 * recognized only when its release entry contains member records; a scoped map
 * keeps track-score records at the first level.
 */
export function matchingBoardScoresForRelease(
  savedScores: MatchingWorkspaceSavedScores | null | undefined,
  release: MatchingWorkspaceRelease,
): Readonly<Record<string, Readonly<Record<string, number>>>> {
  if (!savedScores) return {};
  const nested =
    savedScores[release.provider_release_id] ?? savedScores[release.release_id];
  if (isRecord(nested)) {
    const values = Object.values(nested);
    if (values.length === 0 || values.every(isRecord)) {
      return nested as Readonly<
        Record<string, Readonly<Record<string, number>>>
      >;
    }
  }
  return savedScores as Readonly<
    Record<string, Readonly<Record<string, number>>>
  >;
}

export function matchingBoardSavedTrack(
  automaticAssignments:
    | MatchingWorkspaceAutomaticAssignments
    | null
    | undefined,
  release: MatchingWorkspaceRelease,
  memberId: string,
): string | undefined {
  return matchingBoardAutomaticForRelease(automaticAssignments, release)[
    memberId
  ];
}

export function matchingBoardSavedScore(
  savedScores: MatchingWorkspaceSavedScores | null | undefined,
  release: MatchingWorkspaceRelease,
  memberId: string,
  trackId: string,
): number | undefined {
  const value = matchingBoardScoresForRelease(savedScores, release)[memberId]?.[
    trackId
  ];
  return typeof value === "number" &&
    Number.isFinite(value) &&
    value >= 0 &&
    value <= 1
    ? value
    : undefined;
}

export function matchingBoardPercent(value: number) {
  return `${Math.round(value * 100)}%`;
}
