import {
  type ComponentProps,
  Fragment,
  type Key,
  type ReactNode,
  useEffect,
  useId,
  useRef,
  useState,
} from "react";
import { Tab, TabList, TabPanel, Tabs } from "react-aria-components";
import { AppButton } from "../../components/AppButton";
import { StatusBadge } from "../../components/StatusBadge";
import type { Locale } from "../../i18n/locale";
import { ScreenStatus } from "../../shell/ScreenFeedbackView";
import {
  ContextPublicationPreviewOutlet,
  type ContextPublicationPreviewSource,
} from "../publication/ContextPublicationPreview";
import {
  LibraryPositionEditorsView,
  type LibraryPositionEditorsViewProps,
} from "./LibraryPositionEditorsView";
import {
  LibraryPositionHeadingView,
  libraryPositionPageClassName,
  libraryPositionTitleId,
} from "./LibraryPositionHeadingView";
import { LibraryPositionIdentityView } from "./LibraryPositionIdentityView";
import {
  type LibraryPositionSource,
  LibraryPositionSourcesView,
} from "./LibraryPositionSourcesView";
import { LibraryPositionSourceTagsDrawer } from "./LibraryPositionSourceTagsDrawer";
import { LibraryPositionTechnicalDetailsView } from "./LibraryPositionTechnicalDetailsView";
import { LibraryPositionPreviewButton } from "./LibraryPositionWorkspace";
import type { LibraryPositionTab } from "./libraryDetailRoute";
import {
  PublicationCurrentDetailsView,
  PublicationCurrentSummaryView,
} from "./PublicationCurrentSummaryView";
import { PublicationTagSnapshotView } from "./PublicationTagSnapshotView";

const buttonClass =
  "min-h-9 rounded border border-[var(--line)] bg-[var(--surface)] px-3 py-1 text-[var(--ink)] hover:enabled:border-[var(--accent)] hover:enabled:bg-[var(--raised)] disabled:border-[var(--line)] disabled:bg-[var(--raised)] disabled:text-[var(--muted)] max-[599px]:min-h-10";
const fieldsetClass =
  "min-w-0 rounded-lg border border-[var(--line)] bg-[var(--surface)] px-2.5 py-2";
const alertClass = "text-[var(--error)] wrap-anywhere";
const actionsClass = "my-2 flex flex-wrap items-center gap-[7px]";
const factsClass =
  "my-[10px] grid grid-cols-[minmax(130px,.75fr)_minmax(0,2fr)] gap-x-3 gap-y-[6px] max-[600px]:grid-cols-1 max-[600px]:gap-y-0 [&_dt]:text-xs [&_dt]:text-[var(--muted)] [&_dd]:m-0 [&_dd]:[overflow-wrap:anywhere] max-[600px]:[&_dd]:mb-[7px]";
const tabListClass =
  "inline-flex w-fit min-w-0 flex-wrap items-center gap-1 rounded-lg bg-[var(--raised)] p-1";
const tabClass =
  "flex h-8 cursor-pointer items-center gap-1.5 rounded-md px-3 text-sm font-medium text-[var(--muted)] transition-colors data-[selected]:bg-[var(--surface)] data-[selected]:text-[var(--ink)] data-[selected]:shadow-[0_1px_2px_rgb(0_0_0/0.08)] data-[focus-visible]:outline-2 data-[focus-visible]:outline-[var(--accent)] data-[focus-visible]:outline-offset-2 pointer-coarse:min-h-10 max-[599px]:min-h-9";
const tabPanelClass =
  "min-w-0 focus-visible:outline-2 focus-visible:outline-[var(--focus)] focus-visible:outline-offset-2 data-[inert]:hidden";
const disclosureClass =
  "min-w-0 rounded-lg border border-[var(--line)] bg-[var(--surface)] p-3 [&:not([open])>div]:hidden";
const issueClass =
  "grid min-w-0 gap-2 rounded-lg border border-[color-mix(in_srgb,var(--error)_35%,var(--line))] bg-[var(--error-soft)] p-3 text-[var(--error)]";

