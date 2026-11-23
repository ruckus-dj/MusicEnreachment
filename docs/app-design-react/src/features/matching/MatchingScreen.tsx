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
} from "../../components/ui";
import {
	publicationMetadataIssueMessages,
	publicationSortTagCollisions,
	type CatalogArtist,
} from "../library/catalogData";
import { MatchingSearchPicker } from "./MatchingSearchPicker";
import { MatchingReleasePreparation } from "./MatchingReleasePreparation";
import { MatchingPreview, type MatchingPreviewRow } from "./MatchingPreview";
import { fixtureMatchingBoard } from "../../fixtures/fixtureMatchingBoard";
import { MatchingBoard, MatchingEvidenceInspector } from "./";
import {
	MatchingWorkspaceLayout,
	matchingCandidatesPanel,
	matchingInspectorContent,
	matchingInspectorPanel,
} from "./MatchingWorkspaceLayout";
import { metadataSnapshotDiff } from "../publication/publicationMetadataDiff";
import { PublicationInspector } from "../publication/PublicationInspector";
import type { PublicationInspection } from "../publication/PublicationInspector";
import { publicationMessage } from "../publication/publicationMessages";
import { useI18n } from "../../i18n";
import { matchingAction, matchingMessage } from "./matchingMessages";
import { formatUiNotice, type UiNotice } from "../../infrastructure/uiNotice";
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
} from "../incoming/incomingModel";
import {
	comparisonTableWrap,
	dialogFooter,
	surfacePanel,
	table,
} from "../../components/styles/matchingStyles";

