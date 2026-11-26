import { type ReactNode, type Ref, useEffect, useRef, useState } from "react";
import {
	Button as AriaButton,
	Cell as AriaCell,
	Column as AriaColumn,
	Dialog as AriaDialog,
	Heading as AriaHeading,
	Label as AriaLabel,
	Link as AriaLink,
	ListBox as AriaListBox,
	ListBoxItem as AriaListBoxItem,
	Modal as AriaModal,
	ModalOverlay as AriaModalOverlay,
	Popover as AriaPopover,
	Row as AriaRow,
	Select as AriaSelect,
	SelectValue as AriaSelectValue,
	Table as AriaTable,
	TableBody as AriaTableBody,
	TableHeader as AriaTableHeader,
	type SortDescriptor,
	type SortDirection,
} from "react-aria-components";
import { ActiveFilterTags } from "../../components/ActiveFilterTags";
import { AppSearchField } from "../../components/AppSearchField";
import { AppToolbar } from "../../components/AppToolbar";
import { PageHeader } from "../../components/PageHeader";
import {
	RowActionMenu,
	type RowActionMenuItem,
} from "../../components/RowActionMenu";
import { ui } from "../../components/uiStyles";
import type { Locale } from "../../i18n/types";

/**
 * Portable operations list.
 *
 * This module is intentionally self-contained: it imports only React,
 * React Aria and uses Tailwind utilities. Copy, rows, filters and pagination
 * are explicit props so the component can be copied into the production
 * frontend without prototype fixtures, i18n runtime or an application data
 * provider.
 *
 * The caller owns page slicing: rows are expected to be one page (50 items per
 * the product contract) and `pagination.label` describes that page.
 */

/** Columns whose headers can change the row order. `actions` is not sortable. */
export type OperationsListSortColumn =
	| "target"
	| "operation"
	| "state"
	| "created";

export interface OperationsListCopy {
	title: string;
	description: string;
	refresh: string;
	refreshing: string;
	loading: string;
	loadError: string;
	retryLoad: string;
	emptyTitle: string;
	emptyDescription: string;
	target: string;
	operation: string;
	stateStage: string;
	progress: string;
	created: string;
	error: string;
	actions: string;
	footnote: string;
	search: string;
	statusFilter: string;
	kindFilter: string;
	details: string;
	close: string;
	previous: string;
	next: string;
}

export type OperationsStateTone = "neutral" | "info" | "warning" | "error";

export interface OperationsListAction {
	id: string;
	label: string;
	/** Navigation action; renders a link instead of a button. */
	href?: string;
	onPress?: (trigger: Element) => void;
	isPending?: boolean;
	isDisabled?: boolean;
	/** Rare, destructive secondary action, rendered with the error tone. */
	isDestructive?: boolean;
}

export interface OperationsListRow {
	id: string;
	target: string;
	/** Navigation target for the target column, independent from Details. */
	targetHref?: string;
	/** Secondary line under the target, for example the confirmed scope. */
	targetDetail?: string;
	operation: string;
	state: string;
	stateTone?: OperationsStateTone;
	/** Current stage within a running operation. */
	stage?: string;
	/** Caller-formatted progress, for example "42%". */
	progress?: string;
	/** Caller-formatted creation time. */
	created?: string;
	/** Safe, user-facing failure text. Never raw diagnostics. */
	error?: string;
	actions?: OperationsListAction[];
}

export type OperationsListStatus = "ready" | "loading" | "error";

export interface OperationsListFilterOption {
	value: string;
	label: string;
}

export interface OperationsListFilters {
	query: string;
	onQueryChange: (query: string) => void;
	status: string;
	statusOptions: readonly OperationsListFilterOption[];
	onStatusChange: (status: string) => void;
	kind: string;
	kindOptions: readonly OperationsListFilterOption[];
	onKindChange: (kind: string) => void;
	/**
	 * Resets query, status and kind to the canonical defaults in one update.
	 * Hosts that navigate on each callback should provide it to avoid hash races.
	 */
	onResetFilters?: () => void;
}

