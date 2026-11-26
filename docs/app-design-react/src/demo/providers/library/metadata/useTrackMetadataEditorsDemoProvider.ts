import { useI18n } from "../../../../app/i18n/host";
import { metadataEditorMessages } from "../../../../ui/features/library/metadata/metadataEditorMessages";
import type { TrackMetadataEditorsViewProps } from "../../../../ui/features/library/metadata/TrackMetadataEditorsView";
import { releaseMetadataEditorMessages } from "../../../../ui/features/library/metadata/releaseMetadataEditorMessages";
import {
	type CatalogArtist,
	type OriginalTrack,
	positionTitleIntent,
	recordingTitleBaseline,
	type SourceFiles,
} from "../catalogData";
import { mergeComparisonOverrides } from "../comparisonOverrides";
import { libraryPositionEditorFields } from "./editorLayout";
import type { MetadataEditorProps } from "./MetadataEditor";
import type { MetadataCredit } from "./model";
import type { MetadataGenre, MetadataValues } from "./projection";

type TrackWithExtras = OriginalTrack & { disambiguation?: string };

function mergedValues(
	...values: (MetadataValues | undefined)[]
): MetadataValues {
	return Object.assign({}, ...values.filter(Boolean));
}

function recordingValues(track: OriginalTrack): MetadataValues {
	const values = { ...(track.recordingMetadata ?? {}) };
	if (!("title" in values))
		values.title = [recordingTitleBaseline(track) ?? track.title];
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
		track: [
			track.displayedNumber ?? String(track.mediumPosition ?? track.position),
		],
		disc: [String(track.medium ?? 1)],
		...track.positionMetadata,
		title: [positionTitle.value],
	};
	const audioSources = [
		...new Set([
			...(track.sourceIds ?? []),
			...(track.sourceId ? [track.sourceId] : []),
		]),
	];
	const extended = track as TrackWithExtras;
	const recordingExtras = [
		{
			key: "disambiguation",
			label: m("recordingDisambiguation"),
			value: extended.disambiguation ?? "",
		},
	];
	const releaseText = releaseMetadataEditorMessages[locale];

	const selectedId = track.sourceId;
	const hasSelected = Boolean(selectedId && audioSources.includes(selectedId));
	const otherIds = audioSources.filter((id) => id !== selectedId);
	const fileName = (id: string) => files[id]?.name ?? id;
	return {
		label: `${m("positionDetails")} ${track.id}`,
		recording: {
			label: m("recordingDetails"),
			scope: "recording",
			values: recording,
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
			extraFields: recordingExtras,
			onSave: (values, details) => {
				const title = values.title?.[0] ?? "";
				const titleChanged = title !== recording.title?.[0];
				const update = (current: OriginalTrack): OriginalTrack => {
					const explicitPositionTitle =
						positionTitleIntent(current).state !== "absent";
					const baseline = recordingTitleBaseline(current);
					const updatePositionTitle =
						titleChanged &&
						!explicitPositionTitle &&
						baseline !== undefined &&
						current.title === baseline;
					return {
						...current,
						recordingTitle: title,
						...(updatePositionTitle ? { title } : {}),
						recordingMetadata: values,
						recordingComparisonOverrides: {
							...mergeComparisonOverrides(
								current.recordingComparisonOverrides,
								recording,
								values,
							),
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
						...(details?.extraValues?.disambiguation !== undefined
							? { disambiguation: details.extraValues.disambiguation }
							: {}),
					};
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
			fieldStates: { title: positionTitle.state },
			fieldLabels: { title: releaseText.labels.title },
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
			requiredKeys: ["track"],
			onSave: (values, details) => {
				const displayedNumber = values.track?.[0] ?? "";
				const medium = Number(values.disc?.[0] ?? "1");
				onMutate((current) => {
					const comparison = mergeComparisonOverrides(
						current.positionComparisonOverrides,
						position,
						values,
					);
					const positionMetadata = { ...values };
					delete positionMetadata.title;
					const updated: OriginalTrack = {
						...current,
						displayedNumber,
						mediumPosition:
							Number(displayedNumber) ||
							current.mediumPosition ||
							current.position,
						medium:
							Number.isFinite(medium) && medium > 0 ? medium : current.medium,
						positionMetadata,
						positionComparisonOverrides: comparison,
					};
					const titleState = details?.fieldStates?.title;
					if (titleState !== undefined) {
						const title = values.title?.[0] ?? "";
						// Preserve the recording's value before changing the position.
						const recordingTitle =
							recordingTitleBaseline(current) ?? current.title;
						updated.recordingTitle = recordingTitle;
						delete comparison.title;
						if (titleState === "explicit_empty") comparison.title = [];
						else if (titleState === "value") comparison.title = [title];
						updated.title =
							titleState === "explicit_empty"
								? ""
								: titleState === "value"
									? title
									: recordingTitle;
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
