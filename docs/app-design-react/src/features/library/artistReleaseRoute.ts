import type { CatalogArtist, OriginalRelease } from "./catalogData";

/** Exact local release destination; the identity is the local release ID. */
export function releaseHref(releaseId: string) {
	return `#release/${encodeURIComponent(releaseId)}`;
}

export function artistReleaseHref(
	release: Pick<OriginalRelease, "id" | "artistId" | "credits">,
	artists: CatalogArtist[],
	options: { tags?: "artist" | "release" } = {},
) {
	const candidates = [
		release.artistId,
		...(release.credits ?? []).map((credit) => credit.artistId),
	];
	const artistId = candidates.find(
		(id) => id && artists.some((artist) => artist.id === id),
	);
	if (!artistId) return null;
	const query = options.tags
		? `?tags=${options.tags}&release=${encodeURIComponent(release.id)}`
		: `?release=${encodeURIComponent(release.id)}`;
	return `#artist/${artistId}${query}`;
}