export interface OperationsListPagination {
	label: string;
	onPrevious?: () => void;
	onNext?: () => void;
}

export interface OperationsListProps {
	copy: OperationsListCopy;
	/** Locale for filter summary labels; falls back to English. */
	locale?: Locale;
	rows: OperationsListRow[];
	status?: OperationsListStatus;
	/** Safe failure text for the error state. Falls back to copy.loadError. */
	error?: string;
	/** Action feedback for the latest user action. */
	notice?: string;
	actionError?: OperationsActionError;
	headingRef?: Ref<HTMLHeadingElement>;
	onRefresh?: () => void;
	isRefreshing?: boolean;
	filters?: OperationsListFilters;
	pagination?: OperationsListPagination;
	/**
	 * Controlled sort state, using React Aria's `SortDescriptor`.
	 * `column` is one of `OperationsListSortColumn` for changes initiated here.
	 */
	sortDescriptor?: SortDescriptor;
	/**
	 * Enables sortable headers for `target`, `operation`, `state` and `created`.
	 * The caller sorts its full filtered set, then passes the current page.
	 */
	onSortChange?: (descriptor: SortDescriptor) => void;
}

const panelClass =
	"min-w-0 overflow-hidden rounded-[var(--table-radius)] border border-[var(--line)] bg-[var(--surface)]";
const buttonClass =
	"react-aria-Button inline-flex min-h-9 pointer-coarse:min-h-10 cursor-pointer items-center justify-center gap-2 rounded-[var(--control-radius)] border border-[var(--line)] bg-[var(--surface)] px-3 py-1.5 text-sm font-medium text-[var(--ink)] no-underline hover:border-[var(--control)] hover:bg-[var(--raised)] data-[pressed]:bg-[var(--raised)] disabled:cursor-not-allowed disabled:border-[var(--line)] disabled:bg-[var(--raised)] disabled:text-[var(--muted)] focus-visible:outline-2 focus-visible:outline-[var(--accent)] focus-visible:outline-offset-2";
const rowButtonClass =
	"react-aria-Button inline-flex min-h-8 shrink-0 cursor-pointer items-center justify-center rounded-lg border border-[var(--line)] bg-[var(--surface)] px-2.5 py-1 text-sm whitespace-nowrap text-[var(--ink)] no-underline hover:border-[var(--control)] hover:bg-[var(--raised)] data-[pressed]:bg-[var(--raised)] disabled:cursor-not-allowed disabled:border-[var(--line)] disabled:bg-[var(--raised)] disabled:text-[var(--muted)] focus-visible:outline-2 focus-visible:outline-[var(--accent)] focus-visible:outline-offset-2";
const linkClass =
	"react-aria-Link inline-flex min-h-9 items-center text-sm text-[var(--accent-strong)] no-underline underline-offset-2 hover:underline focus-visible:outline-2 focus-visible:outline-[var(--accent)] focus-visible:outline-offset-2";
const targetLinkClass =
	"block min-w-0 truncate font-medium text-[var(--accent-strong)] no-underline underline-offset-2 hover:underline focus-visible:outline-2 focus-visible:outline-[var(--accent)] focus-visible:outline-offset-2";
const badgeClass =
	"inline-flex w-max max-w-full items-center rounded-full border px-2 py-0.5 text-xs font-medium";
const tableClass =
	"w-full min-w-[760px] table-fixed border-separate border-spacing-0 text-sm [font-variant-numeric:tabular-nums]";
const headCellClass =
	"border-b border-[var(--line)] bg-[var(--raised)] px-4 py-2.5 text-xs leading-[1.4] font-medium text-[var(--muted)]";
