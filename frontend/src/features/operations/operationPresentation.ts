import type { OperationResponse } from "../../api/generated/client.schemas";
import type { Locale } from "../../i18n/locale";

type LocalizedText = Record<Locale, string>;

const operationKindLabels: Record<string, LocalizedText> = {
  install: { en: "Tool download", ru: "Загрузка инструмента" },
  activate: { en: "Tool activation", ru: "Активация инструмента" },
  delete: { en: "Tool removal", ru: "Удаление инструмента" },
  move_tools_root: {
    en: "Tools folder move",
    ru: "Перемещение папки инструментов",
  },
  scan_source: { en: "Source scan", ru: "Сканирование источника" },
  analyze_source: { en: "Source analysis", ru: "Анализ источника" },
  cleanup_source_analysis_artifacts: {
    en: "Analysis artifact cleanup",
    ru: "Очистка файлов анализа",
  },
  provider_release_lookup: { en: "Release lookup", ru: "Поиск релиза" },
  provider_recording_lookup: { en: "Recording lookup", ru: "Поиск записи" },
  provider_release_search: { en: "Release search", ru: "Поиск релиза" },
  provider_recording_search: { en: "Recording search", ru: "Поиск записи" },
  provider_artist_search: { en: "Artist search", ru: "Поиск исполнителя" },
  provider_artist_release_browse: {
    en: "Artist releases",
    ru: "Релизы исполнителя",
  },
  provider_acoustid_lookup: { en: "AcoustID lookup", ru: "Поиск в AcoustID" },
  matching_evaluation_v1: {
    en: "Match evaluation",
    ru: "Оценка совпадений",
  },
  matching_apply_v1: { en: "Match application", ru: "Применение совпадений" },
  publish_track_v1: { en: "Track publication", ru: "Публикация трека" },
  cleanup_publication_sources_v1: {
    en: "Published source cleanup",
    ru: "Очистка исходных файлов публикации",
  },
  publication_reconcile_v1: {
    en: "Publication recovery",
    ru: "Восстановление публикации",
  },
  retry_publication_sources_v1: {
    en: "Source cleanup retry",
    ru: "Повторная очистка исходных файлов публикации",
  },
  obsolete_publication_output_cleanup_v1: {
    en: "Obsolete publication cleanup",
    ru: "Очистка устаревших файлов публикации",
  },
  refresh_genre_catalog_v1: {
    en: "Genre catalog refresh",
    ru: "Обновление каталога жанров",
  },
};

const operationStateLabels: Record<string, LocalizedText> = {
  queued: { en: "Queued", ru: "В очереди" },
  running: { en: "Running", ru: "Выполняется" },
  failed: { en: "Failed", ru: "Ошибка" },
  succeeded: { en: "Completed", ru: "Завершена" },
};

const operationStageLabels: Record<string, LocalizedText> = {
  queued: { en: "Queued", ru: "В очереди" },
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
  traversing: { en: "Reading source folders", ru: "Обход папок источника" },
  applying: { en: "Applying results", ru: "Применение результатов" },
  probing: { en: "Inspecting audio", ru: "Анализ аудио" },
  fingerprinting: {
    en: "Calculating audio fingerprint",
    ru: "Расчёт аудиоотпечатка",
  },
  hashing: { en: "Calculating checksum", ru: "Расчёт контрольной суммы" },
  cleaning_artifacts: {
    en: "Cleaning analysis files",
    ru: "Очистка файлов анализа",
  },
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
  waiting_provider: {
    en: "Waiting for catalog responses",
    ru: "Ожидание ответов каталога",
  },
  manual_lookup: { en: "Manual lookup", ru: "Ручной поиск" },
  refreshing: { en: "Refreshing catalog", ru: "Обновление каталога" },
  finished: { en: "Finished", ru: "Завершено" },
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
  cleanup_source_analysis_artifacts: {
    en: "Analysis files",
    ru: "Файлы анализа",
  },
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

function text(
  labels: Record<string, LocalizedText>,
  key: string,
  locale: Locale,
) {
  return labels[key]?.[locale] ?? labels[key]?.en ?? "Operation";
}

export function operationKindLabel(kind: string, locale: Locale) {
  return text(operationKindLabels, kind, locale);
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

export function operationTargetLabel(
  operation: OperationResponse,
  locale: Locale,
) {
  if (operation.kind === "install") {
    const identity = operation.target_identity?.split(":");
    if (
      identity?.length === 5 &&
      (identity[0] === "ffmpeg" || identity[0] === "fpcalc") &&
      identity[1] &&
      identity[2]
    ) {
      const tool = identity[0] === "ffmpeg" ? "FFmpeg" : "fpcalc";
      return locale === "ru"
        ? `${tool}, версия ${identity[2]}`
        : `${tool}, version ${identity[2]}`;
    }
  }
  return (
    operationTargetLabels[operation.kind]?.[locale] ??
    (locale === "ru" ? "Объект операции" : "Operation target")
  );
}

export function operationProgressLabel(
  operation: OperationResponse,
  locale: Locale,
) {
  const completed = operation.bytes_completed.toLocaleString(locale);
  if (operation.bytes_total == null) {
    return locale === "ru" ? `${completed} байт` : `${completed} bytes`;
  }
  const total = operation.bytes_total.toLocaleString(locale);
  return locale === "ru"
    ? `${completed} / ${total} байт`
    : `${completed} / ${total} bytes`;
}
