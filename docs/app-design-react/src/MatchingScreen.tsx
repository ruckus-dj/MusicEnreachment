import { useLayoutEffect, useRef, useState } from "react";
import type { FormEvent } from "react";
import {
	Button,
	Checkbox,
	Dialog,
	Heading,
	Input,
	Modal,
	Radio,
	RadioGroup,
	Tab,
	TabList,
	TabPanel,
	Tabs,
	TextField,
} from "./ui";
import {
	publicationMetadataIssueMessages,
	publicationSortTagCollisions,
	type CatalogArtist,
	type OriginalTrack,
} from "./catalogData";
import { MatchingSearchPicker } from "./MatchingSearchPicker";
import {
	MatchingDragHandle,
	MatchingDropZone,
	MatchingFileChip,
} from "./MatchingDnD";
import { metadataSnapshotDiff } from "./publicationMetadataDiff";
import { PublicationInspector } from "./PublicationInspector";
import type { PublicationInspection } from "./PublicationInspector";
import { publicationMessage } from "./publicationMessages";
import { useI18n } from "./i18n";
import { matchingAction, matchingMessage } from "./matchingMessages";
import { formatUiNotice, type UiNotice } from "./uiNotice";
import {
	applyPlan,
	artistName,
	assignFile,
	createIncomingState,
	findProviderTrack,
	findTarget,
	getDraft,
	incomingFixtures,
	normalized,
	planRows,
	providerReleases,
	providerTags,
	selectedSourceId,
	sourceIds,
	tagFieldGroups,
	unassignFile,
	usableFile,
	type Assignment,
	type IncomingFiles,
	type IncomingReleases,
	type IncomingPlan,
	type IncomingState,
	type SourceFile,
} from "./incomingModel";
import {
	comparisonTableWrap,
	dialogFooter,
	matchingAssignmentRegion,
	matchingLinkedSources,
	matchingSourceRow,
	matchingTable,
	matchingTableHeading,
	matchingTableWrap,
	surfacePanel,
	table,
} from "./matchingStyles";

export type MatchingScreenProps = {
	destination?: import("./publicationOutput").PublicationDestination;
	groupId?: string;
	files?: IncomingFiles;
	releases?: IncomingReleases;
	providers?: IncomingReleases;
	artists?: CatalogArtist[];
	state?: IncomingState;
	onState?: (state: IncomingState) => void;
	onReleases?: (releases: IncomingReleases) => void;
};

const duration = (seconds: unknown) =>
	Number.isFinite(Number(seconds))
		? `${Math.floor(Number(seconds) / 60)}:${String(Math.round(Number(seconds) % 60)).padStart(2, "0")}`
		: "—";
const tagValue = (tags: Record<string, string[]> | undefined, key: string) =>
	tags?.[key]?.length ? tags[key].join("; ") : "—";
