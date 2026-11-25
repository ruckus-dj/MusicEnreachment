import type { CatalogFilter } from "./availabilityRows";
import type { AvailabilityFilter, PublicationFilter } from "./libraryRoute";
import type {
	CatalogArtist,
	OriginalRelease,
	OriginalTrack,
} from "./catalogData";

export type CatalogTrack = Pick<OriginalTrack, "id" | "title" | "artistId"> & {
	published: boolean;
};
export type CatalogRelease = Omit<OriginalRelease, "tracks"> & {
	tracks: CatalogTrack[];
};

export interface CatalogFilterAxes {
	publication: PublicationFilter;
	availability: AvailabilityFilter;
}

/** One position must satisfy both canonical axes at once. */
export function trackMatchesFilterAxes<T extends { published: unknown }>(
	track: T,
	axes: CatalogFilterAxes,
	hasAudio: (track: T) => boolean,
): boolean {
	const published = Boolean(track.published);
	const matchesPublication =
		axes.publication === "all" ||
		(axes.publication === "published" ? published : !published);
	const matchesAvailability =
		axes.availability === "all" ||
		(axes.availability === "available" ? hasAudio(track) : !hasAudio(track));
	return matchesPublication && matchesAvailability;
}

/** Local release rows count each axis independently, matching the API page. */
export function releaseMatchesFilterAxes<T extends { published: unknown }>(
	tracks: readonly T[],
	axes: CatalogFilterAxes,
	hasAudio: (track: T) => boolean,
): boolean {
	if (axes.publication === "all" && axes.availability === "all") return true;
	const matchesPublication =
		axes.publication === "all" ||
		(axes.publication === "published"
			? tracks.some((track) => Boolean(track.published))
			: tracks.some((track) => !track.published));
	const matchesAvailability =
		axes.availability === "all" ||
		(axes.availability === "available"
			? tracks.some(hasAudio)
			: tracks.some((track) => !hasAudio(track)));
	return matchesPublication && matchesAvailability;
}

const axesForFilter = (filter: CatalogFilter): CatalogFilterAxes => {
	if (filter === "published")
		return { publication: "published", availability: "all" };
	if (filter === "available")
		return { publication: "all", availability: "available" };
	if (filter === "missing")
		return { publication: "all", availability: "missing" };
	return { publication: "all", availability: "all" };
};

export interface AbsentCatalogEdition {
	id: string;
	year: string;
	country: string;
	format: string;
	disambiguation: string;
	titles: string[];
}

export interface AbsentCatalogGroup {
	id: string;
	title: string;
	artistId: string;
	groupId: string;
	editions: AbsentCatalogEdition[];
}

export const normalizeCatalogSearch = (value: unknown) =>
	String(value ?? "")
		.normalize("NFKC")
		.toLocaleLowerCase()
		.replace(/\s+/g, " ")
		.trim();

export function artistReleases<T extends CatalogRelease>(
	releases: T[],
	artistId: string,
): T[] {
	return releases.filter(
		(release) =>
			release.artistId === artistId ||
			release.credits?.some((credit) => credit.artistId === artistId),
	);
}

function searchMissingCatalogGroups(
	groups: AbsentCatalogGroup[],
	releases: CatalogRelease[],
	artists: CatalogArtist[],
	query: string,
) {
	const search = normalizeCatalogSearch(query);
	return groups.filter((group) => {
		if (releases.some((release) => release.releaseGroupId === group.groupId))
			return false;
		const artistName = artists.find(
			(artist) => artist.id === group.artistId,
		)?.name;
		const editionDetails = group.editions
			.map((edition) =>
				[
					edition.year,
					edition.country,
					edition.format,
					edition.disambiguation,
					edition.titles.join(" "),
				].join(" "),
			)
			.join(" ");
		return normalizeCatalogSearch(
			`${group.title} ${artistName ?? ""} ${editionDetails}`,
		).includes(search);
	});
}

export function missingCatalogGroups(
	groups: AbsentCatalogGroup[],
	releases: CatalogRelease[],
	artists: CatalogArtist[],
	query: string,
	filter: CatalogFilter,
) {
	return missingCatalogGroupsForAxes(
		groups,
		releases,
		artists,
		query,
		axesForFilter(filter),
	);
}

/** Missing provider groups only exist outside published and available axes. */
export function missingCatalogGroupsForAxes(
	groups: AbsentCatalogGroup[],
	releases: CatalogRelease[],
	artists: CatalogArtist[],
	query: string,
	axes: CatalogFilterAxes,
) {
	if (axes.publication === "published" || axes.availability === "available")
		return [];
	return searchMissingCatalogGroups(groups, releases, artists, query);
}

export function catalogArtistMatches(
	artist: CatalogArtist,
	releases: CatalogRelease[],
	missingGroups: AbsentCatalogGroup[],
	query: string,
	filter: CatalogFilter,
	hasAudio: (track: CatalogTrack) => boolean,
) {
	return catalogArtistMatchesForAxes(
		artist,
		releases,
		missingGroups,
		query,
		axesForFilter(filter),
		hasAudio,
	);
}

/** Artist rows combine each canonical axis across all owned positions. */
export function catalogArtistMatchesForAxes(
	artist: CatalogArtist,
	releases: CatalogRelease[],
	missingGroups: AbsentCatalogGroup[],
	query: string,
	axes: CatalogFilterAxes,
	hasAudio: (track: CatalogTrack) => boolean,
) {
	const search = normalizeCatalogSearch(query);
	const artistReleases = releases.filter(
		(release) => release.artistId === artist.id,
	);
	const tracks = artistReleases.flatMap((release) => release.tracks);
	const hasMissingGroup = missingGroups.some(
		(group) => group.artistId === artist.id,
	);
	const matchesPublication =
		axes.publication === "all" ||
		(axes.publication === "published"
			? tracks.some((track) => Boolean(track.published))
			: tracks.some((track) => !track.published) || hasMissingGroup);
	const matchesAvailability =
		axes.availability === "all" ||
		(axes.availability === "available"
			? tracks.some(hasAudio)
			: tracks.some((track) => !hasAudio(track)) || hasMissingGroup);
	return (
		(normalizeCatalogSearch(`${artist.name} ${artist.genre ?? ""}`).includes(
			search,
		) ||
			hasMissingGroup) &&
		(artistReleases.length > 0 || hasMissingGroup) &&
		matchesPublication &&
		matchesAvailability
	);
}

export function albumSearchText(
	release: CatalogRelease,
	artists: CatalogArtist[],
) {
	const credits = release.credits
		?.map((credit) => credit.credit || credit.name || "")
		.filter(Boolean)
		.join(" ");
	const primaryArtist = artists.find(
		(artist) => artist.id === release.artistId,
	)?.name;
	return `${release.title} ${credits || primaryArtist || ""} ${release.catalog} ${release.year} ${release.country}`;
}

export function trackSearchText(
	track: CatalogTrack,
	release: CatalogRelease,
	artists: CatalogArtist[],
) {
	const trackArtist = artists.find(
		(artist) => artist.id === track.artistId,
	)?.name;
	return `${track.title} ${trackArtist ?? ""} ${release.title}`;
}
