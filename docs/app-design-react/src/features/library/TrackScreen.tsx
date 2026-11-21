import { useRef, useState } from "react";
import type { DeletionOperation } from "../../components/DeletionRequest";
import {
	Button,
	Disclosure,
	DisclosurePanel,
	Heading,
} from "../../components/ui";
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
import { LibraryPositionIdentityView } from "./LibraryPositionIdentityView";
import { LibraryPositionSourcesView } from "./LibraryPositionSourcesView";
import {
	PublicationAudioMeasurementsView,
	publicationAudioMeasurementTags,
} from "./PublicationAudioMeasurementsView";
import { PublicationCurrentSummaryView } from "./PublicationCurrentSummaryView";
import { TagSnapshotView } from "./TagSnapshotView";
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
	onAudioUpgrade,
	onMetadataPreview = onPreview,
	onMutate,
	onRecordingChange,
	genresLoaded = true,
	onLoadGenres,
	recordingImpact,
	onQueueDeletion,
	files = originalFiles,
	artists,
}: {
	release: OriginalRelease;
	track: OriginalTrack;
	onSource: (id: string) => void;
	onPreview: () => void;
	onRepublish?: () => void;
	onAudioUpgrade?: () => void;
	onMetadataPreview?: (ids: string[]) => void;
	onMutate?: (update: (current: OriginalTrack) => OriginalTrack) => void;
	onRecordingChange?: (
		update: (current: OriginalTrack) => OriginalTrack,
	) => void;
	genresLoaded?: boolean;
	onLoadGenres?: () => void | Promise<void>;
	recordingImpact?: string[];
	onQueueDeletion?: (operation: DeletionOperation) => void;
	files?: SourceFiles;
	artists?: CatalogArtist[];
}) {
	const { locale } = useI18n();
	const publishedTags = track.published?.tags;
	const [inspectionFileId, setInspectionFileId] = useState<string>();
	const tagComparisonRef = useRef<HTMLDetailsElement>(null);
	const sourceIds = track.sourceIds ?? (track.sourceId ? [track.sourceId] : []);
	const file = track.sourceId ? files[track.sourceId] : undefined;
	const inspectedFileId =
		inspectionFileId && sourceIds.includes(inspectionFileId)
			? inspectionFileId
			: track.sourceId;
	const inspectSource = (id: string) => {
		setInspectionFileId(id);
		if (tagComparisonRef.current) tagComparisonRef.current.open = true;
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
		<section className={catalogueStyles.object}>
			<header className={catalogueStyles.objectHeader}>
				<div>
					<h1>
						{String(track.position).padStart(2, "0")} · {track.title}
					</h1>
					<p>
						{originalArtistName(track.artistId, artists)} / {release.title} ·{" "}
						{trackMessage(locale, "trackPosition")}
					</p>
				</div>
				{artistReleaseHref(release, artists ?? catalogData.artists) ? (
					<a
						href={
							artistReleaseHref(release, artists ?? catalogData.artists) ??
							undefined
						}
					>
						{trackMessage(locale, "artistAndRelease")}
					</a>
				) : (
					<a href="#library">{trackMessage(locale, "library")}</a>
				)}
			</header>
			{(onMutate || onRecordingChange) && (
				<TrackMetadataEditors
					track={track}
					artists={artists ?? catalogData.artists}
					files={files}
					genresLoaded={genresLoaded}
					onLoadGenres={onLoadGenres}
					onMutate={onMutate ?? (() => undefined)}
					onRecordingChange={onRecordingChange}
					recordingImpact={recordingImpact}
				/>
			)}
			<div className={catalogueStyles.objectColumns}>
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
						value: inspectedFileId ?? undefined,
						onChange: inspectSource,
					}}
				/>
				<section className={catalogueStyles.panel}>
					<h2>{trackMessage(locale, "publication")}</h2>
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
					<div className={catalogueStyles.actions}>
						{track.published && (
							<Button onPress={() => onMetadataPreview([track.id])}>
								{trackMessage(locale, "updateMetadata")}
							</Button>
						)}
						<Button onPress={onPreview}>
							{trackMessage(locale, "previewPublication")}
						</Button>
					</div>
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
					<details className={catalogueStyles.trackDetails}>
						<summary>{trackMessage(locale, "technicalDetails")}</summary>
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
							<dt>{trackMessage(locale, "publicationFormat")}</dt>
							<dd>
								{track.published
									? `${track.published.container} · ${track.published.codec}`
									: trackMessage(locale, "noPublication")}
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
						{track.published && (
							<Disclosure className={catalogueStyles.trackPublication}>
								<Heading>
									<Button slot="trigger">
										{trackMessage(locale, "currentPublication")}
									</Button>
								</Heading>
								<DisclosurePanel>
									<PublicationCurrentSummaryView
										locale={locale}
										current={{
											details: {
												container: track.published.container,
												codec: track.published.codec,
												path: track.published.path,
												observed: {
													kind: "snapshot",
													value: track.published.readAt,
												},
											},
										}}
									/>
									<TagSnapshotView
										title={trackMessage(locale, "tagSnapshot")}
										rows={Object.entries(track.published.tags)
											.filter(
												([key]) => !publicationAudioMeasurementTags.has(key),
											)
											.map(([key, values]) => ({ key, values }))}
									/>
									{publishedTags && (
										<PublicationAudioMeasurementsView
											locale={locale}
											tags={publishedTags}
										/>
									)}
								</DisclosurePanel>
							</Disclosure>
						)}
						<p>{trackMessage(locale, "localChangesHelp")}</p>
					</details>
				</section>
			</div>
			<details
				ref={tagComparisonRef}
				className="grid min-w-0 gap-3 rounded-[var(--control-radius)] border border-[var(--line)] bg-[var(--surface)] p-3"
			>
				<summary className="cursor-pointer font-semibold">
					{trackMessage(locale, "compareFileAndPublication")}
				</summary>
				<TagsComparison
					releases={[release]}
					artists={artists}
					files={files}
					scope="track"
					initialTrackId={track.id}
					initialFileId={inspectedFileId}
					onPreview={onMetadataPreview}
					fixedContext
				/>
			</details>
		</section>
	);
}