const sortableHeadCellClass = `${headCellClass} cursor-pointer select-none transition-colors hover:bg-[var(--accent-soft)] data-[pressed]:bg-[var(--accent-soft)] data-[sort-direction]:text-[var(--ink)] data-[focus-visible]:outline-2 data-[focus-visible]:outline-[var(--accent)] data-[focus-visible]:outline-offset-[-2px]`;
const sortableWidths: Record<OperationsListSortColumn, string> = {
	target: "w-[27%]",
	operation: "w-[16%]",
	state: "w-[21%]",
	created: "w-[14%]",
};
const cellClass = "border-b border-[var(--line)] px-4 py-3 align-middle";
const selectButtonClass =
	"react-aria-Button flex h-9 min-w-[120px] cursor-pointer items-center justify-between gap-2 rounded-lg border border-[var(--line)] bg-[var(--surface)] px-3 text-left text-sm text-[var(--ink)] hover:border-[var(--control)] hover:bg-[var(--raised)] focus-visible:outline-2 focus-visible:outline-[var(--accent)] focus-visible:outline-offset-0";
const popoverClass = ui.popover;
const optionClass = ui.option;

const stateToneClass: Record<OperationsStateTone, string> = {
	neutral: "border-[var(--line)] bg-[var(--raised)] text-[var(--muted)]",
	info: "border-[color-mix(in_srgb,var(--info)_25%,transparent)] bg-[var(--info-soft)] text-[var(--info)]",
	warning:
		"border-[color-mix(in_srgb,var(--warning)_30%,transparent)] bg-[var(--warning-soft)] text-[var(--warning)]",
	error:
		"border-[color-mix(in_srgb,var(--error)_25%,transparent)] bg-[var(--error-soft)] text-[var(--error)]",
};

/** Canonical defaults; a removed chip restores the safe attention view. */
const defaultFilterStatus = "attention";
const defaultFilterKind = "all";
const filterTagCopy = {
	en: {
		query: (value: string) => `Search: ${value}`,
		status: (value: string) => `Status: ${value}`,
		kind: (value: string) => `Kind: ${value}`,
	},
	ru: {
		query: (value: string) => `Поиск: ${value}`,
		status: (value: string) => `Статус: ${value}`,
		kind: (value: string) => `Тип: ${value}`,
	},
} as const satisfies Record<
	Locale,
	{
		query: (value: string) => string;
		status: (value: string) => string;
		kind: (value: string) => string;
	}
>;

function optionLabel(
	options: readonly OperationsListFilterOption[],
	value: string,
) {
	return options.find((option) => option.value === value)?.label ?? value;
}

function rowMenuAction(action: OperationsListAction): RowActionMenuItem {
	return {
		id: action.id,
		label: action.label,
		href: action.href,
		onAction: action.onPress,
		isDisabled: action.isDisabled,
		isPending: action.isPending,
		isDestructive: action.isDestructive,
	};
}

function SortHeaderLabel({
	label,
	direction,
}: {
	label: string;
	direction?: SortDirection;
}) {
	return (
		<span className="grid grid-cols-[minmax(0,1fr)_12px] items-center gap-1">
			<span>{label}</span>
			<svg
				aria-hidden="true"
				viewBox="0 0 12 12"
				className={`size-3 ${direction ? "text-[var(--accent)]" : "text-[var(--control)]"}`}
			>
				<path
					d={
						direction === "ascending"
							? "M3 7.5 6 4.5 9 7.5"
							: direction === "descending"
								? "M3 4.5 6 7.5 9 4.5"
								: "M3.5 4.25 6 1.75 8.5 4.25 M3.5 7.75 6 10.25 8.5 7.75"
					}
					fill="none"
					stroke="currentColor"
					strokeWidth="1.2"
					strokeLinecap="round"
					strokeLinejoin="round"
				/>
			</svg>
		</span>
	);
}

function StateBadge({
	state,
	tone = "neutral",
}: {
	state: string;
	tone?: OperationsStateTone;
}) {
	return (
		<span className={`${badgeClass} ${stateToneClass[tone]}`}>{state}</span>
	);
}

