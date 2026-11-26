import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useI18n } from "../demoPreferences";
import {
	type OperationsLocation,
	operationsLocationHref,
	parseOperationsLocation,
} from "../../../ui/features/operations/operationsLocation";
import {
	ApplicationOperationsBinder,
	type ApplicationOperationsModel,
	type ApplicationRouteProviderProps,
} from "../../../ui/app/ApplicationRouteOutlet";
import { navigateHash, useHashLocation } from "../../../ui/app/hashLocation";
import { libraryPositionHref } from "../../../ui/features/library/libraryDetailRoute";
import type { OperationsListRow } from "../../../ui/features/operations/OperationsList";
import { operationMessages } from "../../../ui/features/operations/operationMessages";
import { formatOperationTimestamp } from "../../../ui/features/operations/operationTimestamp";
import type {
	OperationsKindFilter,
	OperationsKindSelection,
	OperationsStatusFilter,
} from "../../../ui/features/operations/operationsFilters";
import { sourceMessage } from "../../../ui/features/sources/sourceInventoryMessages";
import operationKinds from "../../../ui/features/operations/operationKindLabels.json";
import { settingsMessages } from "../../../ui/features/settings/settingsFormMessages";
import { sourceCleanupOperationCopy } from "../../../ui/features/operations/sourceCleanupOperationCopy";
import { sourceScanOperationStages } from "../../../ui/features/operations/sourceScanOperationStages";
import { sourceDetailHref } from "../../../ui/features/sources/sourcesRoute";
import { matchingRecheckAbandonFixture } from "../../fixtures/matchingRecheckAbandonFixture";
import {
	type QueuedOperationFixture,
	queuedOperationFixtures,
} from "../../fixtures/operationsQueueFixtures";
import { originalPositionHref, recoveryFixture } from "../library/catalogData";
import type { DeletionOperation } from "../library/DeletionRequest";
import { libraryRootHref } from "../library/libraryRoute";
import type {
	SetupToolDownload,
	SetupToolMove,
} from "../setup/useSetupToolDownloadsDemoProvider";
import type { SourceArtifactCleanupDemoExecution } from "../sources/sourceArtifactCleanupDemoProvider";
import type { SourceRoot } from "../sources/SourcesScreen";
import type {
	DemoScanAttempt,
	DemoScanState,
} from "../sources/useSourceScanDemoProvider";
import type {
	DemoOperationStatus,
	OperationsDemoProvider,
} from "./useOperationsDemoProvider";

const pageSize = 50;
const emptyScanAttempts: readonly DemoScanAttempt[] = [];
const emptySourceRoots: readonly SourceRoot[] = [];
const emptyToolDownloads: readonly SetupToolDownload[] = [];
const emptyToolMoves: readonly SetupToolMove[] = [];

type VisibleOperationStatus = Exclude<DemoOperationStatus, "succeeded">;

interface ScreenRow {
	list: OperationsListRow;
	status: VisibleOperationStatus;
	kind?: OperationsKindFilter;
	createdAt: number;
}

const statusOrder: Record<VisibleOperationStatus, number> = {
	failed: 0,
	running: 1,
	queued: 2,
};

function matchesStatus(row: ScreenRow, filter: OperationsStatusFilter) {
	if (filter === "all") return true;
	if (filter === "attention")
		return row.status === "failed" || row.status === "running";
	return row.status === filter;
}

function scanAttemptStatus(state: DemoScanState): DemoOperationStatus {
	switch (state.kind) {
		case "queued":
			return "queued";
		case "traversing":
		case "applying":
			return "running";
		case "fixtureFailure":
			return "failed";
		default:
			return "succeeded";
	}
}

export interface DemoOperationsScreenProps {
	deletions: DeletionOperation[];
	provider: OperationsDemoProvider;
	isTargetCurrent?: (href: string) => boolean;
	/** Resolves a removal's track to its owning release in the current catalog. */
	positionHrefForTrack?: (positionId: string) => string | undefined;
	/** Scan attempts owned by the source controller; history stays outside Operations. */
	scanAttempts?: readonly DemoScanAttempt[];
	sourceRoots?: readonly SourceRoot[];
	onScanRoot?: (root: SourceRoot) => void;
	sourceCleanup?: Pick<
		SourceArtifactCleanupDemoExecution,
		"operation" | "isPending" | "failedOperations"
	>;
	queue?: readonly QueuedOperationFixture[];
	toolDownloads?: readonly SetupToolDownload[];
	onRetryToolDownload?: (id: string) => void;
	onDismissToolDownload?: (id: string) => void;
	toolMoves?: readonly SetupToolMove[];
	onRetryToolMove?: (id: string) => void;
	onDismissToolMove?: (id: string) => void;
}

