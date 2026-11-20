import { useMemo, useState } from "react";
import { AppButton } from "../../components/AppButton";

export type GenreCatalogEntry = {
  id: string;
  name: string;
  sourceName: string;
};

export type GenreCatalogFeedback =
  | { kind: "detail"; text: string }
  | { kind: "http"; status: string | number }
  | { kind: "status" | "refresh" | "operation" };

type Locale = "en" | "ru";

const messages = {
  en: {
    title: "Genre catalog",
    description: "The MusicBrainz catalog is used to choose genres.",
    available: "Available for genre selection",
    unavailable: "Not available for genre selection",
    refresh: "Refresh catalog",
    sending: "Sending request…",
    loading: "Loading status…",
    count: "Genres",
    updated: "Last successful update",
    attempted: "Last attempt",
    lastError: "Last error:",
    updating: "Catalog refresh:",
    queued: "queued",
    running: "running",
    requested: "A genre catalog refresh has been requested.",
    status: "Could not load genre catalog status.",
    refreshError: "Could not refresh the genre catalog.",
    operation: "Could not load operation status.",
    http: (status: number | string) => `The server returned HTTP ${status}.`,
    search: "Search genres",
    placeholder: "Name or MusicBrainz ID",
    results: "Results",
    empty: "No genres match this search.",
    more: "Show more genres",
  },
  ru: {
    title: "Жанровый каталог",
    description: "Каталог MusicBrainz используется для подбора жанров.",
    available: "Доступен для выбора жанров",
    unavailable: "Недоступен для выбора жанров",
    refresh: "Обновить каталог",
    sending: "Запрос отправляется…",
    loading: "Загрузка статуса…",
    count: "Жанров",
    updated: "Последнее успешное обновление",
    attempted: "Последняя попытка",
    lastError: "Последняя ошибка:",
    updating: "Обновление каталога:",
    queued: "в очереди",
    running: "выполняется",
    requested: "Запрошено обновление жанрового каталога.",
    status: "Не удалось загрузить статус жанрового каталога.",
    refreshError: "Не удалось обновить жанровый каталог.",
    operation: "Не удалось загрузить статус операции.",
    http: (status: number | string) => `Сервер вернул HTTP ${status}.`,
    search: "Поиск жанров",
    placeholder: "Название или MusicBrainz ID",
    results: "Найдено жанров",
    empty: "По этому запросу жанры не найдены.",
    more: "Показать ещё жанры",
  },
} as const;

const muted = "m-0 text-xs text-[var(--muted)]";
const warning = "m-0 text-sm wrap-anywhere text-[var(--error)]";
const pageSize = 100;

function displayTime(value: string | null, locale: Locale) {
  if (!value) return "—";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString(locale);
}

function feedbackText(
  error: GenreCatalogFeedback,
  copy: (typeof messages)[Locale],
) {
  switch (error.kind) {
    case "detail":
      return error.text;
    case "http":
      return copy.http(error.status);
    case "refresh":
      return copy.refreshError;
    default:
      return copy[error.kind];
  }
}

type Props = {
  locale: Locale;
  items: readonly GenreCatalogEntry[];
  status?: {
    count: number;
    available: boolean;
    updatedAt: string | null;
    attemptedAt: string | null;
    lastError?: string | null;
  };
  loading?: boolean;
  refreshState: "idle" | "submitting" | "admitted";
  operation?: {
    active?: "queued" | "running";
    stage?: string;
    error?: string | null;
  };
  refreshRequested?: boolean;
  error?: GenreCatalogFeedback;
  onRefresh: () => void;
};

