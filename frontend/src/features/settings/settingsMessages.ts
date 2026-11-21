import type { Locale } from "../../i18n/locale";

const english = {
  conflictConcurrency: "analysis concurrency",
  conflictMatchingThreshold: "matching threshold",
  conflictTemplate: "publication path template",
  conflictMusicBrainz: "MusicBrainz settings",
  conflictWorkDirectory: "work directory",
  conflictToolsDirectory: "tools directory",
  conflictOutputDirectory: "publication directory",
  pageTitle: "Settings",
  requestFailed: "The request could not be completed.",
  httpError: "The server returned HTTP {status}.",
  settingsConflictRefreshFailed:
    "Settings changed on the server. The draft is saved, but the current state could not be refreshed: {detail}",
  settingsConflictDraftKept:
    "Settings changed on the server. The current snapshot was refreshed; the draft is saved for conflict resolution.",
  settingsAcceptedRefreshFailed:
    "The server accepted the settings, but they could not be confirmed by reading them: {detail}",
  publicationFormatSaved: "Publication format saved.",
  concurrencyMustBePositive: "Concurrency must be a positive whole number.",
  concurrencySaved: "Concurrency saved.",
  matchingThresholdMustBeInRange:
    "Matching threshold must be a number from 0 to 1.",
  matchingThresholdSaved: "Matching threshold saved.",
  templateRequired: "Enter a publication path template.",
  templateSaved: "Publication path template saved.",
  workDirectoryChanged: "Work directory changed.",
  shaSaved: "SHA-256 setting saved.",
  sourceDeletionSaved: "Source deletion setting saved.",
  lrclibSaved: "LRCLIB setting saved.",
  logLevelApplied: "Log level applied.",
  serverDidNotConfirmFormat:
    "The server did not confirm publication format {value}; it is currently {confirmed}.",
  settingConfirmed: "Setting confirmed by the server.",
  outputDirectoryAcceptedRefreshFailed:
    "The publication directory was accepted by the server, but could not be confirmed by reading it: {detail}",
  outputDirectoryChanged: "Publication directory changed.",
  acoustIdSaved: "AcoustID application key saved.",
  acoustIdDeleted: "AcoustID application key deleted.",
  musicBrainzSaved: "MusicBrainz mode saved.",
  musicBrainzThrottleSaved: "MusicBrainz request throttling saved.",
  invalidMusicBrainzUrl: "Enter a valid MusicBrainz base URL first.",
  invalidMusicBrainzDelay:
    "Request interval must be a number from 0 to 60 seconds.",
  serverDidNotConfirmMusicBrainz:
    "The server did not confirm the submitted MusicBrainz settings.",
  musicBrainzAcceptedRefreshFailed:
    "MusicBrainz settings were accepted by the server, but could not be confirmed by reading them: {detail}",
  musicBrainzSavedRefreshFailed:
    "MusicBrainz was saved and checked, but its state could not be confirmed by reading it: {detail}",
  musicBrainzUnavailable: "MusicBrainz is unavailable.",
  unsavedMusicBrainzMode:
    "MusicBrainz still has unsaved changes in the current mode.",
  musicBrainzChecked: "MusicBrainz checked.",
  waitForRequest: "Wait for the current request to finish.",
  newChangesWhileSaving: "New unsaved changes appeared while saving.",
  installVersionRequired: "Choose a version to install.",
  activatedVersion: "{kind} {version} activated.",
  cannotRemoveActive:
    "An active version or one used by an operation cannot be removed.",
  removedVersion: "{kind} {version} removed.",
  currentOutputPath: "Current output directory",
  unsavedPublicationChanges: "Publication changes are unsaved.",
  deleteSourcePermission: "Allow source deletion after publication",
  sourceDeletionNote:
    "Off by default. When enabled, future removal is permitted only after publication is verified successfully. Changing this setting does not delete existing files or start processing.",
  sourceWorkDirectoryHelp:
    "This setting is not required for setup or source-root registration, but is required for staged-source processing. It cannot be changed while operations or retained copies exist.",
  acoustIdKeySavedHelp:
    "The key is saved. Its value cannot be read from settings.",
  acoustIdKeyMissingHelp: "No key is set; AcoustID lookup is unavailable.",
  activeNoOperation:
    "An active version or one used by an operation cannot be removed.",
  lastVerifiedCatalog: "Last successful catalog check:",
  operationError: "Operation error:",
  configurationHealth: "Configuration health",
  versionLabel: "Version {kind}",
  pageDescription:
    "After setup, configuration issues are shown here and do not return the application to the setup wizard.",
  requestInProgress: "Request in progress…",
  draftMemoryWarning:
    "The settings draft is stored only in this page's memory and cannot be restored after the page is closed.",
  loadingSettings: "Loading settings and server state…",
  loadSettingsError: "Could not load settings and server state:",
  retryLoading: "Retry loading",
  healthyConfiguration: "Configuration is healthy.",
  unhealthyConfiguration: "Configuration issues were found.",
  platformUnavailable:
    "Platform unavailable: tool operations are disabled. Settings can still be corrected.",
  ffmpegPackage: "FFmpeg package",
  activeVersion: "Active version",
  noActiveVersion: "No active version",
  installedVersions: "Installed versions",
  active: "active",
  ready: "ready",
  preparing: "Preparing",
  failed: "Failed",
  activateVersion: "Activate {version}",
  removeVersion: "Remove {version}",
  availableVersions: "Available versions",
  versionToInstall: "Version to install",
  chooseVersion: "Choose a version",
  installWithoutActivation: "Install without activation",
  loadingCatalog: "Loading catalog…",
  catalogNotLoaded: "Catalog has not been loaded yet.",
  refreshState: "Refresh state",
  settingsConflict: "Settings conflict: {label}",
  changedSavedValue: "The saved value for “{label}” changed:",
  takeSavedValue: "Use saved value: {label}",
  continueEditing: "Continue editing: {label}",
  unsavedSettings: "Unsaved settings",
  navigationPrompt:
    "Settings contain unsaved text changes. What would you like to do before leaving?",
  stay: "Stay",
  leaveKeepDraft: "Leave and keep draft",
  discardDraftLeave: "Discard draft and leave",
  saveLeave: "Save and leave",
  replaceSavedTitle: "Replace saved value?",
  replaceSavedDescription:
    "The current saved value for “{label}” is {value}. Confirm to replace it with this draft. If another conflict occurs, you will need to confirm again.",
  replaceSaved: "Replace saved value",
  cancel: "Cancel",
  moveToolsTitle: "Move tools directory",
  waitForRunningOperations:
    "Wait for running operations to finish before changing the publication directory.",
  errorSavingDraft:
    "Could not save the draft to browser storage. Stay on this page and try again.",
  errorDiscardingDraft:
    "Could not discard the saved draft. Stay on this page and try again.",
  saveLeaveNewChanges:
    "New unsaved changes appeared. Review the settings before leaving.",
  saveLeaveInvalid: "Correct invalid settings before leaving.",
  saveLeaveConfirmation:
    "Complete the open settings confirmation, then try saving again.",
  saveLeaveConflict:
    "Settings changed on the server. Resolve the conflict before leaving.",
  saveLeaveUncertain:
    "The save could not be confirmed. Review the current state and try again.",
  saveLeaveFailed: "Settings could not be saved before leaving.",
} as const;

