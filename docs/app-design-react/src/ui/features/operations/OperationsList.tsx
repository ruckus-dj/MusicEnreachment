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
}

export interface OperationsListPagination {
	label: string;
	onPrevious?: () => void;
	onNext?: () => void;
}

export interface OperationsListProps {
	copy: OperationsListCopy;
	rows: OperationsListRow[];
	status?: OperationsListStatus;
	/** Safe failure text for the error state. Falls back to copy.loadError. */
	error?: string;
	/** Action feedback for the latest user action. */
	notice?: string;
	actionError?: string;
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
	"min-w-0 overflow-hidden rounded-[var(--radius)] border border-[var(--line)] bg-[var(--surface)]";
const buttonClass =
	"react-aria-Button inline-flex min-h-[30px] pointer-coarse:min-h-[40px] cursor-pointer items-center justify-center gap-1.5 rounded-[var(--control-radius)] border border-[var(--control)] bg-[var(--surface)] px-[9px] py-[5px] text-xs text-[var(--ink)] no-underline hover:border-[var(--accent)] hover:bg-[var(--raised)] active:bg-[var(--accent-soft)] disabled:cursor-not-allowed disabled:border-[var(--line)] disabled:bg-[var(--raised)] disabled:text-[var(--muted)] focus-visible:outline-2 focus-visible:outline-[var(--focus)] focus-visible:outline-offset-2";
const rowButtonClass =
	"react-aria-Button inline-flex min-h-[26px] shrink-0 cursor-pointer items-center justify-center rounded-[var(--control-radius)] border border-[var(--control)] bg-[var(--surface)] px-1.5 py-0.5 text-[11px] whitespace-nowrap text-[var(--ink)] no-underline hover:border-[var(--accent)] hover:bg-[var(--raised)] active:bg-[var(--accent-soft)] disabled:cursor-not-allowed disabled:border-[var(--line)] disabled:bg-[var(--raised)] disabled:text-[var(--muted)] focus-visible:outline-2 focus-visible:outline-[var(--focus)] focus-visible:outline-offset-2";
const linkClass =
	"react-aria-Link inline-flex min-h-[30px] items-center text-xs text-[var(--accent)] no-underline decoration-1 underline-offset-2 hover:underline focus-visible:outline-2 focus-visible:outline-[var(--focus)] focus-visible:outline-offset-2";
const targetLinkClass =
	"block min-w-0 truncate font-medium text-[var(--accent)] no-underline decoration-1 underline-offset-2 hover:underline focus-visible:outline-2 focus-visible:outline-[var(--focus)] focus-visible:outline-offset-2";
const badgeClass =
	"inline-flex w-max max-w-full items-center rounded-sm px-1.5 py-0.5 text-[11px]/[1.4]";
const tableClass =
	"w-full min-w-[760px] table-fixed border-separate border-spacing-0 text-xs [font-variant-numeric:tabular-nums]";
const headCellClass =
	"border-b border-[var(--line)] bg-[var(--raised)] px-1.5 py-1.5 text-[11px] leading-[1.4] font-semibold tracking-[.04em] text-[var(--muted)] uppercase";
const sortableHeadCellClass = `${headCellClass} cursor-pointer select-none transition-colors hover:bg-[var(--accent-soft)] data-[pressed]:bg-[var(--accent-soft)] data-[sort-direction]:text-[var(--ink)] data-[focus-visible]:outline-2 data-[focus-visible]:outline-[var(--focus)] data-[focus-visible]:outline-offset-[-2px]`;
const sortableWidths: Record<OperationsListSortColumn, string> = {
	target: "w-[27%]",
	operation: "w-[16%]",
	state: "w-[21%]",
	created: "w-[14%]",
};
const cellClass = "border-b border-[var(--line)] px-1.5 py-[3px] align-middle";
const selectButtonClass =
	"react-aria-Button flex min-h-[30px] min-w-[104px] cursor-pointer items-center justify-between gap-1.5 rounded-[var(--control-radius)] border border-[var(--control)] bg-[var(--surface)] px-2 py-1 text-left text-xs text-[var(--ink)] hover:border-[var(--accent)] hover:bg-[var(--raised)] focus-visible:outline-2 focus-visible:outline-[var(--focus)] focus-visible:outline-offset-2";
const popoverClass =
	"react-aria-Popover z-50 max-h-[280px] min-w-[var(--trigger-width)] overflow-auto rounded-[var(--radius)] border border-[var(--line)] bg-[var(--surface)] p-1 text-[var(--ink)] shadow-[0_8px_24px_#0002]";
const optionClass =
	"react-aria-ListBoxItem cursor-pointer rounded-sm px-2 py-[7px] text-xs outline-none data-[focused]:bg-[var(--raised)] data-[selected]:bg-[var(--accent-soft)] data-[selected]:text-[var(--accent)] data-[focus-visible]:outline-2 data-[focus-visible]:outline-[var(--focus)] data-[focus-visible]:outline-offset-[-2px]";

const stateToneClass: Record<OperationsStateTone, string> = {
	neutral: "bg-[var(--raised)] text-[var(--muted)]",
	info: "bg-[var(--info-soft)] text-[var(--info)]",
	warning: "bg-[var(--warning-soft)] text-[var(--warning)]",
	error: "bg-[var(--error-soft)] text-[var(--error)]",
};

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
						className={size === "row" ? `${linkClass} min-h-[26px]` : linkClass}
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
			<AriaLabel className="m-0 shrink-0 text-[11px] text-[var(--muted)]">
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
			<dt className="text-[11px] text-[var(--muted)]">{label}</dt>
			<dd className="m-0 min-w-0 [overflow-wrap:anywhere]">{children}</dd>
		</>
	);
}