function Actions({
	actions,
	size = "row",
}: {
	actions: OperationsListAction[];
	size?: "row" | "dialog";
}) {
	const className = size === "row" ? rowButtonClass : buttonClass;
	return (
		<>
			{actions.map((action) =>
				action.href ? (
					<AriaLink
						key={action.id}
						className={size === "row" ? `${linkClass} min-h-8` : linkClass}
						href={action.href}
					>
						{action.label}
					</AriaLink>
				) : (
					<AriaButton
						key={action.id}
						className={className}
						onPress={(event) => action.onPress?.(event.target)}
						isDisabled={action.isDisabled || action.isPending}
					>
						{action.label}
					</AriaButton>
				),
			)}
		</>
	);
}

function chevron() {
	return (
		<svg
			aria-hidden="true"
			viewBox="0 0 12 12"
			className="size-3 shrink-0 text-[var(--muted)]"
		>
			<path
				d="M2.5 4.5 6 8l3.5-3.5"
				fill="none"
				stroke="currentColor"
				strokeWidth="1.5"
				strokeLinecap="round"
				strokeLinejoin="round"
			/>
		</svg>
	);
}

function FilterSelect({
	label,
	value,
	options,
	onChange,
}: {
	label: string;
	value: string;
	options: readonly OperationsListFilterOption[];
	onChange: (value: string) => void;
}) {
	return (
		<AriaSelect
			className="flex min-w-0 shrink-0 items-center gap-1.5"
			selectedKey={value}
			onSelectionChange={(key) => {
				if (key != null) onChange(String(key));
			}}
		>
			<AriaLabel className="m-0 shrink-0 text-sm text-[var(--muted)]">
				{label}
			</AriaLabel>
			<AriaButton className={selectButtonClass}>
				<AriaSelectValue className="min-w-0 truncate" />
				{chevron()}
			</AriaButton>
			<AriaPopover className={popoverClass}>
				<AriaListBox className="grid gap-0.5 outline-none">
					{options.map((option) => (
						<AriaListBoxItem
							key={option.value}
							id={option.value}
							className={optionClass}
						>
							{option.label}
						</AriaListBoxItem>
					))}
				</AriaListBox>
			</AriaPopover>
		</AriaSelect>
	);
}

function DetailField({
	label,
	children,
}: {
	label: string;
	children: ReactNode;
}) {
	return (
		<>
			<dt className="text-xs text-[var(--muted)]">{label}</dt>
			<dd className="m-0 min-w-0 [overflow-wrap:anywhere]">{children}</dd>
		</>
	);
}

export type OperationsActionError = { rowId: string; message: string };

function OperationActionAlert({ message }: { message: string }) {
	return (
		<p
			role="alert"
			className="m-0 text-[var(--error)] [overflow-wrap:anywhere]"
		>
			{message}
		</p>
	);
}

