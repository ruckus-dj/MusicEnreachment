import type { OriginalTrack } from "./catalogData";

export type AvailabilityFilter = "all" | "available" | "missing";

export function availabilityRowClass(
	track: OriginalTrack,
	availability: AvailabilityFilter,
	hasAudio: (track: OriginalTrack) => boolean,
) {
	if (availability === "all") return "";
	const matches =
		availability === "available" ? hasAudio(track) : !hasAudio(track);
	return matches ? "filter-match" : "filter-muted";
}
