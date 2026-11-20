import type { RefObject } from "react";
import { AppButton } from "../../components/AppButton";

// Portable Tools-tab header/summary. The host owns dialogs, catalog requests,
// dialog opener focus restoration, and alert focus; these views only render
// state and forward the pressed control. The root view never renders a pending
// move candidate: it receives the persisted directory, not the editor draft.
const messages = {
  en: {
    toolsDirectory: "Tools directory",
    moveToolsDirectory: "Move tools directory",
    managedTools: "Managed tools",
    lastCatalogCheck: "Last successful catalog check:",
    catalogNotChecked: "Catalog has not been checked yet.",
    refreshingCatalog: "Refreshing catalog…",
    refreshCatalog: "Refresh catalog",
    operationsHint:
      "Tool operations continue in the background; progress, retry, and error dismissal are on the Operations screen.",
    openOperations: "Open operations",
  },
  ru: {
    toolsDirectory: "Каталог инструментов",
    moveToolsDirectory: "Перенести каталог",
    managedTools: "Управляемые инструменты",
    lastCatalogCheck: "Последняя успешная проверка каталога:",
    catalogNotChecked: "Каталог ещё не проверялся.",
    refreshingCatalog: "Обновление каталога…",
    refreshCatalog: "Обновить каталог",
    operationsHint:
      "Ход операций с инструментами, повтор и закрытие ошибок — в разделе «Операции».",
    openOperations: "Открыть операции",
  },
} as const;

const panelClass =
  "grid min-w-0 content-start justify-items-start gap-2 rounded-md border border-[var(--line)] bg-[var(--surface)] p-3 text-sm text-[var(--ink)]";
const headingClass = "m-0 text-sm font-semibold";
const paragraphClass = "m-0 wrap-anywhere";
const codeClass = "font-mono text-[11px]/[1.45] wrap-anywhere";
const alertClass =
  "m-0 wrap-anywhere rounded border border-[var(--error)] bg-[var(--error-soft)] px-3 py-2 text-[var(--error)]";

export type SettingsToolsRootHeaderProps = {
  locale: "en" | "ru";
  /** Persisted tools root; never the pending move candidate. */
  savedDirectory: string;
  operationsHref?: string;
  /** Omit when the host has no move workflow. */
  move?: {
    disabled: boolean;
    /** Receives the pressed control so the host can restore focus on close. */
    onPress: (opener: HTMLElement) => void;
  };
};

export type SettingsToolsCatalogSummaryProps = {
  locale: "en" | "ru";
  /** Omit for a heading-only summary; no demo refresh/status is implied. */
  refresh?: {
    /** Persisted ISO timestamp, or empty when no successful check is recorded. */
    checkedAt: string;
    busy: boolean;
    onRefresh: () => void;
    /** Already-localized failure text. */
    error?: string;
    /** Host focuses this alert when a retryable error appears. */
    errorRef?: RefObject<HTMLParagraphElement | null>;
  };
};

export function SettingsToolsRootHeader({
  locale,
  savedDirectory,
  operationsHref,
  move,
}: SettingsToolsRootHeaderProps) {
  const copy = messages[locale];
  return (
    <>
      {operationsHref && (
        <p className="my-2 max-w-[70ch] text-[var(--muted)]">
          {copy.operationsHint}{" "}
          <a
            href={operationsHref}
            className="text-[var(--accent)] underline decoration-[var(--line)] underline-offset-2 hover:decoration-[var(--accent)] focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--focus)]"
          >
            {copy.openOperations}
          </a>
        </p>
      )}
      <section aria-labelledby="tools-root-title" className={panelClass}>
        <h2 id="tools-root-title" className={headingClass}>
          {copy.toolsDirectory}
        </h2>
        <p className={paragraphClass}>
          {copy.toolsDirectory}:{" "}
          <code className={codeClass}>{savedDirectory}</code>
        </p>
        {move && (
          <AppButton
            isDisabled={move.disabled}
            onPress={(event) => move.onPress(event.target as HTMLElement)}
          >
            {copy.moveToolsDirectory}
          </AppButton>
        )}
      </section>
    </>
  );
}

export function SettingsToolsCatalogSummary({
  locale,
  refresh,
}: SettingsToolsCatalogSummaryProps) {
  const copy = messages[locale];
  const checkedTime = refresh?.checkedAt ? new Date(refresh.checkedAt) : null;
  const validTime = checkedTime && Number.isFinite(checkedTime.getTime());
  return (
    <div className={panelClass}>
      <h2 id="managed-title" className={headingClass}>
        {copy.managedTools}
      </h2>
      {refresh && (
        <>
          <p className={paragraphClass}>
            {copy.lastCatalogCheck}{" "}
            {checkedTime && validTime ? (
              <time dateTime={checkedTime.toISOString()}>
                {checkedTime.toLocaleString(locale)}
              </time>
            ) : (
              copy.catalogNotChecked
            )}
          </p>
          {refresh.error && (
            <p
              ref={refresh.errorRef}
              tabIndex={-1}
              role="alert"
              className={alertClass}
            >
              {refresh.error}
            </p>
          )}
          <AppButton isDisabled={refresh.busy} onPress={refresh.onRefresh}>
            {refresh.busy ? copy.refreshingCatalog : copy.refreshCatalog}
          </AppButton>
        </>
      )}
    </div>
  );
}
