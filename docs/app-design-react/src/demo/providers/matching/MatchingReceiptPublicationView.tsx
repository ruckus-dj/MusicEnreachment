import type { ReactNode } from "react";
import { MatchingReceiptPublicationDataBinder } from "../../../ui/features/matching/MatchingReceiptPublicationOutlet";
import type { Locale } from "../../../ui/i18n/types";
import type {
	IncomingFiles,
	IncomingReleases,
	MatchingLinkReceipt,
} from "../incoming/incomingModel";
import type { CatalogArtist } from "../library/catalogData";
import type { PublicationDestination } from "../publication/publicationOutput";
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
};

/**
 * Saved-result publication provider boundary.
 *
 * Mounts the per-receipt controller and binds only render-ready values into
 * the portable outlet frame. The controller is mounted with this binder, so
 * the host keeps it alive only while the publication surface is open.
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
	children,
}: MatchingReceiptPublicationViewProps & { children: ReactNode }) {
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
	});
	return (
		<MatchingReceiptPublicationDataBinder model={model}>
			{children}
		</MatchingReceiptPublicationDataBinder>
	);
}
