import { useI18n } from "../../../../app/i18n/host";
import type { MetadataFieldState } from "../../../../ui/features/library/MetadataFieldPresentation";
import type { ReleaseMetadataEditorViewProps } from "../../../../ui/features/library/metadata/ReleaseMetadataEditorView";
import {
	releaseMetadataEditorMessages,
	releaseScalarFieldHint,
	releaseScalarFieldLabel,
	releaseScalarFields,
} from "../../../../ui/features/library/metadata/releaseMetadataEditorMessages";
import {
	acceptedReleaseIdentifier,
	type CatalogArtist,
	type OriginalRelease,
	type ScopedScalarIntentMap,
	releaseBasicsBaseValues,
	releaseScalarDraftValues,
	releaseScalarIntentState,
	releaseTitleDraftValues,
	releaseTitleOverrideState,
} from "../catalogData";
import { mergeComparisonOverrides } from "../comparisonOverrides";
import type { MetadataEditorProps } from "./MetadataEditor";
import type { MetadataGenre, MetadataValues } from "./projection";
import {
	releaseCreditBaseline,
	saveScopedArtistCredit,
	scopedArtistCreditEditor,
} from "./scopedArtistCredits";

function releaseValues(release: OriginalRelease): MetadataValues {
	const values: MetadataValues = Object.fromEntries(
		Object.entries(release.metadataValues ?? {}).map(([key, entries]) => [
			key,
			[...entries],
		]),
	);
	// Title intent lives in comparisonOverrides; other scalar intent is separate.
	values.album = releaseTitleDraftValues(release);
	for (const { key, metadataKey, kind } of releaseScalarFields) {
		if (key === "title") continue;
		values[metadataKey] =
			releaseScalarIntentState(release, metadataKey) === "value"
				? releaseScalarDraftValues(release, metadataKey)
				: kind === "boolean"
					? ["false"]
					: kind === "advisory"
						? ["explicit"]
						: [];
	}
	return values;
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
	const releaseText = releaseMetadataEditorMessages[locale];
	const titleState = releaseTitleOverrideState(release);
	const fieldStates: Record<string, MetadataFieldState> = Object.fromEntries(
		releaseScalarFields.map(({ key, metadataKey }) => [
			metadataKey,
			key === "title"
				? titleState
				: releaseScalarIntentState(release, metadataKey),
		]),
	);
	const fieldLabels = Object.fromEntries(
		releaseScalarFields.map((field) => [
			field.metadataKey,
			releaseScalarFieldLabel(locale, field),
		]),
	);
	const fieldKinds = Object.fromEntries(
		releaseScalarFields.map(({ metadataKey, kind }) => [metadataKey, kind]),
	) as NonNullable<MetadataEditorProps["fieldKinds"]>;
	const fieldHints = Object.fromEntries(
		releaseScalarFields.flatMap((field) => {
			const hint = releaseScalarFieldHint(locale, field);
			return hint ? [[field.metadataKey, hint]] : [];
		}),
	);
	const values = releaseValues(release);
	return {
		editor: {
			label: releaseText.title,
			scope: "release",
			libraryMetadata: true,
			values,
			scopedArtistCredit: scopedArtistCreditEditor(
				release.releaseArtistCredit,
				releaseCreditBaseline(release, artists),
				artists,
				"release",
				releaseText.groups.artistCredits,
			),
			genres: release.metadataGenres ?? [],
			genresLoaded,
			onLoadGenres,
			fieldStates,
			fieldLabels,
			fieldKinds,
			fieldHints,
			fieldBaselines: Object.fromEntries(
				(["asin", "script"] as const).flatMap((key) => {
					const value = acceptedReleaseIdentifier(release, key);
					return value
						? [
								[
									key,
									{
										display: value,
										notice: releaseText.providerBaseline,
										populated: true,
									},
								],
							]
						: [];
				}),
			),
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
				const nextScalarIntent: ScopedScalarIntentMap = {
					...release.releaseScalarIntent,
				};
				for (const { key, metadataKey, kind } of releaseScalarFields) {
					if (key === "title") continue;
					if (
						!details?.fieldStates ||
						!Object.hasOwn(details.fieldStates, metadataKey)
					)
						continue;
					const state = details.fieldStates[metadataKey] ?? "absent";
					if (state === "absent") {
						if (key === "asin" || key === "script")
							nextScalarIntent[metadataKey] = { state };
						else delete nextScalarIntent[metadataKey];
					} else if (state === "explicit_empty")
						nextScalarIntent[metadataKey] = {
							state: "explicit_empty",
						};
					else
						nextScalarIntent[metadataKey] = {
							state: "value",
							value:
								kind === "list"
									? [...(next[metadataKey] ?? [])]
									: [next[metadataKey]?.[0] ?? ""],
						};
				}
				const comparisonOverrides = mergeComparisonOverrides(
					release.comparisonOverrides,
					values,
					next,
				);
				for (const { key, metadataKey } of releaseScalarFields)
					if (key !== "title") delete comparisonOverrides[metadataKey];
				if (nextTitleState === "absent") delete comparisonOverrides.album;
				else comparisonOverrides.album = [...titleValues];
				const metadataValues: MetadataValues = {
					...next,
					album: titleValues,
				};
				const baseValues = releaseBasicsBaseValues(release);
				for (const { key, metadataKey } of releaseScalarFields) {
					if (key === "title") continue;
					const base = baseValues[metadataKey];
					// Keep the original underlying facts separate from desired edits.
					if (!base) {
						delete metadataValues[metadataKey];
						const previous = release.metadataValues?.[metadataKey];
						if (previous) metadataValues[metadataKey] = previous;
						continue;
					}
					const intent = nextScalarIntent[metadataKey];
					if (!intent || intent.state === "absent")
						metadataValues[metadataKey] = [...base];
					else if (intent.state === "explicit_empty")
						metadataValues[metadataKey] = [];
					else metadataValues[metadataKey] = [...intent.value];
				}
				const updated = {
					...release,
					metadataValues,
					releaseScalarIntent: Object.keys(nextScalarIntent).length
						? nextScalarIntent
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
					releaseArtistCredit: details?.scopedArtistCredit
						? saveScopedArtistCredit(details.scopedArtistCredit)
						: release.releaseArtistCredit,
				};
				return onSave(updated);
			},
		},
		providerLinks: release.providerId
			? [
					{
						provider: release.provider || "MusicBrainz",
						id: release.providerId,
					},
				]
			: [],
	};
}
