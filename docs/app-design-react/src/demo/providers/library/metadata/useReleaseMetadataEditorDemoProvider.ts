import { useI18n } from "../../../../app/i18n/host";
import { metadataEditorMessages } from "../../../../ui/features/library/metadata/metadataEditorMessages";
import type { ReleaseMetadataEditorViewProps } from "../../../../ui/features/library/metadata/ReleaseMetadataEditorView";
import { type CatalogArtist, type OriginalRelease } from "../catalogData";
import { mergeComparisonOverrides } from "../comparisonOverrides";
import type { MetadataEditorProps } from "./MetadataEditor";
import type { MetadataCredit } from "./model";
import type { MetadataGenre, MetadataValues } from "./projection";

type ReleaseWithExtras = OriginalRelease & {
	disambiguation?: string;
	tags?: string[];
};

function releaseValues(release: OriginalRelease): MetadataValues {
	const values: MetadataValues = Object.fromEntries(
		Object.entries(release.metadataValues ?? {}).map(([key, entries]) => [
			key,
			[...entries],
		]),
	);
	if (!("album" in values)) values.album = [release.title];
	if (!("releasedate" in values)) {
		const metadataDate = release.metadata?.date;
		values.releasedate = [
			metadataDate && metadataDate.slice(0, 4) === release.year
				? metadataDate
				: release.year,
		];
	}
	if (!("releasecountry" in values)) values.releasecountry = [release.country];
	if (!("recordlabel" in values)) values.recordlabel = [release.label];
	if (!("catalognumber" in values)) values.catalognumber = [release.catalog];
	const primaryType = release.metadata?.type;
	if (primaryType && !("release_primary_type" in values))
		values.release_primary_type = [primaryType];
	// Keep explicit empty values from metadataValues authoritative over legacy fields.
	return values;
}

function releaseCredits(
	release: OriginalRelease,
	values: MetadataValues,
	artists: CatalogArtist[],
): MetadataCredit[] {
	if (release.credits?.length) {
		return release.credits.map((credit) => ({
			artistId: credit.artistId ?? "",
			creditName: credit.credit ?? credit.name ?? "",
			joinPhrase: credit.joinPhrase ?? "",
		}));
	}
	const names = values.albumartist ?? values.albumartists ?? [];
	const ids = values.musicbrainz_albumartistid ?? [];
	if (!names.length && !ids.length && release.artistId) {
		const artist = artists.find((item) => item.id === release.artistId);
		if (artist)
			return [{ artistId: artist.id, creditName: artist.name, joinPhrase: "" }];
	}
	return Array.from(
		{ length: Math.max(names.length, ids.length) },
		(_, index) => ({
			artistId: "",
			mbid: ids[index] || undefined,
			creditName: names[index] ?? "",
			joinPhrase: "",
		}),
	);
}

export type ReleaseMetadataEditorProps = {
	release: OriginalRelease;
	artists: CatalogArtist[];
	genresLoaded: boolean;
	onLoadGenres?: () => void | Promise<void>;
	onSave: (updated: OriginalRelease) => void | Promise<void>;
	local?: boolean;
};

export function useReleaseMetadataEditorDemoProvider({
	release,
	artists,
	genresLoaded,
	onLoadGenres,
	onSave,
}: ReleaseMetadataEditorProps): {
	editor: MetadataEditorProps;
	providerLinks: ReleaseMetadataEditorViewProps["providerLinks"];
} {
	const { locale } = useI18n();
	const m = (key: keyof typeof metadataEditorMessages.en) =>
		metadataEditorMessages[locale][key];
	const values = releaseValues(release);
	const extended = release as ReleaseWithExtras;
	const extras = [
		{
			key: "disambiguation",
			label: m("disambiguation"),
			value: extended.disambiguation ?? "",
		},
		{ key: "tags", label: m("tags"), value: extended.tags?.join(", ") ?? "" },
	];
	return {
		editor: {
			label: m("releaseDetails"),
			scope: "release",
			values,
			artists: artists.map(({ id, name, sortName }) => ({
				id,
				name,
				sortName,
			})),
			credits: releaseCredits(release, values, artists),
			genres: release.metadataGenres ?? [],
			genresLoaded,
			onLoadGenres,
			extraFields: extras,
			onSave: (next, details) => {
				const title = next.album?.[0] ?? "";
				const date = next.releasedate?.[0] ?? "";
				const oldDate = values.releasedate?.[0] ?? "";
				const country = next.releasecountry?.[0] ?? "";
				const label = next.recordlabel?.[0] ?? "";
				const catalog = next.catalognumber?.[0] ?? "";
				const credits = details?.credits;
				const extraValues = details?.extraValues;
				const metadata = {
					...(release.metadata ?? {
						date: release.year,
						country: release.country,
						label: release.label,
						catalogNumber: release.catalog,
						type: "",
					}),
					country,
					label,
					catalogNumber: catalog,
					...(date !== oldDate ? { date } : {}),
				};
				const updated = {
					...release,
					title,
					year: date.slice(0, 4),
					country,
					label,
					catalog,
					metadata,
					metadataValues: next,
					comparisonOverrides: {
						...mergeComparisonOverrides(
							release.comparisonOverrides,
							values,
							next,
						),
						...(details?.genres
							? {
									genre: details.genres
										.map(({ name, id }) => name ?? id ?? "")
										.filter(Boolean),
								}
							: {}),
					},
					...(details?.genres
						? { metadataGenres: details.genres as MetadataGenre[] }
						: {}),
					...(credits
						? {
								credits: credits.map((credit, index) => ({
									credit: credit.creditName,
									name: credit.creditName,
									role: release.credits?.[index]?.role ?? "albumartist",
									artistId: credit.artistId || null,
									joinPhrase: credit.joinPhrase,
								})),
							}
						: {}),
					...(extraValues
						? {
								disambiguation:
									extraValues.disambiguation ?? extended.disambiguation,
								tags: (extraValues.tags ?? extended.tags?.join(", ") ?? "")
									.split(",")
									.map((tag) => tag.trim())
									.filter(Boolean),
							}
						: {}),
				};
				return onSave(updated);
			},
		},
		providerLinks: {
			summary: `${m("providerLinks")} · ${release.provider ?? m("unspecified")}`,
			provider: { label: m("provider"), value: release.provider ?? "" },
			id: { label: m("providerId"), value: release.providerId ?? "" },
		},
	};
}
