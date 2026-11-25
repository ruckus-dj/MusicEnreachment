import { useState } from "react";
import {
  type MatchingLinkResultFile,
  type MatchingLinkResultLink,
  type MatchingLinkResultLocale,
  MatchingLinkResultView,
} from "./MatchingLinkResultView";
import {
  MatchingReceiptPublicationOutlet,
  type MatchingReceiptPublicationSource,
} from "./MatchingReceiptPublicationOutlet";
import { useMatchingLinkResultFocus } from "./useMatchingLinkResultFocus";

/** One saved matching receipt, with an optional lazy publication surface. */
export type MatchingSavedReceiptModel = {
  /** Stable receipt identity; the host remounts the view when it changes. */
  key: string;
  locale: MatchingLinkResultLocale;
  releaseTitle: string;
  links: readonly MatchingLinkResultLink[];
  remaining: readonly MatchingLinkResultFile[];
  /** Extra disable for a receipt that has a source but cannot publish yet. */
  publicationDisabled?: boolean;
  onContinue?: () => void;
  /** Provider-requested heading focus token; empty means no pending focus. */
  focusIntent: string;
  publication?: MatchingReceiptPublicationSource;
};

/**
 * Saved matching result surface.
 *
 * Owns whether the publication view is open and restores focus on return. The
 * publication outlet is mounted only while open, so its controller follows the
 * visible surface instead of the saved result lifetime.
 */
export function MatchingSavedReceiptView({
  model,
}: {
  model: MatchingSavedReceiptModel;
}) {
  const [open, setOpen] = useState(false);
  const { headingRef, publicationButtonRef, restorePublicationButtonOnReturn } =
    useMatchingLinkResultFocus({
      focusHeadingKey: open ? undefined : model.focusIntent,
      publicationOpen: open,
    });

  if (open && model.publication) {
    return (
      <MatchingReceiptPublicationOutlet
        locale={model.locale}
        releaseTitle={model.releaseTitle}
        onBack={() => {
          restorePublicationButtonOnReturn();
          setOpen(false);
        }}
        source={model.publication}
      />
    );
  }

  return (
    <MatchingLinkResultView
      locale={model.locale}
      releaseTitle={model.releaseTitle}
      links={model.links}
      remaining={model.remaining}
      headingRef={headingRef}
      publicationButtonRef={publicationButtonRef}
      publicationDisabled={
        !model.publication || Boolean(model.publicationDisabled)
      }
      onOpenPublication={() => setOpen(true)}
      onContinue={model.onContinue}
    />
  );
}
