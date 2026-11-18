import type { ReactNode, Ref } from "react";
import { Button as AriaButton, Link as AriaLink } from "react-aria-components";

/**
 * Portable operations list.
 *
 * This module is intentionally self-contained: it imports only React,
 * React Aria and uses Tailwind utilities. Both copy and rows are explicit
 * props so the component can be copied into the production frontend without
 * prototype fixtures, i18n runtime or an application data provider.
 */

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
}

const panelClass =
	"min-w-0 overflow-hidden rounded-[var(--radius)] border border-[var(--line)] bg-[var(--surface)]";
const rowGridClass =
	"grid gap-x-4 gap-y-2 px-[14px] py-3 min-[560px]:grid-cols-2 min-[1200px]:grid-cols-[minmax(0,1.5fr)_minmax(0,1.1fr)_minmax(0,1.2fr)_minmax(0,.55fr)_minmax(0,.8fr)_minmax(0,1.1fr)_minmax(10rem,auto)] min-[1200px]:items-start";
const headGridClass = `${rowGridClass} hidden bg-[var(--raised)] text-[11px] font-semibold tracking-[.04em] text-[var(--muted)] uppercase min-[1200px]:grid`;
const labelClass =
	"mb-0.5 block text-[11px] font-semibold text-[var(--muted)] min-[1200px]:sr-only";
const valueClass = "block min-w-0 [overflow-wrap:anywhere]";
const emptyValueClass = "block min-w-0 text-[var(--muted)]";
const badgeClass =
	"inline-flex w-max max-w-full items-center rounded-sm px-1.5 py-0.5 text-[11px]/[1.4]";
const buttonClass =
	"react-aria-Button inline-flex min-h-[30px] pointer-coarse:min-h-[40px] cursor-pointer items-center justify-center gap-1.5 rounded-[var(--control-radius)] border border-[var(--control)] bg-[var(--surface)] px-[9px] py-[5px] text-xs text-[var(--ink)] no-underline hover:border-[var(--accent)] hover:bg-[var(--raised)] active:bg-[var(--accent-soft)] disabled:cursor-not-allowed disabled:border-[var(--line)] disabled:bg-[var(--raised)] disabled:text-[var(--muted)] focus-visible:outline-2 focus-visible:outline-[var(--focus)] focus-visible:outline-offset-2";
const linkClass =
	"react-aria-Link inline-flex min-h-[30px] items-center text-xs text-[var(--accent)] no-underline decoration-1 underline-offset-2 hover:underline focus-visible:outline-2 focus-visible:outline-[var(--focus)] focus-visible:outline-offset-2";

const stateToneClass: Record<OperationsStateTone, string> = {
	neutral: "bg-[var(--raised)] text-[var(--muted)]",
	info: "bg-[var(--info-soft)] text-[var(--info)]",
	warning: "bg-[var(--warning-soft)] text-[var(--warning)]",
	error: "bg-[var(--error-soft)] text-[var(--error)]",
};

function FieldLabel({ children }: { children: ReactNode }) {
	return <span className={labelClass}>{children}</span>;
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
}: OperationsListProps) {
	return (
		<section
			className="grid min-w-0 gap-[14px]"
			aria-busy={status === "loading" || undefined}
		>
			<header className="flex flex-wrap items-end justify-between gap-3 border-b border-[var(--line)] pb-[14px]">
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
			</header>
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
						<>
							<div className={headGridClass} aria-hidden="true">
								<span>{copy.target}</span>
								<span>{copy.operation}</span>
								<span>{copy.stateStage}</span>
								<span>{copy.progress}</span>
								<span>{copy.created}</span>
								<span>{copy.error}</span>
								<span className="min-[1200px]:text-right">{copy.actions}</span>
							</div>
							<ul className="m-0 grid list-none p-0">
								{rows.map((row) => (
									<li
										key={row.id}
										className="border-b border-[var(--line)] last:border-b-0"
									>
										<div className={rowGridClass}>
											<div className="min-w-0">
												<FieldLabel>{copy.target}</FieldLabel>
												<span className={`${valueClass} font-medium`}>
													{row.target}
												</span>
												{row.targetDetail && (
													<span className="mt-0.5 block text-[11px] text-[var(--muted)] [overflow-wrap:anywhere]">
														{row.targetDetail}
													</span>
												)}
											</div>
											<div className="min-w-0">
												<FieldLabel>{copy.operation}</FieldLabel>
												<span className={valueClass}>{row.operation}</span>
											</div>
											<div className="min-w-0">
												<FieldLabel>{copy.stateStage}</FieldLabel>
												<div className="flex flex-wrap items-center gap-1.5">
													<span
														className={`${badgeClass} ${stateToneClass[row.stateTone ?? "neutral"]}`}
													>
														{row.state}
													</span>
													{row.stage && (
														<span className="text-[11px] text-[var(--muted)]">
															{row.stage}
														</span>
													)}
												</div>
											</div>
											<div className="min-w-0">
												<FieldLabel>{copy.progress}</FieldLabel>
												<span
													className={
														row.progress ? valueClass : emptyValueClass
													}
												>
													{row.progress || "—"}
												</span>
											</div>
											<div className="min-w-0">
												<FieldLabel>{copy.created}</FieldLabel>
												<span
													className={row.created ? valueClass : emptyValueClass}
												>
													{row.created || "—"}
												</span>
											</div>
											<div className="min-w-0">
												<FieldLabel>{copy.error}</FieldLabel>
												{row.error ? (
													<span className="block min-w-0 text-[var(--error)] [overflow-wrap:anywhere]">
														{row.error}
													</span>
												) : (
													<span className={emptyValueClass}>—</span>
												)}
											</div>
											<div className="min-w-0 min-[560px]:col-span-2 min-[1200px]:col-auto">
												<FieldLabel>{copy.actions}</FieldLabel>
												{row.actions && row.actions.length > 0 ? (
													<div className="flex flex-wrap items-start gap-[6px] min-[1200px]:justify-end">
														{row.actions.map((action) =>
															action.href ? (
																<AriaLink
																	key={action.id}
																	className={linkClass}
																	href={action.href}
																>
																	{action.label}
																</AriaLink>
															) : (
																<AriaButton
																	key={action.id}
																	className={buttonClass}
																	onPress={(event) =>
																		action.onPress?.(event.target)
																	}
																	isDisabled={
																		action.isDisabled || action.isPending
																	}
																>
																	{action.label}
																</AriaButton>
															),
														)}
													</div>
												) : (
													<span className={emptyValueClass}>—</span>
												)}
											</div>
										</div>
									</li>
								))}
							</ul>
						</>
					)}
				</div>
			)}
			<p className="m-0 max-w-[75ch] text-[11px] text-[var(--muted)]">
				{copy.footnote}
			</p>
		</section>
	);
}
