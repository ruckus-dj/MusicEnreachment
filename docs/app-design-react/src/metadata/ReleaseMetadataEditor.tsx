import { MetadataEditor } from "./MetadataEditor";
import type { MetadataCredit } from "./model";
import type { MetadataGenre, MetadataValues } from "./projection";
import type { CatalogArtist, OriginalRelease } from "../catalogData";
import { useI18n } from "../i18n/I18nProvider";
import { metadataEditorMessages } from "./editorMessages";

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
	if (!("releasetype" in values))
		values.releasetype = [release.metadata?.type ?? ""];
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

export function ReleaseMetadataEditor({
	release,
	artists,
	genresLoaded,
	onLoadGenres,
	onSave,
	local = false,
}: {
	release: OriginalRelease;
	artists: CatalogArtist[];
	genresLoaded: boolean;
	onLoadGenres?: () => void | Promise<void>;
	onSave: (updated: OriginalRelease) => void | Promise<void>;
	local?: boolean;
}) {
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
	return (
		<div className="grid min-w-0 gap-3">
			<MetadataEditor
				label={m("releaseDetails")}
				scope="release"
				values={values}
				artists={artists.map(({ id, name, sortName }) => ({
					id,
					name,
					sortName,
				}))}
				credits={releaseCredits(release, values, artists)}
				genres={release.metadataGenres ?? []}
				genresLoaded={genresLoaded}
				onLoadGenres={onLoadGenres}
				extraFields={extras}
				onSave={(next, details) => {
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
					void local;
					return onSave(updated);
				}}
			/>
			<details className="rounded-[var(--control-radius)] border border-[var(--line)] bg-[var(--surface)] p-3">
				<summary className="cursor-pointer font-semibold">
					{m("providerLinks")} · {release.provider ?? m("unspecified")}
				</summary>
				<dl className="mt-3 grid min-w-0 gap-2 text-sm sm:grid-cols-2">
					<div className="min-w-0">
						<dt className="text-[var(--muted)]">{m("provider")}</dt>
						<dd className="m-0 break-all">{release.provider ?? ""}</dd>
					</div>
					<div className="min-w-0">
						<dt className="text-[var(--muted)]">{m("providerId")}</dt>
						<dd className="m-0 break-all">{release.providerId ?? ""}</dd>
					</div>
				</dl>
			</details>
		</div>
	);
}
