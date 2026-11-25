import type { Locale } from "../../i18n/types";
import { releaseControlButtonClass } from "./libraryReleaseControlStyles";

const copy = {
  en: { refreshData: "Refresh data" },
  ru: { refreshData: "Обновить данные" },
} as const;

/**
 * Per-position read failure shown in the release overview. The host owns the
 * failed read and retry action; this module owns the alert presentation.
 */
export function TrackReadErrorNoticeView({
  locale,
  message,
  onRefresh,
}: {
  locale: Locale;
  message: string;
  onRefresh: () => void;
}) {
  return (
    <div
      className="flex flex-wrap items-center gap-2 rounded border border-[var(--line)] bg-[var(--surface)] px-2.5 py-2 text-error wrap-anywhere"
      role="alert"
    >
      {message}{" "}
      <button
        type="button"
        className={releaseControlButtonClass}
        onClick={onRefresh}
      >
        {copy[locale].refreshData}
      </button>
    </div>
  );
}
