/**
 * Locales supported by the portable matching workspace copy. The portable
 * modules never read a concrete application i18n provider; callers pass the
 * locale explicitly.
 */
export type MatchingWorkspaceLocale = "en" | "ru";

/** One release position as rendered by the assignment board. */
export type MatchingWorkspacePosition = {
  track_id: string;
  medium: number;
  position: number;
  displayed_number?: string | null;
  recording_id?: string;
  title: string;
};

/** Release layout used by the assignment board. Generated API releases are
 * structurally compatible with this neutral shape. */
export type MatchingWorkspaceRelease = {
  release_id: string;
  provider_release_id: string;
  title: string;
  tracklist_complete?: boolean | null;
  positions?: readonly MatchingWorkspacePosition[] | null;
};

/** One incoming file offered by the source rail or shown as assigned. */
export type MatchingWorkspaceSource = {
  id: string;
  label: string;
  detail?: string;
  path?: string;
  available?: boolean;
  /**
   * Basename of the actual source file, used for filename ordering. The
   * display `label` is only a fallback when no filename is known.
   */
  fileName?: string;
  /** Disc number taken from the file's own metadata tags, never its name. */
  discNumber?: number;
  /** Track number taken from the file's own metadata tags, never its name. */
  trackNumber?: number;
};

/**
 * Saved automatic assignments. The preferred shape is the direct output of
 * `savedAutomaticAssignments()` (release-scoped member id to track id). A map
 * nested by provider release id is also accepted.
 */
export type MatchingWorkspaceAutomaticAssignments =
  | Readonly<Record<string, string>>
  | Readonly<Record<string, Readonly<Record<string, string>>>>;

/**
 * Saved per-track confidence. The preferred shape is release-scoped member id
 * to track id to a [0, 1] score. A map nested by provider release id is also
 * accepted. Only finite scores are rendered.
 */
export type MatchingWorkspaceSavedScores =
  | Readonly<Record<string, Readonly<Record<string, number>>>>
  | Readonly<
      Record<string, Readonly<Record<string, Readonly<Record<string, number>>>>>
    >;

export type MatchingWorkspaceSavedReference = {
  memberId: string;
  trackId: string;
  confidence?: number;
};

export type MatchingWorkspacePositionContext = {
  position: MatchingWorkspacePosition;
  assigned: readonly MatchingWorkspaceSource[];
  saved: readonly MatchingWorkspaceSavedReference[];
};

/** One saved candidate position, including the assignment confidence. */
export type MatchingWorkspaceCandidatePosition = {
  track_id: string;
  medium: number;
  position: number;
  displayed_number?: string | null;
  title?: string;
  assignment_confidence?: number | null;
};

/**
 * Saved evidence for one file and release. `edges` remain opaque saved
 * diagnostics; the inspector only reads record members defensively.
 */
export type MatchingWorkspaceCandidate = {
  file_member_id: string;
  release_id: string;
  release_confidence?: number | null;
  edges?: readonly unknown[] | null;
  positions?: readonly MatchingWorkspaceCandidatePosition[] | null;
};
