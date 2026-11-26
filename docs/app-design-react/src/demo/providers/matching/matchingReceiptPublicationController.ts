import { useState } from "react";
import type {
	IncomingFiles,
	IncomingReleases,
	MatchingLinkReceipt,
} from "../incoming/incomingModel";
import type { CatalogArtist } from "../library/catalogData";
import {
	fixturePublicationDestination,
	type PublicationDestination,
} from "../publication/publicationOutput";
import {
	type MatchingReceiptPublicationNotice,
	type MatchingReceiptPublicationPreview,
	type MatchingReceiptPublicationRow,
	prepareMatchingReceiptPublication,
} from "./matchingReceiptPublication";
import type { MatchingReceiptPublicationQueuePort } from "./matchingReceiptPublicationQueue";

/** One exact receipt position that a scoped apply did not publish. */
export type MatchingReceiptPublicationFailure = {
	trackId: string;
	notice: MatchingReceiptPublicationNotice;
};

export type MatchingReceiptPublicationOutcome = {
	/** Positions the operator asked to publish in this confirmation. */
	attempted: number;
	published: number;
	failed: readonly MatchingReceiptPublicationFailure[];
	rejected: readonly MatchingReceiptPublicationFailure[];
};

export type MatchingReceiptPublicationController = {
	rows: readonly MatchingReceiptPublicationRow[];
	/** Positions with an explicit prepared preview; an empty set is unprepared. */
	prepared: ReadonlySet<string>;
	/** Positions with a scheduler job that has not reached a terminal state. */
	pendingJobs: ReadonlyMap<string, string>;
	/** Terminal failure notices, keyed by position. */
	failures: ReadonlyMap<string, MatchingReceiptPublicationNotice>;
	included: ReadonlySet<string>;
	selectedPositionId: string;
	selectedRow: MatchingReceiptPublicationRow | undefined;
	canPrepare: boolean;
	canConfirm: boolean;
	preparing: boolean;
	outcome: MatchingReceiptPublicationOutcome | null;
	/** Explicit saved-file choice; `null` means none was made. */
	sourceSelection: (trackId: string) => string | null;
	toggleInclude: (trackId: string) => void;
	selectPosition: (trackId: string) => void;
	selectSource: (trackId: string, sourceId: string | null) => void;
	refresh: () => void;
	confirm: () => void;
};

/**
 * Per-position selections for one saved matching result.
 *
 * A sole linked file is selected explicitly. When a position has several
 * linked files, the initial entry is an explicit `null`, so the provider
 * cannot fall back to the saved assignment choice or a quality-derived pick:
 * the operator must choose the file to publish.
 */
function explicitSelections(
	receipt: MatchingLinkReceipt,
	chosen: Readonly<Record<string, string | null>>,
): Record<string, string | null> {
	const selections: Record<string, string | null> = {};
	for (const position of receipt.positions) {
		const trackId = String(position.trackId);
		const requested = chosen[trackId];
		const fallback =
			position.sourceIds.length === 1 ? (position.sourceIds[0] ?? null) : null;
		selections[trackId] =
			requested !== undefined &&
			(requested === null || position.sourceIds.includes(requested))
				? requested
				: fallback;
	}
	return selections;
}

/**
 * Prototype controller for publishing one saved matching result.
 *
 * Saving links never calls this controller. It prepares an explicit preview;
 * a source choice invalidates that position's preview instead of silently
 * preparing a new confirmation. Confirmation submits the frozen previews and
 * selections to the shared publication scheduler, so this controller can
 * unmount and remount without losing pending status or the terminal outcome.
 */
