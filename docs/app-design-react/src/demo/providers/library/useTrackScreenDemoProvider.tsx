import { type ReactNode, useState } from "react";
import { useI18n } from "../demoPreferences";
import type { LibraryReleasePositionModel } from "../../../ui/features/library/LibraryReleaseWorkspaceView";
import {
	formatTrackMessage,
	trackMessage,
} from "../../../ui/features/library/trackMessages";
import { PublicationMetadataChanges } from "../../../ui/features/publication/PublicationInspector";
import type {
	DemoPositionPublicationBinderProps,
	DemoPositionPublicationSource,
} from "../publication/ContextPublicationPreview";
import { publicationMetadataDiff } from "../publication/publicationMetadataDiff";
import { releaseHref } from "./artistReleaseRoute";
import {
	type CatalogArtist,
	catalogData,
	effectiveRecordingTitle,
	type OriginalRelease,
	type OriginalTrack,
	originalArtistName,
	originalFiles,
	originalProviders,
	recordingIdentity,
	type SourceFiles,
	trackNumberLabel,
} from "./catalogData";
import { type DeletionOperation, DeletionRequest } from "./DeletionRequest";
import { demoPrimaryArtist } from "./libraryPrimaryArtistDemo";
import { libraryRootHref } from "./libraryRoute";
import { RecordingGenreEditor } from "./metadata/RecordingGenreEditor";
import { positionCreditValues } from "./metadata/scopedArtistCredits";
import { TrackMetadataEditors } from "./metadata/TrackMetadataEditors";
import { TagsComparison } from "./TagsComparison";

export type TrackScreenProps = {
	release: OriginalRelease;
	track: OriginalTrack;
	onPreview?: () => void;
	publicationPreviewNotice?: string;
	contextualPublication?: DemoPositionPublicationSource;
	onMutate?: (update: (current: OriginalTrack) => OriginalTrack) => void;
	onRecordingChange?: (
		update: (current: OriginalTrack) => OriginalTrack,
	) => void;
	genresLoaded?: boolean;
	onLoadGenres?: () => void | Promise<void>;
	onQueueDeletion?: (operation: DeletionOperation) => void;
	files?: SourceFiles;
	artists?: CatalogArtist[];
	providers?: OriginalRelease[];
};

const duration = (seconds: number) =>
	`${Math.floor(seconds / 60)}:${String(Math.round(seconds % 60)).padStart(2, "0")}`;

/**
 * Track screen demo provider: owns source inspection state, derived publication
 * status and fixture/provider lookup, then hands render-ready props to the pure
 * view. Nested domain actors are supplied as narrow composition slots.
 *
 * The host may call it with `null` when no position is selected. Its state
 * hooks still run, and a change of position identity (or leaving to the
 * edition) resets the inspection and tag-comparison session.
 */
