import { useEffect, useMemo, useRef, useState } from "react";
import { useI18n } from "../../../app/i18n";
import type { OperationsListRow } from "../../../ui/features/operations/OperationsList";
import {
	type OperationsKindFilter,
	type OperationsKindSelection,
	OperationsScreenView,
	type OperationsSortColumn,
	type OperationsSortDirection,
	type OperationsStatusFilter,
} from "../../../ui/features/operations/OperationsScreenView";
import type { DeletionOperation } from "../../components/DeletionRequest";
import { matchingRecheckAbandonFixture } from "../../fixtures/matchingRecheckAbandonFixture";
import { queuedOperationFixtures } from "../../fixtures/operationsQueueFixtures";
import { recoveryFixture } from "../library/catalogData";
import { operationMessages } from "./operationMessages";
import type {
	DemoOperationStatus,
	OperationsDemoProvider,
} from "./useOperationsDemoProvider";

const pageSize = 50;
const latestQueueSample = queuedOperationFixtures[0]?.createdAt ?? 0;

type VisibleOperationStatus = Exclude<DemoOperationStatus, "succeeded">;

interface ScreenRow {
	list: OperationsListRow;
	status: VisibleOperationStatus;
	kind: OperationsKindFilter;
	createdAt: number;
}

const defaultSort: {
	column: OperationsSortColumn;
	direction: OperationsSortDirection;
} = {
	column: "created",
	direction: "desc",
};
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

export function OperationsScreen({
	deletions,
	provider,
	isTargetCurrent,
}: {
	deletions: DeletionOperation[];
	provider: OperationsDemoProvider;
	isTargetCurrent?: (href: string) => boolean;
}) {
	const { locale } = useI18n();
	const copy = operationMessages[locale];
	const [query, setQuery] = useState("");
	const [status, setStatus] = useState<OperationsStatusFilter>("attention");
	const [kind, setKind] = useState<OperationsKindSelection>("all");
	const [page, setPage] = useState(0);
	const [sort, setSort] = useState(defaultSort);
	const [abandonOpen, setAbandonOpen] = useState(false);
	const [demoStartedAt] = useState(() =>
		Math.max(Date.now(), latestQueueSample + 60_000),
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

		for (const fixture of queuedOperationFixtures) {
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
		provider.deletionStates,
		provider.publicationJobs,
		provider.publicationStates,
		provider.recovery,
		provider.retryDeletion,
		provider.retryRecovery,
		provider.retrySignals,
		provider.signals,
	]);

	const normalizedQuery = query.trim().toLocaleLowerCase(locale);
	const filteredRows = useMemo(
		() =>
			rows.filter(
				(row) =>
					matchesStatus(row, status) &&
					(kind === "all" || row.kind === kind) &&
					(normalizedQuery === "" ||
						row.list.target
							.toLocaleLowerCase(locale)
							.includes(normalizedQuery)),
			),
		[kind, locale, normalizedQuery, rows, status],
	);

	const pageCount = Math.max(1, Math.ceil(filteredRows.length / pageSize));
	const pageIndex = Math.min(page, pageCount - 1);

	// Clamp after deletions shrink the result set so the view never goes blank.
	useEffect(() => {
		if (pageIndex !== page) setPage(pageIndex);
	}, [page, pageIndex]);

	const start = pageIndex * pageSize;
	const sortedRows = useMemo(() => {
		const direction = sort.direction === "asc" ? 1 : -1;
		return [...filteredRows].sort((left, right) => {
			switch (sort.column) {
				case "target":
				case "operation":
					return (
						direction *
						collator.compare(left.list[sort.column], right.list[sort.column])
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
	}, [collator, filteredRows, sort]);
	const pageRows = sortedRows.slice(start, start + pageSize);

	const pagination =
		filteredRows.length > 0
			? {
					from: start + 1,
					to: Math.min(start + pageSize, filteredRows.length),
					total: filteredRows.length,
					onPrevious: pageIndex > 0 ? () => setPage(pageIndex - 1) : undefined,
					onNext:
						pageIndex < pageCount - 1
							? () => setPage(pageIndex + 1)
							: undefined,
				}
			: undefined;

	const changeQuery = (value: string) => {
		setQuery(value);
		setPage(0);
	};
	const changeStatus = (value: OperationsStatusFilter) => {
		setStatus(value);
		setPage(0);
	};
	const changeKind = (value: OperationsKindSelection) => {
		setKind(value);
		setPage(0);
	};

	return (
		<OperationsScreenView
			copy={copy}
			locale={locale}
			rows={pageRows.map((row) => row.list)}
			notice={provider.notice ? copy[provider.notice] : undefined}
			onRefresh={provider.refresh}
			isRefreshing={provider.isRefreshing}
			filters={{
				query,
				onQueryChange: changeQuery,
				status,
				onStatusChange: changeStatus,
				kind,
				onKindChange: changeKind,
			}}
			sortColumn={sort.column}
			sortDirection={sort.direction}
			onSortChange={(column, direction) => {
				setSort({ column, direction });
				setPage(0);
			}}
			pagination={pagination}
			matchingRecheckAbandon={
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
					: undefined
			}
		/>
	);
}
