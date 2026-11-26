import type {
	IncomingFiles,
	IncomingReleases,
	MatchingLinkReceipt,
} from "../incoming/incomingModel";
import type { CatalogArtist, OriginalTrack } from "../library/catalogData";
import type { PublicationDestination } from "../publication/publicationOutput";
import {
	applyMatchingReceiptPublication,
	type MatchingReceiptPublicationNotice,
	type MatchingReceiptPublicationPreview,
} from "./matchingReceiptPublication";

/** One frozen, exact saved-receipt publication position. */
export type MatchingReceiptPublicationJobPlan = {
	receipt: MatchingLinkReceipt;
	selections: Readonly<Record<string, string | null>>;
	positionId: string;
	preview: MatchingReceiptPublicationPreview;
};

/**
 * The latest confirmation of one saved matching result. The host freezes the
 * previews and selections at confirmation time; the queue owns their jobs.
 */
export type MatchingReceiptPublicationSubmission = {
	receipt: MatchingLinkReceipt;
	selections: Readonly<Record<string, string | null>>;
	destination: PublicationDestination;
	artists: readonly CatalogArtist[];
	entries: readonly {
		positionId: string;
		preview: MatchingReceiptPublicationPreview;
	}[];
};

/** Scheduler status for one receipt-publication job. */
export type MatchingReceiptPublicationOperationStatus =
	| "queued"
	| "running"
	| "succeeded"
	| "failed";

export type MatchingReceiptPublicationBatchEntry = {
	trackId: string;
	/** Null when the scheduler already had a pending publication for the position. */
	jobId: string | null;
	preview: MatchingReceiptPublicationPreview;
	/** Terminal rejection cause for an entry that never became a job. */
	notice?: MatchingReceiptPublicationNotice;
};

export type MatchingReceiptPublicationBatchResult = {
	status: "succeeded" | "failed";
	notice?: MatchingReceiptPublicationNotice;
};

/** Host-owned confirmation record; it survives controller remounts. */
export type MatchingReceiptPublicationBatch = {
	id: string;
	receiptId: string;
	entries: readonly MatchingReceiptPublicationBatchEntry[];
	results: Readonly<Record<string, MatchingReceiptPublicationBatchResult>>;
};

/**
 * The shared publication scheduler boundary. The matching surface only
 * submits frozen work and reads scheduler state; it never writes the catalog.
 */
export type MatchingReceiptPublicationQueuePort = {
	batches: readonly MatchingReceiptPublicationBatch[];
	states: ReadonlyMap<string, MatchingReceiptPublicationOperationStatus>;
	submit: (submission: MatchingReceiptPublicationSubmission) => void;
};

export type MatchingReceiptPublicationCatalog = {
	releases: Readonly<IncomingReleases>;
	files: IncomingFiles;
	artists: readonly CatalogArtist[];
	destination: PublicationDestination;
	/** Live saved results at settlement, not the frozen confirmation copy. */
	savedReceipts: readonly MatchingLinkReceipt[];
};

export type MatchingReceiptPublicationSettlement =
	| {
			applied: true;
			releaseId: string;
			positionId: string;
			published: NonNullable<OriginalTrack["published"]>;
			metadataBaseline: OriginalTrack["metadataBaseline"];
			metadataBaselineExtras: OriginalTrack["metadataBaselineExtras"];
	  }
	| {
			applied: false;
			releaseId: string;
			positionId: string;
			notice: MatchingReceiptPublicationNotice;
	  };

/**
 * Revalidates one frozen confirmation against the latest catalog, files,
 * receipts, destination and artists at settlement. A changed fact rejects the
 * position; the scheduler records that failure instead of publishing stale
 * work.
 */
export function settleMatchingReceiptPublicationJob(
	catalog: MatchingReceiptPublicationCatalog,
	plan: MatchingReceiptPublicationJobPlan,
): MatchingReceiptPublicationSettlement {
	const releaseId = plan.receipt.releaseId;
	const reject = (
		notice: MatchingReceiptPublicationNotice,
	): MatchingReceiptPublicationSettlement => ({
		applied: false,
		releaseId,
		positionId: plan.positionId,
		notice,
	});
	const releases = structuredClone([...catalog.releases]);
	const result = applyMatchingReceiptPublication(
		plan.preview,
		plan.receipt,
		releases,
		catalog.files,
		plan.selections,
		catalog.destination,
		[...catalog.artists],
		{ positionId: plan.positionId, savedReceipts: catalog.savedReceipts },
	);
	if (!result.applied)
		return reject(
			result.reason ??
				result.results[0]?.notice ??
				"matchingReceipt.apply.blocked",
		);
	const track = releases
		.find((release) => release.id === releaseId)
		?.tracks.find((candidate) => String(candidate.id) === plan.positionId);
	if (!track?.published) return reject("matchingReceipt.apply.blocked");
	return {
		applied: true,
		releaseId,
		positionId: plan.positionId,
		published: structuredClone(track.published),
		metadataBaseline: structuredClone(track.metadataBaseline),
		metadataBaselineExtras: structuredClone(track.metadataBaselineExtras),
	};
}

/**
 * Commits the exact publication and selects its already-linked source without
 * rewriting the saved receipt or the list of linked sources.
 */
export function commitMatchingReceiptPublication(
	releases: IncomingReleases,
	settlement: Extract<MatchingReceiptPublicationSettlement, { applied: true }>,
): IncomingReleases {
	return releases.map((release) =>
		release.id !== settlement.releaseId
			? release
			: {
					...release,
					tracks: release.tracks.map((track) =>
						String(track.id) !== settlement.positionId
							? track
							: {
									...track,
									sourceId: settlement.published.sourceId,
									published: structuredClone(settlement.published),
									metadataBaseline: structuredClone(
										settlement.metadataBaseline,
									),
									metadataBaselineExtras: structuredClone(
										settlement.metadataBaselineExtras,
									),
								},
					),
				},
	);
}