export function useTrackScreenDemoProvider(
	props?: TrackScreenProps | null,
): LibraryReleasePositionModel<DemoPositionPublicationBinderProps> | null {
	const { locale } = useI18n();
	const entryIdentity = props ? `${props.release.id}:${props.track.id}` : null;
	const [inspectionFileId, setInspectionFileId] = useState<string>();
	const [tagComparisonOpen, setTagComparisonOpen] = useState(false);
	const [lastEntryIdentity, setLastEntryIdentity] = useState(entryIdentity);
	if (lastEntryIdentity !== entryIdentity) {
		setLastEntryIdentity(entryIdentity);
		setInspectionFileId(undefined);
		setTagComparisonOpen(false);
	}
	if (!props) return null;
	const {
		release,
		track,
		onPreview,
		publicationPreviewNotice,
		contextualPublication,
		onMutate,
		onRecordingChange,
		genresLoaded = true,
		onLoadGenres,
		onQueueDeletion,
		files = originalFiles,
		artists,
		providers,
	} = props;
	const sourceIds = track.sourceIds ?? (track.sourceId ? [track.sourceId] : []);
	const recordingId = recordingIdentity(track);
	const localArtists = artists ?? catalogData.artists;
	const primaryArtist = onMutate
		? demoPrimaryArtist(
				positionCreditValues(track, localArtists),
				localArtists,
				track.primaryArtistId,
				(id) => onMutate((current) => ({ ...current, primaryArtistId: id })),
			).control
		: undefined;
	const file = track.sourceId ? files[track.sourceId] : undefined;
	const inspectedFileId =
		inspectionFileId && sourceIds.includes(inspectionFileId)
			? inspectionFileId
			: track.sourceId;
	const inspectSource = (id: string) => {
		setInspectionFileId(id);
		setTagComparisonOpen(true);
	};
	const providerRelease = originalProviders.find((item) =>
		item.tracks.some((candidate) => candidate.id === track.providerTrackId),
	);
	const providerTrack = providerRelease?.tracks.find(
		(candidate) => candidate.id === track.providerTrackId,
	);
	const changes = publicationMetadataDiff(release, track, artists);
	const changed = changes.length > 0;
	const outputStatus = !track.published
		? null
		: track.observedFile?.state === "changed" ||
				track.observedFile?.state === "drift"
			? trackMessage(locale, "fileChanged")
			: track.observedFile?.state === "missing" &&
					track.observedFile.rootAvailable === true
				? trackMessage(locale, "fileMissing")
				: track.observedFile?.state === "missing" ||
						track.observedFile?.state === "output-unavailable"
					? trackMessage(locale, "publicationRootUnavailable")
					: track.observedFile?.state === "available" ||
							track.observedFile?.state === "unchanged"
						? trackMessage(locale, "noDeviations")
						: trackMessage(locale, "fileStateNotChecked");
	const metadataStatus = track.published
		? changed
			? formatTrackMessage(locale, "metadataChanged", {
					count: changes.reduce(
						(count, group) => count + group.changes.length,
						0,
					),
				})
			: trackMessage(locale, "metadataMatches")
		: trackMessage(locale, "noPublication");
	const sourceItems = sourceIds.flatMap((id) => {
		const source = files[id];
		if (!source) return [];
		const availability = trackMessage(
			locale,
			source.available ? "available" : "unavailable",
		);
		return [
			{
				id,
				label: source.name,
				summary: `${source.codec} · ${duration(source.duration)} · ${availability}`,
				selected: track.sourceId === id,
			},
		];
	});
	return {
		heading: {
			displayNumber: track.displayedNumber?.trim()
				? track.displayedNumber
				: String(track.position).padStart(2, "0"),
			title: track.title,
			artist: originalArtistName(track.artistId, artists),
			releaseTitle: release.title,
			releaseHref: releaseHref(release.id),
			libraryHref: libraryRootHref(),
		},
		editors:
			onMutate || onRecordingChange
				? {
						primaryArtist,
						positionNumber: trackNumberLabel(release, track),
						genre:
							recordingId && onRecordingChange
								? {
										bind: (children: ReactNode) => (
											<RecordingGenreEditor
												key={recordingId}
												track={track}
												genresLoaded={genresLoaded}
												onLoadGenres={onLoadGenres}
												onRecordingChange={onRecordingChange}
											>
												{children}
											</RecordingGenreEditor>
										),
									}
								: undefined,
						metadata: {
							bind: (children: ReactNode) => (
								<TrackMetadataEditors
									track={track}
									artists={artists ?? catalogData.artists}
									onMutate={onMutate ?? (() => undefined)}
									onRecordingChange={onRecordingChange}
								>
									{children}
								</TrackMetadataEditors>
							),
						},
					}
				: undefined,
		sources: {
			position: trackNumberLabel(release, track),
			items: sourceItems,
			inspection: {
				value: tagComparisonOpen ? (inspectedFileId ?? undefined) : undefined,
				onChange: inspectSource,
			},
		},
		publication: {
			published: Boolean(track.published),
			status: metadataStatus,
			diff: track.published ? (
				<PublicationMetadataChanges locale={locale} groups={changes} compact />
			) : undefined,
			outputStatus: outputStatus ?? undefined,
			onPreview,
			previewNotice: publicationPreviewNotice,
		},
		contextualPublication,
		deletion:
			track.published && onQueueDeletion ? (
				<DeletionRequest
					release={release}
					track={track}
					onQueue={onQueueDeletion}
				/>
			) : undefined,
		technical: {
			position: trackNumberLabel(release, track),
			title: track.title,
			durationMs:
				track.duration == null ? null : Math.round(track.duration * 1000),
			recordingTitle: track.recordingScalarIntent?.title
				? effectiveRecordingTitle(track)
				: track.recordingTitle ||
					providerTrack?.title ||
					trackMessage(
						locale,
						track.recordingId ? "linkedRecording" : "unlinkedRecording",
					),
			fileState:
				outputStatus ??
				(track.observedFile
					? trackMessage(locale, "fileChangedOutside")
					: trackMessage(locale, "noFileChangesMarked")),
			sourceFormat: file
				? `${file.codec} · ${duration(file.duration)}`
				: undefined,
			path: file?.path,
			composition: track.recordingId ?? undefined,
			providerHint:
				track.providerTitle ||
				providerTrack?.title ||
				trackMessage(locale, "noProviderHint"),
			externalChangeTime: track.observedFile?.mtime,
			help: trackMessage(locale, "localChangesHelp"),
			current: track.published
				? {
						details: {
							container: track.published.container,
							codec: track.published.codec,
							path: track.published.path,
							observed: {
								kind: "snapshot",
								value: track.published.readAt,
							},
						},
						tags: track.published.tags,
					}
				: undefined,
		},
		tags: {
			open: tagComparisonOpen,
			onOpenChange: setTagComparisonOpen,
			comparison: (
				<TagsComparison
					artists={artists}
					release={release}
					track={track}
					files={files}
					providers={providers}
					fileId={inspectedFileId}
				/>
			),
		},
	};
}