export function GenreCatalogView({
  locale,
  items,
  status,
  loading,
  refreshState,
  operation,
  refreshRequested,
  error,
  onRefresh,
}: Props) {
  const copy = messages[locale];
  const [query, setQuery] = useState("");
  const [limit, setLimit] = useState(pageSize);
  const term = query.trim().toLocaleLowerCase("en");
  const results = useMemo(
    () =>
      term
        ? items.filter((item) =>
            [item.name, item.sourceName, item.id].some((value) =>
              value.toLocaleLowerCase("en").includes(term),
            ),
          )
        : items,
    [items, term],
  );
  return (
    <section
      aria-labelledby="genre-catalog-title"
      className="grid min-w-0 gap-3 rounded-md border border-[var(--line)] bg-[var(--surface)] p-3 text-sm text-[var(--ink)]"
    >
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="grid gap-1">
          <h2 id="genre-catalog-title" className="m-0 text-sm font-semibold">
            {copy.title}
          </h2>
          <p className={muted}>{copy.description}</p>
        </div>
        <AppButton isDisabled={refreshState !== "idle"} onPress={onRefresh}>
          {refreshState === "submitting" ? copy.sending : copy.refresh}
        </AppButton>
      </div>
      {loading && <p role="status">{copy.loading}</p>}
      {status && (
        <>
          <p className={muted}>
            {status.available ? copy.available : copy.unavailable}
          </p>
          <dl className="m-0 grid grid-cols-[repeat(auto-fit,minmax(min(100%,200px),1fr))] gap-x-4 gap-y-2 text-sm">
            {[
              [copy.count, status.count.toLocaleString(locale)],
              [copy.updated, displayTime(status.updatedAt, locale)],
              [copy.attempted, displayTime(status.attemptedAt, locale)],
            ].map(([label, value]) => (
              <div key={label} className="min-w-0">
                <dt className={muted}>{label}</dt>
                <dd className="m-0 font-semibold wrap-anywhere">{value}</dd>
              </div>
            ))}
          </dl>
        </>
      )}
      {status?.lastError && (
        <p className={warning}>
          {copy.lastError} {status.lastError}
        </p>
      )}
      {operation?.active && (
        <p role="status" className="m-0">
          {copy.updating} {copy[operation.active]}
          {operation.stage ? ` — ${operation.stage}` : ""}
        </p>
      )}
      {operation?.error && (
        <p className={warning}>
          {copy.refreshError} {operation.error}
        </p>
      )}
      {error && (
        <p role="alert" className={warning}>
          {feedbackText(error, copy)}
        </p>
      )}
      {refreshRequested && (
        <p role="status" className={muted}>
          {copy.requested}
        </p>
      )}
      {items.length > 0 && (
        <>
          <label className="grid w-full max-w-md gap-1 text-xs font-medium">
            {copy.search}
            <input
              type="search"
              placeholder={copy.placeholder}
              value={query}
              onChange={(event) => {
                setQuery(event.target.value);
                setLimit(pageSize);
              }}
              className="min-h-8 w-full rounded-[var(--control-radius)] border border-[var(--control)] bg-[var(--surface)] px-2 py-1 text-sm text-[var(--ink)] focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--focus)]"
            />
          </label>
          <p className={muted} role="status">
            {copy.results}: {results.length.toLocaleString(locale)}
          </p>
          {results.length ? (
            <ul
              aria-label={copy.results}
              className="m-0 grid max-h-[min(50dvh,420px)] grid-cols-[repeat(auto-fill,minmax(min(100%,210px),1fr))] list-none overflow-auto overscroll-contain rounded border border-[var(--line)] bg-[var(--surface)] p-1"
            >
              {results.slice(0, limit).map(({ id, name, sourceName }) => (
                <li
                  key={id}
                  className="min-w-0 rounded px-3 py-2 hover:bg-[var(--raised)]"
                  title={`${sourceName} · ${id}`}
                >
                  <span className="block truncate text-sm">{name}</span>
                </li>
              ))}
            </ul>
          ) : (
            <p className="m-0 rounded border border-dashed border-[var(--line)] p-4 text-sm text-[var(--muted)]">
              {copy.empty}
            </p>
          )}
          {limit < results.length && (
            <AppButton
              className="justify-self-start"
              onPress={() => setLimit((value) => value + pageSize)}
            >
              {copy.more}
            </AppButton>
          )}
        </>
      )}
    </section>
  );
}
