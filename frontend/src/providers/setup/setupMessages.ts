import type { Locale } from "../../ui/i18n/locale";

const english = {
  directories: "Directories",
  toolsDirectory: "Tools directory",
  outputDirectory: "Output directory",
  directoryHelp:
    "These paths belong to the server, not this computer. The output directory must be empty.",
  toolsDirectoryRequired: "Choose the tools directory.",
  outputDirectoryRequired: "Choose the output directory.",
  whatWillHappen: "What will happen",
  updateCheck: "Refresh check",
  toolsDirectoryHelp:
    "Choose a permanent directory inside the server or container. It is retained across restarts and validated on Continue.",
  toolsHelp:
    "FFmpeg includes ffmpeg and ffprobe in one package. Choose a version for each package; the first verified version is activated automatically.",
  publicationHelp:
    "The source format keeps the container. MKA remuxes without re-encoding. M4A keeps AAC and ALAC without re-encoding; other lossless audio is encoded as ALAC and other lossy audio as AAC.",
  metadataHelp:
    "MusicBrainz must be checked before setup can be completed. LRCLIB does not need a network check here.",
  summaryHelp:
    "The summary is loaded from the server. Finishing setup checks all requirements again; return to the relevant step to fix any issues.",
  setupReady: "Setup is ready.",
  activateTools: "Activate verified FFmpeg and fpcalc versions.",
  chooseFormat: "Choose a publication format.",
  setupNotReady: "Setup is not ready.",
  unknownError: "An unknown error occurred.",
  httpError: "The server returned HTTP {status}.",
  serverStateUpdated: "Server state refreshed.",
  requestInProgress: "Request in progress…",
  metadataTitle: "Metadata providers",
  musicBrainzUnavailable: "MusicBrainz is unavailable.",
  saveAndCheckMusicBrainz:
    "Save and check MusicBrainz after changing its configuration.",
  musicBrainzIdentityMissing:
    "MusicBrainz configuration identity is unavailable. Save and check MusicBrainz again.",
  genreCatalogRequired:
    "Load the complete MusicBrainz genre catalog before continuing.",
  genreCatalogOutdated:
    "The MusicBrainz genre catalog no longer matches the current configuration. Load it again.",
  saveSettings: "Save settings",
  toolsInventoryLoadError: "Could not load tool status.",
  toolsInventoryUnavailable: "Tool status is unavailable: ",
  loading: "Loading…",
  unknownToolState: "Status unknown — refresh the check",
  toolNotReady: "Not confirmed ready (state: ",
  ready: "Ready · ",
  active: "Active",
  installedReady: "Ready",
  setupReadyTitle: "Ready to use",
  setupNeedsAttention: "Needs attention",
  setupNotReadyIntro: "Setup is not ready yet:",
  serverNotReady: "The server reports that setup is not ready.",
  publication: "Publication",
  serverDirectory: "Server directory",
  notSpecified: "Not specified",
  sourceFormatExplanation: "Keeps the original container and audio stream.",
  mkaExplanation: "Remuxes to MKA without re-encoding audio.",
  m4aExplanation:
    "AAC and ALAC are copied without re-encoding; other lossless codecs are converted to ALAC and other codecs to lossy AAC. Processing depends on the FFmpeg codecs available and the source audio parameters.",
  unselected: "Not selected",
  sourceFilesAfterPublication: "Source files after successful publication",
  remove: "Remove",
  keep: "Keep",
  tools: "Tools",
  ffmpeg: "FFmpeg",
  fpcalc: "fpcalc",
  metadataAndLyrics: "Metadata and lyrics",
  connectionChecked: "Connection checked · ",
  publicService: "public service",
  connectionNotChecked: "Connection not checked",
  genreCatalogLoaded: "Loaded for the current MusicBrainz configuration",
  genreCatalogMissing: "Not loaded",
  lrclibLyrics: "LRCLIB lyrics",
  enabledAvailableForSearch: "Enabled — available for search",
  disabled: "Disabled",
  acoustid: "AcoustID",
  keySet: "Key is set",
  keyNotSetOptional: "Key is not set (optional)",
  technicalDetails: "Technical details",
  platform: "Platform",
  toolsDirectorySummary: "Tools directory",
  noData: "No data",
  lastMusicBrainzCheck: "Last MusicBrainz check",
  lastFfmpegCheck: "Last FFmpeg check",
  lastFpcalcCheck: "Last fpcalc check",
  nextStep:
    "Next: add a music source and start a scan. Import does not start automatically.",
  loadingToolsState: "Could not load tool status.",
  saveKeyError: "Could not save the AcoustID key.",
  toolCatalogRefresh: "Refresh catalog",
  confirmOverwrite: "Overwrite only these files:",
  confirmOverwriteButton: "Confirm overwrite",
  startingToolDownload: "Starting tool download…",
  cancel: "Cancel",
  hidden: "[hidden]",
  multipleReadyTools:
    "Multiple ready versions of a package were found. Initial tool setup has stopped. Do not delete or switch versions; ask an administrator to inspect the setup state.",
  readyVersionExists: "A ready version of this package already exists.",
  chooseVersion: "Choose a version.",
  retryOperations: "Retry loading operations",
  loadingOperations: "Loading operations…",
  setupToolsInventoryError: "Could not load tool inventory.",
  retryOperationStatus: "Retry status read",
  toolDownload: "Tool download",
  operationStatusLoading: " — loading status…",
  operationStatusLoadingRu: " — loading operation status…",
  retryToolDownload: "Retry download",
  retryDownload: "Retry download",
  retryToolList: "Retry tool list refresh",
  refreshingTools: "Refreshing tools…",
  retryingDownload: "Retrying download…",
  refreshOperationStatus: "Loading operation status…",
  refreshTools: "Loading tools…",
  operationFailed: "Could not refresh tool inventory.",
} as const;