const copy = {
  en: {
    compareEffectiveMetadata: "Compare effective metadata",
    actualCoverRegion: "Actual cover of the current publication",
    actualCover: "Actual cover",
    unknown: "unknown",
    lyrics: "lyrics",
    lyricsUnknown: "unknown",
    generation: "generation",
    coverEmbeddingOutcome: "Selected cover embedding:",
    verified: "verified",
    unverified: "not verified",
    lyricsPublicationResult: "Lyrics result for current publication",
    lyricsInCurrentPublication: "Lyrics in current publication:",
    lyricsSource: "source",
    lyricsSourceNone: "no source selected",
    lyricsSourceLibrary: "library source",
    lyricsSourceLrclib: "LRCLIB",
    lyricsOutcomeNotRequested: "lyrics not requested",
    lyricsOutcomeVerified: "lyrics included and verified",
    lyricsOutcomeRebuiltWithout: "publication rebuilt without lyrics",
    lyricsWarningLookup: "Lookup warning:",
    lyricsWarningPublication: "Publication warning:",
    warningSourceSelection: "a lyrics source must be selected",
    warningSettingsUnavailable: "lyrics lookup settings are unavailable",
    warningMetadataIncomplete: "metadata is insufficient for lyrics lookup",
    warningProviderUnavailable: "lyrics provider is unavailable",
    warningLookupFailed: "lyrics lookup failed",
    warningInvalidSyncedText: "found lyrics did not pass validation",
    warningReadbackMismatch: "lyrics did not pass validation after writing",
    cleanupRetryRunning: "Retry in progress…",
    retrySourceCleanup: "Retry source cleanup",
    cleanupRunning: "Source cleanup retry is in progress.",
    operationRunning: "Operation in progress: {stage} ({state})",
    savedLyricsPending: "Saved lyrics are waiting to be applied.",
    noLyricsPending: "No lyrics application is pending.",
    receipt: "Receipt: lyrics",
    confirmedInGeneration: "confirmed in generation",
    publishedGeneration: "published generation",
    warning: "warning",
    refreshData: "Refresh data",
    fileState: "File state",
    sourceFormat: "Source format",
    notSelected: "Not selected",
    path: "Path",
    composition: "Recording",
    providerHint: "Provider title",
    noLink: "No link",
    externalChangeTime: "External file change · observed at",
    tabList: "Position sections",
    overview: "Overview",
    filesPublication: "Files and publication",
    metadata: "Metadata",
    publication: "Publication",
    positionStatus: "Position status",
    published: "Published",
    unpublished: "Not published",
    selectedSource: "Selected source",
    noSourceSelected: "No source selected",
    currentOutput: "Current output",
    outputNotReported: "Not reported",
    fileCheck: "File check",
    openFilesPublication: "Open Files and publication",
    reviewPublicationIssue: "Review in Files and publication",
    chooseSourceAndPublish: "Choose a source and publish",
    publicationIssue: "Publication issue",
    backToPublicationQueue: "Back to publication queue",
    moreActions: "More actions",
    currentPublicationTags: "Current publication tags",
    effectiveFacts: "Effective metadata",
    noEffectiveFacts: "No effective metadata is available.",
  },
  ru: {
    compareEffectiveMetadata: "Сравнить эффективные метаданные",
    actualCoverRegion: "Фактическая обложка текущей публикации",
    actualCover: "Фактическая обложка",
    unknown: "неизвестно",
    lyrics: "текст",
    lyricsUnknown: "неизвестно",
    generation: "поколение",
    coverEmbeddingOutcome: "Встраивание выбранной обложки:",
    verified: "проверено",
    unverified: "не подтверждено",
    lyricsPublicationResult: "Результат текста песни для текущей публикации",
    lyricsInCurrentPublication: "Текст песни в текущей публикации:",
    lyricsSource: "источник",
    lyricsSourceNone: "источник не выбран",
    lyricsSourceLibrary: "источник медиатеки",
    lyricsSourceLrclib: "LRCLIB",
    lyricsOutcomeNotRequested: "текст не запрашивался",
    lyricsOutcomeVerified: "текст включён и проверен",
    lyricsOutcomeRebuiltWithout: "публикация пересоздана без текста",
    lyricsWarningLookup: "Предупреждение поиска:",
    lyricsWarningPublication: "Предупреждение публикации:",
    warningSourceSelection: "необходимо выбрать источник текста",
    warningSettingsUnavailable: "настройки поиска текста недоступны",
    warningMetadataIncomplete: "недостаточно метаданных для поиска текста",
    warningProviderUnavailable: "провайдер текста недоступен",
    warningLookupFailed: "не удалось найти текст",
    warningInvalidSyncedText: "найденный текст не прошёл проверку",
    warningReadbackMismatch: "текст не прошёл проверку после записи",
    cleanupRetryRunning: "Повтор выполняется…",
    retrySourceCleanup: "Повторить очистку источников",
    cleanupRunning: "Повторная очистка выполняется.",
    operationRunning: "Выполняется операция: {stage} ({state})",
    savedLyricsPending: "Сохранённый текст ожидает применения.",
    noLyricsPending: "Нет ожидающего применения текста.",
    receipt: "Квитанция: текст",
    confirmedInGeneration: "подтверждено в поколении",
    publishedGeneration: "опубликованное поколение",
    warning: "предупреждение",
    refreshData: "Обновить данные",
    fileState: "Состояние файла",
    sourceFormat: "Формат источника",
    notSelected: "Не выбран",
    path: "Путь",
    composition: "Композиция",
    providerHint: "Название у провайдера",
    noLink: "Нет связи",
    externalChangeTime: "Внешнее изменение файла · время наблюдения",
    tabList: "Разделы позиции",
    overview: "Обзор",
    filesPublication: "Файлы и публикация",
    metadata: "Метаданные",
    publication: "Публикация",
    positionStatus: "Состояние позиции",
    published: "Опубликовано",
    unpublished: "Не опубликовано",
    selectedSource: "Выбранный источник",
    noSourceSelected: "Источник не выбран",
    currentOutput: "Текущий результат",
    outputNotReported: "Не сообщено",
    fileCheck: "Проверка файла",
    openFilesPublication: "Открыть «Файлы и публикация»",
    reviewPublicationIssue: "Проверить в «Файлы и публикация»",
    chooseSourceAndPublish: "Выбрать источник и опубликовать",
    publicationIssue: "Проблема публикации",
    backToPublicationQueue: "Назад к очереди публикации",
    moreActions: "Другие действия",
    currentPublicationTags: "Теги текущей публикации",
    effectiveFacts: "Действительные метаданные",
    noEffectiveFacts: "Действительные метаданные недоступны.",
  },
} as const;

