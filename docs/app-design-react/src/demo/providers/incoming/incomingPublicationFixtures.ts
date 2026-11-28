import type { IncomingPublicationEdition } from "../../../ui/features/incoming/incomingPublicationModel";
import {
  libraryPositionHref,
  libraryReleaseHref,
} from "../../../ui/features/library/libraryDetailRoute";
import {
  type CatalogArtist,
  effectiveReleaseTitle,
  effectiveReleaseYear,
  type OriginalRelease,
  originalArtistName,
  originalArtists,
  originalFiles,
  originalReleases,
  trackNumberLabel,
} from "../library/catalogData";
import { demoReleasePrimaryArtistId } from "../library/libraryPrimaryArtistDemo";

export const incomingPublicationQueueHref = "#/incoming-groups?tab=publication";
export const incomingPublicationReadyHref = `${incomingPublicationQueueHref}&status=ready`;

const releaseId = "north-japan-local";
const positionId = "north-japan-local-t2";

export const demoPrimaryArtistPublication = {
  releaseId,
  positionId,
  reason: { code: "missingPrimaryArtist" },
  trackHref: libraryPositionHref(releaseId, positionId, {
    tab: "publication",
    from: "publication-queue",
  }),
  resolveHref: `${libraryReleaseHref(releaseId)}?resolve=primary-artist`,
} as const;

/** Keep the exercise in the demo catalogue, not in shared UI or canonical fixtures. */
export function createPublicationWalkthroughReleases() {
  return structuredClone(originalReleases).map((release) =>
    release.id === releaseId
      ? {
          ...release,
          credits: [
            { artistId: "mira", name: "Mira Vale", joinPhrase: " & " },
            { artistId: "arden", name: "Arden Trio" },
          ],
          primaryArtistId: null,
        }
      : release,
  );
}

export function incomingPublicationEditions(
  releases: readonly OriginalRelease[],
  artists: CatalogArtist[] = originalArtists,
): IncomingPublicationEdition[] {
  const active = releases.find((release) => release.id === releaseId);
  const hours = releases.find((release) => release.id === "hours");
  const rows: IncomingPublicationEdition[] = [];
  for (const release of [active, hours]) {
    if (!release) continue;
    const walkthrough = release.id === releaseId;
    const primaryArtistId = demoReleasePrimaryArtistId(release, artists);
    const blocked = walkthrough && !primaryArtistId;
    const tracks = release.tracks
      .filter(
        (track) =>
          !track.published &&
          (walkthrough ? track.id === positionId : Boolean(track.sourceId)),
      )
      .map((track) => ({
        id: track.id,
        number: trackNumberLabel(release, track),
        title: track.title,
        href: libraryPositionHref(release.id, track.id, {
          tab: "publication",
          from: "publication-queue",
        }),
        source: track.sourceId
          ? originalFiles[track.sourceId]?.name
          : undefined,
      }));
    if (!tracks.length) continue;
    rows.push({
      id: `publication-${release.id}`,
      releaseId: release.id,
      title: walkthrough
        ? `${effectiveReleaseTitle(release)} · JP`
        : effectiveReleaseTitle(release),
      artist: originalArtistName(primaryArtistId ?? release.artistId, artists),
      year: effectiveReleaseYear(release),
      tracks,
      reason: blocked
        ? demoPrimaryArtistPublication.reason
        : walkthrough
          ? { code: "manualReady" }
          : { code: "automaticSkip" },
      nextStep: blocked
        ? {
            kind: "primaryArtist",
            href: demoPrimaryArtistPublication.resolveHref,
          }
        : walkthrough
          ? { kind: "track", href: demoPrimaryArtistPublication.trackHref }
          : { kind: "release", href: libraryReleaseHref(release.id) },
    });
  }
  return rows;
}
