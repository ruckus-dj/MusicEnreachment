import type { Locale } from "../../../i18n/types";
import { releaseMetadataEditorMessages } from "./releaseMetadataEditorMessages";

/** Retained local artist link notice for a contributor row. */
export function ContributorArtistLinkNotice({ locale }: { locale: Locale }) {
  const text = releaseMetadataEditorMessages[locale];
  return (
    <div className="grid min-w-0 gap-1 text-xs text-[var(--muted)]">
      <span className="text-sm text-[var(--ink)]">
        {text.artistLinkRetained}
      </span>
    </div>
  );
}
