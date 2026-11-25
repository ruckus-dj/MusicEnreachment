import type { Locale } from "../../i18n/types";
import {
	type IncomingGroupMoveHandler,
	IncomingGroupsOverviewView,
	type IncomingGroupViewRow,
} from "./IncomingGroupsOverviewView";
import {
	IncomingGroupsPanel,
	IncomingPageHeading,
	incomingGroupsEmpty,
} from "./IncomingGroupsPanel";
import { type IncomingMessage, incomingMessage } from "./incomingMessages";

/**
 * Copy-ready incoming screen composition.
 *
 * The provider keeps the model, automatic processing, group workflows, route
 * hash coordination and focus restoration. This module owns the screen markup:
 * heading and action, processing stages, notice, groups panel and overview.
 * Hosts pass only resolved rows, the localized notice text, locale and the
 * interaction callbacks; no JSX or domain state crosses the boundary.
 */
export type IncomingScreenViewProps = {
	locale: Locale;
	rows: readonly IncomingGroupViewRow[];
	/** Localized notice text; an empty value leaves the notice region hidden. */
	notice?: string;
	onProcess: () => void;
	onToggleGroup: (groupId: string) => void;
	onMove: IncomingGroupMoveHandler;
	onSplit: (groupId: string, memberId: string) => void;
	disclosureRef?: (groupId: string, node: HTMLButtonElement | null) => void;
};

export function IncomingScreenView({
	locale,
	rows,
	notice,
	onProcess,
	onToggleGroup,
	onMove,
	onSplit,
	disclosureRef,
}: IncomingScreenViewProps) {
	const msg = (
		key: IncomingMessage,
		values?: Record<string, string | number>,
	) => incomingMessage(locale, key, values);
	const labels = {
		caption: msg("overviewCaption"),
		group: msg("group"),
		filesPaths: msg("uniqueFilesPaths"),
		ready: msg("readyTotal"),
		status: msg("evaluationState"),
		empty: incomingGroupsEmpty(locale),
		number: msg("columnNumber"),
		title: msg("columnTitle"),
		artist: msg("columnArtist"),
		album: msg("columnAlbum"),
		actions: msg("columnActions"),
		noTitle: msg("missingFileTitle"),
		noValue: msg("noValue"),
		expand: (name: string) => msg("filesIn", { name }),
		openGroup: (name: string) => msg("openGroup", { name }),
	};
	return (
		<section
			aria-labelledby="incoming-title"
			className="min-w-0 w-full px-4 pt-[14px] pb-[18px] max-[600px]:p-3"
		>
			<IncomingPageHeading
				locale={locale}
				action={{ label: msg("process"), onPress: onProcess }}
			/>
			<ol
				className="mb-[10px] grid list-none grid-cols-4 gap-0 rounded-[var(--radius)] border border-[var(--line)] bg-[var(--raised)] p-0 max-[900px]:grid-cols-2 max-[600px]:grid-cols-1"
				aria-label={msg("stages")}
			>
				<li className="flex min-w-0 items-center gap-2 px-[10px] py-2 text-[11px] text-[var(--muted)] max-[600px]:min-h-10">
					<b className="grid h-[22px] w-[22px] flex-[0_0_22px] place-items-center rounded-full bg-[var(--accent-soft)] font-mono text-[10px] text-[var(--accent)]">
						01
					</b>
					<span className="min-w-0">{msg("groupFiles")}</span>
				</li>
				<li className="flex min-w-0 items-center gap-2 border-l border-[var(--line)] px-[10px] py-2 text-[11px] text-[var(--muted)] max-[600px]:min-h-10 max-[600px]:border-l-0 max-[600px]:border-t max-[600px]:border-[var(--line)]">
					<b className="grid h-[22px] w-[22px] flex-[0_0_22px] place-items-center rounded-full bg-[var(--accent-soft)] font-mono text-[10px] text-[var(--accent)]">
						02
					</b>
					<span className="min-w-0">{msg("match")}</span>
				</li>
				<li className="flex min-w-0 items-center gap-2 border-l border-[var(--line)] px-[10px] py-2 text-[11px] text-[var(--muted)] max-[900px]:border-l-0 max-[900px]:border-t max-[900px]:border-[var(--line)] max-[600px]:min-h-10 max-[600px]:border-l-0 max-[600px]:border-t">
					<b className="grid h-[22px] w-[22px] flex-[0_0_22px] place-items-center rounded-full bg-[var(--accent-soft)] font-mono text-[10px] text-[var(--accent)]">
						03
					</b>
					<span className="min-w-0">
						{msg("allConfidentAutomatic")}{" "}
						<i className="text-[var(--accent)] not-italic" aria-hidden="true">
							→
						</i>{" "}
						{msg("automaticDirection")}
					</span>
				</li>
				<li className="flex min-w-0 items-center gap-2 border-l border-[var(--line)] px-[10px] py-2 text-[11px] text-[var(--muted)] max-[900px]:border-t max-[900px]:border-[var(--line)] max-[600px]:min-h-10 max-[600px]:border-l-0 max-[600px]:border-t">
					<b className="grid h-[22px] w-[22px] flex-[0_0_22px] place-items-center rounded-full bg-[var(--accent-soft)] font-mono text-[10px] text-[var(--accent)]">
						04
					</b>
					<span className="min-w-0">
						{msg("otherwiseManual")}{" "}
						<i className="text-[var(--accent)] not-italic" aria-hidden="true">
							→
						</i>{" "}
						{msg("manualDirection")}
					</span>
				</li>
			</ol>
			{notice && (
				<p
					role="status"
					className="mb-[10px] border-l-[3px] border-[var(--accent)] bg-[var(--accent-soft)] px-[10px] py-[7px] text-xs text-[var(--ink)]"
				>
					{notice}
				</p>
			)}
			<IncomingGroupsPanel locale={locale} count={rows.length}>
				<IncomingGroupsOverviewView
					labels={labels}
					rows={rows}
					onToggle={onToggleGroup}
					onMove={onMove}
					onSplit={onSplit}
					disclosureRef={disclosureRef}
				/>
			</IncomingGroupsPanel>
		</section>
	);
}
