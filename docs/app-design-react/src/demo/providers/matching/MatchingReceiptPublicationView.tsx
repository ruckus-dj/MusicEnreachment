import type { Locale } from "../../../ui/i18n/types";
import type {
	IncomingFiles,
	IncomingReleases,
	MatchingLinkReceipt,
} from "../incoming/incomingModel";
import type { CatalogArtist } from "../library/catalogData";
import type { PublicationDestination } from "../publication/publicationOutput";
import { MatchingReceiptPublicationReadyView } from "./MatchingReceiptPublicationReadyView";
import { useMatchingReceiptPublication } from "./matchingReceiptPublicationController";
import { buildMatchingReceiptPublicationReady } from "./matchingReceiptPublicationReady";

export type MatchingReceiptPublicationViewProps = {
	locale: Locale;
	receipt: MatchingLinkReceipt;
	releases: IncomingReleases;
	files: IncomingFiles;
	artists: readonly CatalogArtist[];
	savedReceipts: readonly MatchingLinkReceipt[];
	destination?: PublicationDestination;
	onReleases: (releases: IncomingReleases) => void;
	onBack: () => void;
};

/**
 * Saved-result publication provider boundary.
 *
 * Keeps the existing facade API for matching callers, owns the per-receipt
 * controller and hands only render-ready values to the pure view.
 */
export function MatchingReceiptPublicationView({
	locale,
	receipt,
	releases,
	files,
	artists,
	savedReceipts,
	destination,
	onReleases,
	onBack,
}: MatchingReceiptPublicationViewProps) {
	const controller = useMatchingReceiptPublication({
		receipt,
		releases,
		files,
		artists,
		savedReceipts,
		destination,
		onReleases,
	});
	const model = buildMatchingReceiptPublicationReady({
		locale,
		receipt,
		files,
		controller,
		onBack,
	});
	return <MatchingReceiptPublicationReadyView model={model} />;
}
