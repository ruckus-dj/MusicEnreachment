import { useI18n } from "../../../../app/i18n/host";
import { metadataEditorMessages } from "../../../../ui/features/library/metadata/metadataEditorMessages";
import type { TrackMetadataEditorsViewProps } from "../../../../ui/features/library/metadata/TrackMetadataEditorsView";
import {
	recordingBasicsFields,
	releaseMetadataEditorMessages,
} from "../../../../ui/features/library/metadata/releaseMetadataEditorMessages";
import {
	type CatalogArtist,
	effectiveRecordingTitle,
	type OriginalTrack,
	positionNumberingFields,
	positionTitleIntent,
	recordingTitleBaseline,
	type ScopedScalarIntentMap,
	type SourceFiles,
} from "../catalogData";
import { mergeComparisonOverrides } from "../comparisonOverrides";
import {
	libraryPositionEditorFields,
	libraryRecordingEditorFields,
} from "./editorLayout";
import type { MetadataEditorProps } from "./MetadataEditor";
import type { MetadataCredit } from "./model";
import type { MetadataGenre, MetadataValues } from "./projection";

function mergedValues(
	...values: (MetadataValues | undefined)[]
): MetadataValues {
	return Object.assign({}, ...values.filter(Boolean));
}

function recordingValues(track: OriginalTrack): MetadataValues {
	const values = { ...(track.recordingMetadata ?? {}) };
	for (const { metadataKey, kind } of recordingBasicsFields) {
		const intent = track.recordingScalarIntent?.[metadataKey];
		values[metadataKey] =
			intent?.state === "value"
				? [...intent.value]
				: kind === "advisory"
					? ["explicit"]
					: [];
	}
	if (!("genre" in values)) values.genre = [...(track.recordingGenres ?? [])];
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
	files: SourceFiles;
	genresLoaded: boolean;
	onLoadGenres?: () => void | Promise<void>;
	onMutate: (update: (current: OriginalTrack) => OriginalTrack) => void;
	onRecordingChange?: (
		update: (current: OriginalTrack) => OriginalTrack,
	) => void;
};

type AudioEditor = { id: string; editor: MetadataEditorProps };

export function useTrackMetadataEditorsDemoProvider({
	track,
	artists,
	files,
	genresLoaded,
	onLoadGenres,
	onMutate,
	onRecordingChange,
}: TrackMetadataEditorsProps): Omit<
	TrackMetadataEditorsViewProps,
	"recording" | "position" | "selectedAudio" | "otherAudio"
> & {
	recording: MetadataEditorProps;
	position: MetadataEditorProps;
	selectedAudio?: AudioEditor;
	otherAudio: AudioEditor[];
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
			positionNumberingFields.map((field) => {
				const intent = track.positionScalarIntent?.[field];
				return [field, intent?.state === "value" ? [...intent.value] : []];
			}),
		),
		title: [positionTitle.value],
	};
	const audioSources = [
		...new Set([
			...(track.sourceIds ?? []),
			...(track.sourceId ? [track.sourceId] : []),
		]),
	];
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

	const selectedId = track.sourceId;
	const hasSelected = Boolean(selectedId && audioSources.includes(selectedId));
	const otherIds = audioSources.filter((id) => id !== selectedId);
	const fileName = (id: string) => files[id]?.name ?? id;
	return {
		label: `${m("positionDetails")} ${track.id}`,
		recording: {
			label: releaseText.recordingTitle,
			scope: "recording",
			libraryMetadata: true,
			fields: libraryRecordingEditorFields,
			values: recording,
			fieldStates: Object.fromEntries(
				recordingBasicsFields.map(({ metadataKey }) => [
					metadataKey,
					track.recordingScalarIntent?.[metadataKey]?.state ?? "absent",
				]),
			),
			fieldLabels: Object.fromEntries(
				recordingBasicsFields.map(({ key, metadataKey }) => [
					metadataKey,
					releaseText.labels[key],
				]),
			),
			fieldStateMode,
			fieldStateDisplay,
			artists: artists.map(({ id, name, sortName }) => ({
				id,
				name,
				sortName,
			})),
			credits: creditsFor(track, recording, artists),
			genres: (track.recordingGenreRefs ??
				track.recordingGenres?.map((name) => ({ name })) ??
				[]) as MetadataGenre[],
			genresLoaded,
			onLoadGenres,
			onSave: (values, details) => {
				const nextIntent: ScopedScalarIntentMap = {
					...track.recordingScalarIntent,
				};
				for (const { metadataKey, kind } of recordingBasicsFields) {
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
									: [values[metadataKey]?.[0] ?? ""],
						};
				}
				const update = (current: OriginalTrack): OriginalTrack => {
					const positionTitle = positionTitleIntent(current);
					const nextMetadata = { ...values };
					const comparison = mergeComparisonOverrides(
						current.recordingComparisonOverrides,
						recording,
						values,
					);
					for (const { metadataKey } of recordingBasicsFields) {
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
						recordingComparisonOverrides: {
							...comparison,
							...(details?.genres
								? {
										genre: details.genres
											.map(({ name, id }) => name ?? id ?? "")
											.filter(Boolean),
									}
								: {}),
						},
						...(details?.credits ? { recordingCredits: details.credits } : {}),
						...(details?.genres
							? {
									recordingGenreRefs: details.genres as MetadataGenre[],
									recordingGenres: details.genres
										.map((genre) => genre.name ?? genre.id ?? "")
										.filter(Boolean),
								}
							: {}),
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
				track: track.positionScalarIntent?.track?.state ?? "absent",
				disc: track.positionScalarIntent?.disc?.state ?? "absent",
			},
			fieldLabels: {
				title: releaseText.labels.title,
				track: releaseText.labels.track_number,
				disc: releaseText.labels.disc_number,
			},
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
					for (const field of positionNumberingFields) {
						delete positionMetadata[field];
						delete comparison[field];
						const state = details?.fieldStates?.[field];
						if (state === "absent") delete positionScalarIntent[field];
						else if (state === "explicit_empty")
							positionScalarIntent[field] = { state };
						else if (state === "value")
							positionScalarIntent[field] = {
								state,
								value: [String(Number(values[field]?.[0]))],
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
		audioLabel: m("audioMetadata"),
		audioSummary: `${m("volumeEncoding")} · ${selectedId ? fileName(selectedId) : m("noSelectedSource")}`,
		noLinkedVariant: m("noLinkedVariant"),
		selectedFileUnavailable:
			selectedId && !hasSelected ? m("selectedFileUnavailable") : undefined,
		selectedAudio:
			selectedId && hasSelected
				? {
						id: selectedId,
						editor: {
							label: `${fileName(selectedId)} · ${m("selectedForPublication")}`,
							scope: "audio",
							values: mergedValues(track.audioMetadata?.[selectedId]),
							onSave: (values) =>
								onMutate((current) => ({
									...current,
									audioMetadata: {
										...current.audioMetadata,
										[selectedId]: values,
									},
									audioComparisonOverrides: {
										...current.audioComparisonOverrides,
										[selectedId]: mergeComparisonOverrides(
											current.audioComparisonOverrides?.[selectedId],
											mergedValues(track.audioMetadata?.[selectedId]),
											values,
										),
									},
								})),
						},
					}
				: undefined,
		otherAudioSummary: m("otherAudioVariants", { count: otherIds.length }),
		otherAudio: otherIds.map((id) => ({
			id,
			editor: {
				label: `${fileName(id)} · ${m("readOnly")}`,
				scope: "audio",
				values: mergedValues(track.audioMetadata?.[id]),
				readOnly: true,
				onSave: () => undefined,
			},
		})),
	};
}
