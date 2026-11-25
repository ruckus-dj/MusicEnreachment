import { type ReactNode, useEffect, useRef, useState } from "react";
import { useI18n } from "../../../app/i18n";
import { usePreviewHeadingFocus } from "../../../ui/components/useViewFocus";
import type { MatchingPreviewRow } from "../../../ui/features/matching/MatchingPreview";
import type {
	MatchingScreenViewModel,
	MatchingScreenViewPreview,
	MatchingScreenViewWorkspace,
} from "../../../ui/features/matching/MatchingScreenView";
import { matchingBoardMessages } from "../../../ui/features/matching/matchingBoardMessages";
import { matchingLinksCopy } from "../../../ui/features/matching/matchingLinksCopy";
import { matchingMessage } from "../../../ui/features/matching/matchingWorkspaceMessages";
import { useMatchingLinkResultFocus } from "../../../ui/features/matching/useMatchingLinkResultFocus";
import { fixtureMatchingBoard } from "../../fixtures/fixtureMatchingBoard";
import { incomingGroupsListHref } from "../incoming/incomingGroupsRoute";
import {
	type Assignment,
	artistName,
	assignFile,
	createIncomingState,
	findTarget,
	getDraft,
	type IncomingFiles,
	type IncomingGroup,
	type IncomingPlan,
	type IncomingReleases,
	type IncomingState,
	incomingFixtures,
	type MatchingLinkReceipt,
	normalized,
	providerReleases,
	selectedSourceId,
	sourceIds,
	unassignFile,
	usableFile,
} from "../incoming/incomingModel";
import type { CatalogArtist } from "../library/catalogData";
import { formatUiNotice, type UiNotice } from "../notices/uiNotice";
import { MatchingReceiptPublicationView } from "./MatchingReceiptPublicationView";
import { MatchingReleasePreparation } from "./MatchingReleasePreparation";
import { MatchingSearchPicker } from "./MatchingSearchPicker";
import { saveMatchingLinks } from "./matchingLinks";

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
	releaseId?: string;
	localReleaseId?: string;
	onSelectProviderRelease?: (releaseId?: string) => void;
	onSelectLocalRelease?: (localReleaseId: string) => void;
};

const duration = (seconds: unknown) =>
	Number.isFinite(Number(seconds))
		? `${Math.floor(Number(seconds) / 60)}:${String(Math.round(Number(seconds) % 60)).padStart(2, "0")}`
		: "—";

function receiptMatchesTarget(
	receipt: MatchingLinkReceipt,
	target: IncomingReleases[number],
	mode: IncomingGroup["mode"],
) {
	const id = String(target.id);
	if (mode === "local") {
		return (
			target.providerId != null &&
			receipt.providerReleaseId === target.providerId
		);
	}
	return (
		receipt.releaseId === id ||
		receipt.providerReleaseId === id ||
		(target.providerId != null &&
			receipt.providerReleaseId === target.providerId) ||
		receipt.releaseId === `import-${id}`
	);
}