type SetupMessageKey = keyof typeof english;
type SetupMessages = Record<SetupMessageKey, string>;

const russian: SetupMessages = {
  directories: "Каталоги",
  toolsDirectory: "Каталог инструментов",
  outputDirectory: "Каталог публикации",
  directoryHelp:
    "Пути относятся к серверу, а не к этому компьютеру. Каталог публикации должен быть пустым.",
  toolsDirectoryRequired: "Выберите каталог инструментов.",
  outputDirectoryRequired: "Выберите каталог публикации.",
  whatWillHappen: "Что будет происходить",
  updateCheck: "Обновить проверку",
  toolsDirectoryHelp:
    "Укажите постоянный каталог внутри сервера или контейнера. Путь сохраняется между перезапусками и проверяется при продолжении.",
  toolsHelp:
    "FFmpeg включает ffmpeg и ffprobe в одном пакете. Выберите версию каждого пакета; первая успешно проверенная версия активируется автоматически.",
  publicationHelp:
    "Исходный формат сохраняет контейнер. MKA выполняет перепаковку без перекодирования. M4A сохраняет AAC и ALAC без перекодирования; другие lossless-кодеки преобразуются в ALAC, остальные — в AAC с потерями.",
  metadataHelp:
    "Проверка MusicBrainz обязательна для завершения настройки. LRCLIB не требует сетевой проверки на этом шаге.",
  summaryHelp:
    "Итог загружается с сервера. Завершение повторно проверит все обязательные условия; ошибку можно исправить, вернувшись к нужному шагу.",
  setupReady: "Настройка не готова.",
  activateTools: "Активируйте проверенные версии FFmpeg и fpcalc.",
  chooseFormat: "Выберите формат публикации.",
  setupNotReady: "Настройка не готова.",
  unknownError: "Неизвестная ошибка.",
  httpError: "Сервер вернул HTTP {status}.",
  serverStateUpdated: "Состояние сервера обновлено.",
  requestInProgress: "Выполняется запрос…",
  metadataTitle: "Провайдеры метаданных",
  musicBrainzUnavailable: "MusicBrainz недоступен.",
  saveAndCheckMusicBrainz:
    "Сохраните и проверьте MusicBrainz после изменения конфигурации.",
  musicBrainzIdentityMissing:
    "Идентификатор конфигурации MusicBrainz недоступен. Сохраните и проверьте MusicBrainz снова.",
  genreCatalogRequired:
    "Загрузите полный каталог жанров MusicBrainz, чтобы продолжить.",
  genreCatalogOutdated:
    "Каталог жанров MusicBrainz больше не соответствует текущей конфигурации. Загрузите его снова.",
  saveSettings: "Сохранить настройки",
  toolsInventoryLoadError: "Не удалось загрузить состояние инструментов.",
  toolsInventoryUnavailable: "Состояние инструментов недоступно: ",
  loading: "Загрузка…",
  unknownToolState: "Состояние неизвестно — обновите проверку",
  toolNotReady: "Не подтверждён как готовый (состояние: ",
  ready: "Готов · ",
  active: "Активна",
  installedReady: "Готова",
  setupReadyTitle: "Готово к работе",
  setupNeedsAttention: "Требует внимания",
  setupNotReadyIntro: "Настройка пока не готова:",
  serverNotReady: "Сервер сообщил, что конфигурация не готова.",
  publication: "Публикация",
  serverDirectory: "Каталог на сервере",
  notSpecified: "Не указан",
  sourceFormatExplanation: "Сохраняется исходный контейнер и аудиопоток.",
  mkaExplanation: "Перепаковка в MKA без перекодирования аудио.",
  m4aExplanation:
    "AAC и ALAC копируются без перекодирования; другие lossless-кодеки преобразуются в ALAC, остальные — в AAC с потерями. Возможность обработки зависит от поддерживаемых FFmpeg кодеков и параметров исходного аудио.",
  unselected: "Не выбран",
  sourceFilesAfterPublication: "Исходники после успешной публикации",
  remove: "Удалять",
  keep: "Сохранять",
  tools: "Инструменты",
  ffmpeg: "FFmpeg",
  fpcalc: "fpcalc",
  metadataAndLyrics: "Метаданные и тексты",
  connectionChecked: "Подключение проверено · ",
  publicService: "публичный сервис",
  connectionNotChecked: "Подключение не проверено",
  genreCatalogLoaded: "Загружен для текущей конфигурации MusicBrainz",
  genreCatalogMissing: "Не загружен",
  lrclibLyrics: "Тексты LRCLIB",
  enabledAvailableForSearch: "Включены — доступны для поиска",
  disabled: "Выключены",
  acoustid: "AcoustID",
  keySet: "Ключ задан",
  keyNotSetOptional: "Ключ не задан (необязательно)",
  technicalDetails: "Технические сведения",
  platform: "Платформа",
  toolsDirectorySummary: "Каталог инструментов",
  noData: "Нет данных",
  lastMusicBrainzCheck: "Последняя проверка MusicBrainz",
  lastFfmpegCheck: "Последняя проверка FFmpeg",
  lastFpcalcCheck: "Последняя проверка fpcalc",
  nextStep:
    "Следующий шаг: добавьте источник музыки и запустите сканирование. Импорт не запускается автоматически.",
  loadingToolsState: "Не удалось загрузить состояние инструментов.",
  saveKeyError: "Не удалось сохранить ключ AcoustID.",
  toolCatalogRefresh: "Обновить каталог",
  confirmOverwrite: "Перезапись только следующих файлов:",
  confirmOverwriteButton: "Подтвердить перезапись",
  startingToolDownload: "Запуск загрузки инструмента…",
  cancel: "Отмена",
  hidden: "[скрыто]",
  multipleReadyTools:
    "Найдено несколько готовых версий одного пакета. Первичная настройка инструментов остановлена. Не удаляйте и не переключайте версии; обратитесь к администратору для проверки состояния настройки.",
  readyVersionExists: "Для этого пакета уже есть готовая версия.",
  chooseVersion: "Выберите версию.",
  retryOperations: "Повторить загрузку операций",
  loadingOperations: "Загрузка операций…",
  setupToolsInventoryError: "Не удалось загрузить список инструментов.",
  retryOperationStatus: "Повторить загрузку состояния",
  toolDownload: "Загрузка инструмента",
  operationStatusLoading: " — загрузка состояния…",
  operationStatusLoadingRu: " — загрузка состояния операции…",
  retryToolDownload: "Повторить загрузку",
  retryDownload: "Повторить загрузку",
  retryToolList: "Повторить загрузку списка инструментов",
  refreshingTools: "Обновление списка инструментов…",
  retryingDownload: "Повторная загрузка…",
  refreshOperationStatus: "Загрузка состояния операции…",
  refreshTools: "Загрузка инструментов…",
  operationFailed: "Не удалось обновить список инструментов.",
};