type ViewMessage = keyof typeof copy.en;

function text(
  locale: Locale,
  key: ViewMessage,
  values: Record<string, string | number> = {},
) {
  return copy[locale][key].replace(/\{([^}]+)\}/g, (_, name: string) =>
    String(values[name] ?? `{${name}}`),
  );
}

function lyricsSourceLabel(source: string, locale: Locale) {
  const key =
    source === "none"
      ? "lyricsSourceNone"
      : source === "source"
        ? "lyricsSourceLibrary"
        : source === "lrclib"
          ? "lyricsSourceLrclib"
          : undefined;
  return key ? text(locale, key as ViewMessage) : source;
}

function lyricsOutcomeLabel(outcome: string, locale: Locale) {
  const key =
    outcome === "not_requested"
      ? "lyricsOutcomeNotRequested"
      : outcome === "exact_round_trip"
        ? "lyricsOutcomeVerified"
        : outcome === "rebuilt_without_lyrics"
          ? "lyricsOutcomeRebuiltWithout"
          : undefined;
  return key ? text(locale, key) : outcome;
}

function lyricsWarningLabel(code: string, locale: Locale) {
  const keys: Record<string, ViewMessage> = {
    lyrics_source_selection_required: "warningSourceSelection",
    lyrics_settings_unavailable: "warningSettingsUnavailable",
    lyrics_lookup_metadata_incomplete: "warningMetadataIncomplete",
    lyrics_provider_unavailable: "warningProviderUnavailable",
    lyrics_lookup_failed: "warningLookupFailed",
    lyrics_lookup_invalid_synced_text: "warningInvalidSyncedText",
    readback_mismatch: "warningReadbackMismatch",
  };
  const key = keys[code];
  return key ? text(locale, key) : code;
}

export type TrackScreenHeadingView = {
  displayNumber: string;
  title: string;
  artist?: string;
  releaseTitle: string;
  releaseHref?: string;
  libraryHref: string;
};

export type TrackScreenSourcesView = {
  position: string;
  items: readonly LibraryPositionSource[];
  choice?: { value: string; onChange: (id: string) => void };
  inspection?: {
    value?: string;
    onChange: (id: string, trigger?: HTMLElement | null) => void;
  };
};

/** Selected track screen section. The route codec uses the same identifiers. */
export type TrackScreenTab = LibraryPositionTab;

export const trackScreenTabs = [
  "overview",
  "publication",
  "metadata",
] as const satisfies readonly TrackScreenTab[];

/** A publication-queue issue brought into the position screen by a deep link. */
export type TrackScreenPublicationIssueView = {
  message: string;
  /** Optional queue return address; omit when the host cannot build one. */
  backHref?: string;
  /** Optional localized label; the view defaults to the publication queue. */
  backLabel?: string;
  /** Optional follow-up supplied by the host, rendered before the back link. */
  action?: { label: string; href: string };
  title?: string;
};

/** Simple effective metadata fact, already localized by the host. */
export type TrackScreenMetadataFact = {
  id?: string;
  label: string;
  value: string;
};

export type TrackScreenPublicationCurrentView = {
  details: ComponentProps<typeof PublicationCurrentDetailsView>["details"];
  generation?: string;
  cover?: { state?: string; sha256?: string };
  lyrics?: string;
  tags: ComponentProps<typeof PublicationTagSnapshotView>["tags"];
};

export type TrackScreenCoverEmbeddingView = {
  outcome: string;
  warningCode?: string;
};

export type TrackScreenLyricsResultView = {
  outcome: string;
  source: string;
  resolutionWarningCode?: string;
  outcomeWarningCode?: string;
};

export type TrackScreenCleanupWarningView = {
  key: string;
  message: string;
  canRetry: boolean;
  retryPending: boolean;
  disabled: boolean;
  running: boolean;
  onRetry: () => void;
};

export type TrackScreenPublicationActivityView =
  | { kind: "operation"; stage: string; state: string }
  | { kind: "savedLyricsPending" }
  | { kind: "none" };

export type TrackScreenManualLyricsReceiptView = {
  desiredState: string;
  confirmedGeneration: string;
  publishedGeneration: string;
  warningCode?: string;
};

