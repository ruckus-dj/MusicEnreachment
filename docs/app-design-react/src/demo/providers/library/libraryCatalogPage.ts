import type {
	CatalogArtist,
	OriginalRelease,
	OriginalTrack,
} from "./catalogData";
import type { AbsentCatalogGroup } from "./catalogViewModel";
import { LIBRARY_PAGE_SIZE } from "./libraryRoute";

export type LibraryAlbumEntry =
	| { kind: "release"; release: OriginalRelease }
	| { kind: "missing"; group: AbsentCatalogGroup };

export const libraryPageCount = (
	totalItems: number,
	pageSize = LIBRARY_PAGE_SIZE,
) => Math.max(1, Math.ceil(totalItems / pageSize));

/** A one-based page slice; the caller clamps the requested page for display. */
export function libraryPageSlice<T>(
	items: readonly T[],
	page: number,
	pageSize = LIBRARY_PAGE_SIZE,
): T[] {
	const start = (Math.max(1, page) - 1) * pageSize;
	return items.slice(start, start + pageSize);
}

/**
 * Local releases and known provider groups share one ordered album list, so a
 * page boundary is applied after they are combined rather than to each source.
 */
export function combineLibraryAlbumEntries(
	releases: readonly OriginalRelease[],
	missingGroups: readonly AbsentCatalogGroup[],
): LibraryAlbumEntry[] {
	return [
		...releases.map(
			(release): LibraryAlbumEntry => ({ kind: "release", release }),
		),
		...missingGroups.map(
			(group): LibraryAlbumEntry => ({ kind: "missing", group }),
		),
	];
}

export function splitLibraryAlbumEntries(
	entries: readonly LibraryAlbumEntry[],
): {
	releases: OriginalRelease[];
	missingGroups: AbsentCatalogGroup[];
} {
	return {
		releases: entries.flatMap((entry) =>
			entry.kind === "release" ? [entry.release] : [],
		),
		missingGroups: entries.flatMap((entry) =>
			entry.kind === "missing" ? [entry.group] : [],
		),
	};
}

/** Local positions owned by the visible artist cards, never provider-only groups. */
export function libraryArtistPositionIds(
	artists: readonly CatalogArtist[],
	releases: readonly OriginalRelease[],
): string[] {
	const artistIds = new Set(artists.map((artist) => artist.id));
	const ids = new Set<string>();
	for (const release of releases) {
		const owned =
			(release.artistId !== null && artistIds.has(release.artistId)) ||
			release.credits?.some(
				(credit) =>
					credit.artistId !== null &&
					credit.artistId !== undefined &&
					artistIds.has(credit.artistId),
			);
		if (!owned) continue;
		for (const track of release.tracks) ids.add(track.id);
	}
	return [...ids];
}

export function libraryTrackPositionIds(
	tracks: readonly { track: OriginalTrack }[],
): string[] {
	return tracks.map(({ track }) => track.id);
}

/**
 * Builds track-view release wrappers from the current page only. These wrappers
 * are for rendering; artist totals must still use the full releases array.
 */
export function sliceTrackReleasePage(
	releases: readonly OriginalRelease[],
	pageTracks: readonly { release: OriginalRelease; track: OriginalTrack }[],
): OriginalRelease[] {
	const trackIdsByRelease = new Map<string, Set<string>>();
	for (const { release, track } of pageTracks) {
		const ids = trackIdsByRelease.get(release.id) ?? new Set<string>();
		ids.add(track.id);
		trackIdsByRelease.set(release.id, ids);
	}
	return releases.flatMap((release) => {
		const ids = trackIdsByRelease.get(release.id);
		if (!ids) return [];
		const tracks = release.tracks.filter((track) => ids.has(track.id));
		return tracks.length ? [{ ...release, tracks }] : [];
	});
}