function OperationDetails({
	copy,
	row,
	actionError,
	onClose,
}: {
	copy: OperationsListCopy;
	row: OperationsListRow;
	actionError?: string;
	onClose: () => void;
}) {
	return (
		<AriaModalOverlay
			isOpen
			isDismissable
			onOpenChange={(open) => {
				if (!open) onClose();
			}}
			className="fixed inset-0 z-[100] grid place-items-center overflow-auto overscroll-contain bg-[rgb(0_0_0/0.45)] p-3"
		>
			<AriaModal className="max-h-[calc(100dvh-24px)] w-full max-w-[620px] overflow-auto rounded-[var(--radius)] border border-[var(--line)] bg-[var(--surface)] p-6 text-[var(--ink)] shadow-[0_24px_64px_rgb(0_0_0/0.24)]">
				<AriaDialog className="wrap-anywhere outline-none">
					<AriaHeading
						slot="title"
						className="m-0 text-lg font-semibold leading-tight [text-wrap:balance]"
					>
						{copy.details}
					</AriaHeading>
					<p className="mt-1 mb-0 text-sm font-medium [overflow-wrap:anywhere]">
						{row.targetHref ? (
							<AriaLink className={linkClass} href={row.targetHref}>
								{row.target}
							</AriaLink>
						) : (
							row.target
						)}
					</p>
					<dl className="mt-3 mb-0 grid grid-cols-[minmax(88px,.35fr)_minmax(0,1fr)] gap-x-3 gap-y-1.5 text-xs max-[480px]:grid-cols-1 max-[480px]:gap-y-0.5">
						<DetailField label={copy.target}>
							{row.target}
							{row.targetDetail && (
								<span className="mt-0.5 block text-xs text-[var(--muted)]">
									{row.targetDetail}
								</span>
							)}
						</DetailField>
						<DetailField label={copy.operation}>{row.operation}</DetailField>
						<DetailField label={copy.stateStage}>
							<span className="flex flex-wrap items-center gap-1.5">
								<StateBadge state={row.state} tone={row.stateTone} />
								{row.stage && (
									<span className="text-xs text-[var(--muted)]">
										{row.stage}
									</span>
								)}
							</span>
						</DetailField>
						{row.progress && (
							<DetailField label={copy.progress}>{row.progress}</DetailField>
						)}
						<DetailField label={copy.created}>{row.created || "—"}</DetailField>
						<DetailField label={copy.error}>
							{row.error ? (
								<span className="text-[var(--error)]">{row.error}</span>
							) : (
								<span className="text-[var(--muted)]">—</span>
							)}
						</DetailField>
						<DetailField label={copy.actions}>
							{row.actions && row.actions.length > 0 ? (
								<span className="flex flex-wrap items-center gap-1.5">
									<Actions actions={row.actions} size="dialog" />
								</span>
							) : (
								<span className="text-[var(--muted)]">—</span>
							)}
						</DetailField>
					</dl>
					{actionError && (
						<div className="mt-3 text-xs">
							<OperationActionAlert message={actionError} />
						</div>
					)}
					<div className="mt-4 flex justify-end">
						<AriaButton className={buttonClass} onPress={onClose}>
							{copy.close}
						</AriaButton>
					</div>
				</AriaDialog>
			</AriaModal>
		</AriaModalOverlay>
	);
}