/** CURRENT, lyrics, cover and cleanup reads for the selected position. */
export type TrackScreenPublicationView = {
  published?: boolean;
  status?: string;
  diff?: ReactNode;
  /** Actual output identity, separate from filesystem observation. */
  output?: { label: string; summary?: string };
  outputStatus?: string;
  onPreview?: () => void;
  previewNotice?: string;
  current?: TrackScreenPublicationCurrentView;
  coverEmbedding?: TrackScreenCoverEmbeddingView;
  lyricsResult?: TrackScreenLyricsResultView;
  cleanupWarnings?: readonly TrackScreenCleanupWarningView[];
  activity?: TrackScreenPublicationActivityView;
  receipt?: TrackScreenManualLyricsReceiptView;
  notice?: string;
  error?: string;
  readError?: { text: string; onRefresh: () => void };
};

export type TrackScreenTechnicalView = {
  position: string;
  title?: string;
  durationMs: number | null;
  recordingTitle?: string | null;
  fileState?: string;
  sourceFormat?: string;
  path?: string;
  composition?: string;
  providerHint?: string;
  externalChangeTime?: string;
  help?: string;
  current?: TrackScreenPublicationCurrentView;
};

export type TrackScreenNotFoundView = {
  message: string;
  releaseHref: string;
  releaseLabel: string;
};

export type TrackProviderRefreshModel = {
  action?: {
    label: string;
    disabled: boolean;
    onPress: () => void;
    pending?: string;
    notice?: string;
    error?: string;
  };
  genreError?: string;
};

export function TrackProviderRefreshView({
  action,
  genreError,
}: TrackProviderRefreshModel) {
  return (
    <>
      {action && (
        <div className="space-y-2">
          <button
            type="button"
            className={buttonClass}
            disabled={action.disabled}
            onClick={action.onPress}
          >
            {action.label}
          </button>
          {action.pending && <p role="status">{action.pending}</p>}
          {action.notice && <p role="status">{action.notice}</p>}
          {action.error && (
            <p role="alert" className={alertClass}>
              {action.error}
            </p>
          )}
        </div>
      )}
      {genreError && (
        <p role="alert" className={alertClass}>
          {genreError}
        </p>
      )}
    </>
  );
}

/**
 * Whole position route composition. The route owns every read and action
 * admission; this view renders a typed snapshot, a typed editor stack with
 * named host bridges, and narrow slots for the contextual publication preview,
 * deletion, source tags and tag comparison actors. The publication preview slot
 * stays outside the active-only branches so its controller keeps one
 * reconciliation identity across overview and position routes.
 */
export type TrackScreenViewProps<
  ContextProps extends object = Record<never, never>,
> = {
  locale: Locale;
  active?: boolean;
  notFound?: TrackScreenNotFoundView;
  heading?: TrackScreenHeadingView;
  providerRefresh?: TrackProviderRefreshModel;
  /**
   * Selected position section. `tab` is controlled; `initialTab` seeds the
   * internal selection for an uncontrolled deep link. Both use route values
   * from the `tab` query parameter.
   */
  tab?: TrackScreenTab;
  initialTab?: TrackScreenTab;
  onTabChange?: (tab: TrackScreenTab) => void;
  /**
   * Issue supplied by the publication queue deep link. It is shown above the
   * tabs and the Files and publication details; the view never invents a fix.
   */
  publicationIssue?: TrackScreenPublicationIssueView;
  /**
   * Return address for a position opened from the publication queue. It stays
   * visible near the heading after an artist selection, unless the issue banner
   * already supplies the same back link.
   */
  queueBackHref?: string;
  /**
   * Already localized effective metadata facts shown before the metadata
   * editors. The host owns computed title/artist/genre values.
   */
  metadataFacts?: readonly TrackScreenMetadataFact[];
  /**
   * Typed position editor composition. The portable stack owns editor order and
   * mounts nothing for an inactive retained position.
   */
  editors?: Omit<LibraryPositionEditorsViewProps, "locale" | "active">;
  sources?: TrackScreenSourcesView;
  publication?: TrackScreenPublicationView;
  contextualPublication?: ContextPublicationPreviewSource<ContextProps>;
  actions?: ReactNode;
  deletion?: ReactNode;
  technical?: TrackScreenTechnicalView;
  tags?: {
    open: boolean;
    onOpenChange: (open: boolean) => void;
    /** Exact source whose "Inspect tags" action opened the drawer. */
    sourceId?: string;
    /** Comparison actor for that source; shown only for a reported CURRENT. */
    comparison?: ReactNode;
  };
};

function PublicationCurrentView({
  locale,
  current,
}: {
  locale: Locale;
  current: TrackScreenPublicationCurrentView;
}) {
  return (
    <>
      <PublicationCurrentDetailsView
        locale={locale}
        details={current.details}
      />
      {current.generation && (
        <p>
          {text(locale, "generation")} {current.generation}
        </p>
      )}
      {current.cover && (
        <fieldset
          className={fieldsetClass}
          aria-label={text(locale, "actualCoverRegion")}
        >
          <p className="wrap-anywhere">
            {text(locale, "actualCover")}:{" "}
            {current.cover.state ?? text(locale, "unknown")}
            {current.cover.sha256 ? ` · SHA-256: ${current.cover.sha256}` : ""}
          </p>
        </fieldset>
      )}
      {current.lyrics !== undefined && (
        <p>
          {text(locale, "lyrics")} {current.lyrics}.
        </p>
      )}
      <details className={disclosureClass}>
        <summary className="cursor-pointer font-semibold">
          {text(locale, "currentPublicationTags")}
        </summary>
        <div className="mt-2">
          <PublicationTagSnapshotView locale={locale} tags={current.tags} />
        </div>
      </details>
    </>
  );
}

