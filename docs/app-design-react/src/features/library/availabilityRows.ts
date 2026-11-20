import type { OriginalTrack } from "./catalogData";

export type CatalogFilter = "all" | "missing" | "available" | "published";

export function availabilityRowClass(
	track: OriginalTrack,
	filter: CatalogFilter,
	hasAudio: (track: OriginalTrack) => boolean,
) {
	if (filter === "all") return "";
	const matches =
		filter === "published"
			? Boolean(track.published)
			: filter === "available"
				? hasAudio(track)
				: !hasAudio(track);
	return matches ? "filter-match" : "filter-muted";
}