export type MatchingScreenProps = {
	destination?: import("../publication/publicationOutput").PublicationDestination;
	groupId?: string;
	files?: IncomingFiles;
	releases?: IncomingReleases;
	providers?: IncomingReleases;
	artists?: CatalogArtist[];
	state?: IncomingState;
	onState?: (state: IncomingState) => void;
	onReleases?: (releases: IncomingReleases) => void;
	createLocalReleaseHref?: string;
	prepareReleaseId?: string;
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
	createLocalReleaseHref,
	prepareReleaseId,
}: MatchingScreenProps) {
	const [localState, setLocalState] = useState(createIncomingState);
	const [confirming, setConfirming] = useState(false);
	const [previewOpenSequence, setPreviewOpenSequence] = useState(0);
	const previewHeadingRef = useRef<HTMLHeadingElement>(null);
	const [localReleases, setLocalReleases] = useState<IncomingReleases>(() =>
		structuredClone(incomingFixtures.releases),
	);
	const [recordingQuery, setRecordingQuery] = useState("");
	const [inspectedPair, setInspectedPair] = useState<{
		groupId: string;
		trackId: string;
		fileId: string;
	}>();
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
	const musicBrainzEditions = providers.filter(
		(release) => release.provider !== "Local",
	);
	const resolveMusicBrainzEdition = (identity: string) =>
		musicBrainzEditions.find(
			(release) =>
				release.tracks.length > 0 &&
				(release.id === identity || release.providerId === identity),
		)?.id;
	const searchableEditions = providers.filter(
		(release) => release.provider !== "Local" || release.tracks.length > 0,
	);
	const activeGroupId = groupId ?? state.groupId;
	const group = state.groups.find((item) => item.id === activeGroupId);
	if (!group)
		return (
			<section className="demo-shell mx-auto max-w-[1100px] px-6 py-8">
				<h1>{matchingMessage(locale, "groupNotFound")}</h1>
				<a href="#review">{matchingMessage(locale, "backToIncoming")}</a>
			</section>
		);
	state.groupId = group.id;
	const target = findTarget(group, releases, providers);
	if (!target)
		return (
			<section className="demo-shell mx-auto max-w-[1100px] px-6 py-8">
				<h1>{matchingMessage(locale, "targetNotFound")}</h1>
				<a href="#review">{matchingMessage(locale, "backToIncoming")}</a>
			</section>
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
	const evidenceFile =
		inspectedPair?.groupId === group.id &&
		inspectedPair.trackId === inspected?.id &&
		sourceIds(assignment).includes(inspectedPair.fileId)
			? files[inspectedPair.fileId]
			: selectedFile;
	const fixtureBoard = fixtureMatchingBoard(
		group.id,
		group.fileIds,
		target,
		files,
		assignments,
	);
	const evidencePosition = fixtureBoard.release.positions.find(
		(position) => position.track_id === inspected?.id,
	);
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
		if (group.mode === "provider" && group.candidateId === id) return;
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
		? planRows(preview, files, releases, providers, artists, destination)
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
	const canApplyPlan = Boolean(existingRelease || changedRows.length);
	const linkedRows = previewRows.filter((row) => {
		if (!canApplyPlan) return false;
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
	const assignedFileIds = new Set<string>();
	const fileOutcomes: MatchingPreviewRow[] = previewRows.flatMap((row) => {
		if (!canApplyPlan) return [];
		if (row.assignment?.included && row.file && metadataIssues(row).length)
			return [];
		return sourceIds(row.assignment).flatMap((id) => {
			if (!usableFile(files[id])) return [];
			assignedFileIds.add(id);
			return [
				{
					key: `${row.targetTrack.id}:${id}`,
					source: files[id].name,
					position: `${row.targetTrack.position} · ${row.targetTrack.title}`,
				},
			];
		});
	});
	for (const id of group.fileIds) {
		if (!assignedFileIds.has(id) && files[id])
			fileOutcomes.push({ key: id, source: files[id].name });
	}
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
	return (
		<section
			data-matching-screen
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
						: [
								target.provider ?? "MusicBrainz",
								target.year,
								target.country,
								target.catalog,
							]
								.filter(Boolean)
								.join(" · ")}
				</span>
			</div>
			<section data-matching-candidates className={matchingCandidatesPanel}>
				<h2 className="mb-2.5 mt-0 text-[15px] tracking-[-.01em]">
					{matchingMessage(locale, "findRelease")}
				</h2>
				<div className="grid min-w-0 items-start gap-3 lg:grid-cols-[minmax(0,2fr)_minmax(0,1fr)]">
					<MatchingSearchPicker
						artists={artists}
						releases={searchableEditions}
						selectedReleaseId={
							group.mode === "provider" ? group.candidateId : null
						}
						onSelectRelease={(release) => {
							const targetId =
								release.provider === "Local"
									? release.id
									: resolveMusicBrainzEdition(release.id);
							if (!targetId) return false;
							setCandidate(targetId);
							return true;
						}}
					/>
					<div className="grid min-w-0 gap-2">
						<MatchingReleasePreparation
							key={group.id}
							releaseId={prepareReleaseId}
							resolveRelease={resolveMusicBrainzEdition}
							onPrepared={setCandidate}
						/>
						{createLocalReleaseHref && (
							<a
								className="demo-button w-fit max-w-full text-center no-underline max-[520px]:w-full"
								href={createLocalReleaseHref}
							>
								{locale === "ru"
									? "Создать свой релиз"
									: "Create your own release"}
							</a>
						)}
					</div>
				</div>
			</section>
			<MatchingWorkspaceLayout>
				<MatchingBoard
					{...fixtureBoard}
					locale={locale}
					selectedSourceId={state.selectedFile}
					inspectedTrackId={inspected?.id}
					onSelectedSourceChange={(id) => {
						state.selectedFile = id;
						commitState();
					}}
					onAssign={addSource}
					onUnassign={(fileId, trackId) =>
						mutateAssignment((draft) => unassignFile(draft, trackId, fileId))
					}
					onInspectPosition={(trackId) => {
						setInspectedPair(undefined);
						state.selectedTarget = trackId;
						state.inspector = "tags";
						commitState();
					}}
					onInspectPair={(fileId, trackId) => {
						setInspectedPair({ groupId: group.id, fileId, trackId });
						state.selectedTarget = trackId;
						state.recordingTarget = null;
						state.recordingResults = null;
						state.inspector = "evidence";
						commitState();
					}}
					renderPositionMeta={({ position }) => {
						const trackId = position.track_id;
						const track = tracks.find((item) => String(item.id) === trackId);
						const item = assignments[trackId];
						const ids = sourceIds(item);
						const currentId = selectedSourceId(item);
						const file = files[currentId ?? ""];
						if (!track) return null;
						return (
							<div className="mt-2 flex flex-wrap items-center gap-x-3 gap-y-2">
								{item ? (
									<Checkbox
										aria-label={matchingMessage(locale, "ariaPublishTrack", {
											track: String(track.title),
										})}
										isSelected={Boolean(item.included && usableFile(file))}
										isDisabled={!usableFile(file)}
										onChange={(checked) => {
											item.included = checked;
											clearPreview();
											commitState();
										}}
									>
										<span className="checkbox" aria-hidden="true">
											{item.included && usableFile(file) ? "✓" : ""}
										</span>
									</Checkbox>
								) : null}
								{ids.length ? (
									<RadioGroup
										className="min-w-0"
										aria-label={matchingMessage(locale, "ariaTrackSource", {
											track: String(track.title),
										})}
										value={currentId ?? ""}
										onChange={(value) => sourceSelection(trackId, value)}
									>
										{ids.map((sourceId) => (
											<div
												key={sourceId}
												className="flex min-w-0 items-center gap-1"
											>
												<Radio value={sourceId}>
													<span className="indicator" aria-hidden="true" />
													<span className="truncate text-[10px]">
														{files[sourceId]?.name ?? sourceId}
													</span>
												</Radio>
											</div>
										))}
									</RadioGroup>
								) : null}
								{group.mode === "local" ? (
									<Button
										onPress={() => {
											state.recordingTarget = trackId;
											state.recordingQuery = String(track.title);
											state.recordingResults = allProviderTracks;
											setRecordingQuery(String(track.title));
											commitState();
										}}
									>
										{matchingMessage(locale, "recordingSearch")}
									</Button>
								) : null}
							</div>
						);
					}}
				/>
				<section data-matching-inspector className={matchingInspectorPanel}>
					<div className={matchingInspectorContent}>
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
							<TabList
								aria-label={matchingMessage(locale, "ariaTrackInspector")}
							>
								<Tab id="tags">{matchingMessage(locale, "tagComparison")}</Tab>
								<Tab id="evidence">
									{matchingMessage(locale, "matchReason")}
								</Tab>
							</TabList>
							{state.inspector === "tags" ? (
								<TabPanel id="tags">
									<>
										<div className="tag-controls mb-2.5 mt-0 flex flex-wrap items-center gap-2.5">
											<Tabs
												selectedKey={state.tagScope}
												onSelectionChange={(key) => {
													state.tagScope = key as
														| "artist"
														| "release"
														| "track";
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
														<th>
															{matchingMessage(locale, "sourceFileColumn")}
														</th>
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
									{evidencePosition && evidenceFile ? (
										<>
											<p className="mt-1 text-sm wrap-anywhere">
												{evidenceFile.name}
											</p>
											<MatchingEvidenceInspector
												position={evidencePosition}
												rawTags={evidenceFile.tags}
												locale={locale}
											/>
										</>
									) : (
										<p>{matchingMessage(locale, "noEvidenceFile")}</p>
									)}
								</TabPanel>
							)}
						</Tabs>
					</div>
				</section>
			</MatchingWorkspaceLayout>
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
				<MatchingPreview
					locale={locale}
					headingRef={previewHeadingRef}
					heading={matchingMessage(locale, "previewTitle", {
						title: String(preview.target.title),
					})}
					summary={[
						matchingMessage(locale, "previewSelected", {
							selected: activeRows.length,
							total: previewRows.length,
						}),
						matchingMessage(locale, "previewChanges", {
							count: changedRows.length,
						}),
						matchingMessage(locale, "previewLinked", {
							count: linkedRows.length,
						}),
						matchingMessage(locale, "previewRemainingFiles", {
							count: fileOutcomes.filter((row) => !row.position).length,
						}),
						matchingMessage(locale, "previewFormat"),
					].join(" · ")}
					rows={fileOutcomes}
					notice={stale ? matchingMessage(locale, "stalePreview") : undefined}
					primary={{
						label: matchingMessage(locale, "confirmPublish"),
						onPress: () => setConfirming(true),
						disabled:
							stale ||
							(!changedRows.length && !linkedRows.length) ||
							(hasDowngrade && !state.confirmDowngrade),
					}}
					secondary={{
						label: matchingMessage(locale, "returnToObject"),
						onPress: () => {
							state.plan = null;
							commitState();
						},
					}}
				>
					<p>{matchingMessage(locale, "previewDescription")}</p>
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
				</MatchingPreview>
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
		</section>
	);
}
