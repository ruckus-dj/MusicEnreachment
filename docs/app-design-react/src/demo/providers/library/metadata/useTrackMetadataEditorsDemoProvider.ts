import { useI18n } from "../../../../app/i18n/host";
import { metadataEditorMessages } from "../../../../ui/features/library/metadata/metadataEditorMessages";
import type { TrackMetadataEditorsViewProps } from "../../../../ui/features/library/metadata/TrackMetadataEditorsView";
import {
	recordingScalarFields,
	releaseMetadataEditorMessages,
	releaseScalarFieldHint,
} from "../../../../ui/features/library/metadata/releaseMetadataEditorMessages";
import {
	type CatalogArtist,
	effectiveRecordingTitle,
	type OriginalTrack,
	positionLayoutFields,
	positionTitleIntent,
	recordingTitleBaseline,
	type ScopedScalarIntentMap,
} from "../catalogData";
import { mergeComparisonOverrides } from "../comparisonOverrides";
import {
	libraryPositionEditorFields,
	libraryRecordingEditorFields,
} from "./editorLayout";
import type { MetadataEditorProps } from "./MetadataEditor";
import type { MetadataCredit } from "./model";
import type { MetadataValues } from "./projection";

function recordingValues(track: OriginalTrack): MetadataValues {
	const values = { ...(track.recordingMetadata ?? {}) };
	for (const { metadataKey, kind } of recordingScalarFields) {
		const intent = track.recordingScalarIntent?.[metadataKey];
		values[metadataKey] =
			intent?.state === "value"
				? [...intent.value]
				: kind === "advisory"
					? ["explicit"]
					: [];
	}
	delete values.genre;
	return values;
}

function creditsFor(
	track: OriginalTrack,
	values: MetadataValues,
	artists: CatalogArtist[],
): MetadataCredit[] {
	if (track.recordingCredits) return track.recordingCredits;
	const names = values.artist ?? values.artists ?? [];
	if (!names.length && track.artistId) {
		const artist = artists.find((candidate) => candidate.id === track.artistId);
		if (artist)
			return [
				{
					artistId: artist.id,
					creditName: artist.name,
					joinPhrase: "",
				},
			];
	}
	return names.map((creditName, index) => ({
		artistId: "",
		mbid: values.musicbrainz_artistid?.[index] || undefined,
		creditName,
		joinPhrase: "",
	}));
}

export type TrackMetadataEditorsProps = {
	track: OriginalTrack;
	artists: CatalogArtist[];
	onMutate: (update: (current: OriginalTrack) => OriginalTrack) => void;
	onRecordingChange?: (
		update: (current: OriginalTrack) => OriginalTrack,
	) => void;
};

export function useTrackMetadataEditorsDemoProvider({
	track,
	artists,
	onMutate,
	onRecordingChange,
}: TrackMetadataEditorsProps): Omit<
	TrackMetadataEditorsViewProps,
	"recording" | "position"
