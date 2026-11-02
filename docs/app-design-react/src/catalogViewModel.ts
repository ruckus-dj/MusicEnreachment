import type {
	CatalogArtist,
	OriginalRelease,
	OriginalTrack,
} from "./catalogData";
import type { CatalogFilter } from "./availabilityRows";

export type CatalogTrack = Pick<OriginalTrack, "id" | "title" | "artistId"> & {
	published: boolean;
};
export type CatalogRelease = Omit<OriginalRelease, "tracks"> & {
	tracks: CatalogTrack[];
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

export function missingCatalogGroups(
	groups: AbsentCatalogGroup[],
	releases: CatalogRelease[],
	artists: CatalogArtist[],
	query: string,
	filter: CatalogFilter,
) {
	if (filter !== "all" && filter !== "missing") return [];

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

export function catalogArtistMatches(
	artist: CatalogArtist,
	releases: CatalogRelease[],
	missingGroups: AbsentCatalogGroup[],
	query: string,
	filter: CatalogFilter,
	hasAudio: (track: CatalogTrack) => boolean,
) {
	const search = normalizeCatalogSearch(query);
	const matchingRelease = artistReleases(releases, artist.id).some(
		(release) => {
			return (
				filter === "all" ||
				release.tracks.some((track) =>
					filter === "published"
						? Boolean(track.published)
						: filter === "available"
							? hasAudio(track)
							: !hasAudio(track),
				)
			);
		},
	);
	const hasMissingGroup = missingGroups.some(
		(group) => group.artistId === artist.id,
	);
	return (
		(normalizeCatalogSearch(`${artist.name} ${artist.genre ?? ""}`).includes(
			search,
		) ||
			hasMissingGroup) &&
		(matchingRelease || hasMissingGroup)
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
