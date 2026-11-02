import type { OriginalTrack } from "./catalogData";

export type AvailabilityFilter = "all" | "available" | "missing";
export type PublicationFilter = "all" | "published" | "unpublished";

export function availabilityRowClass(
	track: OriginalTrack,
	availability: AvailabilityFilter,
	hasAudio: (track: OriginalTrack) => boolean,
	publication: PublicationFilter = "all",
) {
	if (availability === "all" && publication === "all") return "";
	const matchesAvailability =
		availability === "all" ||
		(availability === "available" ? hasAudio(track) : !hasAudio(track));
	const matchesPublication =
		publication === "all" ||
		(publication === "published" ? Boolean(track.published) : !track.published);
	const matches = matchesAvailability && matchesPublication;
	return matches ? "filter-match" : "filter-muted";
}