export function useMatchingReceiptPublication({
	receipt,
	releases,
	files,
	artists,
	savedReceipts,
	destination = fixturePublicationDestination,
	publication,
}: {
	receipt: MatchingLinkReceipt;
	releases: IncomingReleases;
	files: IncomingFiles;
	artists: readonly CatalogArtist[];
	savedReceipts: readonly MatchingLinkReceipt[];
	destination?: PublicationDestination;
	publication?: MatchingReceiptPublicationQueuePort;
}): MatchingReceiptPublicationController {
	const [included, setIncluded] = useState<ReadonlySet<string>>(
		() =>
			new Set(
				receipt.positions
					.filter((position) => position.included)
					.map((position) => String(position.trackId)),
			),
	);
	const [selectedPositionId, selectPosition] = useState(
		String(receipt.positions[0]?.trackId ?? ""),
	);
	const [chosen, setChosen] = useState<Record<string, string | null>>({});
	const prepareFor = (
		trackId: string,
		choice: Readonly<Record<string, string | null>>,
		catalog: IncomingReleases = releases,
	) =>
		prepareMatchingReceiptPublication(
			receipt,
			catalog,
			files,
			explicitSelections(receipt, choice),
			destination,
			[...artists],
			{ positionId: trackId, savedReceipts },
		);
	const prepareAllFor = (
		choice: Readonly<Record<string, string | null>>,
		catalog: IncomingReleases = releases,
	) =>
		new Map(
			receipt.positions.map(({ trackId }) => [
				String(trackId),
				prepareFor(String(trackId), choice, catalog),
			]),
		);
	const batch = publication?.batches
		.filter((item) => item.receiptId === receipt.id)
		.at(-1);
	const entryStatus = (entry: { jobId: string | null }) =>
		entry.jobId ? publication?.states.get(entry.jobId) : "failed";
	const [prepared, setPrepared] = useState<
		ReadonlyMap<string, MatchingReceiptPublicationPreview>
	>(() => new Map());

	const pendingJobs = new Map<string, string>();
	const pendingPreviews = new Map<string, MatchingReceiptPublicationPreview>();
	const failures = new Map<string, MatchingReceiptPublicationNotice>();
	const rejected: MatchingReceiptPublicationFailure[] = [];
	let terminal = Boolean(batch);
	if (batch)
		for (const entry of batch.entries) {
			const state = entryStatus(entry);
			if (state === "succeeded") continue;
			if (!entry.jobId) {
				rejected.push({
					trackId: entry.trackId,
					notice: entry.notice ?? "matchingReceipt.apply.blocked",
				});
				continue;
			}
			if (state === "failed") {
				failures.set(
					entry.trackId,
					entry.notice ??
						batch.results[entry.trackId]?.notice ??
						"matchingReceipt.apply.blocked",
				);
				continue;
			}
			terminal = false;
			pendingJobs.set(entry.trackId, entry.jobId);
			pendingPreviews.set(entry.trackId, entry.preview);
		}
	const pendingAny = pendingJobs.size > 0;
	const displayPreviews = new Map([...prepared, ...pendingPreviews]);
	const rows = [...displayPreviews.values()].flatMap((preview) => preview.rows);

	let outcome: MatchingReceiptPublicationOutcome | null = null;
	if (batch && terminal) {
		const failed = [...failures].map(([trackId, notice]) => ({
			trackId,
			notice,
		}));
		outcome = {
			attempted: batch.entries.length,
			published: batch.entries.length - failed.length - rejected.length,
			failed,
			rejected,
		};
	}

	const ready = [...prepared].filter(
		([trackId, preview]) =>
			included.has(trackId) && preview.canApply && !pendingJobs.has(trackId),
	);
	const selectedRow = rows.find((row) => row.trackId === selectedPositionId);

	const toggleInclude = (trackId: string) => {
		if (pendingJobs.has(trackId)) return;
		setIncluded((current) => {
			const next = new Set(current);
			if (next.has(trackId)) next.delete(trackId);
			else next.add(trackId);
			return next;
		});
	};
	const selectSource = (trackId: string, sourceId: string | null) => {
		if (pendingJobs.has(trackId)) return;
		setChosen((current) => ({ ...current, [trackId]: sourceId }));
		// A source change invalidates this position's preview; the operator must
		// prepare it again before it becomes confirmable.
		setPrepared((current) => {
			if (!current.has(trackId)) return current;
			const next = new Map(current);
			next.delete(trackId);
			return next;
		});
	};
	const refresh = () => {
		if (pendingAny) return;
		setPrepared(prepareAllFor(chosen));
	};

	const confirm = () => {
		if (!publication || pendingAny || !ready.length) return;
		publication.submit({
			receipt: structuredClone(receipt),
			selections: explicitSelections(receipt, chosen),
			destination: { ...destination },
			artists: [...artists],
			entries: ready.map(([positionId, preview]) => ({
				positionId,
				preview: structuredClone(preview),
			})),
		});
		setPrepared((current) => {
			const next = new Map(current);
			for (const [id] of ready) next.delete(id);
			return next;
		});
	};

	return {
		rows,
		prepared: new Set(displayPreviews.keys()),
		pendingJobs,
		failures,
		included,
		selectedPositionId,
		selectedRow,
		canPrepare: included.size > 0 && !pendingAny,
		canConfirm: Boolean(publication) && ready.length > 0 && !pendingAny,
		preparing: false,
		outcome,
		sourceSelection: (trackId) =>
			pendingPreviews.get(trackId)?.rows[0]?.sourceId ??
			chosen[trackId] ??
			null,
		toggleInclude,
		selectPosition,
		selectSource,
		refresh,
		confirm,
	};
}
