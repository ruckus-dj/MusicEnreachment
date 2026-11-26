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
import type { MatchingReceiptPublicationQueuePort } from "./matchingReceiptPublicationQueue";
import { buildMatchingReceiptPublicationReady } from "./matchingReceiptPublicationReady";

export type MatchingReceiptPublicationViewProps = {
  locale: Locale;
  receipt: MatchingLinkReceipt;
  releases: IncomingReleases;
  files: IncomingFiles;
  artists: readonly CatalogArtist[];
  savedReceipts: readonly MatchingLinkReceipt[];
  destination?: PublicationDestination;
  /** Shared publication scheduler owned by the host above the route. */
  publication?: MatchingReceiptPublicationQueuePort;
};

/**
 * Saved-result publication provider boundary.
 *
 * Mounts the per-receipt controller and binds only render-ready values into
 * the portable outlet frame. The controller may remount across routes; the
 * host-owned confirmation batch and scheduler state keep pending work and its
 * terminal outcome visible.
 */
export function MatchingReceiptPublicationView({
  locale,
  receipt,
  releases,
  files,
  artists,
  savedReceipts,
  destination,
  publication,
  children,
}: MatchingReceiptPublicationViewProps & { children: ReactNode }) {
  const controller = useMatchingReceiptPublication({
    receipt,
    releases,
    files,
    artists,
    savedReceipts,
    destination,
    publication,
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
