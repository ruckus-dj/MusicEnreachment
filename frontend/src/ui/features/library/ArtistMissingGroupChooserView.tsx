import type { ReactNode } from "react";
import { AppButton } from "../../components/AppButton";
import type { Locale } from "../../i18n/types";

const chooserToggleCopy = {
  en: { open: "Choose an edition", closed: "Browse editions" },
  ru: { open: "Выберите издание", closed: "Показать издания" },
} as const;

/**
 * Toggle and wrapper for one absent provider group's edition chooser. The host
 * owns the open state and fetch eligibility and supplies the chooser content,
 * so this presentation stays independent from provider cache reads.
 */
export function ArtistMissingGroupChooser({
  locale,
  isExpanded,
  onToggle,
  children,
}: {
  locale: Locale;
  isExpanded: boolean;
  onToggle: () => void;
  children?: ReactNode;
}) {
  const copy = chooserToggleCopy[locale];
  return (
    <div className="grid min-w-0 gap-2">
      <AppButton aria-expanded={isExpanded} onPress={onToggle}>
        {isExpanded ? copy.open : copy.closed}
      </AppButton>
      {children}
    </div>
  );
}
