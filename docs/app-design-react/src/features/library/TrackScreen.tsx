import { type ReactNode, useState } from "react";
import type { DeletionOperation } from "../../components/DeletionRequest";
import { Button } from "../../components/ui";
import { useI18n } from "../../i18n/I18nProvider";
import { TrackMetadataEditors } from "../../metadata/TrackMetadataEditors";
import {
	MetadataDiff,
	publicationMetadataDiff,
} from "../publication/publicationMetadataDiff";
import { artistReleaseHref } from "./artistReleaseRoute";
import {
	type CatalogArtist,
	catalogData,
	type OriginalRelease,
	type OriginalTrack,
	originalArtistName,
	originalFiles,
	originalProviders,
	type SourceFiles,
	trackNumberLabel,
} from "./catalogData";
import { catalogueStyles } from "./catalogueStyles";
import {
	LibraryPositionHeadingView,
	libraryPositionPageClassName,
	libraryPositionTitleId,
} from "./LibraryPositionHeadingView";
import { LibraryPositionIdentityView } from "./LibraryPositionIdentityView";
import { LibraryPositionSourcesView } from "./LibraryPositionSourcesView";
import { LibraryPositionTagsDisclosure } from "./LibraryPositionTagsDisclosure";
import { LibraryPositionTechnicalDetailsView } from "./LibraryPositionTechnicalDetailsView";
import {
	LibraryPositionPreviewButton,
	LibraryPositionWorkspace,
} from "./LibraryPositionWorkspace";
import { PublicationCurrentDetailsView } from "./PublicationCurrentSummaryView";
import { PublicationTagSnapshotView } from "./PublicationTagSnapshotView";
import { TagsComparison } from "./TagsComparison";
import { TrackActions } from "./TrackActions";
import { formatTrackMessage, trackMessage } from "./trackMessages";

const duration = (seconds: number) =>
	`${Math.floor(seconds / 60)}:${String(Math.round(seconds % 60)).padStart(2, "0")}`;