export function MatchingScreen({
	destination,
	groupId,
	files: givenFiles,
	releases: givenReleases,
	providers: givenProviders,
	artists: givenArtists,
	state: givenState,
	onState,
	onReleases,
}: MatchingScreenProps) {
	const [localState, setLocalState] = useState(createIncomingState);
	const [confirming, setConfirming] = useState(false);
	const [previewOpenSequence, setPreviewOpenSequence] = useState(0);
	const previewHeadingRef = useRef<HTMLHeadingElement>(null);
	const [localReleases, setLocalReleases] = useState<IncomingReleases>(() =>
		structuredClone(incomingFixtures.releases),
	);
	const [recordingQuery, setRecordingQuery] = useState("");
	const { locale } = useI18n();
	useLayoutEffect(() => {
		if (!previewOpenSequence) return;
		const heading = previewHeadingRef.current;
		if (!heading) return;
		heading.focus();
		const reduceMotion = window.matchMedia?.(
			"(prefers-reduced-motion: reduce)",
		).matches;
		heading.scrollIntoView?.({
			behavior: reduceMotion ? "auto" : "smooth",
			block: "start",
		});
	}, [previewOpenSequence]);
	const state = givenState ?? localState;
	const files = givenFiles ?? incomingFixtures.files;
	const releases = givenReleases ?? localReleases;
	const providers = givenProviders ?? providerReleases;
	const artists = givenArtists ?? incomingFixtures.artists;
	const activeGroupId = groupId ?? state.groupId;
	const group = state.groups.find((item) => item.id === activeGroupId);
	if (!group)
		return (
			<main className="demo-shell mx-auto max-w-[1100px] px-6 py-8">
				<h1>{matchingMessage(locale, "groupNotFound")}</h1>
				<a href="#review">{matchingMessage(locale, "backToIncoming")}</a>
			</main>
		);
	state.groupId = group.id;
	const target = findTarget(group, releases, providers);
	if (!target)
		return (
			<main className="demo-shell mx-auto max-w-[1100px] px-6 py-8">
				<h1>{matchingMessage(locale, "targetNotFound")}</h1>
				<a href="#review">{matchingMessage(locale, "backToIncoming")}</a>
			</main>
		);
	const tracks = target.tracks;
	const assignments = getDraft(group, files, providers, releases);
	const commitState = () => {
		const next = structuredClone(state);
		givenState ? onState?.(next) : setLocalState(next);
	};
	const setNotice = (notice: UiNotice) => {
		state.notice = "";
		state.noticeDescriptor = null;
		state.uiNotice = notice;
	};
	const setReleases = (next: IncomingReleases) =>
		givenReleases ? onReleases?.(next) : setLocalReleases(next);
	const clearPreview = () => {
		state.plan = null;
		state.confirmDowngrade = false;
	};
	const selectedTrackId = tracks.some(
		(track) => track.id === state.selectedTarget,
	)
		? state.selectedTarget
		: String(tracks[0]?.id ?? "");
	const inspected =
		tracks.find((track) => track.id === selectedTrackId) ?? tracks[0];
	const assignment = assignments[String(inspected?.id)];
	const selectedFile = files[selectedSourceId(assignment) ?? ""];
	const providerTrack = findProviderTrack(
		assignment?.providerTrackId ?? null,
		providers,
	);
	const existingRelease =
		group.mode === "local"
			? releases.find((release) => release.id === group.releaseId)
			: releases.find(
					(release) =>
						release.providerId === target.id ||
						release.providerId === target.providerId,
				);
	const localTrack = existingRelease?.tracks.find(
		(track) => track.position === inspected?.position,
	);
	const used = new Set(
		Object.values(assignments).flatMap((item) => item.sourceIds),
	);
	const pool = group.fileIds.filter(
		(id) => usableFile(files[id]) && !used.has(id),
	);
	const allProviderTracks = providers.flatMap((release) =>
		release.tracks.map((track) => ({ release, track })),
	);
	const matchingRecordings = (state.recordingResults ?? []).filter(
		({ release, track }) => {
			const query = normalized(recordingQuery || state.recordingQuery);
			return query
				.split(" ")
				.filter(Boolean)
				.every((term) =>
					normalized(
						`${track.title} ${artistName(String(track.artistId), artists)} ${release.title}`,
					).includes(term),
				);
		},
	);
	const mutateAssignment = (
		callback: (draft: Record<string, Assignment>) => void,
	) => {
		callback(assignments);
		clearPreview();
		commitState();
	};
	const addSource = (fileId: string, trackId: string) => {
		if (!group.fileIds.includes(fileId) || !usableFile(files[fileId])) {
			setNotice({ namespace: "matching", id: "unavailable" });
			commitState();
			return false;
		}
		if (sourceIds(assignments[trackId]).includes(fileId)) {
			setNotice({
				namespace: "matching",
				id: "alreadyAssigned",
				params: { file: files[fileId].name },
			});
			commitState();
			return false;
		}
		const didAssign = assignFile(
			group,
			trackId,
			fileId,
			files,
			releases,
			providers,
		);
		state.selectedFile = null;
		state.selectedTarget = trackId;
		setNotice({
			namespace: "matching",
			id: didAssign ? "assigned" : "addFailed",
			params: { file: files[fileId]?.name ?? fileId },
		});
		clearPreview();
		commitState();
		return didAssign;
	};
	const setCandidate = (id: string) => {
		group.mode = "provider";
		group.candidateId = id;
		state.selectedFile = null;
		state.selectedTarget = null;
		state.recordingTarget = null;
		state.recordingResults = null;
		setNotice({ namespace: "matching", id: "candidateChanged" });
		clearPreview();
		commitState();
	};
	const sourceSelection = (trackId: string, fileId: string) => {
		const item = assignments[trackId];
		if (!item) return;
		item.selectedSourceId = fileId;
		const selected = files[fileId];
		item.method = item.sourceMeta[fileId]?.method ?? item.method;
		item.confidence = selected?.confidence ?? null;
		clearPreview();
		commitState();
	};
	const openPreview = () => {
		state.plan = {
			mode: group.mode,
			groupId: group.id,
			target: structuredClone(target),
			localReleaseId: (existingRelease?.id as string | undefined) ?? null,
			assignments: structuredClone(assignments),
			inventoryVersion: state.inventoryVersion,
		} satisfies IncomingPlan;
		state.confirmDowngrade = false;
		setPreviewOpenSequence((sequence) => sequence + 1);
		commitState();
	};
	const preview = state.plan?.groupId === group.id ? state.plan : null;
	const previewRows = preview
		? planRows(preview, files, releases, providers, artists)
		: [];
	const metadataIssues = (row: (typeof previewRows)[number]) => {
		if (!preview) return [];
		const local = releases.find(
			(release) => release.id === preview.localReleaseId,
		);
		return publicationMetadataIssueMessages(
			preview.mode === "local" ? (local ?? preview.target) : preview.target,
			row.track ?? row.targetTrack,
			locale,
			selectedSourceId(row.assignment),
		);
	};
	const effectiveMetadata = (row: (typeof previewRows)[number]) =>
		metadataSnapshotDiff(
			row.previous?.tags ?? {},
			row.nextTags,
			{
				coverHash: row.previous?.coverHash ?? null,
				lyrics: row.previous?.lyrics ?? null,
			},
			{
				coverHash: (row.track ?? row.targetTrack).coverHash ?? null,
				lyrics: (row.track ?? row.targetTrack).lyrics ?? null,
			},
		);
	const inspectionItems: PublicationInspection[] = previewRows.map((row) => {
		const issues = metadataIssues(row);
		const metadataRelease =
			preview?.mode === "local"
				? (releases.find((release) => release.id === preview.localReleaseId) ??
					preview.target)
				: (preview?.target ?? target);
		const sortCollisions = publicationSortTagCollisions(
			metadataRelease,
			preview?.mode === "local"
				? (row.track ?? row.targetTrack)
				: row.targetTrack,
			artists,
		);
		const sortCollisionNotices = [
			...(sortCollisions.recording
				? [publicationMessage(locale, "recordingSortCollision")]
				: []),
			...(sortCollisions.release
				? [publicationMessage(locale, "releaseSortCollision")]
				: []),
		];
		const invalidMetadata = Boolean(
			row.assignment?.included && row.file && issues.length,
		);
		const included = Boolean(row.assignment?.included && row.file);
		const baseReason = invalidMetadata
			? matchingMessage(locale, "invalidMetadata", {
					issues: issues.join("; "),
				})
			: !row.assignment?.included
				? matchingMessage(locale, "notIncluded")
				: !row.file
					? matchingMessage(locale, "noFile")
					: "";
		const reason = [baseReason, ...sortCollisionNotices]
			.filter(Boolean)
			.join(" · ");
		return {
			id: String(row.targetTrack.id),
			position: String(row.targetTrack.position),
			title: String(row.targetTrack.title),
			context: row.provider?.release.title,
			action:
				(invalidMetadata
					? matchingMessage(locale, "unprocessed")
					: matchingAction(locale, row.action)) +
				(row.downgrade ? ` · ${matchingMessage(locale, "downgrade")}` : ""),
			reason,
			source: row.file?.name ?? matchingMessage(locale, "noFile"),
			groups: effectiveMetadata(row),
			included,
			includeDisabled: !row.file,
			includeLabel: matchingMessage(locale, "ariaIncludeTrack", {
				track: String(row.targetTrack.title),
			}),
			onInclude: (checked) => {
				if (row.assignment) row.assignment.included = checked;
				commitState();
			},
		};
	});
	const activeRows = previewRows.filter(
		(row) => row.assignment?.included && row.file,
	);
	const changedRows = activeRows.filter(
		(row) => row.action !== "Без изменений" && !metadataIssues(row).length,
	);
	const linkedRows = previewRows.filter((row) => {
		if (row.assignment?.included && row.file && metadataIssues(row).length)
			return false;
		const persisted = new Set(
			(row.track?.sourceIds as string[] | undefined) ??
				(row.track?.sourceId ? [String(row.track.sourceId)] : []),
		);
		return sourceIds(row.assignment).some(
			(id) => usableFile(files[id]) && !persisted.has(id),
		);
	});
	const stale = Boolean(
		preview && preview.inventoryVersion !== state.inventoryVersion,
	);
	const hasDowngrade = changedRows.some((row) => row.downgrade);
	const confirmPublish = () => {
		if (!preview || stale || (hasDowngrade && !state.confirmDowngrade)) return;
		setConfirming(false);
		if (preview.inventoryVersion !== state.inventoryVersion) {
			setNotice({ namespace: "matching", id: "candidateChangeNotice" });
			commitState();
			return;
		}
		const nextReleases = structuredClone(releases);
		const changed = applyPlan(
			preview,
			files,
			nextReleases,
			providers,
			false,
			artists,
			destination,
		);
		setNotice(
			changed
				? {
						namespace: "matching",
						id: "planPublished",
						params: { count: changed, linkedCount: linkedRows.length },
					}
				: {
						namespace: "matching",
						id: "linksSavedFilesUnchanged",
						params: { count: linkedRows.length },
					},
		);
		setReleases(nextReleases);
		state.plan = null;
		state.confirmDowngrade = false;
		commitState();
	};
	const tagFields = tagFieldGroups()[state.tagScope];
	const sourceTags = selectedFile?.tags ?? {};
	const remoteTags = providerTrack
		? providerTags(providerTrack.release, providerTrack.track, artists)
		: {};
	const localTags =
		existingRelease && localTrack
			? providerTags(existingRelease, localTrack, artists)
			: group.mode === "local"
				? providerTags(target, inspected, artists)
				: {};
	const publication = localTrack?.published ?? undefined;
	const publicationTags = publication?.tags ?? {};
	const tagRows = tagFields.filter(
		(key) =>
			!state.differencesOnly ||
			new Set(
				[sourceTags, remoteTags, localTags, publicationTags].map((tags) =>
					JSON.stringify(tags[key] ?? []),
				),
			).size > 1,
	);
	const bindRecording = (recordingId: string) => {
		const item = assignments[String(state.recordingTarget)];
		if (!item) {
			setNotice({ namespace: "matching", id: "missingAssignment" });
			commitState();
			return;
		}
		item.providerTrackId = recordingId;
		item.method = "Вручную · композиция";
		if (item.selectedSourceId && item.sourceMeta[item.selectedSourceId])
			item.sourceMeta[item.selectedSourceId].method = item.method;
		state.selectedTarget = state.recordingTarget;
		state.recordingTarget = null;
		setNotice({ namespace: "matching", id: "recordingLinked" });
		clearPreview();
		commitState();
	};
	const fileButton = (id: string, compact = false) => {
		const file = files[id];
		if (!file) return null;
		return (
			<MatchingFileChip
				key={id}
				groupId={group.id}
				fileId={id}
				name={file.name}
				codec={file.codec}
				duration={duration(file.duration)}
				path={compact ? undefined : file.path}
				compact={compact}
				selected={state.selectedFile === id}
				available={group.fileIds.includes(id) && usableFile(file)}
				locale={locale}
				onSelect={() => {
					state.selectedFile = state.selectedFile === id ? null : id;
					commitState();
				}}
			/>
		);
	};
	return (
		<main
			className="relative mx-auto w-full max-w-[1800px] px-[clamp(14px,2vw,30px)] pt-5 pb-[110px] text-[var(--ink)] max-[800px]:px-3 max-[800px]:pt-3.5 max-[800px]:pb-[120px] min-[1400px]:flex min-[1400px]:h-full min-[1400px]:min-h-0 min-[1400px]:flex-col"
			onKeyDown={(event) => {
				if (event.key === "Escape" && state.selectedFile) {
					state.selectedFile = null;
					commitState();
				}
			}}
		>
			<header className="mb-4 min-[1400px]:shrink-0">
				<p className="demo-eyebrow mb-[5px] text-[11px] text-[var(--muted)]">
					<a href="#review">{matchingMessage(locale, "incomingLink")}</a> ·{" "}
					{matchingMessage(locale, "sourceDirectory")}: /mnt/inbox/
					{group.id}
				</p>
				<h1 className="m-0 text-[clamp(22px,2vw,30px)] tracking-[-.025em]">
					{matchingMessage(locale, "matchingTitle", { group: group.title })}
				</h1>
				<p className="mt-1 text-[var(--muted)]">
					{group.mode === "local"
						? matchingMessage(locale, "localTrackListHint")
						: matchingMessage(locale, "providerTrackListHint")}
				</p>
			</header>
			<div
				className="mb-2.5 h-12 min-w-0 max-[600px]:h-16 min-[1400px]:shrink-0"
				data-matching-feedback
			>
				<div
					role="status"
					aria-live="polite"
					className={`h-full overflow-auto rounded px-3 py-2 text-xs ${state.uiNotice || state.notice ? "border border-[var(--line)] bg-[var(--accent-soft)]" : ""}`}
				>
					{state.notice ||
						(state.uiNotice ? formatUiNotice(locale, state.uiNotice) : "")}
				</div>
			</div>
			<div className="incoming-summary mb-3 flex flex-wrap items-center gap-x-5 gap-y-2 rounded-[5px] border border-[var(--line)] bg-[var(--raised)] px-3 py-[9px] text-[var(--muted)] min-[1400px]:shrink-0">
				<strong className="text-[var(--ink)]">
					{matchingMessage(locale, "assignedCount", {
						assigned: Object.keys(assignments).length,
						total: tracks.length,
					})}
				</strong>
				<span>
					{matchingMessage(locale, "freeCount", { count: pool.length })}
				</span>
				<span>
					{group.mode === "local"
						? matchingMessage(locale, "localOrder")
						: `${target.year ?? ""} · ${target.country ?? ""} · ${target.catalog ?? ""}`}
				</span>
			</div>
			<div className="matching-layout grid grid-cols-[minmax(220px,.72fr)_minmax(450px,2.1fr)_minmax(220px,.78fr)] items-start gap-3 max-[1250px]:grid-cols-[minmax(200px,.72fr)_minmax(0,1.8fr)] max-[800px]:grid-cols-1 min-[1400px]:min-h-0 min-[1400px]:flex-1 min-[1400px]:grid-rows-[minmax(0,1fr)_minmax(180px,min(32dvh,340px))] min-[1400px]:items-stretch">
				<section
					className={`matching-candidates ${surfacePanel} sticky top-[10px] max-h-[calc(100dvh_-_24px)] overflow-auto max-[1250px]:row-start-1 max-[1250px]:col-start-1 max-[800px]:static max-[800px]:max-h-none min-[1400px]:min-h-0 min-[1400px]:max-h-none`}
				>
					<h2 className="mb-2.5 mt-0 text-[15px] tracking-[-.01em]">
						{matchingMessage(locale, "findRelease")}
					</h2>
					<MatchingSearchPicker
						artists={artists}
						releases={providers}
						selectedReleaseId={
							group.mode === "provider" ? group.candidateId : null
						}
						onSelectRelease={setCandidate}
					/>
					<Button
						className="demo-button max-[520px]:w-full"
						aria-pressed={group.mode === "local"}
						onPress={() => {
							group.mode = "local";
							state.selectedTarget = null;
							state.selectedFile = null;
							state.recordingTarget = null;
							clearPreview();
							commitState();
						}}
					>
						{matchingMessage(locale, "localRelease")}
					</Button>
				</section>
				<section
					className={`matching-target ${surfacePanel} max-[1250px]:row-span-2 max-[1250px]:col-start-2 max-[800px]:row-auto max-[800px]:col-auto min-[1400px]:min-h-0 min-[1400px]:max-h-none min-[1400px]:overflow-y-auto`}
				>
					<h2 className="mb-2.5 mt-0 text-[15px] tracking-[-.01em]">
						{matchingMessage(locale, "positionCount", {
							title: String(target.title),
							count: tracks.length,
						})}
					</h2>
					<div className={matchingTableWrap}>
						<table className={matchingTable}>
							<thead>
								<tr>
									<th className={matchingTableHeading}>
										{matchingMessage(locale, "publishColumn")}
									</th>
									<th className={matchingTableHeading}>
										{matchingMessage(locale, "positionColumn")}
									</th>
									<th className={matchingTableHeading}>
										{matchingMessage(locale, "trackColumn")}
									</th>
									<th className={matchingTableHeading}>
										{matchingMessage(locale, "durationColumn")}
									</th>
									<th className={matchingTableHeading}>
										{matchingMessage(locale, "sourceColumn")}
									</th>
									<th className={matchingTableHeading}>
										{matchingMessage(locale, "evidenceColumn")}
									</th>
								</tr>
							</thead>
							<tbody>
								{tracks.map((track) => {
									const id = String(track.id);
									const item = assignments[id];
									const ids = sourceIds(item);
									const currentId = selectedSourceId(item);
									const file = files[currentId ?? ""];
									const bound =
										group.mode === "local"
											? findProviderTrack(
													item?.providerTrackId ?? null,
													providers,
												)
											: null;
									return (
										<tr
											key={id}
											className={
												id === selectedTrackId
													? "inspected bg-[var(--accent-soft)]"
													: ""
											}
										>
											<td>
												<Checkbox
													aria-label={matchingMessage(
														locale,
														"ariaPublishTrack",
														{
															track: String(track.title),
														},
													)}
													isSelected={Boolean(
														item?.included && usableFile(file),
													)}
													isDisabled={!usableFile(file)}
													onChange={(checked) => {
														if (item) item.included = checked;
														clearPreview();
														commitState();
													}}
												>
													<span className="checkbox" aria-hidden="true">
														{item?.included && usableFile(file) ? "✓" : ""}
													</span>
												</Checkbox>
											</td>
											<td>{String(track.position).padStart(2, "0")}</td>
											<td>
												<Button
													className="min-h-0 border-0 bg-transparent p-0 text-left font-semibold text-[var(--accent)] hover:bg-transparent"
													onPress={() => {
														state.selectedTarget = id;
														state.inspector = "tags";
														commitState();
													}}
												>
													{String(track.title)}
												</Button>
												<span className="block text-[10px] text-[var(--muted)]">
													{artistName(String(track.artistId), artists)}
													{group.mode === "local"
														? ` · ${bound?.release.title ?? matchingMessage(locale, "unlinked")}`
														: ""}
												</span>
											</td>
											<td>{duration(track.duration)}</td>
											<td className="min-w-[180px]">
												<div className={matchingAssignmentRegion}>
													<MatchingDropZone
														groupId={group.id}
														trackId={id}
														trackName={String(track.title)}
														locale={locale}
														onAssign={(fileId) => addSource(fileId, id)}
													>
														<div
															className={matchingLinkedSources}
															role={ids.length ? "group" : undefined}
															aria-label={
																ids.length
																	? matchingMessage(locale, "linkedSources", {
																			track: String(track.title),
																		})
																	: undefined
															}
														>
															{ids.length ? (
																<RadioGroup
																	aria-label={matchingMessage(
																		locale,
																		"ariaTrackSource",
																		{ track: String(track.title) },
																	)}
																	value={currentId ?? ""}
																	onChange={(value) =>
																		sourceSelection(id, value)
																	}
																>
																	{ids.map((sourceId) => (
																		<div
																			key={sourceId}
																			className={matchingSourceRow}
																		>
																			<Radio value={sourceId}>
																				<span
																					className="indicator"
																					aria-hidden="true"
																				/>
																				<span className="truncate text-[10px]">
																					{files[sourceId]?.name ?? sourceId}
																				</span>
																			</Radio>
																			<MatchingDragHandle
																				groupId={group.id}
																				fileId={sourceId}
																				name={files[sourceId]?.name ?? sourceId}
																				available={usableFile(files[sourceId])}
																				locale={locale}
																			/>
																			<Button
																				className="h-6 min-h-0 w-7 p-0 text-[12px]"
																				aria-label={matchingMessage(
																					locale,
																					"removeSource",
																					{
																						file:
																							files[sourceId]?.name ?? sourceId,
																						track: String(track.title),
																					},
																				)}
																				onPress={() =>
																					mutateAssignment((draft) =>
																						unassignFile(draft, id, sourceId),
																					)
																				}
																			>
																				×
																			</Button>
																		</div>
																	))}
																</RadioGroup>
															) : (
																<span className="text-[10px] text-[var(--muted)]">
																	{matchingMessage(locale, "noFile")}
																</span>
															)}
														</div>
														<Button
															className="h-6 min-h-0 self-end px-2 py-0 text-[10px] pointer-coarse:min-h-0"
															aria-label={`${matchingMessage(locale, "assignSelected")}${state.selectedFile ? `: ${files[state.selectedFile]?.name ?? state.selectedFile}` : ` · ${matchingMessage(locale, "chooseFile")}`}`}
															isDisabled={!state.selectedFile}
															onPress={() => {
																if (state.selectedFile)
																	addSource(state.selectedFile, id);
															}}
														>
															{matchingMessage(locale, "assign")}
														</Button>
													</MatchingDropZone>
												</div>
											</td>
											<td>
												{file ? (
													<>
														<span>
															{item?.method.startsWith("Вручную")
																? matchingMessage(locale, "manual")
																: file.confidence === "high"
																	? matchingMessage(locale, "highConfidence")
																	: matchingMessage(locale, "lowConfidence")}
														</span>
														<Button
															className="demo-button"
															onPress={() => {
																state.selectedTarget = id;
																state.inspector = "evidence";
																commitState();
															}}
														>
															{matchingMessage(locale, "evidenceButton")}
														</Button>
													</>
												) : (
													matchingMessage(locale, "noFile")
												)}
												{group.mode === "local" && (
													<Button
														onPress={() => {
															state.recordingTarget = id;
															state.recordingQuery = String(track.title);
															state.recordingResults = allProviderTracks;
															setRecordingQuery(String(track.title));
															commitState();
														}}
													>
														{matchingMessage(locale, "recordingSearch")}
													</Button>
												)}
											</td>
										</tr>
									);
								})}
							</tbody>
						</table>
					</div>
					<p>{matchingMessage(locale, "matchingDisclaimer")}</p>
				</section>
				<section
					className={`matching-pool ${surfacePanel} sticky top-[10px] max-h-[calc(100dvh_-_24px)] overflow-auto max-[1250px]:row-start-2 max-[1250px]:col-start-1 max-[800px]:static max-[800px]:row-auto max-[800px]:col-auto max-[800px]:max-h-none min-[1400px]:min-h-0 min-[1400px]:max-h-none`}
				>
					<h2 className="mb-2.5 mt-0 text-[15px] tracking-[-.01em]">
						{matchingMessage(locale, "poolTitle", { count: pool.length })}
					</h2>
					{pool.length ? (
						pool.map((id) => fileButton(id))
					) : (
						<p>{matchingMessage(locale, "allFilesAssigned")}</p>
					)}
					<p>{matchingMessage(locale, "poolInstructions")}</p>
				</section>
				<section
					className={`matching-inspector ${surfacePanel} mt-3 max-[1399px]:col-span-full min-[1400px]:col-span-3 min-[1400px]:min-h-0 min-[1400px]:overflow-y-auto`}
				>
					<h2 className="mb-2.5 mt-0 text-[15px] tracking-[-.01em]">
						{matchingMessage(locale, "inspectorTitle", {
							track: String(inspected.title),
						})}
					</h2>
					<Tabs
						className="matching-inspector-tabs min-w-0 [&_.ui-tab-panel]:pt-3"
						selectedKey={state.inspector}
						onSelectionChange={(key) => {
							state.inspector = key as "tags" | "evidence";
							commitState();
						}}
					>
						<TabList aria-label={matchingMessage(locale, "ariaTrackInspector")}>
							<Tab id="tags">{matchingMessage(locale, "tagComparison")}</Tab>
							<Tab id="evidence">{matchingMessage(locale, "matchReason")}</Tab>
						</TabList>
						{state.inspector === "tags" ? (
							<TabPanel id="tags">
								<>
									<div className="tag-controls mb-2.5 mt-0 flex flex-wrap items-center gap-2.5">
										<Tabs
											selectedKey={state.tagScope}
											onSelectionChange={(key) => {
												state.tagScope = key as "artist" | "release" | "track";
												commitState();
											}}
										>
											<TabList
												aria-label={matchingMessage(locale, "ariaTagScope")}
											>
												<Tab id="artist">
													{matchingMessage(locale, "artistScope")}
												</Tab>
												<Tab id="release">
													{matchingMessage(locale, "releaseScope")}
												</Tab>
												<Tab id="track">
													{matchingMessage(locale, "trackScope")}
												</Tab>
											</TabList>
										</Tabs>
										<Checkbox
											isSelected={state.differencesOnly}
											onChange={(checked) => {
												state.differencesOnly = checked;
												commitState();
											}}
										>
											<span className="checkbox" />
											{matchingMessage(locale, "differencesOnly")}
										</Checkbox>
									</div>
									<div className={comparisonTableWrap}>
										<table className={table}>
											<thead>
												<tr>
													<th>{matchingMessage(locale, "fieldColumn")}</th>
													<th>{matchingMessage(locale, "sourceFileColumn")}</th>
													<th>
														{matchingMessage(locale, "providerCacheColumn")}
													</th>
													<th>
														{matchingMessage(locale, "localLibraryColumn")}
													</th>
													<th>
														{matchingMessage(
															locale,
															"currentPublicationColumn",
														)}
													</th>
												</tr>
											</thead>
											<tbody>
												{tagRows.map((key) => (
													<tr key={key}>
														<th>{key}</th>
														<td>{tagValue(sourceTags, key)}</td>
														<td>
															{providerTrack
																? tagValue(remoteTags, key)
																: matchingMessage(locale, "noProviderLink")}
														</td>
														<td>{tagValue(localTags, key)}</td>
														<td>
															{publication
																? tagValue(publicationTags, key)
																: matchingMessage(locale, "unpublished")}
														</td>
													</tr>
												))}
											</tbody>
										</table>
									</div>
									<p>
										{selectedFile?.path ??
											matchingMessage(locale, "noSourceSelected")}{" "}
										·{" "}
										{publication
											? matchingMessage(locale, "publicationRead", {
													path: String(publication.path),
													readAt: String(publication.readAt),
												})
											: matchingMessage(locale, "publicationAbsent")}
									</p>
								</>
							</TabPanel>
						) : (
							<TabPanel id="evidence">
								<Evidence
									file={selectedFile}
									track={inspected}
									assignment={assignment}
									providerArtists={artists}
									locale={locale}
								/>
							</TabPanel>
						)}
					</Tabs>
				</section>
			</div>
			{state.recordingTarget && (
				<section
					className={`matching-recording ${surfacePanel} mt-3 min-[1400px]:max-h-[25dvh] min-[1400px]:shrink-0 min-[1400px]:overflow-y-auto`}
				>
					<h2 className="mb-2.5 mt-0 text-[15px] tracking-[-.01em]">
						{matchingMessage(locale, "recordingSearch")}
					</h2>
					<form
						className="flex flex-wrap items-end gap-[7px] [&_.ui-field]:min-w-[190px] [&_.ui-field]:flex-1"
						onSubmit={(event) => {
							event.preventDefault();
							state.recordingQuery = recordingQuery;
							state.recordingResults = allProviderTracks;
							commitState();
						}}
					>
						<label htmlFor="recording-query">
							{matchingMessage(locale, "searchTitleArtist")}
						</label>
						<TextField
							className="ui-field"
							aria-label={matchingMessage(locale, "ariaSearchTitleArtist")}
							value={recordingQuery}
							onChange={setRecordingQuery}
						>
							<Input id="recording-query" />
						</TextField>
						<Button type="submit">
							{matchingMessage(locale, "searchRecordings")}
						</Button>
						<Button
							onPress={() => {
								state.recordingTarget = null;
								commitState();
							}}
						>
							{matchingMessage(locale, "close")}
						</Button>
					</form>
					<p>{matchingMessage(locale, "recordingLinkHint")}</p>
					<ul>
						{matchingRecordings.map(({ release, track }) => (
							<li key={String(track.id)}>
								{String(track.title)} · {String(release.title)} ·{" "}
								{duration(track.duration)}{" "}
								<Button onPress={() => bindRecording(String(track.id))}>
									{matchingMessage(locale, "linkRecording")}
								</Button>
							</li>
						))}
					</ul>
				</section>
			)}
			{!preview && (
				<div className="matching-commit fixed bottom-3.5 left-[calc(174px_+_18px)] right-[18px] z-[5] m-0 flex flex-wrap items-center justify-between gap-x-5 gap-y-2 rounded-md border border-[var(--line)] bg-[color-mix(in_srgb,var(--surface)_95%,transparent)] px-3.5 py-2.5 shadow-[0_6px_24px_#0002] backdrop-blur-[12px] max-[800px]:right-2 max-[800px]:bottom-2 max-[800px]:left-2 max-[800px]:p-2 max-[520px]:items-stretch">
					<strong>
						{matchingMessage(locale, "selectedForPublicationCount", {
							count: Object.values(assignments).filter(
								(item) =>
									item.included &&
									usableFile(files[selectedSourceId(item) ?? ""]),
							).length,
						})}
					</strong>
					<span className="min-w-[220px] flex-1 text-[11px] text-[var(--muted)] max-[800px]:hidden">
						{matchingMessage(locale, "unassignedSummary", {
							empty: tracks.length - Object.keys(assignments).length,
							files: pool.length,
						})}
					</span>
					<Button
						className="demo-button max-[520px]:w-full"
						isDisabled={
							!Object.values(assignments).some(
								(item) =>
									item.included &&
									usableFile(files[selectedSourceId(item) ?? ""]),
							)
						}
						onPress={openPreview}
					>
						{matchingMessage(locale, "publicationPreview")}
					</Button>
				</div>
			)}
			{preview && (
				<section
					className={`matching-preview ${surfacePanel} mt-3.5 max-w-[min(1100px,95vw)] min-[1400px]:mb-[90px]`}
					aria-label={matchingMessage(locale, "ariaPublicationPreview")}
				>
					<h2 ref={previewHeadingRef} tabIndex={-1}>
						{matchingMessage(locale, "previewTitle", {
							title: String(preview.target.title),
						})}
					</h2>
					<p>{matchingMessage(locale, "previewDescription")}</p>
					{stale && (
						<p role="alert">{matchingMessage(locale, "stalePreview")}</p>
					)}
					<p>
						{matchingMessage(locale, "previewSelected", {
							selected: activeRows.length,
							total: previewRows.length,
						})}{" "}
						·{" "}
						{matchingMessage(locale, "previewChanges", {
							count: changedRows.length,
						})}{" "}
						·{" "}
						{matchingMessage(locale, "previewLinked", {
							count: linkedRows.length,
						})}{" "}
						·{" "}
						{matchingMessage(locale, "previewRemainingFiles", {
							count:
								group.fileIds.length -
								new Set(activeRows.map((row) => row.file?.id)).size,
						})}{" "}
						· {matchingMessage(locale, "previewFormat")}
					</p>
					<PublicationInspector items={inspectionItems} locale={locale} />
					{hasDowngrade && (
						<Checkbox
							isSelected={state.confirmDowngrade}
							onChange={(checked) => {
								state.confirmDowngrade = checked;
								commitState();
							}}
						>
							<span className="checkbox" />
							{matchingMessage(locale, "confirmDowngrade")}
						</Checkbox>
					)}
					<div className="incoming-actions flex flex-wrap justify-end gap-2">
						<Button
							isDisabled={
								stale ||
								(!changedRows.length && !linkedRows.length) ||
								(hasDowngrade && !state.confirmDowngrade)
							}
							onPress={() => setConfirming(true)}
						>
							{matchingMessage(locale, "confirmPublish")}
						</Button>
						<Button
							onPress={() => {
								state.plan = null;
								commitState();
							}}
						>
							{matchingMessage(locale, "returnToObject")}
						</Button>
					</div>
				</section>
			)}
			<Modal isOpen={confirming} onOpenChange={setConfirming} isDismissable>
				<Dialog
					aria-labelledby="incoming-confirm-title"
					className="grid gap-2.5"
				>
					<Heading slot="title" id="incoming-confirm-title">
						{changedRows.length
							? matchingMessage(locale, "confirmPublishCount", {
									count: changedRows.length,
								})
							: matchingMessage(locale, "confirmLinksCount", {
									count: linkedRows.length,
								})}
					</Heading>
					<p>{matchingMessage(locale, "unchangedOnConfirm")}</p>
					{stale && (
						<p role="alert">{matchingMessage(locale, "stalePreview")}</p>
					)}
					<div className={dialogFooter}>
						<Button onPress={() => setConfirming(false)}>
							{matchingMessage(locale, "cancel")}
						</Button>
						<Button
							className="border-[var(--button)]! bg-[var(--button)]! font-semibold text-[var(--button-text)]! hover:brightness-90"
							isDisabled={stale || (hasDowngrade && !state.confirmDowngrade)}
							onPress={confirmPublish}
						>
							{matchingMessage(locale, "confirm")}
						</Button>
					</div>
				</Dialog>
			</Modal>
		</main>
	);
}

