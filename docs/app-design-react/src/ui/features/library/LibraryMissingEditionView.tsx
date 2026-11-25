import { useId } from "react";
import { AppButton } from "../../components/AppButton";

/**
 * A single selectable edition. `label` is formatted by the host (artists,
 * date, country, labels, catalogue numbers, disambiguation and provider key),
 * so the view never reconstructs edition identity or invents missing details.
 */
export interface LibraryEditionOption {
  /** Provider key; the single controlled value for the selection. */
  key: string;
  label: string;
  /** Declared provider total. `null` means the total is unknown. */
  trackCount: number | null;
}

/**
 * `loading`/`error` are transport states, `empty` means the provider cache
 * knows no editions at all, and `ready` with an empty `editions` list means the
 * current page is empty while known editions exist elsewhere.
 */
export type LibraryEditionsStatus = "loading" | "error" | "empty" | "ready";

export interface LibraryEditionPagination {
  /** One-based page number. Visibility stays with the host. */
  page: number;
  hasPrevious: boolean;
  hasNext: boolean;
  onPrevious: () => void;
  onNext: () => void;
}

/**
 * A cached edition track. Fixture tracks may supply only `title`; all other
 * fields are optional and absent values are omitted instead of inferred.
 */
export interface LibraryEditionTrack {
  key: string;
  title: string;
  /** Preformatted position, for example "1.03". */
  positionLabel?: string | null;
  recordingTitle?: string | null;
  durationMs?: number | null;
}

export interface LibraryEditionTracks {
  /** `false` = known to be incomplete, `null` = completeness unknown. */
  complete?: boolean | null;
  /** Cached total. `null` means unknown. */
  trackCount: number | null;
  tracks: readonly LibraryEditionTrack[];
}

export type LibraryEditionTracksStatus = "loading" | "error" | "ready";

export interface LibraryMissingEditionViewProps {
  locale: "en" | "ru";
  groupTitle?: string;
  pickerVisible?: boolean;
  /** Options for the loaded page; the host must include an off-page selection. */
  editions: readonly LibraryEditionOption[];
  editionsStatus: LibraryEditionsStatus;
  /** Host-provided safe error text; omitted text uses the localized fallback. */
  editionsError?: string | null;
  onRetryEditions?: () => void;
  /** Empty string means no edition is selected. */
  selectedEditionKey: string;
  /** Receives the provider key; the host owns loading that edition's tracks. */
  onSelectEdition: (editionKey: string) => void;
  pagination?: LibraryEditionPagination | null;
  tracksStatus: LibraryEditionTracksStatus;
  tracksError?: string | null;
  onRetryTracks?: () => void;
  tracks?: LibraryEditionTracks | null;
}

const copy = {
  en: {
    editions: "Choose an edition",
    editionsFor: (title: string) => `Choose an edition for ${title}`,
    chooseEdition: "Choose an edition to inspect",
    loadingEditions: "Loading editions…",
    editionsError: "Could not load MusicBrainz editions.",
    retry: "Retry",
    trackCount: "Tracks",
    noTrackCount: "—",
    loadingTracks: "Loading track list…",
    tracksError: "Could not load this edition’s track list.",
    noTracks: "No tracks are listed for this edition.",
    partialCache:
      "The cached track list is incomplete; it may not represent the full edition.",
    unknownCache: "Track-list completeness is unknown.",
    page: "Page",
    previous: "Previous editions",
    next: "More editions",
    emptyEditionPage: "No editions on this page.",
    noKnownEditions: "No editions are known in the local cache.",
    readOnly: "Viewing an edition does not add it to the library.",
  },
  ru: {
    editions: "Выберите издание",
    editionsFor: (title: string) => `Выберите издание для «${title}»`,
    chooseEdition: "Выберите издание для просмотра",
    loadingEditions: "Загрузка изданий…",
    editionsError: "Не удалось загрузить издания MusicBrainz.",
    retry: "Повторить",
    trackCount: "Треков",
    noTrackCount: "—",
    loadingTracks: "Загрузка списка треков…",
    tracksError: "Не удалось загрузить список треков этого издания.",
    noTracks: "Для этого издания треки не указаны.",
    partialCache:
      "Кэшированный список неполный и может не включать все треки издания.",
    unknownCache: "Полнота списка треков неизвестна.",
    page: "Страница",
    previous: "Предыдущие издания",
    next: "Следующие издания",
    emptyEditionPage: "На этой странице нет изданий.",
    noKnownEditions: "В локальном кэше нет известных изданий.",
    readOnly: "Просмотр издания не добавляет его в медиатеку.",
  },
} as const;

const styles = {
  root: "grid min-w-0 gap-3 text-xs",
  field: "grid min-w-0 gap-1",
  label: "text-[11px] font-semibold text-[var(--muted)]",
  select:
    "w-full min-w-0 rounded border border-[var(--control)] bg-[var(--surface)] px-2 py-1.5 text-xs text-[var(--ink)] focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--focus)] pointer-coarse:min-h-10",
  muted: "m-0 text-[11px] text-[var(--muted)] [overflow-wrap:anywhere]",
  status: "m-0 text-[11px] text-[var(--muted)]",
  error:
    "m-0 grid min-w-0 gap-2 rounded border border-[var(--error)] bg-[var(--error-soft)] p-2 text-[11px] text-[var(--error)] [overflow-wrap:anywhere]",
  pagination: "flex min-w-0 flex-wrap items-center gap-2",
  page: "text-[11px] text-[var(--muted)] [font-variant-numeric:tabular-nums]",
  tracks: "grid min-w-0 gap-2",
  count: "m-0 text-[11px] font-semibold",
  trackList:
    "m-0 grid min-w-0 list-none gap-1 p-0 [font-variant-numeric:tabular-nums]",
  trackRow:
    "grid min-w-0 gap-0.5 border-b border-[var(--line)] pb-1 last:border-b-0 last:pb-0",
  trackPrimary: "min-w-0 [overflow-wrap:anywhere]",
  trackPosition: "text-[var(--muted)]",
  trackSecondary:
    "block text-[11px] font-normal text-[var(--muted)] [overflow-wrap:anywhere]",
} as const;

