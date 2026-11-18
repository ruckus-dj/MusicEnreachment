import { useEffect, useMemo, useRef, useState } from "react";
import type { SortDescriptor } from "react-aria-components";
import { recoveryFixture } from "../../catalogData";
import type { DeletionOperation } from "../../DeletionRequest";
import { queuedOperationFixtures } from "../../fixtures/operationsQueueFixtures";
import type {
	DemoOperationStatus,
	OperationsDemoProvider,
} from "../../fixtures/useOperationsDemoProvider";
import { useI18n } from "../../i18n";
import { OperationsList, type OperationsListRow } from "./OperationsList";
import { operationMessages } from "./operationMessages";

const pageSize = 50;
const latestQueueSample = queuedOperationFixtures[0]?.createdAt ?? 0;

type OperationStatusFilter =
	| "attention"
	| "all"
	| "failed"
	| "running"
	| "queued";

type VisibleOperationStatus = Exclude<DemoOperationStatus, "succeeded">;

type OperationKind = "publication" | "analysis" | "removal" | "matching";

interface ScreenRow {
	list: OperationsListRow;
	status: VisibleOperationStatus;
	kind: OperationKind;
	createdAt: number;
}

const defaultSort: SortDescriptor = {
	column: "created",
	direction: "descending",
};
const statusOrder: Record<VisibleOperationStatus, number> = {
	failed: 0,
	running: 1,
	queued: 2,
};

const statusFilters: OperationStatusFilter[] = [
	"attention",
	"all",
	"failed",
	"running",
	"queued",
];

const kindFilters: OperationKind[] = [
	"publication",
	"analysis",
	"removal",
	"matching",
];

function matchesStatus(row: ScreenRow, filter: OperationStatusFilter) {
	if (filter === "all") return true;
	if (filter === "attention")
		return row.status === "failed" || row.status === "running";
	return row.status === filter;
}

function isStatusFilter(value: string): value is OperationStatusFilter {
	return statusFilters.some((candidate) => candidate === value);
}

function isKindFilter(value: string): value is OperationKind {
	return kindFilters.some((candidate) => candidate === value);
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
	const [status, setStatus] = useState<OperationStatusFilter>("attention");
	const [kind, setKind] = useState<OperationKind | "all">("all");
	const [page, setPage] = useState(0);
	const [sort, setSort] = useState(defaultSort);
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
			rowKind: OperationKind,
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
			targetHref: "#track/signals-t6",
			operation: copy.publishPositions,
			error: `${copy.writeAccessFailure} ${copy.prepareAgain}`,
			actions: [
				{ id: "retry", label: copy.retry, onPress: provider.retrySignals },
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
		provider.recovery,
		provider.retryDeletion,
		provider.retryRecovery,
		provider.retrySignals,
		provider.signals,
	]);

	const statusOptions = useMemo(
		() =>
			statusFilters.map((value) => ({
				value,
				label:
					value === "attention"
						? copy.attention
						: value === "all"
							? copy.allStatuses
							: copy[value],
			})),
		[copy],
	);

	const kindOptions = useMemo(
		() => [
			{ value: "all", label: copy.allKinds },
			...kindFilters.map((value) => ({
				value,
				label:
					value === "publication"
						? copy.kindPublication
						: value === "analysis"
							? copy.kindAnalysis
							: value === "removal"
								? copy.kindRemoval
								: copy.kindMatching,
			})),
		],
		[copy],
	);

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

	const numberFormat = useMemo(() => new Intl.NumberFormat(locale), [locale]);
	const start = pageIndex * pageSize;
	const sortedRows = useMemo(() => {
		const direction = sort.direction === "ascending" ? 1 : -1;
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

	const changeQuery = (value: string) => {
		setQuery(value);
		setPage(0);
	};
	const changeStatus = (value: string) => {
		if (isStatusFilter(value)) setStatus(value);
		setPage(0);
	};
	const changeKind = (value: string) => {
		if (isKindFilter(value)) setKind(value);
		else if (value === "all") setKind("all");
		setPage(0);
	};

	return (
		<OperationsList
			copy={copy}
			rows={pageRows.map((row) => row.list)}
			sortDescriptor={sort}
			onSortChange={(value) => {
				setSort(value);
				setPage(0);
			}}
			notice={provider.notice ? copy[provider.notice] : undefined}
			onRefresh={provider.refresh}
			isRefreshing={provider.isRefreshing}
			filters={{
				query,
				onQueryChange: changeQuery,
				status,
				statusOptions,
				onStatusChange: changeStatus,
				kind,
				kindOptions,
				onKindChange: changeKind,
			}}
			pagination={
				filteredRows.length > 0
					? {
							label: copy.paginationRange
								.replace("{from}", numberFormat.format(start + 1))
								.replace(
									"{to}",
									numberFormat.format(
										Math.min(start + pageSize, filteredRows.length),
									),
								)
								.replace("{total}", numberFormat.format(filteredRows.length)),
							onPrevious:
								pageIndex > 0 ? () => setPage(pageIndex - 1) : undefined,
							onNext:
								pageIndex < pageCount - 1
									? () => setPage(pageIndex + 1)
									: undefined,
						}
					: undefined
			}
		/>
	);
}