function PublicationNotices({
  locale,
  publication,
}: {
  locale: Locale;
  publication?: TrackScreenPublicationView;
}) {
  return (
    <>
      {publication?.notice && <p role="status">{publication.notice}</p>}
      {publication?.error && (
        <p role="alert" className={alertClass}>
          {publication.error}
        </p>
      )}
      {publication?.readError && (
        <div role="alert" className={alertClass}>
          {publication.readError.text}{" "}
          <button
            type="button"
            className={buttonClass}
            onClick={publication.readError.onRefresh}
          >
            {text(locale, "refreshData")}
          </button>
        </div>
      )}
    </>
  );
}

function PublicationReads({
  locale,
  publication,
  deletion,
}: {
  locale: Locale;
  publication?: TrackScreenPublicationView;
  deletion?: ReactNode;
}) {
  const activity = publication?.activity;
  return (
    <div className="grid min-w-0 gap-2">
      {publication?.diff && (
        <details>
          <summary>{text(locale, "compareEffectiveMetadata")}</summary>
          {publication.diff}
        </details>
      )}
      <PublicationCurrentSummaryView
        locale={locale}
        published={publication?.published ?? false}
      />
      <dl className={factsClass}>
        <dt>{text(locale, "currentOutput")}</dt>
        <dd>
          <PublicationOutputValue locale={locale} publication={publication} />
        </dd>
        {publication?.outputStatus && (
          <>
            <dt>{text(locale, "fileCheck")}</dt>
            <dd>{publication.outputStatus}</dd>
          </>
        )}
      </dl>
      {publication?.current && (
        <PublicationCurrentView locale={locale} current={publication.current} />
      )}
      {publication?.coverEmbedding && (
        <p>
          {text(locale, "coverEmbeddingOutcome")}{" "}
          {publication.coverEmbedding.outcome === "verified"
            ? text(locale, "verified")
            : text(locale, "unverified")}
          {publication.coverEmbedding.warningCode
            ? ` · ${publication.coverEmbedding.warningCode}`
            : ""}
        </p>
      )}
      {publication?.lyricsResult && (
        <fieldset
          className={fieldsetClass}
          aria-label={text(locale, "lyricsPublicationResult")}
        >
          <p>
            {text(locale, "lyricsInCurrentPublication")}{" "}
            {lyricsOutcomeLabel(publication.lyricsResult.outcome, locale)}
            {` · ${text(locale, "lyricsSource")}: ${lyricsSourceLabel(
              publication.lyricsResult.source,
              locale,
            )}`}
          </p>
          {publication.lyricsResult.resolutionWarningCode && (
            <p>
              {text(locale, "lyricsWarningLookup")}{" "}
              {lyricsWarningLabel(
                publication.lyricsResult.resolutionWarningCode,
                locale,
              )}
            </p>
          )}
          {publication.lyricsResult.outcomeWarningCode && (
            <p>
              {text(locale, "lyricsWarningPublication")}{" "}
              {lyricsWarningLabel(
                publication.lyricsResult.outcomeWarningCode,
                locale,
              )}
            </p>
          )}
        </fieldset>
      )}
      {publication?.cleanupWarnings?.map((warning) => (
        <div key={warning.key} className={alertClass} role="alert">
          <p>{warning.message}</p>
          {warning.canRetry && (
            <button
              type="button"
              className={buttonClass}
              disabled={warning.retryPending || warning.disabled}
              onClick={warning.onRetry}
            >
              {warning.retryPending
                ? text(locale, "cleanupRetryRunning")
                : text(locale, "retrySourceCleanup")}
            </button>
          )}
          {warning.running && (
            <p role="status">{text(locale, "cleanupRunning")}</p>
          )}
        </div>
      ))}
      {activity && (
        <p>
          {activity.kind === "operation"
            ? text(locale, "operationRunning", {
                stage: activity.stage,
                state: activity.state,
              })
            : activity.kind === "savedLyricsPending"
              ? text(locale, "savedLyricsPending")
              : text(locale, "noLyricsPending")}
        </p>
      )}
      {publication?.receipt && (
        <p>
          {text(locale, "receipt")} {publication.receipt.desiredState}{" "}
          {text(locale, "confirmedInGeneration")}{" "}
          {publication.receipt.confirmedGeneration},
          {text(locale, "publishedGeneration")}{" "}
          {publication.receipt.publishedGeneration}
          {publication.receipt.warningCode
            ? ` · ${text(locale, "warning")}: ${publication.receipt.warningCode}`
            : ""}
          .
        </p>
      )}
      {deletion && (
        <details className={disclosureClass}>
          <summary className="cursor-pointer font-semibold">
            {text(locale, "moreActions")}
          </summary>
          <div className="mt-2">{deletion}</div>
        </details>
      )}
    </div>
  );
}