export const setupMessages: Record<Locale, SetupMessages> = {
  en: english,
  ru: russian,
};

export type SetupFeedback =
  | {
      kind: "message";
      key: SetupMessageKey;
      values?: Record<string, string | number>;
    }
  | { kind: "diagnostic"; text: string; secret?: string };

export class SetupMessageError extends Error {
  constructor(readonly feedback: SetupFeedback) {
    super();
  }
}

export function setupMessage(
  key: SetupMessageKey,
  values?: Record<string, string | number>,
): SetupFeedback {
  return { kind: "message", key, values };
}

export function setupHttpError(response: {
  status: number;
  data?: unknown;
}): Error {
  const data = response.data;
  const detail =
    typeof data === "object" &&
    data !== null &&
    "detail" in data &&
    typeof data.detail === "string"
      ? data.detail
      : "";
  if (detail) return new Error(detail);
  return new SetupMessageError(
    setupMessage("httpError", { status: response.status }),
  );
}

export function setupFeedback(
  reason: unknown,
  fallbackKey: SetupMessageKey,
  secret?: string,
): SetupFeedback {
  if (reason instanceof SetupMessageError) return reason.feedback;
  if (reason instanceof Error)
    return { kind: "diagnostic", text: reason.message, secret };
  if (
    typeof reason === "object" &&
    reason !== null &&
    "kind" in reason &&
    reason.kind === "message" &&
    "key" in reason &&
    typeof reason.key === "string" &&
    reason.key !== "steps" &&
    reason.key in english
  ) {
    return reason as SetupFeedback;
  }
  return setupMessage(fallbackKey);
}

export function setupFeedbackText(
  feedback: SetupFeedback,
  locale: Locale,
): string {
  if (feedback.kind === "message") {
    return setupMessages[locale][feedback.key].replace(
      /\{([^}]+)\}/g,
      (match, name: string) => {
        const value = feedback.values?.[name];
        return value === undefined ? match : String(value);
      },
    );
  }
  return feedback.secret
    ? feedback.text.replaceAll(feedback.secret, setupMessages[locale].hidden)
    : feedback.text;
}

export type { SetupMessageKey };
