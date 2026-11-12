import type { Locale } from "../../i18n/locale";

const english = {
  steps: [
    "Platform",
    "Directories",
    "Tools",
    "Publication",
    "Metadata providers",
    "Summary",
  ],
  firstRun: "First run",
  server: "Server",
  setupSteps: "Setup steps",
  aboutCurrentStep: "About this step",
  platformSupported: "This platform is supported.",
  directories: "Directories",
  toolsDirectory: "Tools directory",
  outputDirectory: "Output directory",
  directoryHelp:
    "These paths belong to the server, not this computer. The output directory must be empty.",
  directoryRequired: "Enter both server directories.",
  format: "Publication format",
  whatWillHappen: "What will happen",
  sourceFormat: "Source format",
  mkaRemux: "MKA remux",
  removeSources: "Remove source files after successful publication",
  preserveSourcesDescription:
    "When disabled, source files are kept. They are removed only after successful publication, never after a failure.",
  updateCheck: "Refresh check",
  finishSetup: "Finish setup",
  continue: "Continue",
  back: "Back",
  importantToKnow: "Important to know",
  platformHelp:
    "The server determines its platform; it does not change with the browser. Check the OS and architecture before setup.",
  directoryHelpDetails:
    "Choose permanent directories inside the server or container. The publication directory must be empty; the tools directory is retained across restarts.",
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
  musicBrainz: "MusicBrainz",
  public: "Public",
  selfHosted: "Self-hosted",
  musicBrainzBaseUrl: "MusicBrainz base URL",
  lrclibEnabled: "LRCLIB enabled",
  checkMusicBrainz: "Check MusicBrainz",
  musicBrainzUnavailable: "MusicBrainz is unavailable.",
  musicBrainzVerified: "MusicBrainz has been checked.",
  musicBrainzChanged: "Configuration changed. Check MusicBrainz again.",
  musicBrainzCheckedAt: "Checked: ",
  musicBrainzNotChecked: "Connection has not been checked yet.",
  saveAndCheckMusicBrainz:
    "Save and check MusicBrainz after changing its configuration.",
  acoustidOptional: "AcoustID (optional)",
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
  acoustidDescription:
    "An application key enables audio-fingerprint search. The fingerprint, not the audio file, is sent. Use an application key, not a user API key (that is used to submit fingerprints).",
  registerAcoustid: "Register an AcoustID application",
  apiDocumentation: "API documentation",
  commercialTerms: "Commercial use terms",
  acoustidFreeUse: "Free AcoustID use is intended for non-commercial projects.",
  acoustidApplicationKey: "AcoustID application key",
  keySavedHidden: "Key saved (value hidden).",
  keyNotSet: "Key is not set.",
  saveKey: "Save key",
  deleteKey: "Delete key",
  keySaved: "AcoustID application key saved.",
  keyDeleted: "AcoustID application key deleted.",
  saveKeyError: "Could not save the AcoustID key.",
  toolCatalogRefresh: "Refresh catalog",
  selectVersion: "Select a version",
  toolVersion: "Version ",
  checkToolInstall: "Check ",
  install: " installation",
  confirmOverwrite: "Overwrite only these files:",
  confirmOverwriteButton: "Confirm overwrite",
  cancel: "Cancel",
  conflictConfirmation: "Confirm file conflicts",
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

type SetupMessageKey = Exclude<keyof typeof english, "steps">;
type SetupMessages = Record<SetupMessageKey, string> & {
  steps: readonly string[];
};

const russian: SetupMessages = {
  steps: [
    "Платформа",
    "Каталоги",
    "Инструменты",
    "Публикация",
    "Провайдеры метаданных",
    "Итог",
  ],
  firstRun: "Первый запуск",
  server: "Сервер",
  setupSteps: "Шаги настройки",
  aboutCurrentStep: "О текущем шаге",
  platformSupported: "Платформа поддерживается.",
  directories: "Каталоги",
  toolsDirectory: "Каталог инструментов",
  outputDirectory: "Каталог публикации",
  directoryHelp:
    "Пути относятся к серверу, а не к этому компьютеру. Каталог публикации должен быть пустым.",
  directoryRequired: "Укажите оба каталога сервера.",
  format: "Формат публикации",
  whatWillHappen: "Что будет происходить",
  sourceFormat: "Исходный формат",
  mkaRemux: "Перепаковка в MKA",
  removeSources: "Удалять исходники после успешной публикации",
  preserveSourcesDescription:
    "Если флажок снят, исходники останутся. Они удаляются только после успешной публикации, не при ошибке.",
  updateCheck: "Обновить проверку",
  finishSetup: "Завершить настройку",
  continue: "Продолжить",
  back: "Назад",
  importantToKnow: "Важно знать",
  platformHelp:
    "Платформа определяется сервером и не меняется в браузере. Проверьте ОС и архитектуру перед настройкой.",
  directoryHelpDetails:
    "Укажите постоянные каталоги внутри сервера или контейнера. Каталог публикации должен быть пустым; путь к инструментам сохраняется между перезапусками.",
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
  musicBrainz: "MusicBrainz",
  public: "Публичный сервис",
  selfHosted: "Свой сервер",
  musicBrainzBaseUrl: "Базовый URL MusicBrainz",
  lrclibEnabled: "LRCLIB включён",
  checkMusicBrainz: "Проверить MusicBrainz",
  musicBrainzUnavailable: "MusicBrainz недоступен.",
  musicBrainzVerified: "MusicBrainz проверен.",
  musicBrainzChanged: "Конфигурация изменена. Проверьте MusicBrainz снова.",
  musicBrainzCheckedAt: "Проверено: ",
  musicBrainzNotChecked: "Соединение ещё не проверено.",
  saveAndCheckMusicBrainz:
    "Сохраните и проверьте MusicBrainz после изменения конфигурации.",
  acoustidOptional: "AcoustID (необязательно)",
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
  acoustidDescription:
    "Ключ приложения включает поиск по аудиоотпечатку: отправляется отпечаток, а не аудиофайл. Нужен ключ приложения, а не пользовательский API key (он используется для отправки отпечатков).",
  registerAcoustid: "Зарегистрировать приложение AcoustID",
  apiDocumentation: "Документация API",
  commercialTerms: "Условия коммерческого использования",
  acoustidFreeUse:
    "Бесплатное использование AcoustID предназначено для некоммерческих проектов.",
  acoustidApplicationKey: "Ключ приложения AcoustID",
  keySavedHidden: "Ключ сохранён (значение скрыто).",
  keyNotSet: "Ключ не задан.",
  saveKey: "Сохранить ключ",
  deleteKey: "Удалить ключ",
  keySaved: "Ключ приложения AcoustID сохранён.",
  keyDeleted: "Ключ приложения AcoustID удалён.",
  saveKeyError: "Не удалось сохранить ключ AcoustID.",
  toolCatalogRefresh: "Обновить каталог",
  selectVersion: "Выберите версию",
  toolVersion: "Версия ",
  checkToolInstall: "Проверить установку ",
  install: "",
  confirmOverwrite: "Перезапись только следующих файлов:",
  confirmOverwriteButton: "Подтвердить перезапись",
  cancel: "Отмена",
  conflictConfirmation: "Подтверждение конфликтов",
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
