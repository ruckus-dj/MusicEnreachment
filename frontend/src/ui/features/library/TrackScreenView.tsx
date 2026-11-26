import type { ComponentProps, ReactNode } from "react";
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
import { LibraryPositionTagsDisclosure } from "./LibraryPositionTagsDisclosure";
import { LibraryPositionTechnicalDetailsView } from "./LibraryPositionTechnicalDetailsView";
import {
  LibraryPositionPreviewButton,
  LibraryPositionWorkspace,
} from "./LibraryPositionWorkspace";
import {
  PublicationCurrentDetailsView,
  PublicationCurrentSummaryView,
} from "./PublicationCurrentSummaryView";
import { PublicationTagSnapshotView } from "./PublicationTagSnapshotView";

const buttonClass =
  "min-h-[30px] rounded border border-[var(--control)] bg-[var(--surface)] px-2 py-1 text-[var(--ink)] hover:enabled:border-[var(--accent)] hover:enabled:bg-[var(--raised)] disabled:border-[var(--line)] disabled:bg-[var(--raised)] disabled:text-[var(--muted)] max-[599px]:min-h-10";
const fieldsetClass =
  "min-w-0 rounded-[5px] border border-[var(--line)] bg-[var(--surface)] px-2.5 py-2";
const alertClass = "text-[var(--error)] wrap-anywhere";
const actionsClass = "my-2 flex flex-wrap items-center gap-[7px]";
const factsClass =
  "my-[10px] grid grid-cols-[minmax(130px,.75fr)_minmax(0,2fr)] gap-x-3 gap-y-[6px] max-[600px]:grid-cols-1 max-[600px]:gap-y-0 [&_dt]:text-[11px] [&_dt]:text-[var(--muted)] [&_dd]:m-0 [&_dd]:[overflow-wrap:anywhere] max-[600px]:[&_dd]:mb-[7px]";

const copy = {
  en: {
    metadataState: "Publication metadata state",
    compareEffectiveMetadata: "Compare effective metadata",
    publicationFileStatus: "Publication file",
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
  },
  ru: {
    metadataState: "Состояние метаданных публикации",
    compareEffectiveMetadata: "Сравнить эффективные метаданные",
    publicationFileStatus: "Файл публикации",
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
  inspection?: { value?: string; onChange: (id: string) => void };
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
 * deletion and tag evidence actors. The publication preview slot stays outside
 * the active-only branches so its controller keeps one reconciliation identity
 * across overview and position routes.
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
    comparison?: ReactNode;
  };
  evidence?: ReactNode;
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
      <PublicationTagSnapshotView locale={locale} tags={current.tags} />
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
    <>
      {publication?.status && (
        <p role="note" aria-label={text(locale, "metadataState")}>
          <strong>{publication.status}</strong>
        </p>
      )}
      {publication?.diff && (
        <details>
          <summary>{text(locale, "compareEffectiveMetadata")}</summary>
          {publication.diff}
        </details>
      )}
      {publication?.outputStatus && (
        <p>
          {text(locale, "publicationFileStatus")}: {publication.outputStatus}.
        </p>
      )}
      <PublicationCurrentSummaryView
        locale={locale}
        published={publication?.published ?? false}
      />
      {deletion}
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
  editors,
  sources,
  publication,
  contextualPublication,
  actions,
  deletion,
  technical,
  tags,
  evidence,
}: TrackScreenViewProps<ContextProps>) {
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
      {active && providerRefresh && (
        <TrackProviderRefreshView {...providerRefresh} />
      )}
      <article
        aria-labelledby={active ? libraryPositionTitleId : undefined}
        className="grid min-w-0 gap-3"
        hidden={!active}
      >
        <LibraryPositionWorkspace
          locale={locale}
          editors={
            editors ? (
              <LibraryPositionEditorsView
                locale={locale}
                active={active}
                {...editors}
              />
            ) : undefined
          }
          sources={
            active && sources ? (
              <LibraryPositionSourcesView
                locale={locale}
                position={sources.position}
                sources={sources.items}
                choice={sources.choice}
                inspection={sources.inspection}
              />
            ) : undefined
          }
          publication={
            active ? (
              <PublicationReads
                locale={locale}
                publication={publication}
                deletion={deletion}
              />
            ) : undefined
          }
          actions={
            <>
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
              {contextualPublication ? (
                <ContextPublicationPreviewOutlet
                  source={contextualPublication}
                />
              ) : null}
              {actions}
            </>
          }
          technicalDetails={
            active && technical ? (
              <TechnicalDetails locale={locale} technical={technical} />
            ) : undefined
          }
        />
        {active && tags && (
          <LibraryPositionTagsDisclosure
            locale={locale}
            open={tags.open}
            onOpenChange={tags.onOpenChange}
          >
            {tags.comparison ?? evidence}
          </LibraryPositionTagsDisclosure>
        )}
      </article>
    </section>
  );
}