function PublicationOutputValue({
  locale,
  publication,
}: {
  locale: Locale;
  publication?: TrackScreenPublicationView;
}) {
  if (publication?.output) {
    return (
      <>
        <strong>{publication.output.label}</strong>
        {publication.output.summary ? ` · ${publication.output.summary}` : ""}
      </>
    );
  }
  return (
    <>
      {publication?.published
        ? text(locale, "outputNotReported")
        : text(locale, "unpublished")}
    </>
  );
}

function PositionStatus({
  locale,
  publication,
  issue,
}: {
  locale: Locale;
  publication?: TrackScreenPublicationView;
  issue?: TrackScreenPublicationIssueView;
}) {
  return (
    <section
      aria-label={text(locale, "positionStatus")}
      className="grid min-w-0 gap-2 rounded-lg border border-[var(--line)] bg-[var(--surface)] p-3"
    >
      <div className="flex flex-wrap items-center gap-2">
        <StatusBadge
          tone={
            publication?.published ? "success" : issue ? "warning" : "neutral"
          }
        >
          {publication?.published
            ? text(locale, "published")
            : text(locale, "unpublished")}
        </StatusBadge>
        {publication?.status && (
          <span className="min-w-0 text-sm text-[var(--ink)] wrap-anywhere">
            {publication.status}
          </span>
        )}
      </div>
      <PublicationNotices locale={locale} publication={publication} />
    </section>
  );
}

function PublicationIssueBanner({
  locale,
  issue,
}: {
  locale: Locale;
  issue: TrackScreenPublicationIssueView;
}) {
  return (
    <section
      role="alert"
      aria-label={text(locale, "publicationIssue")}
      className={issueClass}
    >
      <h2 className="m-0 text-sm font-semibold">
        {issue.title ?? text(locale, "publicationIssue")}
      </h2>
      <p className="my-0 wrap-anywhere">{issue.message}</p>
      {issue.action && (
        <a className="w-fit font-medium underline" href={issue.action.href}>
          {issue.action.label}
        </a>
      )}
      {issue.backHref && (
        <a className="w-fit font-medium underline" href={issue.backHref}>
          {issue.backLabel ?? text(locale, "backToPublicationQueue")}
        </a>
      )}
    </section>
  );
}

function TrackScreenOverview({
  locale,
  sources,
  publication,
  issue,
  onOpenPublication,
}: {
  locale: Locale;
  sources?: TrackScreenSourcesView;
  publication?: TrackScreenPublicationView;
  issue?: TrackScreenPublicationIssueView;
  onOpenPublication: () => void;
}) {
  const selectedSource = sources?.items.find((source) => source.selected);
  const actionLabel = issue
    ? text(locale, "reviewPublicationIssue")
    : publication?.published
      ? text(locale, "openFilesPublication")
      : text(locale, "chooseSourceAndPublish");
  return (
    <section
      aria-label={text(locale, "overview")}
      className="grid min-w-0 gap-3 rounded-lg border border-[var(--line)] bg-[var(--surface)] p-3"
    >
      <dl className={factsClass}>
        <dt>{text(locale, "selectedSource")}</dt>
        <dd>
          {selectedSource ? (
            <>
              <strong>{selectedSource.label}</strong>
              {selectedSource.summary ? ` · ${selectedSource.summary}` : ""}
            </>
          ) : (
            text(locale, "noSourceSelected")
          )}
        </dd>
        <dt>{text(locale, "currentOutput")}</dt>
        <dd>
          <PublicationOutputValue locale={locale} publication={publication} />
        </dd>
        {publication?.outputStatus && (
          <>
            <dt>{text(locale, "fileCheck")}</dt>
            <dd>{publication.outputStatus}</dd>
          </>
        )}
      </dl>
      <div className={actionsClass}>
        <AppButton type="button" variant="primary" onPress={onOpenPublication}>
          {actionLabel}
        </AppButton>
      </div>
    </section>
  );
}

function PublicationPanel<ContextProps extends object = Record<never, never>>({
  locale,
  visible,
  sources,
  publication,
  contextualPublication,
  actions,
  deletion,
  technical,
}: {
  locale: Locale;
  visible: boolean;
  sources?: TrackScreenSourcesView;
  publication?: TrackScreenPublicationView;
  contextualPublication?: ContextPublicationPreviewSource<ContextProps>;
  actions?: ReactNode;
  deletion?: ReactNode;
  technical?: TrackScreenTechnicalView;
}) {
  const publicationTitle = useId();
  return (
    <div className="grid min-w-0 gap-3">
      <div className="grid min-w-0 gap-3 [grid-template-columns:minmax(0,1.05fr)_minmax(0,.95fr)] max-[1000px]:grid-cols-1">
        <div className="min-w-0">
          {visible && sources ? (
            <LibraryPositionSourcesView
              locale={locale}
              position={sources.position}
              sources={sources.items}
              choice={sources.choice}
              inspection={sources.inspection}
            />
          ) : null}
        </div>
        <section
          aria-labelledby={publicationTitle}
          className="mb-[10px] min-w-0 self-start rounded-lg border border-[var(--line)] bg-[var(--surface)] p-[10px] wrap-anywhere [&_h2]:mb-[9px] [&_h2]:text-sm [&_h3]:mb-2 [&_h3]:text-sm [&_h3]:font-semibold [&_p]:my-2"
        >
          <h2 id={publicationTitle}>{text(locale, "publication")}</h2>
          {visible ? (
            <>
              <PublicationReads
                locale={locale}
                publication={publication}
                deletion={deletion}
              />
              {publication?.onPreview && (
                <div className={actionsClass}>
                  <LibraryPositionPreviewButton
                    locale={locale}
                    onPreview={publication.onPreview}
                  />
                </div>
              )}
              {publication?.previewNotice ? (
                <ScreenStatus>{publication.previewNotice}</ScreenStatus>
              ) : null}
              {actions}
            </>
          ) : null}
          {contextualPublication ? (
            <ContextPublicationPreviewOutlet source={contextualPublication} />
          ) : null}
        </section>
      </div>
      {visible && technical ? (
        <TechnicalDetails locale={locale} technical={technical} />
      ) : null}
    </div>
  );
}

