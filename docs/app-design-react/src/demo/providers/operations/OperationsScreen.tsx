import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useI18n } from "../../../app/i18n";
import { useHashLocation } from "../../../app/navigation/hashLocation";
import {
	type OperationsLocation,
	operationsLocationHref,
	parseOperationsLocation,
} from "../../../app/navigation/operationsLocation";
import {
	ApplicationOperationsBinder,
	type ApplicationOperationsModel,
	type ApplicationRouteProviderProps,
} from "../../../ui/app/ApplicationRouteOutlet";
import type { OperationsListRow } from "../../../ui/features/operations/OperationsList";
import { operationMessages } from "../../../ui/features/operations/operationMessages";
import type {
	OperationsKindFilter,
	OperationsKindSelection,
	OperationsStatusFilter,
} from "../../../ui/features/operations/operationsFilters";
import { matchingRecheckAbandonFixture } from "../../fixtures/matchingRecheckAbandonFixture";
import {
	type QueuedOperationFixture,
	queuedOperationFixtures,
} from "../../fixtures/operationsQueueFixtures";
import { recoveryFixture } from "../library/catalogData";
import type { DeletionOperation } from "../library/DeletionRequest";
import type {
	DemoOperationStatus,
	OperationsDemoProvider,
} from "./useOperationsDemoProvider";

const pageSize = 50;

type VisibleOperationStatus = Exclude<DemoOperationStatus, "succeeded">;

interface ScreenRow {
	list: OperationsListRow;
	status: VisibleOperationStatus;
	kind: OperationsKindFilter;
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

export interface DemoOperationsScreenProps {
	deletions: DeletionOperation[];
	provider: OperationsDemoProvider;
	isTargetCurrent?: (href: string) => boolean;
	queue?: readonly QueuedOperationFixture[];
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
	queue = queuedOperationFixtures,
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
			if (replace) window.location.replace(href);
			else window.location.hash = href;
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
	const dates = useMemo(
		() =>
			new Intl.DateTimeFormat(locale, {
				dateStyle: "short",
				timeStyle: "short",
			}),
		[locale],
	);
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
			rowKind: OperationsKindFilter,
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
					created: dates.format(createdAt),
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
			operation: copy.publishPositions,
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
			targetHref: `#track/${recoveryFixture.trackId}`,
			operation: copy.publishPositions,
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
					targetHref: singleTrackId ? `#track/${singleTrackId}` : undefined,
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
										href: "#library",
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
					targetHref: `#track/${job.positionId}`,
					operation: copy.publishPositions,
					error: `${copy.staleScope} ${copy.prepareAgain}`,
				},
				job.createdAt,
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
					created: dates.format(fixture.createdAt),
				},
			});
		}

		return result;
	}, [
		copy,
		dates,
		demoStartedAt,
		deletions,
		isTargetCurrent,
		queue,
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
