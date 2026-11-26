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
	positionScalarFields,
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
import type { MetadataValues } from "./projection";
import {
	knownArtistCredit,
	recordingCreditBaseline,
	saveScopedArtistCredit,
	scopedArtistCreditEditor,
} from "./scopedArtistCredits";
import {
	compositionDraftValues,
	compositionKeys,
	compositionStates,
	updateComposition,
} from "./scopedComposition";

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
	Object.assign(recording, compositionDraftValues(track.recordingComposition));
	const positionTitle = positionTitleIntent(track);
	const position: MetadataValues = {
		...track.positionMetadata,
		...compositionDraftValues(track.positionComposition),
		...Object.fromEntries(
			positionScalarFields.map(({ metadataKey, kind }) => {
				const intent = track.positionScalarIntent?.[metadataKey];
				return [
					metadataKey,
					intent?.state === "value"
						? [...intent.value]
						: kind === "advisory"
							? ["explicit"]
							: [],
				];
			}),
		),
		title: [positionTitle.value],
	};
	const releaseText = releaseMetadataEditorMessages[locale];
	const positionArtist = artists.find(({ id }) => id === track.artistId);
	const recordingKey = track.recordingScalarIntent?.key;
	const inheritedKey =
		(recordingKey?.state === "value" && recordingKey.value[0]) ||
		recordingKey?.state === "explicit_empty"
			? {
					display:
						recordingKey.state === "explicit_empty"
							? releaseText.explicitEmpty
							: (recordingKey.value[0] ?? ""),
					notice: releaseText.inheritedFromRecording,
					populated:
						recordingKey.state === "value" && Boolean(recordingKey.value[0]),
				}
			: undefined;
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
			scopedContributors: track.recordingContributors ?? { state: "absent" },
			fieldStates: {
				...Object.fromEntries(
					recordingScalarFields.map(({ metadataKey }) => [
						metadataKey,
						track.recordingScalarIntent?.[metadataKey]?.state ?? "absent",
					]),
				),
				...compositionStates(track.recordingComposition),
			},
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
			scopedArtistCredit: scopedArtistCreditEditor(
				track.recordingArtistCredit,
				recordingCreditBaseline(track, artists),
				artists,
				"recording",
				releaseText.groups.artistCredits,
			),
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
					for (const key of compositionKeys) {
						delete nextMetadata[key];
						delete comparison[key];
						const raw = current.recordingMetadata?.[key];
						if (raw) nextMetadata[key] = raw;
					}
					const updated: OriginalTrack = {
						...current,
						recordingTitle: recordingTitleBaseline(current) ?? current.title,
						recordingScalarIntent: nextIntent,
						recordingComposition: updateComposition(
							current.recordingComposition,
							values,
							details?.fieldStates,
						),
						recordingMetadata: nextMetadata,
						recordingComparisonOverrides: comparison,
						recordingArtistCredit: details?.scopedArtistCredit
							? saveScopedArtistCredit(details.scopedArtistCredit)
							: current.recordingArtistCredit,
						recordingContributors:
							details?.scopedContributors ?? current.recordingContributors,
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
			scopedContributors: track.positionContributors ?? { state: "absent" },
			scopedArtistCredit: scopedArtistCreditEditor(
				track.positionArtistCredit,
				track.positionCreditEvidence && "rows" in track.positionCreditEvidence
					? track.positionCreditEvidence.rows
					: knownArtistCredit(positionArtist),
				artists,
				"position",
				releaseText.groups.artistCredits,
				track.positionCreditEvidence,
			),
			fieldStates: {
				title: positionTitle.state,
				...compositionStates(track.positionComposition),
				...Object.fromEntries(
					positionScalarFields.map(({ metadataKey }) => [
						metadataKey,
						track.positionScalarIntent?.[metadataKey]?.state ?? "absent",
					]),
				),
			},
			fieldLabels: {
				title: releaseText.labels.title,
				...Object.fromEntries(
					positionScalarFields.map(({ key, metadataKey }) => [
						metadataKey,
						releaseText.labels[key],
					]),
				),
			},
			fieldKinds: Object.fromEntries(
				positionScalarFields.map(({ metadataKey, kind }) => [
					metadataKey,
					kind,
				]),
			),
			fieldBaselines: inheritedKey ? { key: inheritedKey } : undefined,
			fieldHints: { recordingdate: releaseText.dateHint },
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
					for (const { metadataKey: field, kind } of positionScalarFields) {
						delete positionMetadata[field];
						delete comparison[field];
						const state = details?.fieldStates?.[field];
						if (state === "absent") delete positionScalarIntent[field];
						else if (state === "explicit_empty")
							positionScalarIntent[field] = { state };
						else if (state === "value")
							positionScalarIntent[field] = {
								state,
								value:
									kind === "list"
										? [...(values[field] ?? [])]
										: [
												kind === "integer" || kind === "number"
													? String(Number(values[field]?.[0]))
													: (values[field]?.[0] ?? ""),
											],
							};
					}
					for (const key of compositionKeys) {
						delete positionMetadata[key];
						delete comparison[key];
						const raw = current.positionMetadata?.[key];
						if (raw) positionMetadata[key] = raw;
					}
					const updated: OriginalTrack = {
						...current,
						positionMetadata,
						positionScalarIntent,
						positionArtistCredit: details?.scopedArtistCredit
							? saveScopedArtistCredit(details.scopedArtistCredit)
							: current.positionArtistCredit,
						positionContributors:
							details?.scopedContributors ?? current.positionContributors,
						positionComposition: updateComposition(
							current.positionComposition,
							values,
							details?.fieldStates,
						),
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