export function OperationsList({
	copy,
	locale = "en",
	rows,
	status = "ready",
	error,
	notice,
	actionError,
	headingRef,
	onRefresh,
	isRefreshing = false,
	filters,
	pagination,
	sortDescriptor,
	onSortChange,
}: OperationsListProps) {
	const [selectedId, setSelectedId] = useState<string | null>(null);
	const sectionRef = useRef<HTMLElement>(null);
	const queryInputRef = useRef<HTMLInputElement>(null);
	const selectedRow = selectedId
		? rows.find((row) => row.id === selectedId)
		: undefined;
	const detailError =
		actionError?.rowId === selectedRow?.id ? actionError?.message : undefined;
	const filterTags: { id: string; label: string }[] = [];
	if (filters) {
		const tags = filterTagCopy[locale];
		if (filters.query.trim())
			filterTags.push({ id: "query", label: tags.query(filters.query) });
		if (filters.status !== defaultFilterStatus)
			filterTags.push({
				id: "status",
				label: tags.status(optionLabel(filters.statusOptions, filters.status)),
			});
		if (filters.kind !== defaultFilterKind)
			filterTags.push({
				id: "kind",
				label: tags.kind(optionLabel(filters.kindOptions, filters.kind)),
			});
	}
	const resetFilters = () => {
		if (!filters) return;
		if (filters.onResetFilters) {
			filters.onResetFilters();
			return;
		}
		filters.onQueryChange("");
		filters.onStatusChange(defaultFilterStatus);
		filters.onKindChange(defaultFilterKind);
	};
	const removeFilter = (id: string) => {
		if (!filters) return;
		if (id === "query") filters.onQueryChange("");
		else if (id === "status") filters.onStatusChange(defaultFilterStatus);
		else if (id === "kind") filters.onKindChange(defaultFilterKind);
	};
	useEffect(() => {
		if (selectedId === null || selectedRow) return;
		setSelectedId(null);
		sectionRef.current?.querySelector("h1")?.focus({ preventScroll: true });
	}, [selectedId, selectedRow]);

	return (
		<section
			ref={sectionRef}
			className="grid min-w-0 gap-[14px]"
			aria-busy={status === "loading" || undefined}
		>
			<PageHeader
				title={copy.title}
				subtitle={copy.description}
				headingRef={headingRef}
				actions={
					onRefresh ? (
						<AriaButton
							className={buttonClass}
							onPress={onRefresh}
							isDisabled={status === "loading" || isRefreshing}
						>
							{isRefreshing || status === "loading"
								? copy.refreshing
								: copy.refresh}
						</AriaButton>
					) : undefined
				}
			/>
			{notice && (
				<p
					className="m-0 rounded-[var(--control-radius)] bg-[var(--accent-soft)] px-3 py-2 text-[var(--ink)]"
					role="status"
					aria-live="polite"
				>
					{notice}
				</p>
			)}
			{status === "error" && (
				<div className={`${panelClass} grid gap-3 p-[18px]`} role="alert">
					<p className="m-0 text-[var(--error)] [overflow-wrap:anywhere]">
						{error || copy.loadError}
					</p>
					{onRefresh && (
						<div>
							<AriaButton
								className={buttonClass}
								onPress={onRefresh}
								isDisabled={isRefreshing}
							>
								{copy.retryLoad}
							</AriaButton>
						</div>
					)}
				</div>
			)}
			{actionError && !detailError && (
				<OperationActionAlert message={actionError.message} />
			)}
			{filters && (
				<>
					<AppToolbar label={copy.title}>
						<AppSearchField
							aria-label={copy.search}
							placeholder={copy.search}
							value={filters.query}
							onChange={filters.onQueryChange}
							inputRef={queryInputRef}
							className="w-full max-w-[420px] flex-[1_1_260px]"
						/>
						<FilterSelect
							label={copy.statusFilter}
							value={filters.status}
							options={filters.statusOptions}
							onChange={filters.onStatusChange}
						/>
						<FilterSelect
							label={copy.kindFilter}
							value={filters.kind}
							options={filters.kindOptions}
							onChange={filters.onKindChange}
						/>
					</AppToolbar>
					<ActiveFilterTags
						locale={locale}
						items={filterTags}
						onRemove={removeFilter}
						onReset={resetFilters}
						focusTargetRef={queryInputRef}
					/>
				</>
			)}
			{(status !== "error" || rows.length > 0) && (
				<div className={panelClass}>
					{status === "loading" && rows.length === 0 ? (
						<p className="m-0 p-6 text-sm text-[var(--muted)]" role="status">
							{copy.loading}
						</p>
					) : rows.length === 0 ? (
						<div className="grid gap-1 p-6">
							<h2 className="m-0 text-base font-semibold">{copy.emptyTitle}</h2>
							<p className="m-0 max-w-[75ch] text-sm text-[var(--muted)]">
								{copy.emptyDescription}
							</p>
						</div>
					) : (
						<div className="w-full overflow-x-auto [overscroll-behavior-inline:contain]">
							<AriaTable
								aria-label={copy.title}
								className={tableClass}
								sortDescriptor={sortDescriptor}
								onSortChange={onSortChange}
							>
								<AriaTableHeader>
									{(
										[
											["target", copy.target],
											["operation", copy.operation],
											["state", copy.stateStage],
											["created", copy.created],
										] as const
									).map(([column, label]) => (
										<AriaColumn
											key={column}
											id={column}
											isRowHeader={column === "target"}
											allowsSorting={onSortChange !== undefined}
											className={`${onSortChange ? sortableHeadCellClass : headCellClass} ${sortableWidths[column]} text-left`}
										>
											{({ allowsSorting, sortDirection }) =>
												allowsSorting ? (
													<SortHeaderLabel
														label={label}
														direction={sortDirection}
													/>
												) : (
													label
												)
											}
										</AriaColumn>
									))}
									<AriaColumn className={`${headCellClass} w-[22%] text-right`}>
										{copy.actions}
									</AriaColumn>
								</AriaTableHeader>
								<AriaTableBody>
									{rows.map((row) => {
										const primaryAction = row.actions?.find(
											(action) => !action.href && action.id === "retry",
										);
										const secondaryActions = (row.actions ?? []).filter(
											(action) => action !== primaryAction,
										);
										return (
											<AriaRow
												key={row.id}
												id={row.id}
												className="min-h-14 transition-colors hover:bg-[var(--raised)] [&:last-child>td]:border-b-0"
											>
												<AriaCell className={cellClass}>
													<div className="min-w-0">
														{row.targetHref ? (
															<AriaLink
																href={row.targetHref}
																className={targetLinkClass}
															>
																{row.target}
															</AriaLink>
														) : (
															<span
																title={row.target}
																className="block min-w-0 truncate font-medium"
															>
																{row.target}
															</span>
														)}
														{row.targetDetail && (
															<span
																title={row.targetDetail}
																className="block min-w-0 truncate text-xs text-[var(--muted)]"
															>
																{row.targetDetail}
															</span>
														)}
													</div>
												</AriaCell>
												<AriaCell className={cellClass}>
													<span
														title={row.operation}
														className="block min-w-0 truncate"
													>
														{row.operation}
													</span>
												</AriaCell>
												<AriaCell className={cellClass}>
													<div className="flex min-w-0 items-center gap-1.5">
														<StateBadge
															state={row.state}
															tone={row.stateTone}
														/>
														{row.stage && (
															<span
																title={row.stage}
																className="min-w-0 truncate text-xs text-[var(--muted)]"
															>
																{row.stage}
															</span>
														)}
													</div>
												</AriaCell>
												<AriaCell className={cellClass}>
													<span
														title={row.created}
														className={
															row.created
																? "block truncate"
																: "block whitespace-nowrap text-[var(--muted)]"
														}
													>
														{row.created || "—"}
													</span>
												</AriaCell>
												<AriaCell className={`${cellClass} text-right`}>
													<div className="flex min-w-0 flex-wrap items-center justify-end gap-1">
														<AriaButton
															className={rowButtonClass}
															aria-haspopup="dialog"
															onPress={() => setSelectedId(row.id)}
														>
															{copy.details}
														</AriaButton>
														{primaryAction && (
															<AriaButton
																className={rowButtonClass}
																onPress={(event) =>
																	primaryAction.onPress?.(event.target)
																}
																isDisabled={
																	primaryAction.isDisabled ||
																	primaryAction.isPending
																}
															>
																{primaryAction.label}
															</AriaButton>
														)}
														{secondaryActions.length > 0 && (
															<RowActionMenu
																label={`${copy.actions}: ${row.target}`}
																actions={secondaryActions.map(rowMenuAction)}
															/>
														)}
													</div>
												</AriaCell>
											</AriaRow>
										);
									})}
								</AriaTableBody>
							</AriaTable>
						</div>
					)}
				</div>
			)}
			{pagination && rows.length > 0 && (
				<div className="flex flex-wrap items-center justify-between gap-2">
					<p className="m-0 text-sm text-[var(--muted)]">{pagination.label}</p>
					<div className="flex items-center gap-1.5">
						<AriaButton
							className={buttonClass}
							onPress={pagination.onPrevious}
							isDisabled={!pagination.onPrevious}
						>
							{copy.previous}
						</AriaButton>
						<AriaButton
							className={buttonClass}
							onPress={pagination.onNext}
							isDisabled={!pagination.onNext}
						>
							{copy.next}
						</AriaButton>
					</div>
				</div>
			)}
			<p className="m-0 max-w-[75ch] text-sm text-[var(--muted)]">
				{copy.footnote}
			</p>
			{selectedRow && (
				<OperationDetails
					copy={copy}
					row={selectedRow}
					actionError={detailError}
					onClose={() => setSelectedId(null)}
				/>
			)}
		</section>
	);
}
