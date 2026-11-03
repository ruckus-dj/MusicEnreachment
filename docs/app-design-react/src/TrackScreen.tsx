import { useRef, useState } from "react";
import {
	Button,
	Disclosure,
	DisclosurePanel,
	Heading,
	Radio,
	RadioGroup,
} from "./ui";
import { TrackActions } from "./TrackActions";
import { TagsComparison } from "./TagsComparison";
import { artistReleaseHref } from "./artistReleaseRoute";
import type { DeletionOperation } from "./DeletionRequest";
import { catalogueStyles } from "./catalogueStyles";
import { TrackMetadataEditors } from "./metadata/TrackMetadataEditors";
import {
	catalogData,
	originalArtistName,
	originalFiles,
	originalProviders,
	type OriginalRelease,
	type OriginalTrack,
	type SourceFiles,
	type CatalogArtist,
} from "./catalogData";
import {
	MetadataDiff,
	publicationMetadataDiff,
} from "./publicationMetadataDiff";
import { useI18n } from "./i18n/I18nProvider";
import { formatTrackMessage, trackMessage } from "./trackMessages";

const duration = (seconds: number) =>
	`${Math.floor(seconds / 60)}:${String(Math.round(seconds % 60)).padStart(2, "0")}`;
export function TrackScreen({
	release,
	track,
	onSource,
	onPreview,
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
				<section className={catalogueStyles.panel}>
					<h2>{trackMessage(locale, "audioSources")}</h2>
					<p className="mb-2 text-xs text-[var(--muted)]">
						{trackMessage(locale, "publicationSourceHelp")}
					</p>
					{sourceIds.length ? (
						<RadioGroup
							aria-label={trackMessage(locale, "publicationSource")}
							value={track.sourceId ?? ""}
							onChange={onSource}
						>
							{sourceIds.map((id) => {
								const source = files[id];
								if (!source) return null;
								const selectedForPublication = track.sourceId === id;
								return (
									<div
										key={id}
										className="grid min-w-0 grid-cols-[minmax(0,1fr)_auto] items-center gap-2 border-b border-[var(--line)] py-2 last:border-0"
									>
										<Radio value={id} className={catalogueStyles.source}>
											<span
												aria-hidden="true"
												className={catalogueStyles.radioDot}
											/>
											<span className="min-w-0">
												<strong className="block truncate">
													{source.name}
												</strong>
												<span className={catalogueStyles.secondaryInline}>
													{source.codec} · {duration(source.duration)} ·{" "}
													{source.available
														? trackMessage(locale, "available")
														: trackMessage(locale, "unavailable")}
												</span>
												<span className="block text-[11px] text-[var(--muted)]">
													{selectedForPublication
														? trackMessage(locale, "selectedForPublication")
														: trackMessage(locale, "chooseForPublication")}
												</span>
											</span>
										</Radio>
										<Button
											aria-pressed={inspectedFileId === id}
											onPress={() => inspectSource(id)}
										>
											{inspectedFileId === id
												? trackMessage(locale, "inspectingTags")
												: trackMessage(locale, "inspectTags")}
										</Button>
									</div>
								);
							})}
						</RadioGroup>
					) : (
						<p>{trackMessage(locale, "noLinkedSources")}</p>
					)}
				</section>
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
							artists={artists}
							onQueueDeletion={onQueueDeletion}
						/>
					)}
					<details className={catalogueStyles.trackDetails}>
						<summary>{trackMessage(locale, "technicalDetails")}</summary>
						<dl className={catalogueStyles.facts}>
							<dt>{trackMessage(locale, "composition")}</dt>
							<dd>
								{track.recordingTitle ||
									providerTrack?.title ||
									(track.recordingId
										? trackMessage(locale, "linkedRecording")
										: trackMessage(locale, "unlinkedRecording"))}
							</dd>
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
									<dl className={catalogueStyles.facts}>
										<dt>{trackMessage(locale, "path")}</dt>
										<dd>{track.published.path}</dd>
										<dt>{trackMessage(locale, "tagSnapshot")}</dt>
										<dd>
											{Object.entries(track.published.tags)
												.map(([key, values]) => `${key}: ${values.join(" · ")}`)
												.join("; ")}
										</dd>
										<dt>{trackMessage(locale, "readAt")}</dt>
										<dd>{track.published.readAt}</dd>
									</dl>
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
