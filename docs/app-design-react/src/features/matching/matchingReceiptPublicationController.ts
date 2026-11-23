import { useState } from "react";
import type {
	IncomingFiles,
	IncomingReleases,
	MatchingLinkReceipt,
} from "../incoming/incomingModel";
import type { CatalogArtist } from "../library/catalogData";
import {
	type PublicationDestination,
	fixturePublicationDestination,
} from "../publication/publicationOutput";
import {
	applyMatchingReceiptPublication,
	type MatchingReceiptPublicationNotice,
	type MatchingReceiptPublicationPreview,
	type MatchingReceiptPublicationRow,
	prepareMatchingReceiptPublication,
} from "./matchingReceiptPublication";

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
};

export type MatchingReceiptPublicationController = {
	rows: readonly MatchingReceiptPublicationRow[];
	included: ReadonlySet<string>;
	selectedPositionId: string;
	selectedRow: MatchingReceiptPublicationRow | undefined;
	canConfirm: boolean;
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
 * Saving links never calls this controller. It only prepares a preview and,
 * on explicit confirmation, applies every included position as its own
 * scoped operation. The saved receipt is passed unchanged to the provider so
 * its receipt authority check compares the exact saved positions.
 */
export function useMatchingReceiptPublication({
	receipt,
	releases,
	files,
	artists,
	savedReceipts,
	destination = fixturePublicationDestination,
	onReleases,
}: {
	receipt: MatchingLinkReceipt;
	releases: IncomingReleases;
	files: IncomingFiles;
	artists: readonly CatalogArtist[];
	savedReceipts: readonly MatchingLinkReceipt[];
	destination?: PublicationDestination;
	onReleases: (releases: IncomingReleases) => void;
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
	const [outcome, setOutcome] =
		useState<MatchingReceiptPublicationOutcome | null>(null);

	const prepare = (
		trackId: string,
		choice: Readonly<Record<string, string | null>>,
		catalog = releases,
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
	const prepareAll = () =>
		new Map(
			receipt.positions.map(({ trackId }) => [
				trackId,
				prepare(trackId, chosen),
			]),
		);
	const [prepared, setPrepared] =
		useState<ReadonlyMap<string, MatchingReceiptPublicationPreview>>(
			prepareAll,
		);
	const rows = [...prepared.values()].flatMap((preview) => preview.rows);
	const ready = [...prepared].filter(
		([id, preview]) => included.has(id) && preview.canApply,
	);
	const selectedRow = rows.find((row) => row.trackId === selectedPositionId);

	const toggleInclude = (trackId: string) => {
		setOutcome(null);
		setIncluded((current) => {
			const next = new Set(current);
			if (next.has(trackId)) next.delete(trackId);
			else next.add(trackId);
			return next;
		});
	};
	const selectSource = (trackId: string, sourceId: string | null) => {
		setOutcome(null);
		const next = { ...chosen, [trackId]: sourceId };
		setChosen(next);
		setPrepared(new Map(prepared).set(trackId, prepare(trackId, next)));
	};
	const refresh = () => {
		setOutcome(null);
		setPrepared(prepareAll());
	};

	const confirm = () => {
		if (!ready.length) return;
		const next = structuredClone(releases);
		const nextPrepared = new Map(prepared);
		const failed: MatchingReceiptPublicationFailure[] = [];
		let published = 0;
		for (const [trackId, preview] of ready) {
			const result = applyMatchingReceiptPublication(
				preview,
				receipt,
				next,
				files,
				explicitSelections(receipt, chosen),
				destination,
				[...artists],
				{ positionId: trackId, savedReceipts },
			);
			if (result.applied) {
				published++;
				nextPrepared.set(trackId, prepare(trackId, chosen, next));
			} else {
				const notice =
					result.reason ??
					result.results[0]?.notice ??
					"matchingReceipt.apply.blocked";
				failed.push({ trackId, notice });
				nextPrepared.set(trackId, {
					...preview,
					canApply: false,
					rows: preview.rows.map((row) => ({
						...row,
						action: "blocked",
						reason: notice,
						blockers: [notice],
					})),
				});
			}
		}
		if (published) onReleases(next);
		setPrepared(nextPrepared);
		setOutcome({ attempted: ready.length, published, failed });
	};

	return {
		rows,
		included,
		selectedPositionId,
		selectedRow,
		canConfirm: ready.length > 0,
		outcome,
		sourceSelection: (trackId) => chosen[trackId] ?? null,
		toggleInclude,
		selectPosition,
		selectSource,
		refresh,
		confirm,
	};
}
