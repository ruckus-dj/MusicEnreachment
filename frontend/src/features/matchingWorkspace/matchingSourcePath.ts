/**
 * Visible path split for the free-source rail.
 *
 * The rail shows the file name plus only the nearest parent directories; older
 * ancestors stay available through the full path on the control. Paths are
 * parsed by their own separators, so a POSIX and a Windows path both work.
 */
export type MatchingSourcePathParts = {
  /** Final path segment, the file name. */
  fileName: string;
  /** At most the nearest parents, ordered from the oldest shown to nearest. */
  parents: readonly string[];
  /** Older ancestors exist but are omitted from `parents`. */
  truncated: boolean;
};

export const matchingSourcePathParentLimit = 2;

export function readMatchingSourcePath(
  path: string | null | undefined,
): MatchingSourcePathParts | null {
  const segments = (path ?? "")
    .split(/[\\/]+/)
    .filter((segment) => segment !== "" && segment !== ".");
  if (!segments.length) return null;
  const ancestors = segments.slice(0, -1);
  return {
    fileName: segments[segments.length - 1],
    parents: ancestors.slice(
      Math.max(0, ancestors.length - matchingSourcePathParentLimit),
    ),
    truncated: ancestors.length > matchingSourcePathParentLimit,
  };
}