export type SettingsMessageKey = keyof typeof english;

const russian: Record<SettingsMessageKey, string> = {
  conflictConcurrency: "параллельность анализа",
  conflictMatchingThreshold: "порог сопоставления",
  conflictTemplate: "шаблон пути публикации",
  conflictMusicBrainz: "настройки MusicBrainz",
  conflictWorkDirectory: "рабочая папка",
  conflictToolsDirectory: "каталог инструментов",
  conflictOutputDirectory: "каталог публикации",
  requestFailed: "Не удалось выполнить запрос.",
  httpError: "Сервер вернул HTTP {status}.",
  settingsConflictRefreshFailed:
    "Настройки изменились на сервере. Черновик сохранён, но текущее состояние не удалось обновить: {detail}",
  settingsConflictDraftKept:
    "Настройки изменились на сервере. Текущий снимок обновлён; черновик сохранён для разрешения конфликта.",
  settingsAcceptedRefreshFailed:
    "Настройки приняты сервером, но не удалось подтвердить их чтением: {detail}",
  publicationFormatSaved: "Формат публикации сохранён.",
  concurrencyMustBePositive:
    "Параллельность должна быть положительным целым числом.",
  concurrencySaved: "Параллельность анализа сохранена.",
  matchingThresholdMustBeInRange:
    "Порог сопоставления должен быть числом от 0 до 1.",
  matchingThresholdSaved: "Порог сопоставления сохранён.",
  templateRequired: "Укажите шаблон пути публикации.",
  templateSaved: "Шаблон пути публикации сохранён.",
  workDirectoryChanged: "Рабочая папка изменена.",
  shaSaved: "Настройка SHA-256 сохранена.",
  sourceDeletionSaved: "Настройка удаления исходников сохранена.",
  lrclibSaved: "Настройка LRCLIB сохранена.",
  logLevelApplied: "Уровень журнала применён.",
  serverDidNotConfirmFormat:
    "Сервер не подтвердил формат публикации {value}; сейчас сохранено {confirmed}.",
  settingConfirmed: "Настройка подтверждена сервером.",
  outputDirectoryAcceptedRefreshFailed:
    "Каталог публикации принят сервером, но не удалось подтвердить его чтением: {detail}",
  outputDirectoryChanged: "Каталог публикации изменён.",
  acoustIdSaved: "Ключ приложения AcoustID сохранён.",
  acoustIdDeleted: "Ключ приложения AcoustID удалён.",
  musicBrainzSaved: "Режим MusicBrainz сохранён.",
  musicBrainzThrottleSaved: "Ограничение частоты MusicBrainz сохранено.",
  invalidMusicBrainzUrl: "Сначала задайте корректный базовый URL MusicBrainz.",
  invalidMusicBrainzDelay:
    "Интервал запросов должен быть числом от 0 до 60 секунд.",
  serverDidNotConfirmMusicBrainz:
    "Сервер не подтвердил отправленные настройки MusicBrainz.",
  musicBrainzAcceptedRefreshFailed:
    "Настройки MusicBrainz приняты сервером, но не удалось подтвердить их чтением: {detail}",
  musicBrainzSavedRefreshFailed:
    "MusicBrainz сохранён и проверен, но не удалось подтвердить состояние чтением: {detail}",
  musicBrainzUnavailable: "MusicBrainz недоступен.",
  unsavedMusicBrainzMode:
    "В текущем режиме остались несохранённые изменения MusicBrainz.",
  musicBrainzChecked: "MusicBrainz проверен.",
  waitForRequest: "Дождитесь завершения текущего запроса.",
  newChangesWhileSaving:
    "Во время сохранения появились новые несохранённые изменения.",
  installVersionRequired: "Выберите версию для установки.",
  activatedVersion: "{kind} {version} активирован.",
  cannotRemoveActive: "Активную или занятую операцией версию удалить нельзя.",
  removedVersion: "{kind} {version} удалён.",
  currentOutputPath: "Текущий каталог публикации",
  unsavedPublicationChanges: "Есть несохранённые изменения публикации.",
  deleteSourcePermission: "Разрешить удаление исходников после публикации",
  sourceDeletionNote:
    "Выключено по умолчанию. При включении удаление в будущем может применяться только после успешной проверки публикации; изменение настройки не удаляет существующие файлы и не запускает обработку.",
  sourceWorkDirectoryHelp:
    "Настройка не требуется для Setup или регистрации source root, но обязательна для обработки staged-источников. Каталог нельзя изменить, пока существуют операции или сохранённые копии.",
  acoustIdKeySavedHelp:
    "Ключ сохранён. Его значение нельзя прочитать из настроек.",
  acoustIdKeyMissingHelp: "Ключ не задан; поиск AcoustID недоступен.",
  activeNoOperation: "Активную или занятую операцией версию удалить нельзя.",
  lastVerifiedCatalog: "Последняя успешная проверка каталога:",
  operationError: "Ошибка операции:",
  configurationHealth: "Состояние конфигурации",
  versionLabel: "Версия {kind}",
  pageTitle: "Настройки",
  pageDescription:
    "После завершения первичной настройки проблемы конфигурации отображаются здесь и не возвращают приложение в мастер настройки.",
  requestInProgress: "Выполняется запрос…",
  draftMemoryWarning:
    "Черновик настроек сохранён только в памяти этой страницы; после её закрытия восстановить изменения не удастся.",
  loadingSettings: "Загрузка настроек и состояния сервера…",
  loadSettingsError: "Не удалось загрузить настройки и состояние сервера:",
  retryLoading: "Повторить загрузку",
  healthyConfiguration: "Конфигурация исправна.",
  unhealthyConfiguration: "Есть проблемы конфигурации.",
  platformUnavailable:
    "Платформа недоступна: операции с инструментами отключены. Настройки можно исправлять.",
  ffmpegPackage: "Пакет FFmpeg",
  activeVersion: "Активная версия",
  noActiveVersion: "Нет активной версии",
  installedVersions: "Установленные версии",
  active: "активна",
  ready: "готова",
  preparing: "Подготовка",
  failed: "Ошибка",
  activateVersion: "Активировать {version}",
  removeVersion: "Удалить {version}",
  availableVersions: "Доступные версии",
  versionToInstall: "Версия для установки",
  chooseVersion: "Выберите версию",
  installWithoutActivation: "Установить без активации",
  loadingCatalog: "Загрузка каталога…",
  catalogNotLoaded: "Каталог пока не загружен.",
  refreshState: "Обновить состояние",
  settingsConflict: "Конфликт настроек: {label}",
  changedSavedValue: "Сохранённое значение для «{label}» изменилось:",
  takeSavedValue: "Взять сохранённое: {label}",
  continueEditing: "Продолжить редактирование: {label}",
  unsavedSettings: "Несохранённые настройки",
  navigationPrompt:
    "В настройках есть несохранённые текстовые изменения. Что сделать перед переходом?",
  stay: "Остаться",
  leaveKeepDraft: "Уйти, сохранив черновик",
  discardDraftLeave: "Удалить черновик и уйти",
  saveLeave: "Сохранить и уйти",
  replaceSavedTitle: "Заменить сохранённое значение?",
  replaceSavedDescription:
    "Для «{label}» сейчас сохранено {value}. Подтвердите замену этим черновиком. При новом конфликте потребуется подтвердить ещё раз.",
  replaceSaved: "Заменить сохранённое значение",
  cancel: "Отмена",
  moveToolsTitle: "Перенос каталога инструментов",
  waitForRunningOperations:
    "Дождитесь завершения выполняющихся операций, прежде чем менять каталог публикации.",
  errorSavingDraft:
    "Не удалось сохранить черновик в хранилище браузера. Останьтесь на странице и повторите попытку.",
  errorDiscardingDraft:
    "Не удалось удалить сохранённый черновик. Останьтесь на странице и повторите попытку.",
  saveLeaveNewChanges:
    "Появились новые несохранённые изменения. Проверьте настройки перед выходом.",
  saveLeaveInvalid: "Исправьте некорректные настройки перед выходом.",
  saveLeaveConfirmation:
    "Завершите открытое подтверждение настроек, затем повторите сохранение.",
  saveLeaveConflict:
    "Настройки изменились на сервере. Разрешите конфликт перед выходом.",
  saveLeaveUncertain:
    "Не удалось подтвердить сохранение. Проверьте состояние и повторите попытку.",
  saveLeaveFailed: "Не удалось сохранить настройки перед выходом.",
};

export type SettingsMessageValue =
  | string
  | number
  | { key: SettingsMessageKey; values?: SettingsMessageValues };
export type SettingsMessageValues = Partial<
  Record<string, SettingsMessageValue>
>;

export class SettingsMessageError extends Error {
  constructor(
    readonly descriptor: {
      key: SettingsMessageKey;
      values?: SettingsMessageValues;
    },
  ) {
    super();
  }
}

export function settingsHttpError(response: {
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
  return new SettingsMessageError({
    key: "httpError",
    values: { status: response.status },
  });
}

export function settingsMessage(
  locale: Locale,
  key: SettingsMessageKey,
  values: SettingsMessageValues = {},
): string {
  const template = (locale === "ru" ? russian : english)[key];
  return template.replace(/\{([^}]+)\}/g, (match, name: string) => {
    const value = values[name];
    if (value === undefined) return match;
    if (typeof value === "object")
      return settingsMessage(locale, value.key, value.values);
    return String(value);
  });
}