function MetadataPanel({
  locale,
  active,
  technical,
  metadataFacts,
  editors,
}: {
  locale: Locale;
  active: boolean;
  technical?: TrackScreenTechnicalView;
  metadataFacts?: readonly TrackScreenMetadataFact[];
  editors?: TrackScreenViewProps["editors"];
}) {
  return (
    <div className="grid min-w-0 gap-3">
      <section
        aria-label={text(locale, "effectiveFacts")}
        className={disclosureClass}
      >
        {metadataFacts?.length ? (
          <dl className={factsClass}>
            {metadataFacts.map((fact, index) => (
              <Fragment key={fact.id ?? `${fact.label}-${index}`}>
                <dt>{fact.label}</dt>
                <dd>{fact.value}</dd>
              </Fragment>
            ))}
          </dl>
        ) : technical ? (
          <LibraryPositionIdentityView
            locale={locale}
            position={technical.position}
            title={technical.title}
            durationMs={technical.durationMs}
            recordingTitle={technical.recordingTitle}
          />
        ) : (
          <p className="m-0">{text(locale, "noEffectiveFacts")}</p>
        )}
      </section>
      {active && editors ? (
        <LibraryPositionEditorsView
          locale={locale}
          active={active}
          {...editors}
        />
      ) : null}
    </div>
  );
}

function TechnicalDetails({
  locale,
  technical,
}: {
  locale: Locale;
  technical: TrackScreenTechnicalView;
}) {
  return (
    <LibraryPositionTechnicalDetailsView
      locale={locale}
      afterCurrent={technical.help ? <p>{technical.help}</p> : undefined}
      current={
        technical.current && (
          <PublicationCurrentView locale={locale} current={technical.current} />
        )
      }
    >
      <LibraryPositionIdentityView
        locale={locale}
        position={technical.position}
        title={technical.title}
        durationMs={technical.durationMs}
        recordingTitle={technical.recordingTitle}
      />
      {technical.fileState && (
        <dl className={factsClass}>
          <dt>{text(locale, "fileState")}</dt>
          <dd>{technical.fileState}</dd>
          <dt>{text(locale, "sourceFormat")}</dt>
          <dd>{technical.sourceFormat ?? text(locale, "notSelected")}</dd>
        </dl>
      )}
      {(technical.path ||
        technical.composition ||
        technical.providerHint ||
        technical.externalChangeTime) && (
        <dl className={factsClass}>
          <dt>{text(locale, "path")}</dt>
          <dd>{technical.path ?? text(locale, "notSelected")}</dd>
          <dt>{text(locale, "composition")}</dt>
          <dd>{technical.composition ?? text(locale, "noLink")}</dd>
          <dt>{text(locale, "providerHint")}</dt>
          <dd>{technical.providerHint}</dd>
          {technical.externalChangeTime && (
            <>
              <dt>{text(locale, "externalChangeTime")}</dt>
              <dd>{technical.externalChangeTime}</dd>
            </>
          )}
        </dl>
      )}
    </LibraryPositionTechnicalDetailsView>
  );
}

export function TrackScreenView<
  ContextProps extends object = Record<never, never>,
