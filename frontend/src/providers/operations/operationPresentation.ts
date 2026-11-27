import type { OperationResponse } from "../../api/generated/client.schemas";
import operationKinds from "../../ui/features/operations/operationKindLabels.json";
import { sourceCleanupOperationCopy } from "../../ui/features/operations/sourceCleanupOperationCopy";
import { sourceScanOperationStages } from "../../ui/features/operations/sourceScanOperationStages";
import type { OperationProgressBar } from "../../ui/features/setup/OperationProgressView";
import type { Locale } from "../../ui/i18n/locale";

type LocalizedText = Record<Locale, string>;

const operationKindLabels: Record<string, LocalizedText> = operationKinds.kinds;

const operationStateLabels: Record<string, LocalizedText> = {
  queued: { en: "Queued", ru: "В очереди" },
  running: { en: "Running", ru: "Выполняется" },
  failed: { en: "Failed", ru: "Ошибка" },
  succeeded: { en: "Completed", ru: "Завершена" },
};

const operationStageLabels: Record<string, LocalizedText> = {
  ...sourceCleanupOperationCopy.stages,
  failed: { en: "Failed", ru: "Ошибка" },
  succeeded: { en: "Completed", ru: "Завершена" },
  resolve: { en: "Preparing download", ru: "Подготовка загрузки" },
  download: { en: "Downloading files", ru: "Загрузка файлов" },
  extract: { en: "Unpacking files", ru: "Распаковка файлов" },
  materialize: { en: "Preparing files", ru: "Подготовка файлов" },
  files_materialized: { en: "Files prepared", ru: "Файлы подготовлены" },
  copy: { en: "Copying", ru: "Копирование" },
  verify: { en: "Verifying", ru: "Проверка" },
  commit_targets: { en: "Updating paths", ru: "Обновление путей" },
  prepare_restore: {
    en: "Preparing recovery",
    ru: "Подготовка восстановления",
  },
  switch: { en: "Switching folders", ru: "Переключение папок" },
  rollback_pending: { en: "Waiting to restore", ru: "Ожидание восстановления" },
  move: { en: "Moving tools folder", ru: "Перемещение папки инструментов" },
  ...sourceScanOperationStages,
  probing: { en: "Inspecting audio", ru: "Анализ аудио" },
  fingerprinting: {
    en: "Calculating audio fingerprint",
    ru: "Расчёт аудиоотпечатка",
  },
  hashing: { en: "Calculating checksum", ru: "Расчёт контрольной суммы" },
  fetching_provider: {
    en: "Requesting MusicBrainz",
    ru: "Запрос к MusicBrainz",
  },
  fetching_acoustid: { en: "Requesting AcoustID", ru: "Запрос к AcoustID" },
  provider_cache_reused: {
    en: "Using saved data",
    ru: "Использованы сохранённые данные",
  },
  provider_fallback_result_reused: {
    en: "Using saved fallback results",
    ru: "Использованы сохранённые результаты поиска",
  },
  provider_fallback_lookup_reused: {
    en: "Using saved fallback lookup",
    ru: "Использованы сохранённые результаты поиска",
  },
  provider_result_reused: {
    en: "Using saved results",
    ru: "Использованы сохранённые результаты",
  },
  provider_response_saved: { en: "Results saved", ru: "Результаты сохранены" },
  provider_fetch_failed: {
    en: "Request failed",
    ru: "Не удалось выполнить запрос",
  },
  provider_temporarily_unavailable: {
    en: "MusicBrainz temporarily unavailable",
    ru: "MusicBrainz временно недоступен",
  },
  waiting_provider: {
    en: "Waiting for catalog responses",
    ru: "Ожидание ответов каталога",
  },
  manual_lookup: { en: "Manual lookup", ru: "Ручной поиск" },
  refreshing: { en: "Refreshing catalog", ru: "Обновление каталога" },
  reconciling: { en: "Reconciling publication", ru: "Сверка публикации" },
  waiting: { en: "Waiting", ru: "Ожидание" },
  completed: { en: "Completed", ru: "Завершено" },
  review_required: { en: "Review required", ru: "Требуется проверка" },
  applied: { en: "Applied", ru: "Применено" },
  pending_seal: { en: "Preparing results", ru: "Подготовка результатов" },
  review_pending: { en: "Waiting for review", ru: "Ожидание проверки" },
  manual_review: { en: "Waiting for review", ru: "Ожидание проверки" },
  source_republish_recovery_required: {
    en: "Publication recovery required",
    ru: "Требуется восстановление публикации",
  },
  cleanup_recovery_required: {
    en: "Source cleanup recovery required",
    ru: "Требуется восстановление очистки исходных файлов",
  },
  cleanup_retry_running: {
    en: "Retrying source cleanup",
    ru: "Повторная очистка исходных файлов",
  },
  cleanup_retry_recovery_required: {
    en: "Source cleanup recovery required",
    ru: "Требуется восстановление очистки исходных файлов",
  },
  preparing: { en: "Preparing publication", ru: "Подготовка публикации" },
  running: { en: "Running", ru: "Выполняется" },
  rolled_back: {
    en: "Restored previous folder",
    ru: "Восстановлена прежняя папка",
  },
  switched: { en: "Folder switched", ru: "Папка переключена" },
  source_changed: { en: "Source changed", ru: "Источник изменился" },
};