export function TrackScreen({
	release,
	track,
	onSource,
	onPreview,
	onRepublish,
	publicationPreview,
	onAudioUpgrade,
	onMetadataPreview = onPreview,
	onMutate,
	onRecordingChange,
	genresLoaded = true,
	onLoadGenres,
	onQueueDeletion,
	files = originalFiles,
	artists,
	providers,
}: {
	release: OriginalRelease;
	track: OriginalTrack;
	onSource: (id: string) => void;
	onPreview: () => void;
	onRepublish?: () => void;
	publicationPreview?: ReactNode;
	onAudioUpgrade?: () => void;
	onMetadataPreview?: (ids: string[]) => void;
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
}) {
	const { locale } = useI18n();
	const [inspectionFileId, setInspectionFileId] = useState<string>();
	const [tagComparisonOpen, setTagComparisonOpen] = useState(false);
	const sourceIds = track.sourceIds ?? (track.sourceId ? [track.sourceId] : []);
	const file = track.sourceId ? files[track.sourceId] : undefined;
	const inspectedFileId =
		inspectionFileId && sourceIds.includes(inspectionFileId)
			? inspectionFileId
			: track.sourceId;
	const inspectSource = (id: string) => {
		setInspectionFileId(id);
		setTagComparisonOpen(true);
	};
	const provider = originalProviders.find((item) =>
		item.tracks.some((candidate) => candidate.id === track.providerTrackId),
	);
	const providerTrack = provider?.tracks.find(
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
	return (
		<section
			aria-labelledby={libraryPositionTitleId}
			className={libraryPositionPageClassName}
		>
			<LibraryPositionHeadingView
				locale={locale}
				displayNumber={
					track.displayedNumber?.trim()
						? track.displayedNumber
						: String(track.position).padStart(2, "0")
				}
				title={track.title}
				artist={originalArtistName(track.artistId, artists)}
				releaseTitle={release.title}
				releaseHref={
					artistReleaseHref(release, artists ?? catalogData.artists) ??
					undefined
				}
				libraryHref="#library"
			/>
			<LibraryPositionWorkspace
				locale={locale}
				editors={
					(onMutate || onRecordingChange) && (
						<TrackMetadataEditors
							track={track}
							artists={artists ?? catalogData.artists}
							files={files}
							genresLoaded={genresLoaded}
							onLoadGenres={onLoadGenres}
							onMutate={onMutate ?? (() => undefined)}
							onRecordingChange={onRecordingChange}
						/>
					)
				}
				sources={
					<LibraryPositionSourcesView
						locale={locale}
						position={trackNumberLabel(release, track)}
						sources={sourceIds.flatMap((id) => {
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
						})}
						choice={{ value: track.sourceId ?? "", onChange: onSource }}
						inspection={{
							value: tagComparisonOpen
								? (inspectedFileId ?? undefined)
								: undefined,
							onChange: inspectSource,
						}}
					/>
				}
				publication={
					<>
						<p aria-label={trackMessage(locale, "metadataState")}>
							<strong>
								{track.published
									? changed
										? formatTrackMessage(locale, "metadataChanged", {
												count: changes.reduce(
													(count, group) => count + group.changes.length,
													0,
												),
											})
										: trackMessage(locale, "metadataMatches")
									: trackMessage(locale, "noPublication")}
							</strong>
						</p>
						{track.published && (
							<details>
								<summary>
									{trackMessage(locale, "compareEffectiveMetadata")}
								</summary>
								<MetadataDiff groups={changes} />
							</details>
						)}
						{outputStatus && (
							<p>
								{trackMessage(locale, "publicationFileStatus")}: {outputStatus}.
							</p>
						)}
					</>
				}
				actions={
					<>
						<div className={catalogueStyles.actions}>
							{track.published && (
								<Button onPress={() => onMetadataPreview([track.id])}>
									{trackMessage(locale, "updateMetadata")}
								</Button>
							)}
							<LibraryPositionPreviewButton
								locale={locale}
								onPreview={onPreview}
							/>
						</div>
						{publicationPreview}
						{onMutate && (
							<TrackActions
								release={release}
								track={track}
								onMutate={onMutate}
								files={files}
								onQueueDeletion={onQueueDeletion}
								onRepublish={onRepublish ?? onPreview}
								onAudioUpgrade={onAudioUpgrade}
							/>
						)}
					</>
				}
				technicalDetails={
					<LibraryPositionTechnicalDetailsView
						locale={locale}
						current={
							track.published && (
								<>
									<PublicationCurrentDetailsView
										locale={locale}
										details={{
											container: track.published.container,
											codec: track.published.codec,
											path: track.published.path,
											observed: {
												kind: "snapshot",
												value: track.published.readAt,
											},
										}}
									/>
									<PublicationTagSnapshotView
										locale={locale}
										tags={track.published.tags}
									/>
								</>
							)
						}
						afterCurrent={<p>{trackMessage(locale, "localChangesHelp")}</p>}
					>
						<LibraryPositionIdentityView
							locale={locale}
							position={trackNumberLabel(release, track)}
							title={track.title}
							durationMs={
								track.duration == null
									? null
									: Math.round(track.duration * 1000)
							}
							recordingTitle={
								track.recordingTitle ||
								providerTrack?.title ||
								trackMessage(
									locale,
									track.recordingId ? "linkedRecording" : "unlinkedRecording",
								)
							}
						/>
						<dl className={catalogueStyles.facts}>
							<dt>{trackMessage(locale, "fileState")}</dt>
							<dd>
								{outputStatus ??
									(track.observedFile
										? trackMessage(locale, "fileChangedOutside")
										: trackMessage(locale, "noFileChangesMarked"))}
							</dd>
							<dt>{trackMessage(locale, "sourceFormat")}</dt>
							<dd>
								{file
									? `${file.codec} · ${duration(file.duration)}`
									: trackMessage(locale, "notSelected")}
							</dd>
						</dl>
						<dl className={catalogueStyles.facts}>
							<dt>{trackMessage(locale, "path")}</dt>
							<dd>{file?.path ?? trackMessage(locale, "notSelected")}</dd>
							<dt>{trackMessage(locale, "composition")}</dt>
							<dd>{track.recordingId ?? trackMessage(locale, "noLink")}</dd>
							<dt>{trackMessage(locale, "providerHint")}</dt>
							<dd>
								{track.providerTitle ||
									providerTrack?.title ||
									trackMessage(locale, "noProviderHint")}
							</dd>
							{track.observedFile?.mtime && (
								<>
									<dt>{trackMessage(locale, "externalChangeTime")}</dt>
									<dd>{track.observedFile.mtime}</dd>
								</>
							)}
						</dl>
					</LibraryPositionTechnicalDetailsView>
				}
			/>
			<LibraryPositionTagsDisclosure
				locale={locale}
				open={tagComparisonOpen}
				onOpenChange={setTagComparisonOpen}
			>
				<TagsComparison
					releases={[release]}
					artists={artists}
					files={files}
					providers={providers}
					scope="track"
					initialTrackId={track.id}
					initialFileId={inspectedFileId}
					onPreview={onMetadataPreview}
					fixedContext
				/>
			</LibraryPositionTagsDisclosure>
		</section>
	);
}