function OperationDetails({
	copy,
	row,
	onClose,
}: {
	copy: OperationsListCopy;
	row: OperationsListRow;
	onClose: () => void;
}) {
	return (
		<AriaModalOverlay
			isOpen
			isDismissable
			onOpenChange={(open) => {
				if (!open) onClose();
			}}
			className="fixed inset-0 z-[100] grid place-items-center overflow-auto overscroll-contain bg-[#0008] p-3"
		>
			<AriaModal className="max-h-[calc(100dvh-24px)] w-full max-w-[620px] overflow-auto rounded-[var(--radius)] border border-[var(--line)] bg-[var(--surface)] p-4 text-[var(--ink)] shadow-[0_16px_48px_#0003]">
				<AriaDialog className="wrap-anywhere outline-none">
					<AriaHeading
						slot="title"
						className="m-0 text-base leading-tight [text-wrap:balance]"
					>
						{copy.details}
					</AriaHeading>
					<p className="mt-1 mb-0 text-[13px] font-medium [overflow-wrap:anywhere]">
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
								<span className="mt-0.5 block text-[11px] text-[var(--muted)]">
									{row.targetDetail}
								</span>
							)}
						</DetailField>
						<DetailField label={copy.operation}>{row.operation}</DetailField>
						<DetailField label={copy.stateStage}>
							<span className="flex flex-wrap items-center gap-1.5">
								<StateBadge state={row.state} tone={row.stateTone} />
								{row.stage && (
									<span className="text-[11px] text-[var(--muted)]">
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
	const selectedRow = selectedId
		? rows.find((row) => row.id === selectedId)
		: undefined;
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
			<div className="flex flex-wrap items-end justify-between gap-3 border-b border-[var(--line)] pb-[14px]">
				<div className="grid min-w-0 gap-[5px]">
					<h1
						ref={headingRef}
						tabIndex={-1}
						className="m-0 text-[clamp(21px,2.5vw,28px)] leading-[1.18] [text-wrap:balance]"
					>
						{copy.title}
					</h1>
					<p className="m-0 max-w-[75ch] text-[var(--muted)]">
						{copy.description}
					</p>
				</div>
				{onRefresh && (
					<AriaButton
						className={buttonClass}
						onPress={onRefresh}
						isDisabled={status === "loading" || isRefreshing}
					>
						{isRefreshing || status === "loading"
							? copy.refreshing
							: copy.refresh}
					</AriaButton>
				)}
			</div>
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
			{actionError && (
				<p
					role="alert"
					className="m-0 text-[var(--error)] [overflow-wrap:anywhere]"
				>
					{actionError}
				</p>
			)}
			{filters && (
				<div className="flex min-w-0 flex-wrap items-center gap-2">
					<label className="flex min-h-[30px] min-w-0 flex-[1_1_220px] items-center rounded-[var(--control-radius)] border border-[var(--control)] bg-[var(--surface)] px-2 focus-within:outline-2 focus-within:outline-[var(--focus)] focus-within:outline-offset-1">
						<span className="sr-only">{copy.search}</span>
						<input
							type="search"
							value={filters.query}
							onChange={(event) => filters.onQueryChange(event.target.value)}
							placeholder={copy.search}
							className="min-h-7 w-full min-w-0 border-0 bg-transparent p-0 text-xs text-[var(--ink)] outline-none placeholder:text-[var(--muted)]"
						/>
					</label>
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
				</div>
			)}
			{(status !== "error" || rows.length > 0) && (
				<div className={panelClass}>
					{status === "loading" && rows.length === 0 ? (
						<p className="m-0 p-[18px] text-[var(--muted)]" role="status">
							{copy.loading}
						</p>
					) : rows.length === 0 ? (
						<div className="grid gap-1 p-[18px]">
							<h2 className="m-0 text-[15px]">{copy.emptyTitle}</h2>
							<p className="m-0 max-w-[75ch] text-[var(--muted)]">
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
									{rows.map((row) => (
										<AriaRow
											key={row.id}
											id={row.id}
											className="h-9 transition-colors hover:bg-[var(--raised)] [&:last-child>td]:border-b-0"
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
															className="block min-w-0 truncate text-[11px] text-[var(--muted)]"
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
													<StateBadge state={row.state} tone={row.stateTone} />
													{row.stage && (
														<span
															title={row.stage}
															className="min-w-0 truncate text-[11px] text-[var(--muted)]"
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
												<div className="flex min-w-0 flex-nowrap items-center justify-end gap-1">
													<AriaButton
														className={rowButtonClass}
														aria-haspopup="dialog"
														onPress={() => setSelectedId(row.id)}
													>
														{copy.details}
													</AriaButton>
													{row.actions && (
														<Actions
															actions={row.actions.filter(
																(action) => !action.href,
															)}
														/>
													)}
												</div>
											</AriaCell>
										</AriaRow>
									))}
								</AriaTableBody>
							</AriaTable>
						</div>
					)}
				</div>
			)}
			{pagination && rows.length > 0 && (
				<div className="flex flex-wrap items-center justify-between gap-2">
					<p className="m-0 text-[11px] text-[var(--muted)]">
						{pagination.label}
					</p>
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
			<p className="m-0 max-w-[75ch] text-[11px] text-[var(--muted)]">
				{copy.footnote}
			</p>
			{selectedRow && (
				<OperationDetails
					copy={copy}
					row={selectedRow}
					onClose={() => setSelectedId(null)}
				/>
			)}
		</section>
	);
}