export function DemoOperationsBinder({
	children,
	...props
}: DemoOperationsScreenProps & ApplicationRouteProviderProps) {
	const model = useDemoOperationsModel(props);
	return (
		<ApplicationOperationsBinder model={model}>
			{children}
		</ApplicationOperationsBinder>
	);
}

function useDemoOperationsModel({
	deletions,
	provider,
	isTargetCurrent,
	positionHrefForTrack,
	scanAttempts = emptyScanAttempts,
	sourceRoots = emptySourceRoots,
	onScanRoot,
	sourceCleanup,
	queue = queuedOperationFixtures,
	toolDownloads = emptyToolDownloads,
	onRetryToolDownload,
	onDismissToolDownload,
	toolMoves = emptyToolMoves,
	onRetryToolMove,
	onDismissToolMove,
}: DemoOperationsScreenProps): ApplicationOperationsModel {
	const { locale } = useI18n();
	const copy = operationMessages[locale];
	const hash = useHashLocation();
	const location = parseOperationsLocation(hash);
	const [query, setQuery] = useState("");
	const [abandonOpen, setAbandonOpen] = useState(false);
	const [demoStartedAt] = useState(() =>
		Math.max(Date.now(), (queue[0]?.createdAt ?? 0) + 60_000),
	);
	const updateLocation = useCallback(
		(next: Partial<OperationsLocation>, replace = false) => {
			const href = operationsLocationHref({
				status: location.status,
				kind: location.kind,
				sort: location.sort,
				direction: location.direction,
				offset: location.offset,
				...next,
			});
			navigateHash(href, replace);
		},
		[
			location.status,
			location.kind,
			location.sort,
			location.direction,
			location.offset,
		],
	);
	const deletionRows = useRef(
		new WeakMap<DeletionOperation, { id: string; createdAt: number }>(),
	);
	const nextDeletionId = useRef(0);
	const collator = useMemo(
		() => new Intl.Collator(locale, { sensitivity: "base", numeric: true }),
		[locale],
	);

	const rows = useMemo(() => {
		const result: ScreenRow[] = [];
		function currentHref(href?: string) {
			return href && (!isTargetCurrent || isTargetCurrent(href))
				? href
				: undefined;
		}

		function addRow(
			rowStatus: DemoOperationStatus,
			rowKind: OperationsKindFilter | undefined,
			row: Omit<OperationsListRow, "state" | "stateTone">,
			createdAt = demoStartedAt,
		) {
			if (rowStatus === "succeeded") return;
			result.push({
				status: rowStatus,
				kind: rowKind,
				createdAt,
				list: {
					...row,
					created: formatOperationTimestamp(createdAt, locale),
					targetHref: currentHref(row.targetHref),
					error: rowStatus === "failed" ? row.error : undefined,
					actions: rowStatus === "failed" ? row.actions : undefined,
					state: copy[rowStatus],
					stateTone: rowStatus === "failed" ? "error" : "info",
				},
			});
		}

		addRow(provider.signals, "publication", {
			id: "signals",
			target: copy.signalsTrack,
			targetHref: matchingRecheckAbandonFixture.targetHref,
			operation: copy.publishTrack,
			error: `${copy.writeAccessFailure} ${copy.prepareAgain}`,
			actions: [
				{ id: "retry", label: copy.retry, onPress: provider.retrySignals },
				{
					id: "abandon",
					label: copy.abandon,
					onPress: () => setAbandonOpen(true),
				},
			],
		});
		addRow(provider.recovery, "publication", {
			id: "recovery",
			target: copy.recoveryTrack,
			targetHref: originalPositionHref(recoveryFixture.trackId),
			operation: copy.publishTrack,
			error: copy.knownTargetMissingResult,
			actions: [
				{ id: "retry", label: copy.retry, onPress: provider.retryRecovery },
			],
		});

		for (const operation of deletions) {
			let identity = deletionRows.current.get(operation);
			if (!identity) {
				identity = {
					id: `removal-${++nextDeletionId.current}`,
					createdAt: Math.max(
						Date.now(),
						demoStartedAt + nextDeletionId.current,
					),
				};
				deletionRows.current.set(operation, identity);
			}
			const state = provider.deletionStates.get(operation);
			const failure = state?.failure;
			const singleTrackId =
				operation.kind === "publication" &&
				operation.libraryTrackIds?.length === 1
					? operation.libraryTrackIds[0]
					: undefined;
			addRow(
				state?.status ?? "queued",
				"removal",
				{
					id: identity.id,
					target: operation.target || copy.unknownTarget,
					targetDetail: operation.scope,
					targetHref: singleTrackId
						? positionHrefForTrack?.(singleTrackId)
						: undefined,
					operation:
						operation.kind === "publication"
							? copy.removePublications
							: operation.kind === "artist"
								? copy.removeArtist
								: copy.removeLibrary,
					error:
						failure === "stale"
							? copy.removalStale
							: failure === "conflict"
								? copy.removalConflict
								: copy.removalRetry,
					stage:
						failure === "stale"
							? copy.staleScope
							: failure === "conflict"
								? copy.removalStateConflict
								: failure === "retry"
									? copy.removalStateRetry
									: undefined,
					actions:
						failure === "stale"
							? [
									{
										id: "review",
										label: copy.returnAndConfirm,
										href: libraryRootHref(),
									},
								]
							: [
									{
										id: "retry",
										label: copy.retry,
										onPress: () => provider.retryDeletion(operation),
									},
								],
				},
				identity.createdAt,
			);
		}

		for (const job of provider.publicationJobs ?? []) {
			addRow(
				provider.publicationStates?.get(job.id) ?? "queued",
				"publication",
				{
					id: job.id,
					target: job.title,
					targetHref: libraryPositionHref(job.releaseId, job.positionId),
					operation: copy.publishTrack,
					error: `${copy.staleScope} ${copy.prepareAgain}`,
				},
				job.createdAt,
			);
		}

		for (const attempt of toolDownloads) {
			const { kind, release } = attempt.preflight;
			const packageName = kind === "ffmpeg" ? "FFmpeg" : "fpcalc";
			addRow(
				attempt.status,
				undefined,
				{
					id: attempt.id,
					target: `${packageName}, ${locale === "ru" ? "версия" : "version"} ${release}`,
					operation: operationKinds.kinds.install[locale],
					stage:
						attempt.status === "running"
							? locale === "ru"
								? "Загрузка файлов"
								: "Downloading files"
							: undefined,
					error: settingsMessages[locale].staleInstall,
					actions:
						attempt.status === "failed"
							? [
									...(onRetryToolDownload
										? [
												{
													id: "retry",
													label: copy.retry,
													onPress: () => onRetryToolDownload(attempt.id),
												},
											]
										: []),
									...(onDismissToolDownload
										? [
												{
													id: "dismiss",
													label: copy.dismiss,
													onPress: () => onDismissToolDownload(attempt.id),
												},
											]
										: []),
								]
							: undefined,
				},
				attempt.createdAt,
			);
		}

		for (const move of toolMoves) {
			addRow(
				move.status,
				undefined,
				{
					id: move.id,
					target: locale === "ru" ? "Папка инструментов" : "Tools folder",
					operation: operationKinds.kinds.move_tools_root[locale],
					stage:
						move.status === "running"
							? locale === "ru"
								? "Копирование"
								: "Copying"
							: move.status === "queued"
								? locale === "ru"
									? "В очереди"
									: "Queued"
								: undefined,
					error: settingsMessages[locale].staleMove,
					actions:
						move.status === "failed"
							? [
									...(onRetryToolMove
										? [
												{
													id: "retry",
													label: copy.retry,
													onPress: () => onRetryToolMove(move.id),
												},
											]
										: []),
									...(onDismissToolMove
										? [
												{
													id: "dismiss",
													label: copy.dismiss,
													onPress: () => onDismissToolMove(move.id),
												},
											]
										: []),
								]
							: undefined,
				},
				move.createdAt,
			);
		}

		for (const attempt of scanAttempts) {
			const root = sourceRoots.find((item) => item.id === attempt.rootId);
			if (!root) continue;
			const status = scanAttemptStatus(attempt.state);
			addRow(
				status,
				"analysis",
				{
					id: attempt.id,
					target: root.name,
					targetHref: sourceDetailHref(root.id),
					operation: operationKinds.kinds.scan_source[locale],
					stage: attempt.phase
						? sourceScanOperationStages[attempt.phase][locale]
						: undefined,
					error: sourceMessage(locale, "scanFailedFixture"),
					actions:
						status === "failed" &&
						onScanRoot &&
						root.path === attempt.path &&
						root.mode === attempt.mode
							? [
									{
										id: "retry",
										label: copy.retry,
										onPress: () => onScanRoot(root),
									},
								]
							: undefined,
				},
				attempt.createdAt,
			);
		}

		const cleanups = [...(sourceCleanup?.failedOperations ?? [])];
		if (sourceCleanup?.isPending && sourceCleanup.operation)
			cleanups.push(sourceCleanup.operation);
		for (const attempt of cleanups) {
			addRow(
				attempt.state,
				"removal",
				{
					id: attempt.id,
					target: sourceCleanupOperationCopy.target[locale],
					operation:
						operationKinds.kinds.cleanup_source_analysis_artifacts[locale],
					stage: sourceCleanupOperationCopy.stages[attempt.stage][locale],
					error: attempt.safe_error,
				},
				attempt.createdAt,
			);
		}

		for (const fixture of queue) {
			result.push({
				status: "queued",
				kind: fixture.kind,
				createdAt: fixture.createdAt,
				list: {
					id: fixture.id,
					target: fixture.target,
					targetDetail: fixture.targetDetail,
					targetHref: currentHref(fixture.targetHref),
					operation: copy[fixture.operation],
					state: copy.queued,
					stateTone: "info",
					created: formatOperationTimestamp(fixture.createdAt, locale),
				},
			});
		}

		return result;
	}, [
		copy,
		demoStartedAt,
		deletions,
		isTargetCurrent,
		locale,
		onScanRoot,
		onRetryToolDownload,
		onDismissToolDownload,
		onRetryToolMove,
		onDismissToolMove,
		queue,
		scanAttempts,
		sourceCleanup,
		sourceRoots,
		toolDownloads,
		toolMoves,
		provider.deletionStates,
		provider.publicationJobs,
		provider.publicationStates,
		provider.recovery,
		provider.retryDeletion,
		provider.retryRecovery,
		provider.retrySignals,
		provider.signals,
	]);

	const normalizedQuery = query.trim().slice(0, 200).toLocaleLowerCase(locale);
	const filteredRows = useMemo(
		() =>
			rows.filter(
				(row) =>
					matchesStatus(row, location.status) &&
					(location.kind === "all" || row.kind === location.kind) &&
					(normalizedQuery === "" ||
						row.list.target
							.toLocaleLowerCase(locale)
							.includes(normalizedQuery)),
			),
		[location.kind, location.status, locale, normalizedQuery, rows],
	);

	// Clamp after deletions shrink the result set so the view never goes blank.
	const lastOffset = Math.max(
		0,
		Math.floor((filteredRows.length - 1) / pageSize) * pageSize,
	);
	useEffect(() => {
		if (location.offset > lastOffset)
			updateLocation({ offset: lastOffset }, true);
	}, [lastOffset, location.offset, updateLocation]);

	const start = Math.min(location.offset, lastOffset);
	const sortedRows = useMemo(() => {
		const direction = location.direction === "asc" ? 1 : -1;
		return [...filteredRows].sort((left, right) => {
			switch (location.sort) {
				case "target":
				case "operation":
					return (
						direction *
						collator.compare(
							left.list[location.sort],
							right.list[location.sort],
						)
					);
				case "state":
					return (
						direction * (statusOrder[left.status] - statusOrder[right.status])
					);
				case "created":
					return direction * (left.createdAt - right.createdAt);
				default:
					return 0;
			}
		});
	}, [collator, filteredRows, location.direction, location.sort]);
	const pageRows = sortedRows.slice(start, start + pageSize);

	const pagination =
		filteredRows.length > 0
			? {
					from: start + 1,
					to: Math.min(start + pageSize, filteredRows.length),
					total: filteredRows.length,
					onPrevious:
						start > 0
							? () =>
									updateLocation({
										offset: Math.max(0, start - pageSize),
									})
							: undefined,
					onNext:
						start + pageSize < filteredRows.length
							? () => updateLocation({ offset: start + pageSize })
							: undefined,
				}
			: undefined;

	const changeQuery = (value: string) => {
		setQuery(value);
		if (location.offset !== 0) updateLocation({ offset: 0 });
	};
	const changeStatus = (value: OperationsStatusFilter) => {
		updateLocation({ status: value, offset: 0 });
	};
	const changeKind = (value: OperationsKindSelection) => {
		updateLocation({ kind: value, offset: 0 });
	};

	return {
		locale,
		rows: pageRows.map((row) => row.list),
		notice: provider.notice ? copy[provider.notice] : undefined,
		onRefresh: provider.refresh,
		isRefreshing: provider.isRefreshing,
		filters: {
			query,
			onQueryChange: changeQuery,
			status: location.status,
			onStatusChange: changeStatus,
			kind: location.kind,
			onKindChange: changeKind,
		},
		sortColumn: location.sort,
		sortDirection: location.direction,
		onSortChange: (sort, direction) =>
			updateLocation({ sort, direction, offset: 0 }),
		pagination,
		matchingRecheckAbandon:
			abandonOpen && provider.signals === "failed"
				? {
						positions: matchingRecheckAbandonFixture.positions,
						busy: false,
						onConfirm: () => {
							setAbandonOpen(false);
							provider.abandonSignals();
						},
						onCancel: () => setAbandonOpen(false),
					}
				: undefined,
	};
}
