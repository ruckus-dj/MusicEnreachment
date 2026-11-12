import type { Locale } from "../../i18n/locale";

const english = {
  generalTitle: "General",
  publicationFormat: "Publication format",
  originalFormat: "Original format",
  mkaRemux: "MKA remux",
  m4aHelp:
    "M4A keeps AAC and ALAC without transcoding; other lossless formats are encoded to ALAC, and other lossy formats to AAC.",
  concurrentFiles: "Concurrent files",
  concurrencyHelp:
    "A positive whole number of files to process at once. The value must be represented exactly in the browser.",
  concurrencyInvalid:
    "Enter a positive whole number that can be represented exactly in the browser.",
  concurrencyDirty: "Concurrency has an unsaved change.",
  saveConcurrency: "Save concurrency",
  calculateSha: "Calculate SHA-256",
  shaHelp:
    "This setting applies to future analyses. Changing it does not delete saved results or start a bulk recalculation.",
  logLevel: "Log level",
  pageTitle: "Settings",
  conflictConcurrency: "analysis concurrency",
  conflictTemplate: "publication path template",
  conflictMusicBrainz: "MusicBrainz settings",
  conflictWorkDirectory: "work directory",
  conflictToolsDirectory: "tools directory",
  conflictOutputDirectory: "publication directory",
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
  catalogNotChecked: "Catalog has not been checked yet.",
  unsavedChanges: "There are unsaved changes.",
  saveFailed: "Could not save {label}: {value}.",
  saveUnconfirmed:
    "The server accepted {label} ({value}), but reading it for confirmation failed.",
  retrySave: "Retry save: {label} ({value})",
  retryConfirm: "Retry confirmation: {label} ({value})",
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
  publicationFormatLabel: "publication format",
  logLevelLabel: "log level",
  enabled: "enabled",
  disabled: "disabled",
  versionLabel: "Version {kind}",
  bytes: "bytes",
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
  publication: "Publication",
  outputDirectory: "Output directory",
  unsavedPublication: "Publication changes are unsaved.",
  publicationTemplate: "Publication path template",
  templateHelp:
    "Tokens in braces are filled from metadata. Add a fallback after | when a value may be missing. The template can contain a file name only, not directories.",
  unsavedTemplate: "The publication path template has unsaved changes.",
  saveTemplate: "Save path template",
  allowSourceDeletion: "Allow source deletion after publication",
  sourceDeletionHelp:
    "Off by default. When enabled, future deletion is permitted only after publication is verified successfully. Changing this setting does not delete existing files or start processing.",
  unsavedSourceDeletion: "The source deletion setting has unsaved changes.",
  changeOutput: "Change output directory",
  sourceProcessing: "Source processing",
  workDirectory: "Work directory for staged copies",
  workDirectoryHelp:
    "This setting is not required for Setup or source-root registration, but is required to process staged sources. It cannot be changed while operations or retained copies exist.",
  unsavedWorkDirectory: "The work directory has unsaved changes.",
  saveWorkDirectory: "Save work directory",
  metadataProviders: "Metadata providers",
  acoustIdKey: "AcoustID application key",
  keySaved: "The key is saved. Its value cannot be read from settings.",
  keyMissing: "No key is set; AcoustID lookup is unavailable.",
  saveAcoustIdKey: "Save AcoustID key",
  deleteAcoustIdKey: "Delete AcoustID key",
  musicBrainzMode: "MusicBrainz mode",
  public: "Public",
  selfHosted: "Self-hosted",
  musicBrainzBaseUrl: "MusicBrainz base URL",
  throttleMusicBrainz: "Throttle requests to self-hosted MusicBrainz",
  requestInterval: "Request interval (seconds)",
  lastCheck: "Last check:",
  notChecked: "not checked",
  unsavedMusicBrainz: "MusicBrainz has unsaved changes.",
  saveAndCheckMusicBrainz: "Save and check MusicBrainz",
  lrclibEnabled: "LRCLIB is enabled",
  unsavedLrclib: "LRCLIB has unsaved changes.",
  toolsDirectory: "Tools directory",
  moveToolsDirectory: "Move tools directory",
  managedTools: "Managed tools",
  lastCatalogCheck: "Last successful catalog check:",
  refreshingCatalog: "Refreshing catalog…",
  refreshCatalog: "Refresh catalog",
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
  currentOperations: "Current operations",
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
  changePublicationTitle: "Change publication directory?",
  changePublicationWarning:
    "Changing the output directory cancels queued operations and clears the application's references to previous files. The previous files themselves are not deleted.",
  confirmOutputChange: "Confirm output directory change",
  confirmInstallation: "Confirm installation of {release}",
  installDoesNotActivate:
    "Installation does not activate the selected version.",
  confirmConflicts: "Confirm listed conflicts",
  moveToolsTitle: "Move tools directory",
  newToolsDirectory: "New tools directory",
  removeOldManagedFiles: "Remove old managed files after a successful move",
  checkMove: "Check move",
  sourceDirectory: "Source directory:",
  destinationDirectory: "Destination directory:",
  removeOldFiles: "Remove old managed files:",
  yes: "Yes",
  no: "No",
  managedFileCount: "Managed files:",
  conflictCount: "Conflicts:",
  confirmMove: "Confirm move",
  targetFiles: "Target files",
  conflictsToConfirm: "Conflicts to confirm",
  noConflicts: "No conflicts.",
  operation: "Operation {id}",
  operationPending:
    "Operation in progress; state is being refreshed from the server.",
  retryOperation: "Retry operation {id}",
  dismissOperation: "Dismiss error {id}",
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
  genres: "Genre catalog",
  genreCatalogDescription: "The MusicBrainz catalog is used to choose genres.",
  genreRefreshRequested: "A genre catalog refresh has been requested.",
  genreRefreshError: "Could not refresh the genre catalog.",
  genreStatusError: "Could not load genre catalog status.",
  operationStatusError: "Could not load operation status.",
  genreRefreshSending: "Sending request…",
  genreRefresh: "Refresh catalog",
  loadingStatus: "Loading status…",
  genreCount: "Genres",
  lastSuccessfulUpdate: "Last successful update",
  lastAttempt: "Last attempt",
  lastError: "Last error:",
  catalogUpdating: "Catalog refresh:",
  queued: "queued",
  running: "running",
} as const;