>({
  locale,
  active = true,
  notFound,
  heading,
  providerRefresh,
  tab,
  initialTab,
  onTabChange,
  publicationIssue,
  queueBackHref,
  metadataFacts,
  editors,
  sources,
  publication,
  contextualPublication,
  actions,
  deletion,
  technical,
  tags,
}: TrackScreenViewProps<ContextProps>) {
  const [internalTab, setInternalTab] = useState<TrackScreenTab>(
    initialTab ?? "overview",
  );
  const lastInitialTab = useRef(initialTab);
  useEffect(() => {
    if (tab !== undefined || lastInitialTab.current === initialTab) return;
    lastInitialTab.current = initialTab;
    setInternalTab(initialTab ?? "overview");
  }, [initialTab, tab]);
  const selectedTab = tab ?? internalTab;
  const [metadataVisited, setMetadataVisited] = useState(
    selectedTab === "metadata",
  );
  useEffect(() => {
    if (selectedTab === "metadata" && !metadataVisited)
      setMetadataVisited(true);
  }, [metadataVisited, selectedTab]);
  const selectTab = (next: TrackScreenTab) => {
    if (tab === undefined) setInternalTab(next);
    onTabChange?.(next);
  };
  const tagsOpen = tags?.open ?? false;
  const tagTrigger = useRef<HTMLElement | null>(null);
  const activeTab = useRef(selectedTab);
  useEffect(() => {
    if (activeTab.current === selectedTab) return;
    activeTab.current = selectedTab;
    if (!tagsOpen) return;
    tagTrigger.current = null;
    tags?.onOpenChange(false);
  }, [selectedTab, tags, tagsOpen]);
  const wasActive = useRef(active);
  useEffect(() => {
    if (wasActive.current === active) return;
    wasActive.current = active;
    if (active || !tagsOpen) return;
    tagTrigger.current = null;
    tags?.onOpenChange(false);
  }, [active, tags, tagsOpen]);
  const inspection = sources?.inspection
    ? {
        value: sources.inspection.value,
        onChange: (id: string, trigger?: HTMLElement | null) => {
          tagTrigger.current = trigger ?? null;
          sources.inspection?.onChange(id, trigger);
        },
      }
    : undefined;
  const taggedSources = sources ? { ...sources, inspection } : undefined;
  const tagSourceId = tags?.sourceId ?? inspection?.value;
  const tagSource = tagSourceId
    ? taggedSources?.items.find((source) => source.id === tagSourceId)
    : undefined;
  const currentTags = publication?.current ?? technical?.current;
  if (notFound) {
    return (
      <section className={libraryPositionPageClassName}>
        <a className="underline" href={notFound.releaseHref}>
          {notFound.releaseLabel}
        </a>
        <p role="alert" className={alertClass}>
          {notFound.message}
        </p>
      </section>
    );
  }
  return (
    <section
      aria-labelledby={active ? libraryPositionTitleId : undefined}
      className={active ? libraryPositionPageClassName : "contents"}
      hidden={!active}
    >
      {active && heading && (
        <LibraryPositionHeadingView locale={locale} {...heading} />
      )}
      {active && queueBackHref && !publicationIssue?.backHref && (
        <p className="m-0 text-sm">
          <a className="font-medium underline" href={queueBackHref}>
            {text(locale, "backToPublicationQueue")}
          </a>
        </p>
      )}
      {active && providerRefresh && (
        <TrackProviderRefreshView {...providerRefresh} />
      )}
      <article
        aria-labelledby={active ? libraryPositionTitleId : undefined}
        className="grid min-w-0 gap-3"
        hidden={!active}
      >
        <PositionStatus
          locale={locale}
          publication={publication}
          issue={publicationIssue}
        />
        {publicationIssue && (
          <PublicationIssueBanner locale={locale} issue={publicationIssue} />
        )}
        <Tabs
          selectedKey={selectedTab}
          onSelectionChange={(key: Key) => {
            const next =
              typeof key === "string" &&
              trackScreenTabs.includes(key as TrackScreenTab)
                ? (key as TrackScreenTab)
                : "overview";
            selectTab(next);
          }}
          aria-label={text(locale, "tabList")}
          className="grid min-w-0 gap-3"
        >
          <TabList
            aria-label={text(locale, "tabList")}
            className={tabListClass}
          >
            {trackScreenTabs.map((tabKey) => (
              <Tab key={tabKey} id={tabKey} className={tabClass}>
                {text(
                  locale,
                  tabKey === "publication" ? "filesPublication" : tabKey,
                )}
              </Tab>
            ))}
          </TabList>
          <TabPanel id="overview" className={tabPanelClass}>
            <TrackScreenOverview
              locale={locale}
              sources={sources}
              publication={publication}
              issue={publicationIssue}
              onOpenPublication={() => selectTab("publication")}
            />
          </TabPanel>
          <TabPanel id="publication" shouldForceMount className={tabPanelClass}>
            <div hidden={selectedTab !== "publication"}>
              <PublicationPanel
                locale={locale}
                visible={active && selectedTab === "publication"}
                sources={taggedSources}
                publication={publication}
                contextualPublication={contextualPublication}
                actions={actions}
                deletion={deletion}
                technical={technical}
              />
            </div>
          </TabPanel>
          <TabPanel
            id="metadata"
            shouldForceMount={metadataVisited}
            className={tabPanelClass}
          >
            <div hidden={selectedTab !== "metadata"}>
              <MetadataPanel
                locale={locale}
                active={active}
                technical={technical}
                metadataFacts={metadataFacts}
                editors={editors}
              />
            </div>
          </TabPanel>
        </Tabs>
        {active && tagsOpen && tags && (
          <LibraryPositionSourceTagsDrawer
            locale={locale}
            source={tagSource}
            currentReported={Boolean(currentTags)}
            comparison={tags.comparison}
            onClose={() => tags.onOpenChange(false)}
            returnFocusRef={tagTrigger}
          />
        )}
      </article>
    </section>
  );
}