function Evidence({
	file,
	track,
	assignment,
	providerArtists,
	locale,
}: {
	file?: SourceFile;
	track: OriginalTrack;
	assignment?: Assignment;
	providerArtists: CatalogArtist[];
	locale: import("./i18n/types").Locale;
}) {
	if (!file) return <p>{matchingMessage(locale, "noEvidenceFile")}</p>;
	const rawTitle = file.tags.TITLE?.join("; ") ?? "";
	const rawArtist = file.tags.ARTIST?.join("; ") ?? "";
	const compare = (source: string, remote: string) =>
		normalized(source) === normalized(remote)
			? matchingMessage(locale, "equalAfterNormalization")
			: matchingMessage(locale, "differs");
	return (
		<>
			<p>
				{assignment?.method.startsWith("Вручную")
					? matchingMessage(locale, "assignedByOperator")
					: file.confidence === "high"
						? matchingMessage(locale, "illustrativeHighConfidence")
						: matchingMessage(locale, "illustrativeLowConfidence")}
			</p>
			<strong>{matchingMessage(locale, "numericScoreNotCalculated")}</strong>
			<div className={comparisonTableWrap}>
				<table className={table}>
					<thead>
						<tr>
							<th>{matchingMessage(locale, "evidenceFeature")}</th>
							<th>{matchingMessage(locale, "sourceValue")}</th>
							<th>{matchingMessage(locale, "candidateValue")}</th>
							<th>{matchingMessage(locale, "resultDifference")}</th>
							<th>{matchingMessage(locale, "weightContribution")}</th>
						</tr>
					</thead>
					<tbody>
						<tr>
							<th>{matchingMessage(locale, "trackTitle")}</th>
							<td>{rawTitle}</td>
							<td>{String(track.title)}</td>
							<td>{compare(rawTitle, String(track.title))}</td>
							<td>{matchingMessage(locale, "notModeled")}</td>
						</tr>
						<tr>
							<th>{matchingMessage(locale, "artist")}</th>
							<td>{rawArtist}</td>
							<td>{artistName(String(track.artistId), providerArtists)}</td>
							<td>
								{compare(
									rawArtist,
									artistName(String(track.artistId), providerArtists),
								)}
							</td>
							<td>{matchingMessage(locale, "notModeled")}</td>
						</tr>
						<tr>
							<th>{matchingMessage(locale, "durationFeature")}</th>
							<td>{duration(file.duration)}</td>
							<td>{duration(track.duration)}</td>
							<td>{Number(file.duration) - Number(track.duration)} с</td>
							<td>{matchingMessage(locale, "notModeled")}</td>
						</tr>
						<tr>
							<th>{matchingMessage(locale, "positionNumber")}</th>
							<td>{tagValue(file.tags, "TRACKNUMBER")}</td>
							<td>{String(track.position)}</td>
							<td>{matchingMessage(locale, "differs")}</td>
							<td>{matchingMessage(locale, "notModeled")}</td>
						</tr>
						<tr>
							<th>{matchingMessage(locale, "recordingMbid")}</th>
							<td>{tagValue(file.tags, "MUSICBRAINZ_TRACKID")}</td>
							<td>
								{String(
									track.recordingId ?? matchingMessage(locale, "notSpecified"),
								)}
							</td>
							<td>{matchingMessage(locale, "noMbidEvidence")}</td>
							<td>{matchingMessage(locale, "notModeled")}</td>
						</tr>
						<tr>
							<th>{matchingMessage(locale, "fingerprint")}</th>
							<td>
								{file.fingerprint ??
									matchingMessage(locale, "noFingerprintResult")}
							</td>
							<td>AcoustID / Chromaprint</td>
							<td>{matchingMessage(locale, "fingerprintNotProof")}</td>
							<td>{matchingMessage(locale, "notModeled")}</td>
						</tr>
					</tbody>
				</table>
			</div>
			<p>{matchingMessage(locale, "illustrativeComparison")}</p>
		</>
	);
}
