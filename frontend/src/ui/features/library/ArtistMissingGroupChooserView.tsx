import type { ReactNode } from "react";
import { AppButton } from "../../components/AppButton";
import type { Locale } from "../../i18n/types";

const chooserToggleCopy = {
  en: {
    open: "Choose an edition",
    closed: "Browse editions",
    hideTracks: "Hide tracks",
    showTracks: "Show tracks",
  },
  ru: {
    open: "Выберите издание",
    closed: "Показать издания",
    hideTracks: "Скрыть треки",
    showTracks: "Показать треки",
  },
} as const;

/**
 * Toggle and wrapper for one absent provider group's edition chooser. The host
 * owns the open state and fetch eligibility and supplies the chooser content,
 * so this presentation stays independent from provider cache reads.
 */
export function ArtistMissingGroupChooser({
  locale,
  isExpanded,
  hasSoleEdition,
  onToggle,
  children,
}: {
  locale: Locale;
  isExpanded: boolean;
  hasSoleEdition: boolean;
  onToggle: () => void;
  children?: ReactNode;
}) {
  const copy = chooserToggleCopy[locale];
  const label = hasSoleEdition
    ? isExpanded
      ? copy.hideTracks
      : copy.showTracks
    : isExpanded
      ? copy.open
      : copy.closed;
  return (
    <div className="grid min-w-0 gap-2">
      <AppButton aria-expanded={isExpanded} onPress={onToggle}>
        {label}
      </AppButton>
      {children}
    </div>
  );
}