> & {
	recording: MetadataEditorProps;
	position: MetadataEditorProps;
} {
	const { locale } = useI18n();
	const m = (
		key: keyof typeof metadataEditorMessages.en,
		values: Record<string, string | number> = {},
	) =>
		metadataEditorMessages[locale][key].replace(
			/\{([^}]+)\}/g,
			(_, name: string) => String(values[name] ?? `{${name}}`),
		);
	const recording = recordingValues(track);
	const positionTitle = positionTitleIntent(track);
	const position: MetadataValues = {
		...track.positionMetadata,
		...Object.fromEntries(
			positionLayoutFields.map(({ metadataKey }) => {
				const intent = track.positionScalarIntent?.[metadataKey];
				return [
					metadataKey,
					intent?.state === "value" ? [...intent.value] : [],
				];
			}),
		),
		title: [positionTitle.value],
	};
	const releaseText = releaseMetadataEditorMessages[locale];
	const fieldStateMode = {
		label: releaseText.mode,
		absent: releaseText.absent,
		value: releaseText.value,
		explicitEmpty: releaseText.explicitEmpty,
	};
	const fieldStateDisplay = {
		absent: releaseText.noOverride,
		explicitEmpty: releaseText.explicitEmpty,
		emptyValue: releaseText.emptyValue,
	};

	return {
		label: `${m("positionDetails")} ${track.id}`,
		recording: {
			label: releaseText.recordingTitle,
			scope: "recording",
			libraryMetadata: true,
			fields: libraryRecordingEditorFields,
			values: recording,
			fieldStates: Object.fromEntries(
				recordingScalarFields.map(({ metadataKey }) => [
					metadataKey,
					track.recordingScalarIntent?.[metadataKey]?.state ?? "absent",
				]),
			),
			fieldLabels: Object.fromEntries(
				recordingScalarFields.map(({ key, metadataKey }) => [
					metadataKey,
					releaseText.labels[key],
				]),
			),
			fieldKinds: Object.fromEntries(
				recordingScalarFields.map(({ metadataKey, kind }) => [
					metadataKey,
					kind,
				]),
			),
			fieldHints: Object.fromEntries(
				recordingScalarFields.flatMap((field) => {
					const hint = releaseScalarFieldHint(locale, field);
					return hint ? [[field.metadataKey, hint]] : [];
				}),
			),
			fieldStateMode,
			fieldStateDisplay,
			artists: artists.map(({ id, name, sortName }) => ({
				id,
				name,
				sortName,
			})),
			credits: creditsFor(track, recording, artists),
			onSave: (values, details) => {
				const nextIntent: ScopedScalarIntentMap = {
					...track.recordingScalarIntent,
				};
				for (const { metadataKey, kind } of recordingScalarFields) {
					const state = details?.fieldStates?.[metadataKey];
					if (!state) continue;
					if (state === "absent") delete nextIntent[metadataKey];
					else if (state === "explicit_empty")
						nextIntent[metadataKey] = { state };
					else
						nextIntent[metadataKey] = {
							state,
							value:
								kind === "list"
									? [...(values[metadataKey] ?? [])]
									: [
											kind === "number"
												? String(Number(values[metadataKey]?.[0]))
												: (values[metadataKey]?.[0] ?? ""),
										],
						};
				}
				const update = (current: OriginalTrack): OriginalTrack => {
					const positionTitle = positionTitleIntent(current);
					const nextMetadata = { ...values };
					if (current.recordingMetadata?.genre)
						nextMetadata.genre = current.recordingMetadata.genre;
					const comparison = mergeComparisonOverrides(
						current.recordingComparisonOverrides,
						recording,
						values,
					);
					for (const { metadataKey } of recordingScalarFields) {
						delete nextMetadata[metadataKey];
						delete comparison[metadataKey];
						const raw = current.recordingMetadata?.[metadataKey];
						if (raw) nextMetadata[metadataKey] = raw;
					}
					const updated: OriginalTrack = {
						...current,
						recordingTitle: recordingTitleBaseline(current) ?? current.title,
						recordingScalarIntent: nextIntent,
						recordingMetadata: nextMetadata,
						recordingComparisonOverrides: comparison,
						...(details?.credits ? { recordingCredits: details.credits } : {}),
					};
					if (details?.fieldStates?.title) {
						if (positionTitle.state === "absent")
							updated.title = effectiveRecordingTitle(updated);
						else if (
							positionTitle.state === "value" &&
							!Object.hasOwn(current.positionComparisonOverrides ?? {}, "title")
						)
							updated.positionTitleBaseline = positionTitle.value;
					}
					return updated;
				};
				(onRecordingChange ?? onMutate)(update);
			},
		},
		position: {
			label: releaseText.positionTitle,
			scope: "track",
			libraryMetadata: true,
			values: position,
			fields: libraryPositionEditorFields,
			fieldStates: {
				title: positionTitle.state,
				...Object.fromEntries(
					positionLayoutFields.map(({ metadataKey }) => [
						metadataKey,
						track.positionScalarIntent?.[metadataKey]?.state ?? "absent",
					]),
				),
			},
			fieldLabels: {
				title: releaseText.labels.title,
				...Object.fromEntries(
					positionLayoutFields.map(({ key, metadataKey }) => [
						metadataKey,
						releaseText.labels[key],
					]),
				),
			},
			fieldKinds: Object.fromEntries(
				positionLayoutFields.map(({ metadataKey, kind }) => [
					metadataKey,
					kind,
				]),
			),
			fieldStateMode,
			fieldStateDisplay,
			onSave: (values, details) => {
				onMutate((current) => {
					const comparison = mergeComparisonOverrides(
						current.positionComparisonOverrides,
						position,
						values,
					);
					const positionMetadata = { ...values };
					delete positionMetadata.title;
					const positionScalarIntent: ScopedScalarIntentMap = {
						...current.positionScalarIntent,
					};
					for (const { metadataKey: field, kind } of positionLayoutFields) {
						delete positionMetadata[field];
						delete comparison[field];
						const state = details?.fieldStates?.[field];
						if (state === "absent") delete positionScalarIntent[field];
						else if (state === "explicit_empty")
							positionScalarIntent[field] = { state };
						else if (state === "value")
							positionScalarIntent[field] = {
								state,
								value: [
									kind === "integer"
										? String(Number(values[field]?.[0]))
										: (values[field]?.[0] ?? ""),
								],
							};
					}
					const updated: OriginalTrack = {
						...current,
						positionMetadata,
						positionScalarIntent,
						positionComparisonOverrides: comparison,
					};
					const titleState = details?.fieldStates?.title;
					if (titleState !== undefined) {
						const title = values.title?.[0] ?? "";
						// Preserve the recording's value before changing the position.
						const recordingTitle =
							recordingTitleBaseline(current) ?? current.title;
						updated.recordingTitle = recordingTitle;
						delete updated.positionTitleBaseline;
						delete comparison.title;
						if (titleState === "explicit_empty") comparison.title = [];
						else if (titleState === "value") comparison.title = [title];
						updated.title =
							titleState === "explicit_empty"
								? ""
								: titleState === "value"
									? title
									: effectiveRecordingTitle(updated);
					}
					return updated;
				});
			},
		},
	};
}