function errorMessage(message: string | null | undefined, fallback: string) {
  const trimmed = message?.trim();
  return trimmed ? trimmed : fallback;
}

function formatDuration(durationMs: number | null | undefined) {
  if (durationMs == null || !Number.isFinite(durationMs) || durationMs < 0) {
    return null;
  }
  const totalSeconds = Math.floor(durationMs / 1000);
  return `${Math.floor(totalSeconds / 60)}:${String(totalSeconds % 60).padStart(2, "0")}`;
}

function TrackRow({ track }: { track: LibraryEditionTrack }) {
  const position = track.positionLabel;
  const secondary = [track.recordingTitle, formatDuration(track.durationMs)]
    .filter((part): part is string => Boolean(part))
    .join(" · ");
  return (
    <li className={styles.trackRow}>
      <span className={styles.trackPrimary}>
        {position && <span className={styles.trackPosition}>{position} </span>}
        {track.title}
      </span>
      {secondary && <span className={styles.trackSecondary}>{secondary}</span>}
    </li>
  );
}

/**
 * Details-only edition chooser: no release heading, credits, outer card or
 * requests. The host supplies already loaded editions, tracks and localized
 * error text, and owns paging and selection side effects.
 */
export function LibraryMissingEditionView({
  locale,
  groupTitle,
  pickerVisible = true,
  editions,
  editionsStatus,
  editionsError,
  onRetryEditions,
  selectedEditionKey,
  onSelectEdition,
  pagination,
  tracksStatus,
  tracksError,
  onRetryTracks,
  tracks,
}: LibraryMissingEditionViewProps) {
  const text = copy[locale];
  const selectId = useId();
  const selectedEdition =
    editions.find((edition) => edition.key === selectedEditionKey) ?? null;

  return (
    <div className={styles.root}>
      {pickerVisible &&
        (editionsStatus === "loading" ? (
          <p className={styles.status} role="status">
            {text.loadingEditions}
          </p>
        ) : editionsStatus === "error" ? (
          <section className={styles.error} role="alert">
            <p>{errorMessage(editionsError, text.editionsError)}</p>
            {onRetryEditions && (
              <AppButton
                className="justify-self-start"
                onPress={onRetryEditions}
              >
                {text.retry}
              </AppButton>
            )}
          </section>
        ) : editionsStatus === "empty" ? (
          <p className={styles.muted}>{text.noKnownEditions}</p>
        ) : editions.length === 0 ? (
          <p className={styles.muted}>{text.emptyEditionPage}</p>
        ) : (
          <div className={styles.field}>
            <label className={styles.label} htmlFor={selectId}>
              {text.editions}
            </label>
            <select
              className={styles.select}
              id={selectId}
              aria-label={groupTitle ? text.editionsFor(groupTitle) : undefined}
              value={selectedEditionKey}
              onChange={(event) => onSelectEdition(event.currentTarget.value)}
            >
              <option value="">{text.chooseEdition}</option>
              {editions.map((edition) => (
                <option key={edition.key} value={edition.key}>
                  {edition.label}
                </option>
              ))}
            </select>
          </div>
        ))}

      {pickerVisible && pagination && editionsStatus === "ready" && (
        <nav
          className={styles.pagination}
          aria-label={`${text.page} ${pagination.page}`}
        >
          <span className={styles.page}>
            {text.page} {pagination.page}
          </span>
          <AppButton
            isDisabled={!pagination.hasPrevious}
            onPress={pagination.onPrevious}
          >
            {text.previous}
          </AppButton>
          <AppButton
            isDisabled={!pagination.hasNext}
            onPress={pagination.onNext}
          >
            {text.next}
          </AppButton>
        </nav>
      )}

      {selectedEditionKey !== "" && (
        <section
          className={styles.tracks}
          aria-label={selectedEdition?.label ?? text.editions}
        >
          <p className={styles.count}>
            {text.trackCount}:{" "}
            {tracks?.trackCount ??
              selectedEdition?.trackCount ??
              text.noTrackCount}
          </p>
          {tracksStatus === "loading" ? (
            <p className={styles.status} role="status">
              {text.loadingTracks}
            </p>
          ) : tracksStatus === "error" ? (
            <section className={styles.error} role="alert">
              <p>{errorMessage(tracksError, text.tracksError)}</p>
              {onRetryTracks && (
                <AppButton
                  className="justify-self-start"
                  onPress={onRetryTracks}
                >
                  {text.retry}
                </AppButton>
              )}
            </section>
          ) : tracks ? (
            <>
              {tracks.complete === false && (
                <p className={styles.muted}>{text.partialCache}</p>
              )}
              {tracks.complete === null && (
                <p className={styles.muted}>{text.unknownCache}</p>
              )}
              {tracks.tracks.length ? (
                <ol className={styles.trackList}>
                  {tracks.tracks.map((track) => (
                    <TrackRow key={track.key} track={track} />
                  ))}
                </ol>
              ) : (
                <p className={styles.muted}>{text.noTracks}</p>
              )}
            </>
          ) : null}
        </section>
      )}
      <p className={styles.muted}>{text.readOnly}</p>
    </div>
  );
}
