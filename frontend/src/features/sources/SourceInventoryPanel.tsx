import { useEffect, useRef } from "react";
import { AppButton } from "../../components/AppButton";
import {
  type SourceInventoryRow,
  SourceInventoryView,
} from "./SourceInventoryView";

export type SourceInventoryPanelProps = {
  locale: "en" | "ru";
  titleId?: string;
  /** Folder whose successful scan produced the inventory on screen. */
  path?: string;
  /** Scan generation zero: no successful scan has published inventory yet. */
  unpublished?: boolean;
  /** Source diagnostics such as a stale path or an unavailable source. */
  notes?: readonly string[];
  loading?: boolean;
  error?: string;
  onRetry?: () => void;
  rows: readonly SourceInventoryRow[];
  loadingMore?: boolean;
  onLoadMore?: () => void;
};

const messages = {
  en: {
    title: "Inventory files",
    help: "Files from the last successful scan of",
    unpublished:
      "There is no inventory: the source has not been scanned successfully. Files will appear after a successful scan.",
    loading: "Loading file list…",
    empty:
      "The last successful scan found no files with supported audio extensions.",
    retry: "Retry loading",
    showMore: "Show more",
    loadingMore: "Loading next page…",
    shown: "Showing:",
  },
  ru: {
    title: "Файлы инвентаря",
    help: "Файлы последнего успешного сканирования каталога",
    unpublished:
      "Инвентаря нет: каталог ещё ни разу не сканировался успешно. Список файлов появится после успешного сканирования.",
    loading: "Загрузка списка файлов…",
    empty:
      "Последнее успешное сканирование не нашло файлов с поддерживаемым аудиорасширением.",
    retry: "Повторить загрузку",
    showMore: "Показать ещё",
    loadingMore: "Загрузка следующей страницы…",
    shown: "Показано:",
  },
} as const;

const panel =
  "min-w-0 rounded-[8px] border border-[var(--line)] bg-[var(--surface)] p-[10px] text-xs wrap-anywhere";
const note = "mt-2 block text-[11px] text-[var(--muted)]";
const code = "font-mono text-[11px] [overflow-wrap:anywhere]";
const errorBox =
  "mt-2 rounded border border-[var(--line)] bg-[var(--error-soft)] p-2 text-[var(--error)]";

export function SourceInventoryPanel({
  locale,
  titleId = "source-locations-title",
  path,
  unpublished = false,
  notes,
  loading = false,
  error,
  onRetry,
  rows,
  loadingMore = false,
  onLoadMore,
}: SourceInventoryPanelProps) {
  const copy = messages[locale];
  const alertRef = useRef<HTMLParagraphElement>(null);
  const hasError = Boolean(error);
  useEffect(() => {
    if (hasError) alertRef.current?.focus();
  }, [hasError]);
  return (
    <section aria-labelledby={titleId} className={panel}>
      <h2 id={titleId} className="text-sm font-semibold">
        {copy.title}
      </h2>
      {unpublished ? (
        <p className={note}>{copy.unpublished}</p>
      ) : (
        <>
          {path && (
            <p className={note}>
              {copy.help} <code className={code}>{path}</code>.
            </p>
          )}
          {notes?.map((item) => (
            <p key={item} className={note}>
              {item}
            </p>
          ))}
          {error ? (
            <>
              <p ref={alertRef} tabIndex={-1} role="alert" className={errorBox}>
                {error}
              </p>
              {onRetry && (
                <div className="mt-2">
                  <AppButton
                    isDisabled={loading || loadingMore}
                    onPress={onRetry}
                  >
                    {copy.retry}
                  </AppButton>
                </div>
              )}
            </>
          ) : loading ? (
            <p role="status" className={note}>
              {copy.loading}
            </p>
          ) : rows.length === 0 ? (
            <p role="status" className={note}>
              {copy.empty}
            </p>
          ) : null}
          {rows.length > 0 && (
            <>
              <div className="mt-2">
                <SourceInventoryView locale={locale} rows={rows} />
              </div>
              <p className={note}>
                {copy.shown} {new Intl.NumberFormat(locale).format(rows.length)}
              </p>
              {!error && onLoadMore && (
                <div className="mt-2">
                  <AppButton isDisabled={loadingMore} onPress={onLoadMore}>
                    {copy.showMore}
                  </AppButton>
                </div>
              )}
              {loadingMore && (
                <p role="status" className={note}>
                  {copy.loadingMore}
                </p>
              )}
            </>
          )}
        </>
      )}
    </section>
  );
}
