import type { CatalogArtist, OriginalRelease } from "./catalogData";
import { normalized } from "./incomingModel";

export const DEFAULT_MATCHING_PAGE_SIZE = 10;

export type MatchingSearchEntity = "artist" | "release";

export function matchingPage<T>(
	items: T[],
	page: number,
	pageSize = DEFAULT_MATCHING_PAGE_SIZE,
) {
	const safePageSize = Math.max(1, Math.floor(pageSize));
	const pageCount = Math.max(1, Math.ceil(items.length / safePageSize));
	const safePage = Math.min(Math.max(0, Math.floor(page)), pageCount - 1);
	return {
		items: items.slice(safePage * safePageSize, (safePage + 1) * safePageSize),
		page: safePage,
		pageCount,
	};
}

export function searchMatchingArtists(query: string, artists: CatalogArtist[]) {
	const terms = normalized(query).split(" ").filter(Boolean);
	if (!terms.length) return [];
	return artists.filter((artist) =>
		terms.every((term) =>
			normalized(
				`${artist.name} ${artist.sortName ?? ""} ${artist.disambiguation ?? ""} ${artist.id}`,
			).includes(term),
		),
	);
}

export function searchMatchingReleases(
	query: string,
	releases: OriginalRelease[],
	artists: CatalogArtist[],
) {
	const terms = normalized(query).split(" ").filter(Boolean);
	if (!terms.length) return [];
	return releases.filter((release) => {
		const artist =
			artists.find((item) => item.id === release.artistId)?.name ?? "";
		return terms.every((term) =>
			normalized(
				`${release.title} ${artist} ${release.catalog} ${release.id} ${release.providerId ?? ""} ${release.year}`,
			).includes(term),
		);
	});
}

export type ExactIdentityResult<T> = {
	items: T[];
	message: string;
};

const mbidPattern =
	/^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;

function musicBrainzMBID(value: string, entity: MatchingSearchEntity) {
	if (mbidPattern.test(value)) return value;
	try {
		const url = new URL(value);
		if (
			!new Set(["http:", "https:"]).has(url.protocol) ||
			!new Set(["musicbrainz.org", "www.musicbrainz.org"]).has(url.hostname)
		)
			return null;
		const segments = url.pathname.split("/").filter(Boolean);
		const resourceType = segments[0];
		const id = segments[1];
		return segments.length === 2 &&
			resourceType === entity &&
			typeof id === "string" &&
			mbidPattern.test(id)
			? id
			: null;
	} catch {
		return null;
	}
}

/** Match only identity values actually present in fixture data; never infer IDs from names. */
export function findExactMatchingIdentity(
	entity: MatchingSearchEntity,
	query: string,
	artists: CatalogArtist[],
	releases: OriginalRelease[],
): ExactIdentityResult<CatalogArtist | OriginalRelease> {
	const value = query.trim();
	if (!value)
		return { items: [], message: "Введите идентификатор или официальный URL." };

	const pool = entity === "artist" ? artists : releases;
	const mbid = musicBrainzMBID(value, entity);
	const matches = pool.filter((item) => {
		const identities = [item.id];
		if (entity === "release")
			identities.push((item as OriginalRelease).providerId ?? "");
		return identities.some(
			(identity) =>
				identity !== "" &&
				(mbid
					? mbidPattern.test(identity) &&
						identity.toLocaleLowerCase() === mbid.toLocaleLowerCase()
					: identity.toLocaleLowerCase() === value.toLocaleLowerCase()),
		);
	});
	if (matches.length) return { items: matches, message: "" };

	const hasFixtureMBID = pool.some((item) =>
		[
			item.id,
			...(entity === "release"
				? [(item as OriginalRelease).providerId ?? ""]
				: []),
		].some((identity) => mbidPattern.test(identity)),
	);
	const message =
		mbid ||
		value.toLocaleLowerCase().includes("musicbrainz.org") ||
		/^[0-9a-f-]{36}$/i.test(value)
			? hasFixtureMBID
				? "В доступных fixture нет сущности с этим MBID или URL."
				: "У доступных fixture нет MusicBrainz MBID/URL для точной проверки; запись не сопоставлена."
			: "Точная идентичность не найдена в доступных fixture. Поиск по имени не выполнялся.";
	return { items: [], message };
}