export function useMatchingScreenDemoProvider({
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
	releaseId,
	localReleaseId,
	onSelectProviderRelease,
	onSelectLocalRelease,
}: MatchingScreenProps): MatchingScreenViewModel {
	const [localState, setLocalState] = useState(createIncomingState);
	const [previewOpenSequence, setPreviewOpenSequence] = useState(0);
	const [dismissedReceiptId, setDismissedReceiptId] = useState("");
	const [publicationReceiptId, setPublicationReceiptId] = useState("");
	const [focusReceiptId, setFocusReceiptId] = useState("");
	const previewHeadingRef = useRef<HTMLHeadingElement>(null);
	const {
		headingRef: receiptHeadingRef,
		publicationButtonRef,
		restorePublicationButtonOnReturn,
	} = useMatchingLinkResultFocus({
		focusHeadingKey: focusReceiptId,
		publicationOpen: Boolean(publicationReceiptId),
	});
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
	usePreviewHeadingFocus(previewHeadingRef, previewOpenSequence);
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
	const rememberedTargets = useRef<Record<string, string>>({});
	const localMode = Boolean(localReleaseId);
	const storedTarget = group ? findTarget(group, releases, providers) : null;
	const localTarget = localReleaseId
		? providers.find(
				(release) =>
					release.provider === "Local" && release.id === localReleaseId,
			)
		: undefined;
	const routeTarget =
		!localMode && releaseId
			? providers.find(
					(release) =>
						release.provider !== "Local" &&
						release.tracks.length > 0 &&
						release.id === releaseId,
				)
			: undefined;
	const rememberedTargetId = group ? rememberedTargets.current[group.id] : "";
	const rememberedTarget =
		!localMode && rememberedTargetId
			? providers.find(
					(release) =>
						release.provider !== "Local" &&
						release.tracks.length > 0 &&
						release.id === rememberedTargetId,
				)
			: undefined;
	if (group && routeTarget)
		rememberedTargets.current[group.id] = String(routeTarget.id);
	const activeTarget = localMode
		? (localTarget ?? null)
		: (routeTarget ?? rememberedTarget ?? storedTarget);
	const activeMode = localMode
		? ("local" as const)
		: group && activeTarget === storedTarget
			? group.mode
			: ("provider" as const);
	const activeTargetKey = group
		? `${group.id}:${activeMode}:${activeTarget ? String(activeTarget.id) : ""}`
		: "";
	const targetResetRef = useRef("");
	const groupReceipts = state.receipts.filter(
		(item) => item.groupId === activeGroupId,
	);
	const receipt = prepareReleaseId
		? undefined
		: localMode || releaseId || rememberedTarget
			? groupReceipts
					.filter(
						(item) =>
							activeTarget &&
							receiptMatchesTarget(item, activeTarget, activeMode),
					)
					.at(-1)
			: groupReceipts.at(-1);
	const backHref = incomingGroupsListHref(group ? [group.id] : []);
	const backLabel = matchingMessage(locale, "backToIncoming");
	useEffect(() => {
		if (!group) return;
		const previous = targetResetRef.current;
		targetResetRef.current = activeTargetKey;
		if (previous === activeTargetKey) return;
		if (!previous && !releaseId && !localReleaseId) return;
		setInspectedPair(undefined);
		setPublicationReceiptId("");
		setFocusReceiptId("");
		setDismissedReceiptId("");
		if (
			!state.selectedFile &&
			!state.selectedTarget &&
			!state.recordingTarget &&
			!state.recordingResults &&
			!state.plan
		)
			return;
		const next = structuredClone(state);
		next.selectedFile = null;
		next.selectedTarget = null;
		next.recordingTarget = null;
		next.recordingResults = null;
		next.plan = null;
		next.confirmDowngrade = false;
		givenState ? onState?.(next) : setLocalState(next);
	}, [
		activeTargetKey,
		givenState,
		group,
		localReleaseId,
		onState,
		releaseId,
		state,
	]);

	if (receipt && dismissedReceiptId !== receipt.id) {
		const publicationOpen = publicationReceiptId === receipt.id;
		return {
			kind: "receipt",
			locale,
			heading: {
				backHref,
				backLabel,
				title: matchingMessage(locale, "matchingTitle", {
					group: receipt.groupTitle,
				}),
			},
			body: publicationOpen
				? {
						kind: "publication",
						content: (
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
									restorePublicationButtonOnReturn();
									setPublicationReceiptId("");
								}}
							/>
						),
					}
				: {
						kind: "links",
						releaseTitle: receipt.releaseTitle,
						links: receipt.positions.flatMap((position) =>
							position.sourceIds.map((sourceId) => ({
								key: `${position.trackId}:${sourceId}`,
								source: files[sourceId]?.name ?? sourceId,
								positionId: position.trackId,
								position: `${position.displayedNumber ?? position.position} · ${position.title}`,
							})),
						),
						remaining: receipt.remainingFileIds.map((id, index) => ({
							key: id,
							name: receipt.remainingFileNames[index] ?? id,
						})),
						headingRef: receiptHeadingRef,
						publicationButtonRef,
						publicationDisabled: !receipt.positions.some(
							(position) => position.sourceIds.length,
						),
						onOpenPublication: () => setPublicationReceiptId(receipt.id),
						onContinue: group?.fileIds.length
							? () => setDismissedReceiptId(receipt.id)
							: undefined,
					},
		};
	}
	if (!group)
		return {
			kind: "missing",
			locale,
			heading: {
				backHref,
				backLabel,
				title: matchingMessage(locale, "groupNotFound"),
			},
		};
	state.groupId = group.id;
	if (!activeTarget) {
		if (!localMode)
			return {
				kind: "missing",
				locale,
				heading: {
					backHref,
					backLabel,
					title: matchingMessage(locale, "targetNotFound"),
				},
			};
		const boardCopy = matchingBoardMessages[locale];
		return {
			kind: "workspace",
			locale,
			heading: {
				backHref,
				backLabel: matchingMessage(locale, "incomingLink"),
				sourceDirectoryLabel: matchingMessage(locale, "sourceDirectory"),
				sourcePath: `/mnt/inbox/${group.id}`,
				title: matchingMessage(locale, "matchingTitle", { group: group.title }),
				hint: matchingMessage(locale, "localTrackListHint"),
			},
			feedback: { text: "", visible: false },
			status: { error: matchingMessage(locale, "targetNotFound") },
			search: {
				bind: (children: ReactNode) => (
					<MatchingSearchPicker
						artists={artists}
						releases={searchableEditions}
						selectedReleaseId={null}
						onSelectRelease={(release) => {
							const targetId =
								release.provider === "Local"
									? release.id
									: resolveMusicBrainzEdition(release.id);
							if (!targetId) return false;
							if (release.provider === "Local")
								onSelectLocalRelease?.(String(targetId));
							else onSelectProviderRelease?.(String(targetId));
							return true;
						}}
					>
						{children}
					</MatchingSearchPicker>
				),
			},
			preparation: (
				<MatchingReleasePreparation
					key={group.id}
					releaseId={prepareReleaseId}
					selectionKey={activeTargetKey}
					resolveRelease={resolveMusicBrainzEdition}
					onPrepared={(id) => onSelectProviderRelease?.(id)}
				/>
			),
			createLocalReleaseHref,
			board: null,
			emptyBoard: {
				positions: {
					heading: boardCopy.positionsHeading(group.title),
					instruction: matchingMessage(locale, "targetNotFound"),
				},
				sources: {
					heading: boardCopy.sourcesHeading(group.fileIds.length),
					instruction: boardCopy.sourcesInstructions,
					members: group.fileIds.map((id) => ({
						key: id,
						label: files[id]?.name ?? id,
					})),
					emptyText: boardCopy.allAssigned,
				},
			},
			inspector: {
				ariaLabel: matchingMessage(locale, "ariaTrackInspector"),
				title: matchingMessage(locale, "inspectorTitle", {
					track: group.title,
				}),
				evidence: null,
				emptyText: matchingMessage(locale, "noEvidenceFile"),
			},
			footer: null,
			preview: null,
		};
	}
	const editionGroup =
		activeTarget === storedTarget && !localMode
			? group
			: {
					...group,
					mode: activeMode,
					candidateId: localMode ? group.candidateId : String(activeTarget.id),
					...(localMode ? { releaseId: String(activeTarget.id) } : {}),
				};
	const tracks = activeTarget.tracks;
	const assignments = getDraft(
		editionGroup,
		files,
		providers,
		releases,
		activeTarget,
	);
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
		activeTarget,
		files,
		assignments,
	);
	const evidencePosition = fixtureBoard.release.positions.find(
		(position) => position.track_id === inspected?.id,
	);
	const existingRelease =
		activeMode === "local"
			? releases.find(
					(release) =>
						release.provider === "Local" &&
						activeTarget.providerId != null &&
						release.providerId === activeTarget.providerId,
				)
			: releases.find(
					(release) =>
						release.providerId === activeTarget.id ||
						release.providerId === activeTarget.providerId,
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
			editionGroup,
			trackId,
			fileId,
			files,
			releases,
			providers,
			activeTarget,
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
	const setCandidate = (
		id: string,
		options: { forceRoute?: boolean; writeRoute?: boolean } = {},
	) => {
		const release = providers.find((item) => item.id === id);
		if (!release) return;
		const routedLocal =
			release.provider === "Local" && onSelectLocalRelease !== undefined;
		const writeRoute =
			options.writeRoute !== false &&
			release.provider !== "Local" &&
			release.tracks.length > 0;
		const canonicalRoute = !releaseId || releaseId === id;
		const canonicalLocal = !localReleaseId || localReleaseId === id;
		const sameActiveTarget = String(activeTarget.id) === String(id);
		const canSkip =
			!options.forceRoute &&
			!prepareReleaseId &&
			(writeRoute
				? sameActiveTarget &&
					activeMode === "provider" &&
					canonicalRoute &&
					!localMode
				: sameActiveTarget &&
					activeMode === "local" &&
					canonicalLocal &&
					!releaseId);
		if (canSkip) return;
		state.selectedFile = null;
		state.selectedTarget = null;
		state.recordingTarget = null;
		state.recordingResults = null;
		setNotice({ namespace: "matching", id: "candidateChanged" });
		clearPreview();
		if (routedLocal) {
			onSelectLocalRelease?.(String(id));
		} else {
			group.mode = "provider";
			group.candidateId = id;
			if (writeRoute) {
				rememberedTargets.current[group.id] = String(id);
				onSelectProviderRelease?.(id);
			} else {
				delete rememberedTargets.current[group.id];
				onSelectProviderRelease?.(undefined);
			}
		}
		commitState();
	};
	const openPreview = () => {
		state.plan = {
			mode: activeMode,
			groupId: group.id,
			target: structuredClone(activeTarget),
			localReleaseId: (existingRelease?.id as string | undefined) ?? null,
			assignments: structuredClone(assignments),
			inventoryVersion: state.inventoryVersion,
		} satisfies IncomingPlan;
		state.confirmDowngrade = false;
		setPreviewOpenSequence((sequence) => sequence + 1);
		commitState();
	};
	const preview =
		state.plan?.groupId === group.id &&
		state.plan.mode === activeMode &&
		String(state.plan.target.id) === String(activeTarget.id)
			? state.plan
			: null;
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
		const previewTrackIds = preview.target.tracks.map((track) =>
			String(track.id),
		);
		const currentTrackIds = activeTarget.tracks.map((track) =>
			String(track.id),
		);
		if (
			preview.mode !== activeMode ||
			String(preview.target.id) !== String(activeTarget.id) ||
			previewTrackIds.length !== currentTrackIds.length ||
			previewTrackIds.some((id, index) => id !== currentTrackIds[index])
		)
			return;
		if (preview.mode === "local") {
			const providerId = preview.target.providerId;
			if (!providerId) return;
			const current = providers.find(
				(release) =>
					release.provider === "Local" &&
					release.providerId === providerId &&
					release.id === preview.target.id,
			);
			if (!current) return;
			const exactReleases = releases.filter(
				(release) =>
					release.provider === "Local" && release.providerId === providerId,
			);
			if (exactReleases.length > 1) return;
			const targetIds = new Set(previewTrackIds);
			const existing = exactReleases[0];
			if (existing) {
				const boundIds = existing.tracks
					.filter((track) => track.localProviderTrackId)
					.map((track) => String(track.localProviderTrackId));
				if (
					new Set(boundIds).size !== boundIds.length ||
					boundIds.some((id) => !targetIds.has(id))
				)
					return;
			}
		}
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
	const openRecordingSearch = (trackId: string) => {
		const track = tracks.find((item) => String(item.id) === trackId);
		if (!track) return;
		state.recordingTarget = trackId;
		state.recordingQuery = String(track.title);
		state.recordingResults = allProviderTracks;
		setRecordingQuery(String(track.title));
		commitState();
	};
	const search = {
		bind: (children: ReactNode) => (
			<MatchingSearchPicker
				artists={artists}
				releases={searchableEditions}
				selectedReleaseId={
					activeMode === "provider" ? String(activeTarget.id) : null
				}
				onSelectRelease={(release) => {
					const targetId =
						release.provider === "Local"
							? release.id
							: resolveMusicBrainzEdition(release.id);
					if (!targetId) return false;
					setCandidate(targetId, {
						writeRoute: release.provider !== "Local",
					});
					return true;
				}}
			>
				{children}
			</MatchingSearchPicker>
		),
	};
	const preparation = (
		<MatchingReleasePreparation
			key={group.id}
			releaseId={prepareReleaseId}
			selectionKey={activeTargetKey}
			resolveRelease={resolveMusicBrainzEdition}
			onPrepared={(id) => setCandidate(id, { forceRoute: true })}
		/>
	);
	const previewModel: MatchingScreenViewPreview | null = preview
		? {
				headingRef: previewHeadingRef,
				heading: linkCopy.preview,
				releaseTitle: String(preview.target.title),
				summary: [
					linkCopy.assignedFiles(assignedFileIds.size),
					linkCopy.remainingFiles(
						fileOutcomes.filter((row) => !row.position).length,
					),
				].join(" · "),
				rows: fileOutcomes,
				notice: stale ? matchingMessage(locale, "stalePreview") : undefined,
				primary: {
					label: linkCopy.save,
					onPress: confirmLinks,
					disabled: stale || !assignedFileIds.size,
				},
				secondary: {
					label: matchingMessage(locale, "returnToObject"),
					onPress: () => {
						state.plan = null;
						commitState();
					},
				},
			}
		: null;
	const model: MatchingScreenViewWorkspace = {
		kind: "workspace",
		locale,
		heading: {
			backHref,
			backLabel: matchingMessage(locale, "incomingLink"),
			sourceDirectoryLabel: matchingMessage(locale, "sourceDirectory"),
			sourcePath: `/mnt/inbox/${group.id}`,
			title: matchingMessage(locale, "matchingTitle", { group: group.title }),
			hint:
				activeMode === "local"
					? matchingMessage(locale, "localTrackListHint")
					: matchingMessage(locale, "providerTrackListHint"),
		},
		feedback: {
			text:
				state.notice ||
				(state.uiNotice ? formatUiNotice(locale, state.uiNotice) : ""),
			visible: Boolean(state.uiNotice || state.notice),
		},
		summary: {
			assigned: matchingMessage(locale, "assignedCount", {
				assigned: Object.keys(assignments).length,
				total: tracks.length,
			}),
			free: matchingMessage(locale, "freeCount", { count: pool.length }),
			detail:
				activeMode === "local"
					? matchingMessage(locale, "localOrder")
					: [
							activeTarget.provider ?? "MusicBrainz",
							activeTarget.year,
							activeTarget.country,
							activeTarget.catalog,
						]
							.filter(Boolean)
							.join(" · "),
		},
		search,
		preparation,
		createLocalReleaseHref,
		board: {
			...fixtureBoard,
			locale,
			selectedSourceId: state.selectedFile,
			inspectedTrackId: inspected?.id,
			onSelectedSourceChange: (id) => {
				state.selectedFile = id;
				commitState();
			},
			onAssign: addSource,
			onUnassign: (fileId, trackId) =>
				mutateAssignment((draft) => unassignFile(draft, trackId, fileId)),
			onInspectPosition: (trackId) => {
				setInspectedPair(undefined);
				state.selectedTarget = trackId;
				commitState();
			},
			onInspectPair: (fileId, trackId) => {
				setInspectedPair({ groupId: group.id, fileId, trackId });
				state.selectedTarget = trackId;
				state.recordingTarget = null;
				state.recordingResults = null;
				commitState();
			},
			recordingSearch:
				activeMode === "local"
					? {
							label: matchingMessage(locale, "recordingSearch"),
							trackIds: tracks.map((track) => String(track.id)),
							onOpen: openRecordingSearch,
						}
					: null,
		},
		inspector: {
			ariaLabel: matchingMessage(locale, "ariaTrackInspector"),
			title: matchingMessage(locale, "inspectorTitle", {
				track: inspected ? String(inspected.title) : group.title,
			}),
			evidence:
				evidencePosition && evidenceFile
					? {
							fileName: evidenceFile.name,
							position: evidencePosition,
							rawTags: evidenceFile.tags,
						}
					: null,
			emptyText: matchingMessage(locale, "noEvidenceFile"),
		},
		recording: state.recordingTarget
			? {
					title: matchingMessage(locale, "recordingSearch"),
					queryLabel: matchingMessage(locale, "searchTitleArtist"),
					ariaSearchLabel: matchingMessage(locale, "ariaSearchTitleArtist"),
					query: recordingQuery,
					onQueryChange: setRecordingQuery,
					onSubmit: () => {
						state.recordingQuery = recordingQuery;
						state.recordingResults = allProviderTracks;
						commitState();
					},
					searchLabel: matchingMessage(locale, "searchRecordings"),
					closeLabel: matchingMessage(locale, "close"),
					onClose: () => {
						state.recordingTarget = null;
						commitState();
					},
					hint: matchingMessage(locale, "recordingLinkHint"),
					results: matchingRecordings.map(({ release, track }) => ({
						key: String(track.id),
						title: String(track.title),
						releaseTitle: String(release.title),
						duration: duration(track.duration),
						linkLabel: matchingMessage(locale, "linkRecording"),
						onLink: () => bindRecording(String(track.id)),
					})),
				}
			: null,
		footer: previewModel
			? null
			: {
					assignedLabel: linkCopy.assignedFiles(
						new Set(
							Object.values(assignments)
								.flatMap(sourceIds)
								.filter((id) => activeFileIds.has(id) && usableFile(files[id])),
						).size,
					),
					summary: matchingMessage(locale, "unassignedSummary", {
						empty: tracks.length - Object.keys(assignments).length,
						files: pool.length,
					}),
					previewLabel: linkCopy.preview,
					previewDisabled: !Object.values(assignments).some((item) =>
						sourceIds(item).some(
							(id) => activeFileIds.has(id) && usableFile(files[id]),
						),
					),
					onPreview: openPreview,
				},
		preview: previewModel,
		onEscape: () => {
			if (!state.selectedFile) return;
			state.selectedFile = null;
			commitState();
		},
	};
	return model;
}
