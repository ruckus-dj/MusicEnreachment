import type { CatalogArtist, OriginalRelease } from "../library/catalogData";
import { normalized } from "../incoming/incomingModel";

export const DEFAULT_MATCHING_PAGE_SIZE = 10;

export type MatchingEditionDetails = {
	disambiguation?: string;
	date?: string;
	country?: string;
	trackCount: number;
	formats: string[];
	mediaCount: number;
	labels: { name?: string; catalogNumber?: string }[];
};

const present = (value: unknown) => {
	const text = typeof value === "string" ? value.trim() : "";
	return text || undefined;
};

/** Only describe release values present in the source data; never synthesize edition fields. */
export function matchingEditionDetails(
	release: OriginalRelease,
): MatchingEditionDetails {
	const media = Array.isArray(release.media) ? release.media : [];
	const formats = [
		...new Set(
			media
				.map((item) => present(item.format))
				.filter((format): format is string => Boolean(format)),
		),
	];
	const name = present(release.metadata?.label) ?? present(release.label);
	const catalogNumber =
		present(release.metadata?.catalogNumber) ?? present(release.catalog);
	const date = present(release.metadata?.date) ?? present(release.year);
	const country =
		present(release.metadata?.country) ?? present(release.country);
	const disambiguation = present(release.disambiguation);
	return {
		...(disambiguation ? { disambiguation } : {}),
		...(date ? { date } : {}),
		...(country ? { country } : {}),
		trackCount: release.tracks.length,
		formats,
		mediaCount: media.length,
		labels:
			name || catalogNumber
				? [
						{
							...(name ? { name } : {}),
							...(catalogNumber ? { catalogNumber } : {}),
						},
					]
				: [],
	};
}

/** Ordered release credits, including join phrases, when the fixture provides them. */
export function matchingArtistCredit(
	release: OriginalRelease,
	artists: CatalogArtist[],
) {
	const credit = (release.credits ?? [])
		.map(
			(entry) =>
				`${present(entry.credit) ?? present(entry.name) ?? ""}${entry.joinPhrase ?? ""}`,
		)
		.join("")
		.trim();
	if (credit) return credit;
	return artists.find((artist) => artist.id === release.artistId)?.name ?? "";
}

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

/**
 * One freeform artist-and-title query. Every token must occur in the ordered
 * artist credit, the release title, or an available public edition detail.
 * Internal catalog and provider identities are never searchable display data.
 */
export function searchMatchingReleases(
	query: string,
	releases: OriginalRelease[],
	artists: CatalogArtist[],
) {
	const terms = normalized(query).split(" ").filter(Boolean);
	if (!terms.length) return [];
	return releases.filter((release) => {
		const details = matchingEditionDetails(release);
		const searchable = normalized(
			[
				matchingArtistCredit(release, artists),
				release.title,
				details.date,
				details.country,
				...details.formats,
				...details.labels.flatMap((label) => [label.name, label.catalogNumber]),
			]
				.filter(Boolean)
				.join(" "),
		);
		return terms.every((term) => searchable.includes(term));
	});
}