const operationTargetLabels: Record<string, LocalizedText> = {
  install: { en: "Tool", ru: "Инструмент" },
  activate: { en: "Tool", ru: "Инструмент" },
  delete: { en: "Tool", ru: "Инструмент" },
  move_tools_root: { en: "Tools folder", ru: "Папка инструментов" },
  scan_source: { en: "Source", ru: "Источник" },
  analyze_source: { en: "Source", ru: "Источник" },
  cleanup_source_analysis_artifacts: sourceCleanupOperationCopy.target,
  provider_release_lookup: { en: "MusicBrainz", ru: "MusicBrainz" },
  provider_recording_lookup: { en: "MusicBrainz", ru: "MusicBrainz" },
  provider_release_search: { en: "MusicBrainz", ru: "MusicBrainz" },
  provider_recording_search: { en: "MusicBrainz", ru: "MusicBrainz" },
  provider_artist_search: { en: "MusicBrainz", ru: "MusicBrainz" },
  provider_artist_release_browse: { en: "MusicBrainz", ru: "MusicBrainz" },
  provider_acoustid_lookup: { en: "AcoustID", ru: "AcoustID" },
  matching_evaluation_v1: { en: "Library recordings", ru: "Записи библиотеки" },
  matching_apply_v1: { en: "Library recordings", ru: "Записи библиотеки" },
  publish_track_v1: { en: "Track", ru: "Трек" },
  cleanup_publication_sources_v1: {
    en: "Published source files",
    ru: "Исходные файлы публикации",
  },
  publication_reconcile_v1: { en: "Publication", ru: "Публикация" },
  retry_publication_sources_v1: {
    en: "Published source files",
    ru: "Исходные файлы публикации",
  },
  obsolete_publication_output_cleanup_v1: {
    en: "Publication files",
    ru: "Файлы публикации",
  },
  refresh_genre_catalog_v1: { en: "Genre catalog", ru: "Каталог жанров" },
};

export function operationKindLabel(kind: string, locale: Locale) {
  return operationKindLabels[kind]?.[locale] ?? operationKinds.fallback[locale];
}

export function operationStateLabel(state: string, locale: Locale) {
  return (
    operationStateLabels[state]?.[locale] ??
    (locale === "ru" ? "Статус операции" : "Operation status")
  );
}

export function operationStageLabel(stage: string, locale: Locale) {
  const retrying = stage.startsWith("retry:");
  const normalizedStage = retrying ? stage.slice("retry:".length) : stage;
  const label = operationStageLabels[normalizedStage]?.[locale] ?? "";
  if (!label || !retrying) return label;
  return locale === "ru"
    ? `Повтор: ${label.toLocaleLowerCase(locale)}`
    : `Retry: ${label}`;
}

function installIdentity(operation: OperationResponse) {
  if (operation.kind !== "install") return undefined;
  const identity = operation.target_identity?.split(":");
  if (
    identity?.length === 5 &&
    (identity[0] === "ffmpeg" || identity[0] === "fpcalc") &&
    identity[1] &&
    identity[2]
  ) {
    return {
      tool: identity[0] === "ffmpeg" ? "FFmpeg" : "fpcalc",
      release: identity[2],
    };
  }
  return undefined;
}

/** Compact target used by wizard copy: "FFmpeg 9.0". */
export function toolInstallTarget(
  operation: OperationResponse,
  locale: Locale,
) {
  const install = installIdentity(operation);
  if (install) return `${install.tool} ${install.release}`;
  return operationTargetLabel(operation, locale);
}

export function operationTargetLabel(
  operation: OperationResponse,
  locale: Locale,
) {
  const install = installIdentity(operation);
  if (install) {
    return locale === "ru"
      ? `${install.tool}, версия ${install.release}`
      : `${install.tool}, version ${install.release}`;
  }
  return (
    operationTargetLabels[operation.kind]?.[locale] ??
    (locale === "ru" ? "Объект операции" : "Operation target")
  );
}

/** Concise installed sentence for a settled tool install operation. */
export function toolInstallCompletedText(
  operation: OperationResponse,
  locale: Locale,
) {
  const target = toolInstallTarget(operation, locale);
  return locale === "ru" ? `${target} — установлен.` : `${target} — installed.`;
}

function normalizedStage(stage: string) {
  return stage.startsWith("retry:") ? stage.slice("retry:".length) : stage;
}

/**
 * Progress for a running tool install. Only the download stage can report a
 * truthful byte total; extract and verification stages stay indeterminate so a
 * completed download never leaves a stale 100% bar behind.
 */
export function toolInstallProgressBar(
  operation: OperationResponse,
  locale: Locale,
): OperationProgressBar | undefined {
  if (operation.state !== "running") return undefined;
  const stage = normalizedStage(operation.stage);
  const target = toolInstallTarget(operation, locale);
  const stageLabel = operationStageLabel(operation.stage, locale);
  const fallback = locale === "ru" ? "Ход выполнения" : "Progress";
  const label = `${target} — ${stageLabel || fallback}`;
  const total = operation.bytes_total;
  if (stage !== "download" || total == null || total <= 0) {
    return { kind: "indeterminate", label };
  }
  const completed = Math.max(0, Math.min(operation.bytes_completed, total));
  const percent = Math.floor((completed / total) * 100) / 100;
  return {
    kind: "determinate",
    label,
    value: completed,
    max: total,
    text: new Intl.NumberFormat(locale, {
      style: "percent",
      maximumFractionDigits: 0,
    }).format(percent),
  };
}
