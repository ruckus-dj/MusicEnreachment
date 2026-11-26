import { useI18n } from "../../../../app/i18n/host";
import type { MetadataFieldState } from "../../../../ui/features/library/MetadataFieldPresentation";
import { metadataEditorMessages } from "../../../../ui/features/library/metadata/metadataEditorMessages";
import type { ReleaseMetadataEditorViewProps } from "../../../../ui/features/library/metadata/ReleaseMetadataEditorView";
import {
	releaseBasicsFieldHint,
	releaseBasicsFieldLabel,
	releaseBasicsFields,
	releaseMetadataEditorMessages,
} from "../../../../ui/features/library/metadata/releaseMetadataEditorMessages";
import {
	type CatalogArtist,
	type OriginalRelease,
	type ReleaseBasicsIntentMap,
	releaseBasicsBaseValues,
	releaseBasicsDraftValues,
	releaseBasicsIntentState,
	releaseTitleDraftValues,
	releaseTitleOverrideState,
} from "../catalogData";
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
	// Title intent lives in comparisonOverrides; basic intent is typed separately.
	values.album = releaseTitleDraftValues(release);
	for (const [key, entries] of Object.entries(releaseBasicsBaseValues(release)))
		if (!(key in values) && entries.length) values[key] = [...entries];
	for (const { key, metadataKey } of releaseBasicsFields) {
		if (key === "title") continue;
		values[metadataKey] = releaseBasicsDraftValues(release, metadataKey);
	}
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
	const releaseText = releaseMetadataEditorMessages[locale];
	const titleState = releaseTitleOverrideState(release);
	const fieldStates: Record<string, MetadataFieldState> = Object.fromEntries(
		releaseBasicsFields.map(({ key, metadataKey }) => [
			metadataKey,
			key === "title"
				? titleState
				: releaseBasicsIntentState(release, metadataKey),
		]),
	);
	const fieldLabels = Object.fromEntries(
		releaseBasicsFields.map((field) => [
			field.metadataKey,
			releaseBasicsFieldLabel(locale, field),
		]),
	);
	const fieldKinds = Object.fromEntries(
		releaseBasicsFields.map(({ metadataKey, kind }) => [metadataKey, kind]),
	) as NonNullable<MetadataEditorProps["fieldKinds"]>;
	const fieldHints = Object.fromEntries(
		releaseBasicsFields.flatMap((field) => {
			const hint = releaseBasicsFieldHint(locale, field);
			return hint ? [[field.metadataKey, hint]] : [];
		}),
	);
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
			label: releaseText.title,
			scope: "release",
			libraryMetadata: true,
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
			fieldStates,
			fieldLabels,
			fieldKinds,
			fieldHints,
			fieldStateMode: {
				label: releaseText.mode,
				absent: releaseText.absent,
				value: releaseText.value,
				explicitEmpty: releaseText.explicitEmpty,
			},
			fieldStateDisplay: {
				absent: releaseText.noOverride,
				explicitEmpty: releaseText.explicitEmpty,
				emptyValue: releaseText.emptyValue,
			},
			onSave: (next, details) => {
				const nextTitleState =
					details?.fieldStates?.album ?? releaseTitleOverrideState(release);
				const titleValues =
					nextTitleState === "explicit_empty"
						? []
						: nextTitleState === "value"
							? next.album?.length
								? [...next.album]
								: [""]
							: [release.title];
				const nextBasicsIntent: ReleaseBasicsIntentMap = {
					...release.releaseBasicsIntent,
				};
				for (const { key, metadataKey } of releaseBasicsFields) {
					if (key === "title") continue;
					if (
						!details?.fieldStates ||
						!Object.hasOwn(details.fieldStates, metadataKey)
					)
						continue;
					const state = details.fieldStates[metadataKey] ?? "absent";
					if (state === "absent") delete nextBasicsIntent[metadataKey];
					else if (state === "explicit_empty")
						nextBasicsIntent[metadataKey] = {
							state: "explicit_empty",
						};
					else
						nextBasicsIntent[metadataKey] = {
							state: "value",
							value: [...(next[metadataKey] ?? [])],
						};
				}
				const credits = details?.credits;
				const extraValues = details?.extraValues;
				const comparisonOverrides = mergeComparisonOverrides(
					release.comparisonOverrides,
					values,
					next,
				);
				for (const { key, metadataKey } of releaseBasicsFields)
					if (key !== "title") delete comparisonOverrides[metadataKey];
				if (nextTitleState === "absent") delete comparisonOverrides.album;
				else comparisonOverrides.album = [...titleValues];
				const metadataValues: MetadataValues = {
					...next,
					album: titleValues,
				};
				const baseValues = releaseBasicsBaseValues(release);
				for (const { key, metadataKey } of releaseBasicsFields) {
					if (key === "title") continue;
					const intent = nextBasicsIntent[metadataKey];
					if (!intent)
						metadataValues[metadataKey] = [...(baseValues[metadataKey] ?? [])];
					else if (intent.state === "explicit_empty")
						metadataValues[metadataKey] = [];
					else metadataValues[metadataKey] = [...intent.value];
				}
				const updated = {
					...release,
					metadataValues,
					releaseBasicsIntent: Object.keys(nextBasicsIntent).length
						? nextBasicsIntent
						: undefined,
					comparisonOverrides: {
						...comparisonOverrides,
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
