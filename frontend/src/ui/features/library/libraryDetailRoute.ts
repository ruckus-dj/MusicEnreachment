/**
 * Portable release (edition) and position hash addresses. The codec owns the
 * exact nested route shape and URI encoding only; identifier policy, including
 * UUID validation for live identifiers, belongs to the host route adapter.
 */

export interface LibraryReleaseAddress {
  releaseId: string;
}

export interface LibraryPositionAddress extends LibraryReleaseAddress {
  positionId: string;
}

/** Host-supplied check for a raw, still-encoded path segment. */
export type LibraryIdValidator = (rawId: string) => boolean;

const releasePrefix = "/library/releases/";
const releasePattern = /^\/library\/releases\/([^/]+)$/;
const positionPattern = /^\/library\/releases\/([^/]+)\/positions\/([^/]+)$/;

function pathnameOf(route: string) {
  const path = route.startsWith("#") ? route.slice(1) : route;
  const queryIndex = path.indexOf("?");
  return queryIndex === -1 ? path : path.slice(0, queryIndex);
}

function decodeId(value: string) {
  try {
    return decodeURIComponent(value) || undefined;
  } catch {
    return undefined;
  }
}

/**
 * Parses the exact edition address `#/library/releases/<release>`. The optional
 * validator sees the raw segment, before URI decoding, so a host can require an
 * exact identifier form instead of accepting its encoded equivalent.
 */
export function parseLibraryReleaseAddress(
  route: string,
  validateRawId?: LibraryIdValidator,
): LibraryReleaseAddress | undefined {
  const match = releasePattern.exec(pathnameOf(route));
  if (!match) return undefined;
  const [rawReleaseId] = match.slice(1);
  if (validateRawId && !validateRawId(rawReleaseId)) return undefined;
  const releaseId = decodeId(rawReleaseId);
  return releaseId ? { releaseId } : undefined;
}

/**
 * Parses the exact `#/library/releases/<release>/positions/<position>` tuple.
 * The optional validator sees both raw segments before URI decoding.
 */
export function parseLibraryPositionAddress(
  route: string,
  validateRawId?: LibraryIdValidator,
): LibraryPositionAddress | undefined {
  const match = positionPattern.exec(pathnameOf(route));
  if (!match) return undefined;
  const [rawReleaseId, rawPositionId] = match.slice(1);
  if (
    validateRawId &&
    (!validateRawId(rawReleaseId) || !validateRawId(rawPositionId))
  )
    return undefined;
  const releaseId = decodeId(rawReleaseId);
  const positionId = decodeId(rawPositionId);
  return releaseId && positionId ? { releaseId, positionId } : undefined;
}

/** Builds the canonical edition address for a release identifier. */
export function libraryReleaseHref(releaseId: string) {
  return `#${releasePrefix}${encodeURIComponent(releaseId)}`;
}

/** Builds the canonical nested address for a release position. */
export function libraryPositionHref(releaseId: string, positionId: string) {
  return `${libraryReleaseHref(releaseId)}/positions/${encodeURIComponent(positionId)}`;
}