export type SettingsMessageKey = keyof typeof english;

const russian: Record<SettingsMessageKey, string> = {
  conflictConcurrency: "параллельность анализа",
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
  catalogNotChecked: "Каталог ещё не проверялся.",
  unsavedChanges: "Есть несохранённые изменения.",
  saveFailed: "Не удалось сохранить {label}: {value}.",
  saveUnconfirmed:
    "Значение {label} ({value}) принято сервером, но чтение для подтверждения не удалось.",
  retrySave: "Повторить сохранение: {label} ({value})",
  retryConfirm: "Повторить подтверждение: {label} ({value})",
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
  publicationFormatLabel: "формат публикации",
  logLevelLabel: "уровень журнала",
  enabled: "включено",
  disabled: "выключено",
  versionLabel: "Версия {kind}",
  bytes: "байт",
  generalTitle: "Общие",
  publicationFormat: "Формат публикации",
  originalFormat: "Исходный формат",
  mkaRemux: "Ремакс в MKA",
  m4aHelp:
    "M4A сохраняет AAC и ALAC без перекодирования; другие lossless кодируются в ALAC, другие lossy — в AAC с потерями.",
  concurrentFiles: "Одновременные файлы",
  concurrencyHelp:
    "Положительное целое число файлов, обрабатываемых одновременно. Значение должно точно представляться в браузере.",
  concurrencyInvalid:
    "Введите положительное целое число, точно представимое в браузере.",
  concurrencyDirty: "Есть несохранённое изменение параллельности.",
  saveConcurrency: "Сохранить параллельность",
  calculateSha: "Вычислять SHA-256",
  shaHelp:
    "Настройка применяется к последующим анализам. Её изменение не удаляет сохранённые результаты и не запускает массовый пересчёт.",
  logLevel: "Уровень журнала",
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
  publication: "Публикация",
  outputDirectory: "Каталог публикации",
  unsavedPublication: "Есть несохранённые изменения публикации.",
  publicationTemplate: "Шаблон пути публикации",
  templateHelp:
    "Токены в фигурных скобках подставляются из метаданных; для отсутствующего значения задайте альтернативу через |. Шаблон может содержать только имя файла без каталогов.",
  unsavedTemplate: "Есть несохранённый шаблон пути публикации.",
  saveTemplate: "Сохранить шаблон пути",
  allowSourceDeletion: "Разрешить удаление исходников после публикации",
  sourceDeletionHelp:
    "Выключено по умолчанию. При включении удаление в будущем может применяться только после успешной проверки публикации; изменение настройки не удаляет существующие файлы и не запускает обработку.",
  unsavedSourceDeletion: "Есть несохранённая настройка удаления исходников.",
  changeOutput: "Сменить каталог публикации",
  sourceProcessing: "Обработка источников",
  workDirectory: "Рабочая папка для подготовленных копий",
  workDirectoryHelp:
    "Настройка не требуется для первичной настройки или регистрации корня источников, но обязательна для обработки подготовленных источников. Каталог нельзя изменить, пока существуют операции или сохранённые копии.",
  unsavedWorkDirectory: "Есть несохранённые изменения рабочей папки.",
  saveWorkDirectory: "Сохранить рабочую папку",
  metadataProviders: "Провайдеры метаданных",
  acoustIdKey: "Ключ приложения AcoustID",
  keySaved: "Ключ сохранён. Его значение нельзя прочитать из настроек.",
  keyMissing: "Ключ не задан; поиск AcoustID недоступен.",
  saveAcoustIdKey: "Сохранить ключ AcoustID",
  deleteAcoustIdKey: "Удалить ключ AcoustID",
  musicBrainzMode: "Режим MusicBrainz",
  public: "Публичный",
  selfHosted: "Собственный сервер",
  musicBrainzBaseUrl: "Базовый URL MusicBrainz",
  throttleMusicBrainz:
    "Ограничивать частоту запросов к собственному MusicBrainz",
  requestInterval: "Интервал запросов (секунды)",
  lastCheck: "Последняя проверка:",
  notChecked: "не проверено",
  unsavedMusicBrainz: "Есть несохранённые изменения MusicBrainz.",
  saveAndCheckMusicBrainz: "Сохранить и проверить MusicBrainz",
  lrclibEnabled: "LRCLIB включён",
  unsavedLrclib: "Есть несохранённые изменения LRCLIB.",
  toolsDirectory: "Каталог инструментов",
  moveToolsDirectory: "Перенести каталог",
  managedTools: "Управляемые инструменты",
  lastCatalogCheck: "Последняя успешная проверка каталога:",
  refreshingCatalog: "Обновление каталога…",
  refreshCatalog: "Обновить каталог",
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
  currentOperations: "Текущие операции",
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
  changePublicationTitle: "Сменить каталог публикации?",
  changePublicationWarning:
    "Изменение каталога отменит все операции в очереди и очистит ссылки приложения на прежние файлы. Сами файлы не удаляются.",
  confirmOutputChange: "Подтвердить смену каталога публикации",
  confirmInstallation: "Подтверждение установки {release}",
  installDoesNotActivate: "Установка не активирует выбранную версию.",
  confirmConflicts: "Подтвердить перечисленные конфликты",
  moveToolsTitle: "Перенос каталога инструментов",
  newToolsDirectory: "Новый каталог инструментов",
  removeOldManagedFiles:
    "Удалить прежние управляемые файлы после успешного переноса",
  checkMove: "Проверить перенос",
  sourceDirectory: "Исходный каталог:",
  destinationDirectory: "Каталог назначения:",
  removeOldFiles: "Удалить прежние управляемые файлы:",
  yes: "Да",
  no: "Нет",
  managedFileCount: "Управляемых файлов:",
  conflictCount: "Конфликтов:",
  confirmMove: "Подтвердить перенос",
  targetFiles: "Целевые файлы",
  conflictsToConfirm: "Конфликты для подтверждения",
  noConflicts: "Конфликтов нет.",
  operation: "Операция {id}",
  operationPending: "Операция выполняется; состояние обновляется с сервера.",
  retryOperation: "Повторить операцию {id}",
  dismissOperation: "Закрыть ошибку {id}",
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
  genres: "Жанровый каталог",
  genreCatalogDescription:
    "Каталог MusicBrainz используется для подбора жанров.",
  genreRefreshRequested: "Запрошено обновление жанрового каталога.",
  genreRefreshError: "Не удалось обновить жанровый каталог.",
  genreStatusError: "Не удалось загрузить статус жанрового каталога.",
  operationStatusError: "Не удалось загрузить статус операции.",
  genreRefreshSending: "Запрос отправляется…",
  genreRefresh: "Обновить каталог",
  loadingStatus: "Загрузка статуса…",
  genreCount: "Жанров",
  lastSuccessfulUpdate: "Последнее успешное обновление",
  lastAttempt: "Последняя попытка",
  lastError: "Последняя ошибка:",
  catalogUpdating: "Обновление каталога:",
  queued: "в очереди",
  running: "выполняется",
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
