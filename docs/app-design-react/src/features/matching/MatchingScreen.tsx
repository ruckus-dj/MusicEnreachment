import type { FormEvent } from "react";
import { useLayoutEffect, useRef, useState } from "react";
import { surfacePanel } from "../../components/styles/matchingStyles";
import { Button, Input, TextField } from "../../components/ui";
import { fixtureMatchingBoard } from "../../fixtures/fixtureMatchingBoard";
import { useI18n } from "../../i18n";
import { formatUiNotice, type UiNotice } from "../../infrastructure/uiNotice";
import {
	type Assignment,
	artistName,
	assignFile,
	createIncomingState,
	findTarget,
	getDraft,
	type IncomingFiles,
	type IncomingPlan,
	type IncomingReleases,
	type IncomingState,
	incomingFixtures,
	normalized,
	providerReleases,
	selectedSourceId,
	sourceIds,
	unassignFile,
	usableFile,
} from "../incoming/incomingModel";
import type { CatalogArtist } from "../library/catalogData";
import { MatchingBoard, MatchingEvidenceInspector } from "./";
import { MatchingLinkResultView } from "./MatchingLinkResultView";
import { MatchingReceiptPublicationView } from "./MatchingReceiptPublicationView";
import { MatchingPreview, type MatchingPreviewRow } from "./MatchingPreview";
import { MatchingReleasePreparation } from "./MatchingReleasePreparation";
import { MatchingReviewSearchView } from "./MatchingReviewSearchView";
import { MatchingSearchPicker } from "./MatchingSearchPicker";
import {
	MatchingWorkspaceLayout,
	matchingInspectorContent,
	matchingInspectorPanel,
} from "./MatchingWorkspaceLayout";
import { saveMatchingLinks } from "./matchingLinks";
import { matchingLinksCopy } from "./matchingLinksCopy";
import { matchingMessage } from "./matchingMessages";

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
	const [previewOpenSequence, setPreviewOpenSequence] = useState(0);
	const [dismissedReceiptId, setDismissedReceiptId] = useState("");
	const [publicationReceiptId, setPublicationReceiptId] = useState("");
	const [focusReceiptId, setFocusReceiptId] = useState("");
	const previewHeadingRef = useRef<HTMLHeadingElement>(null);
	const receiptHeadingRef = useRef<HTMLHeadingElement>(null);
	const publicationButtonRef = useRef<HTMLButtonElement>(null);
	const restorePublicationFocus = useRef(false);
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
	const linkCopy = matchingLinksCopy[locale];
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
	useLayoutEffect(() => {
		if (!focusReceiptId) return;
		receiptHeadingRef.current?.focus();
	}, [focusReceiptId]);
	useLayoutEffect(() => {
		if (!publicationReceiptId && restorePublicationFocus.current) {
			restorePublicationFocus.current = false;
			publicationButtonRef.current?.focus();
		}
	}, [publicationReceiptId]);
	const state = givenState ?? localState;
	const files = givenFiles ?? incomingFixtures.files;
	const releases = givenReleases ?? localReleases;
	const providers = givenProviders ?? providerReleases;
	const artists = givenArtists ?? incomingFixtures.artists;
	const setReleases = (next: IncomingReleases) =>
		givenReleases ? onReleases?.(next) : setLocalReleases(next);
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
	const receipt = state.receipts
		.filter((item) => item.groupId === activeGroupId)
		.at(-1);
	if (receipt && dismissedReceiptId !== receipt.id) {
		return (
			<section
				data-matching-screen
				className="mx-auto w-full max-w-[1800px] space-y-4 px-[clamp(14px,2vw,30px)] py-5 text-[var(--ink)] max-[800px]:px-3"
			>
				<header>
					<a href="#review">{matchingMessage(locale, "backToIncoming")}</a>
					<h1 className="mt-1 text-[clamp(22px,2vw,30px)] tracking-[-.025em]">
						{matchingMessage(locale, "matchingTitle", {
							group: receipt.groupTitle,
						})}
					</h1>
				</header>
				{publicationReceiptId === receipt.id ? (
					<MatchingReceiptPublicationView
						key={receipt.id}
						locale={locale}
						receipt={receipt}
						releases={releases}
						files={files}
						artists={artists}
						savedReceipts={state.receipts}
						destination={destination}
						onReleases={setReleases}
						onBack={() => {
							restorePublicationFocus.current = true;
							setPublicationReceiptId("");
						}}
					/>
				) : (
					<MatchingLinkResultView
						locale={locale}
						releaseTitle={receipt.releaseTitle}
						links={receipt.positions.flatMap((position) =>
							position.sourceIds.map((sourceId) => ({
								key: `${position.trackId}:${sourceId}`,
								source: files[sourceId]?.name ?? sourceId,
								positionId: position.trackId,
								position: `${position.displayedNumber ?? position.position} · ${position.title}`,
							})),
						)}
						remaining={receipt.remainingFileIds.map((id, index) => ({
							key: id,
							name: receipt.remainingFileNames[index] ?? id,
						}))}
						headingRef={receiptHeadingRef}
						publicationButtonRef={publicationButtonRef}
						publicationDisabled={
							!receipt.positions.some((position) => position.sourceIds.length)
						}
						onOpenPublication={() => setPublicationReceiptId(receipt.id)}
						onContinue={
							group?.fileIds.length
								? () => setDismissedReceiptId(receipt.id)
								: undefined
						}
					/>
				)}
			</section>
		);
	}
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
	const existingRelease =
		group.mode === "local"
			? releases.find((release) => release.id === group.releaseId)
			: releases.find(
					(release) =>
						release.providerId === target.id ||
						release.providerId === target.providerId,
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
	const stale = Boolean(
		preview && preview.inventoryVersion !== state.inventoryVersion,
	);
	const activeFileIds = new Set(group.fileIds);
	const fileOutcomes: MatchingPreviewRow[] = (
		preview?.target.tracks ?? []
	).flatMap((track) => {
		const ids = sourceIds(preview?.assignments[String(track.id)]);
		return ids.flatMap((id) =>
			activeFileIds.has(id) && usableFile(files[id])
				? [
						{
							key: id,
							source: files[id].name,
							position: `${track.displayedNumber ?? track.position} · ${track.title}`,
						},
					]
				: [],
		);
	});
	const assignedFileIds = new Set(fileOutcomes.map((row) => row.key));
	for (const id of group.fileIds) {
		if (!assignedFileIds.has(id) && files[id])
			fileOutcomes.push({ key: id, source: files[id].name });
	}
	const confirmLinks = () => {
		if (!preview || stale) return;
		const nextReleases = structuredClone(releases);
		const result = saveMatchingLinks(
			preview,
			group,
			state,
			files,
			nextReleases,
			providers,
		);
		if (!result) return;
		setReleases(nextReleases);
		setFocusReceiptId(result.receipt.id);
		commitState();
	};
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
			<MatchingReviewSearchView
				locale={locale}
				createLocalReleaseHref={createLocalReleaseHref}
				search={
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
				}
				preparation={
					<MatchingReleasePreparation
						key={group.id}
						releaseId={prepareReleaseId}
						resolveRelease={resolveMusicBrainzEdition}
						onPrepared={setCandidate}
					/>
				}
			/>
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
						commitState();
					}}
					onInspectPair={(fileId, trackId) => {
						setInspectedPair({ groupId: group.id, fileId, trackId });
						state.selectedTarget = trackId;
						state.recordingTarget = null;
						state.recordingResults = null;
						commitState();
					}}
					renderPositionMeta={({ position }) => {
						const trackId = position.track_id;
						const track = tracks.find((item) => String(item.id) === trackId);
						if (!track || group.mode !== "local") return null;
						return (
							<div className="mt-2 flex flex-wrap items-center gap-x-3 gap-y-2">
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
							</div>
						);
					}}
				/>
				<section
					data-matching-inspector
					aria-label={matchingMessage(locale, "ariaTrackInspector")}
					className={matchingInspectorPanel}
				>
					<div className={matchingInspectorContent}>
						<h2 className="mb-2.5 mt-0 text-[15px] tracking-[-.01em]">
							{matchingMessage(locale, "inspectorTitle", {
								track: String(inspected.title),
							})}
						</h2>
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
						{linkCopy.assignedFiles(
							new Set(
								Object.values(assignments)
									.flatMap(sourceIds)
									.filter(
										(id) => activeFileIds.has(id) && usableFile(files[id]),
									),
							).size,
						)}
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
							!Object.values(assignments).some((item) =>
								sourceIds(item).some(
									(id) => activeFileIds.has(id) && usableFile(files[id]),
								),
							)
						}
						onPress={openPreview}
					>
						{linkCopy.preview}
					</Button>
				</div>
			)}
			{preview && (
				<MatchingPreview
					locale={locale}
					headingRef={previewHeadingRef}
					heading={linkCopy.preview}
					releaseTitle={String(preview.target.title)}
					summary={[
						linkCopy.assignedFiles(assignedFileIds.size),
						linkCopy.remainingFiles(
							fileOutcomes.filter((row) => !row.position).length,
						),
					].join(" · ")}
					rows={fileOutcomes}
					notice={stale ? matchingMessage(locale, "stalePreview") : undefined}
					primary={{
						label: linkCopy.save,
						onPress: confirmLinks,
						disabled: stale || !assignedFileIds.size,
					}}
					secondary={{
						label: matchingMessage(locale, "returnToObject"),
						onPress: () => {
							state.plan = null;
							commitState();
						},
					}}
				/>
			)}
		</section>
	);
}
