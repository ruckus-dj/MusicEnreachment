import type { Locale } from "../../i18n/locale";

const en = {
  title: "Sources",
  description:
    "Server directories MeloTrove reads from. Paths are on the server; directories are read-only and files are never uploaded through the browser.",
  loading: "Loading…",
  requestRunning: "Request in progress…",
  retryLoad: "Retry loading",
  sourcesLoadError: "Could not load sources.",
  createTitle: "New source",
  name: "Source name",
  serverPath: "Server path",
  processingMode: "Processing mode",
  chooseMode: "Choose a mode",
  inPlace: "In place",
  staged: "Staged copy",
  sourcePolicy:
    "The path belongs to the server, not this computer. MeloTrove does not create or write to the source directory; the browser does not upload files.",
  invalidCreate:
    "Enter a source name, an absolute server path, and a processing mode.",
  invalidEdit: "Enter a source name and an absolute server path.",
  register: "Register source",
  cancel: "Cancel",
  addSource: "Add source",
  connectedSources: "Connected sources",
  noneRegistered: "No sources are registered.",
  sourceCaption:
    "Registered server sources: name, path, availability, enabled state, last scan, and inventory",
  nameColumn: "Name",
  pathColumn: "Path",
  availability: "Availability",
  enabled: "Enabled",
  lastScan: "Last scan",
  inventory: "Inventory",
  actions: "Actions",
  enabledYes: "Enabled",
  enabledNo: "Disabled",
  openSource: "Open {name}",
  unavailableInventory:
    "An unavailable source or changed path does not remove the previous inventory; it remains from the last successful scan.",
  manualScanSuccess:
    "Source “{name}” was registered, but the scan was not accepted by the server. The source is saved. Open it and start a scan manually.",
  manualScanFailure:
    "Source “{name}” was registered, but the scan could not be started. The source is saved. Open it and start a scan manually.",
  openManualScan: "Open source to scan manually",
  backToSources: "Back to sources",
  sourceTitle: "Source",
  sourceSummary: "Source",
  serverDirectory: "Server directory",
  availabilityLabel: "Availability",
  yesNewScans: "Yes: new scans are allowed.",
  noNewScans: "No: new scans are not allowed.",
  lastSuccessfulScan: "Last successful scan",
  inventoryPath: "Inventory path",
  previousPath:
    "This is the previous path. The inventory will be replaced after a successful scan of the current path.",
  sourceReadOnly:
    "The directory belongs to the server: MeloTrove reads it but does not write to it.",
  editTitle: "Name, path, and mode",
  sourceName: "Source name",
  modeChange:
    "Mode changes apply only to future processing and do not rerun analysis of the current inventory.",
  pathChange:
    "The path belongs to the server, not this computer. Changing it does not delete the inventory: previous records remain visible as belonging to the previous path until a scan of the new path succeeds.",
  saveSource: "Save source",
  sourceSaved: "Source saved.",
  sourceSavedStale:
    "Source saved. The inventory still belongs to the previous path {path} and will be replaced after a successful scan of the new path.",
  editSource: "Edit source",
  deleteTitle: "Delete source",
  deleteExplanation:
    "Deleting also removes its inventory, application-owned analysis copies, and completed failed jobs that cannot be retried. Source files, library objects, current publications, and files in output are not affected. The server rejects deletion while jobs are queued or running.",
  deleteSource: "Delete source",
  deleteConfirmTitle: "Delete the inventory for “{name}”?",
  deleteConfirmBody:
    "This deletes {count} inventory records at the server path {path}, as well as saved analysis copies and completed failed jobs that cannot be retried.",
  sourceFilesRemain:
    "Source files remain in place: MeloTrove deletes only its own records and copies. Library objects, current publications, and files in output are not affected.",
  confirmDelete: "Confirm deletion",
  loadingLocations: "Loading source…",
  inspectorPath: "File path",
  inspector: "File inspector",
  breadcrumb: "File path",
  file: "File",
  originalFile: "Source file",
  relativePath: "Relative path",
  size: "Size",
  modified: "Modified",
  analysisImpact: "Analysis impact",
  analysisDoesNotChange: "Analysis does not change the source file",
  sourceAvailability: "Source availability",
  disabledAnalysis: "Source is disabled: analysis is unavailable.",
  staleInventory: "Inventory is stale. Scan the current source path first.",
  unavailableResult:
    "Source is unavailable. The saved result is still available.",
  technicalAnalysis: "Technical analysis",
  runningRequest: "Request in progress…",
  loadingInspection: "Loading file and checking active analysis…",
  queuedStep: "Analysis step queued.",
  savingStep: "Saving analysis step result.",
  runningStep: "Analysis step is running.",
  streamInterrupted:
    "The event stream was interrupted. The connection will recover automatically; the latest server response is shown.",
  stagedArtifact: "Staged analysis artifact",
  artifactState: "State",
  artifactReadyNote:
    "The artifact is registered as ready; this does not confirm that a file exists on disk.",
  artifactUnknownNote: "Staged artifact information is unknown.",
  requestedSteps: "Requested steps",
  noSteps: "None",
  emptyStepsNote:
    "An empty list of requested steps does not mean that steps are complete.",
  requestedHistoryUnknown:
    "Requested-step history is unknown; an empty list does not mean steps were completed or were not requested.",
  artifactReused:
    "The staged artifact was created by another operation and is used by this operation.",
  artifactCleanupFailed: "Could not clean up the staged artifact:",
  analysisSteps: "File analysis steps",
  stepError: "Step error “{step}”:",
  state: "State",
  fingerprint: "Acoustic fingerprint",
  sha256: "SHA-256",
  probeStep: "Technical analysis (ffprobe)",
  metadata: "Tag metadata",
  notRequested: "Not requested",
  loadingState: "Loading",
  queued: "Queued",
  running: "Running",
  failed: "Failed",
  succeeded: "Completed",
  skipped: "Skipped",
  pending: "Pending",
  unknown: "Unknown",
  fingerprintValue: "Fingerprint:",
  resultVersion: "Result fpcalc version:",
  activeVersion: "Active fpcalc version:",
  activeVersionUnavailable: "Active fpcalc version is unavailable.",
  metadataSource: "Source:",
  noSavedTags: "No saved tags.",
  retryStep: "Retry step “{step}”",
  rerunFingerprint: "Recalculate fingerprint",
  noSuccessfulStep: "No successful step result is available.",
  matching: "Matching",
  available: "Available",
  unavailable: "Unavailable",
  noAudioSupport: "Not supported for a file without audio streams",
  multipleAudioSupport: "Not supported for a file with multiple audio streams",
  savedTechnicalResult: "Saved technical result",
  analysisAt: "Analysis on {date}",
  policy: "Policy",
  container: "Container",
  containerDescription: "Description",
  duration: "Duration",
  containerBitrate: "Container bit rate",
  audioStreams: "Audio streams",
  audioStream: "Audio stream {index}",
  stream: "Stream {index}",
  codecProfile: "Codec / profile",
  bitrate: "Bit rate",
  sampleRate: "Sample rate",
  sampleFormat: "Sample format",
  bitDepth: "Bit depth",
  channelsLayout: "Channels / layout",
  noAudioStreams: "No audio streams are listed.",
  sourceTags: "Source tags",
  noSourceTags: "No source tags are present.",
  rawFfprobe: "Raw ffprobe JSON",
  inventoryFiles: "Inventory files",
  noInventoryYet:
    "There is no inventory: the source has not been scanned successfully. Files will appear after a successful scan.",
  inventoryHelp:
    "Showing records from the last successful scan: files with supported audio extensions. Paths are relative to",
  loadingFiles: "Loading file list…",
  noSupportedFiles:
    "The last successful scan found no files with supported audio extensions.",
  filesCaption:
    "Files from the last successful scan: relative path, size, modification time, and audio stream check",
  audioCheck: "Audio check",
  noAudio: "No audio track",
  probeError: "Check failed",
  noAudioReason: "No audio track: this file cannot be analyzed.",
  probeRetryNote: "The check will be retried during the next scan.",
  shownCount: "Showing {count}.",
  showMore: "Show more",
  loadingNextPage: "Loading next page…",
  retryScan: "Retry scan",
  scan: "Scan",
  scanningTitle: "Scanning",
  manualScanOnly:
    "Scanning starts only when requested. The number of files is unknown in advance, so server stages are shown without percentages or byte counters.",
  disabledScan:
    "Source is disabled: new scans are not allowed until it is enabled.",
  checkingScan: "Checking for an active scan of this source…",
  queuedScan: "Scan queued.",
  traversing: "Scanning directory: listing files and reading their metadata.",
  applying: "Applying results: confirmed inventory is being saved.",
  scanningStage: "Scan in progress (server stage: {stage}).",
  scanDone:
    "Scan completed: the inventory was updated by the last successful traversal. This does not mean audio-file analysis is complete.",
  scanStreamError:
    "Event stream updates were interrupted. The latest server state is shown; the connection will recover automatically.",
  discoverScanError:
    "Could not check for an active source scan (status {status}). The server will reject a duplicate start if a scan is already running.",
  scanAlreadyRunning:
    "A scan is already running. The server rejected a duplicate start. Wait for it to finish, then retry.",
  sourceMissing:
    "Source not found; it may have been deleted in another window.",
  scanRejected:
    "The server rejected the scan: the source may be disabled or unavailable, or setup may be incomplete.",
  serverError: "The server returned an error ({status}).",
  scanFailed: "The scan did not complete. Try again.",
  cleanupTitle: "Temporary analysis copies",
  cleanupDescription:
    "This is a server-limited selection of copies explicitly allowed for cleanup. Source files are not affected.",
  loadingCopies: "Loading available copies…",
  refreshCopies: "Refresh selection",
  currentBatch:
    "Current selection: {count}. This is the number of records in the returned batch, not the total number of copies or a deletion limit. Maximum batch size: 1000.",
  refreshingCopies: "Refreshing selection…",
  noCopies: "No copies are available for cleanup.",
  cleanupCandidates: "Copies available for cleanup",
  cleanupSelected: "Clean up selected ({count})",
  outsideBatch: "Selected copies outside the current selection",
  outsideBatchHelp:
    "These selected IDs are no longer in the current selection; they have not been replaced by other copies:",
  deselect: "Deselect",
  cleanupOperation: "Copy cleanup",
  operationRunning: "Operation {state}: {stage}.",
  operationComplete: "Operation completed: {state}.",
  cleanupStreamInterrupted:
    "Updates were interrupted; the client is still waiting for the latest operation snapshot.",
  cleanupResults: "Cleanup results",
  unknownRemoval:
    "The server returned no per-copy results; deletion state is unknown.",
  confirmCleanup: "Clean up selected copies?",
  confirmationIds:
    "Only these {count} IDs from the displayed list snapshot will be submitted:",
  sourceFilesNotDeleted: "Source files are not deleted.",
  confirmCleanupAction: "Confirm cleanup",
  cleanupEligible: "Eligible for cleanup",
  cleanupFailed: "Previous cleanup failed",
  deleted: "Deleted",
  missing: "File is already missing",
  deletionFailed: "Could not delete",
  claimed: "In progress",
  listUnavailable: "Could not load the sources.",
  readUnavailable: "Could not read the source.",
  createUnavailable: "Could not register the source.",
  editUnavailable: "Could not update the source.",
  deleteUnavailable: "Could not delete the source.",
  sourceConflict:
    "The source is used by an active scan. Path changes, disabling, and deletion were rejected; retry after the scan completes.",
  deleteConfirmationStale:
    "Deletion confirmation no longer matches the current source. Refresh the page and retry.",
  sourcePathRejected:
    "The server rejected this source: the path must be an existing readable server directory, must not overlap MeloTrove directories, and must not duplicate a registered path.",
  requestFailed: "The request failed.",
  sourceAvailable: "Available",
  sourceUnavailable: "Unavailable",
  sourceUnchecked: "Not checked",
  neverScanned: "Never scanned",
  scannedInventoryUnavailable:
    "Source unavailable: showing inventory from the last successful scan.",
  oldInventoryPath: "Inventory belongs to the previous path {path}.",
  emptyInventory:
    "The last successful scan found no files with supported audio extensions.",
  fileCountOne: "{count} file",
  fileCountOther: "{count} files",
  fileCountFew: "{count} files",
  fileCountMany: "{count} files",
  inventorySummary: "The last successful scan inventory contains {count}.",
  streamAudio: "Audio",
  details: "Details",
  artifactPreparation: "Preparing",
  artifactAcquiring: "Acquiring artifact",
  artifactReady: "Ready",
  artifactRetained: "Retained",
  inspectorNotFound:
    "File or source not found; the record may have been deleted.",
  inspectorConflict:
    "The request was rejected because the file or source changed, or another operation is running.",
  inspectorConflictHelp: "Refresh file details and retry the available step.",
  bytesUnit: "B",
  bitsPerSecond: "bps",
  bitsUnit: "bits",
  hertzUnit: "Hz",
} as const;

const ru: Record<keyof typeof en, string> = {
  title: "Источники",
  description:
    "Серверные каталоги, из которых MeloTrove читает файлы. Путь указывается на сервере; каталог читается только для чтения, файлы через браузер не загружаются.",
  loading: "Загрузка…",
  requestRunning: "Выполняется запрос…",
  retryLoad: "Повторить загрузку",
  sourcesLoadError: "Не удалось загрузить источники.",
  createTitle: "Новый каталог",
  name: "Имя каталога",
  serverPath: "Путь на сервере",
  processingMode: "Режим обработки",
  chooseMode: "Выберите режим",
  inPlace: "На месте",
  staged: "Через рабочую копию",
  sourcePolicy:
    "Путь принадлежит серверу, а не этому компьютеру. MeloTrove ничего не создаёт и не записывает в каталог источника; браузер не передаёт файлы.",
  invalidCreate:
    "Укажите имя каталога, абсолютный путь на сервере и режим обработки.",
  invalidEdit: "Укажите имя каталога и абсолютный путь на сервере.",
  register: "Зарегистрировать каталог",
  cancel: "Отмена",
  addSource: "Добавить каталог",
  connectedSources: "Подключённые каталоги",
  noneRegistered: "Ни один каталог не зарегистрирован.",
  sourceCaption:
    "Зарегистрированные серверные каталоги: имя, путь, доступность, включение, последнее сканирование и инвентарь",
  nameColumn: "Имя",
  pathColumn: "Путь",
  availability: "Доступность",
  enabled: "Включён",
  lastScan: "Последнее сканирование",
  inventory: "Инвентарь",
  actions: "Действия",
  enabledYes: "Включён",
  enabledNo: "Выключен",
  openSource: "Открыть {name}",
  unavailableInventory:
    "Недоступность каталога или смена пути не удаляют прежний инвентарь: он остаётся от последнего успешного сканирования.",
  manualScanSuccess:
    "Каталог «{name}» зарегистрирован, но сервер не принял запуск сканирования. Каталог сохранён. Откройте его и запустите сканирование вручную.",
  manualScanFailure:
    "Каталог «{name}» зарегистрирован, но запустить сканирование не удалось. Каталог сохранён. Откройте его и запустите сканирование вручную.",
  openManualScan: "Открыть каталог для ручного сканирования",
  backToSources: "К списку каталогов",
  sourceTitle: "Каталог источника",
  sourceSummary: "Каталог",
  serverDirectory: "Путь на сервере",
  availabilityLabel: "Доступность",
  yesNewScans: "Да: новые сканирования разрешены.",
  noNewScans: "Нет: новые сканирования запрещены.",
  lastSuccessfulScan: "Последнее успешное сканирование",
  inventoryPath: "Путь инвентаря",
  previousPath:
    "Это прежний путь: инвентарь заменится после успешного сканирования текущего пути.",
  sourceReadOnly:
    "Каталог принадлежит серверу: MeloTrove читает его и ничего в него не записывает.",
  editTitle: "Имя, путь и режим",
  sourceName: "Имя каталога",
  modeChange:
    "Смена режима влияет только на будущую обработку и не запускает повторный анализ текущего инвентаря.",
  pathChange:
    "Путь принадлежит серверу, а не этому компьютеру. Смена пути не удаляет инвентарь: прежние записи остаются видимыми как относящиеся к прежнему пути, пока новое сканирование не завершится успешно.",
  saveSource: "Сохранить каталог",
  editSource: "Изменить каталог",
  deleteTitle: "Удаление каталога",
  deleteExplanation:
    "Вместе с инвентарём удаляются сохранённые копии анализа, принадлежащие приложению, и завершённые неудачные задания этого каталога без возможности повтора. Исходные файлы, объекты медиатеки, текущие публикации и файлы в output не затрагиваются. Пока задания ждут запуска или выполняются, сервер отклоняет удаление.",
  deleteSource: "Удалить каталог",
  deleteConfirmTitle: "Удалить инвентарь каталога «{name}»?",
  deleteConfirmBody:
    "Будут удалены записи инвентаря: {count} по серверному пути {path}, а также сохранённые копии анализа и завершённые неудачные задания без возможности повтора.",
  sourceFilesRemain:
    "Исходные файлы каталога остаются на месте: MeloTrove удаляет только собственные записи и копии. Объекты медиатеки, текущие публикации и файлы в output не затрагиваются.",
  confirmDelete: "Подтвердить удаление",
  loadingLocations: "Загрузка каталога…",
  inspectorPath: "Путь к файлу",
  inspector: "Инспектор файла",
  breadcrumb: "Путь к файлу",
  file: "Файл",
  originalFile: "Исходный файл",
  relativePath: "Относительный путь",
  size: "Размер",
  modified: "Изменён",
  analysisImpact: "Воздействие анализа",
  analysisDoesNotChange: "Анализ не изменяет исходный файл",
  sourceAvailability: "Доступность каталога",
  disabledAnalysis: "Каталог выключен: анализ недоступен.",
  staleInventory:
    "Инвентарь устарел: сначала сканируйте текущий путь каталога.",
  unavailableResult:
    "Каталог недоступен. Сохранённый результат остаётся доступен.",
  technicalAnalysis: "Технический анализ",
  runningRequest: "Выполняется запрос…",
  loadingInspection: "Загрузка файла и проверка активного анализа…",
  queuedStep: "Этап анализа поставлен в очередь.",
  savingStep: "Сохранение результата этапа анализа.",
  runningStep: "Выполняется этап анализа файла.",
  streamInterrupted:
    "Поток событий прерван. Соединение восстановится автоматически; показан последний ответ сервера.",
  stagedArtifact: "Промежуточный артефакт анализа",
  artifactState: "Состояние",
  artifactReadyNote:
    "Артефакт зарегистрирован как готовый; это не подтверждает наличие файла на диске.",
  artifactUnknownNote: "Сведения о промежуточном артефакте неизвестны.",
  requestedSteps: "Запрошенные этапы",
  noSteps: "Нет",
  emptyStepsNote:
    "Пустой список запрошенных этапов не означает, что этапы завершены.",
  requestedHistoryUnknown:
    "История запрошенных этапов неизвестна; пустой список не означает, что этапы завершены или не запрашивались.",
  artifactReused:
    "Промежуточный артефакт создан другой операцией и используется текущей операцией.",
  artifactCleanupFailed: "Не удалось очистить промежуточный артефакт:",
  analysisSteps: "Этапы анализа файла",
  stepError: "Ошибка этапа «{step}»:",
  state: "Состояние",
  fingerprint: "Акустический отпечаток",
  sha256: "SHA-256",
  probeStep: "Технический анализ (ffprobe)",
  metadata: "Метаданные тегов",
  notRequested: "Не запрошено",
  loadingState: "Загрузка",
  queued: "В очереди",
  running: "Выполняется",
  failed: "Ошибка",
  succeeded: "Завершено",
  skipped: "Пропущено",
  pending: "Ожидает выполнения",
  unknown: "Неизвестно",
  fingerprintValue: "Отпечаток:",
  resultVersion: "Версия fpcalc результата:",
  activeVersion: "Активная версия fpcalc:",
  activeVersionUnavailable: "Активная версия fpcalc недоступна.",
  metadataSource: "Источник:",
  noSavedTags: "Сохранённых тегов нет.",
  retryStep: "Повторить этап «{step}»",
  rerunFingerprint: "Повторно вычислить отпечаток",
  noSuccessfulStep: "Успешный результат этапа отсутствует.",
  matching: "Сопоставление",
  available: "Доступно",
  unavailable: "Недоступно",
  noAudioSupport: "Не поддерживается для файла без аудиопотоков",
  multipleAudioSupport:
    "Не поддерживается для файла с несколькими аудиопотоками",
  savedTechnicalResult: "Сохранённый технический результат",
  analysisAt: "Анализ от {date}",
  policy: "Политика",
  container: "Контейнер",
  containerDescription: "Описание",
  duration: "Длительность",
  containerBitrate: "Битрейт контейнера",
  audioStreams: "Аудиопотоки",
  audioStream: "Аудиопоток {index}",
  stream: "Поток {index}",
  codecProfile: "Кодек / профиль",
  bitrate: "Битрейт",
  sampleRate: "Частота дискретизации",
  sampleFormat: "Формат сэмплов",
  bitDepth: "Разрядность",
  channelsLayout: "Каналы / схема",
  noAudioStreams: "Аудиопотоки не указаны.",
  sourceTags: "Исходные теги",
  noSourceTags: "Исходные теги отсутствуют.",
  rawFfprobe: "Исходный JSON ffprobe",
  sourceSaved: "Каталог сохранён.",
  sourceSavedStale:
    "Каталог сохранён. Инвентарь относится к прежнему пути {path} и заменится после успешного сканирования нового пути.",
  artifactPreparation: "Подготовка",
  artifactAcquiring: "Получение артефакта",
  artifactReady: "Готов",
  artifactRetained: "Сохранён",
  inspectorNotFound: "Файл или каталог не найден: возможно, запись удалили.",
  inspectorConflict:
    "Запрос отклонён: состояние файла или каталога изменилось, либо выполняется другая операция.",
  inspectorConflictHelp:
    "Обновите сведения о файле и повторите доступный этап.",
  bytesUnit: "Б",
  bitsPerSecond: "бит/с",
  bitsUnit: "бит",
  hertzUnit: "Гц",
  inventoryFiles: "Файлы инвентаря",
  noInventoryYet:
    "Инвентаря нет: каталог ещё ни разу не сканировался успешно. Список файлов появится после успешного сканирования.",
  inventoryHelp:
    "Показаны записи последнего успешного сканирования: файлы с поддерживаемым аудиорасширением. Пути указаны относительно",
  loadingFiles: "Загрузка списка файлов…",
  noSupportedFiles:
    "Последнее успешное сканирование не нашло файлов с поддерживаемым аудиорасширением.",
  filesCaption:
    "Файлы последнего успешного сканирования: относительный путь, размер, время изменения и проверка аудиопотока",
  audioCheck: "Проверка аудио",
  noAudio: "Нет аудиодорожки",
  probeError: "Ошибка проверки",
  noAudioReason: "Аудиодорожки нет: файл не может участвовать в анализе.",
  probeRetryNote: "Проверка повторится при следующем сканировании.",
  shownCount: "Показано: {count}.",
  showMore: "Показать ещё",
  loadingNextPage: "Загрузка следующей страницы…",
  retryScan: "Повторить сканирование",
  scan: "Сканировать",
  scanningTitle: "Сканирование",
  manualScanOnly:
    "Сканирование запускается только вручную. Число файлов заранее неизвестно, поэтому показываются этапы сервера, без процентов и счётчиков байтов.",
  disabledScan:
    "Каталог выключен: новые сканирования запрещены, пока каталог не включён.",
  checkingScan: "Проверяем активное сканирование этого каталога…",
  queuedScan: "Сканирование поставлено в очередь.",
  traversing: "Обход каталога: перечисление файлов и чтение их метаданных.",
  applying: "Применение результатов: подтверждённый инвентарь сохраняется.",
  scanningStage: "Сканирование выполняется (этап сервера: {stage}).",
  scanDone:
    "Сканирование завершено: инвентарь обновлён последним успешным обходом. Это не означает завершение анализа аудиофайлов.",
  scanStreamError:
    "Обновления по потоку событий прерваны: показано состояние из последнего ответа сервера. Соединение восстановится автоматически.",
  discoverScanError:
    "Не удалось проверить активное сканирование каталога (код {status}). Повторный запуск сервер отклонит, если сканирование уже идёт.",
  scanAlreadyRunning:
    "Сканирование уже выполняется: сервер отклонил повторный запуск. Дождитесь завершения текущего сканирования и повторите.",
  sourceMissing: "Каталог не найден: возможно, его удалили в другом окне.",
  scanRejected:
    "Сервер отклонил сканирование: каталог выключен, недоступен или настройка ещё не завершена.",
  serverError: "Сервер вернул ошибку {status}.",
  scanFailed: "Сканирование не завершилось. Повторите попытку.",
  cleanupTitle: "Временные копии анализа",
  cleanupDescription:
    "Здесь показана ограниченная сервером подборка копий, для которых разрешена явная очистка. Исходные файлы не затрагиваются.",
  loadingCopies: "Загрузка доступных копий…",
  refreshCopies: "Обновить подборку",
  currentBatch:
    "В текущей подборке: {count}. Это количество записей в возвращённом пакете, а не общее число копий или лимит удаления. Максимум в пакете — 1000.",
  refreshingCopies: "Обновление подборки…",
  noCopies: "Нет копий, доступных для очистки.",
  cleanupCandidates: "Копии, доступные для очистки",
  cleanupSelected: "Очистить выбранные ({count})",
  outsideBatch: "Выбранные копии вне текущей подборки",
  outsideBatchHelp:
    "Эти выбранные идентификаторы больше не входят в текущую подборку; они не заменены другими копиями:",
  deselect: "Снять выбор",
  cleanupOperation: "Очистка копий",
  operationRunning: "Операция {state}: {stage}.",
  operationComplete: "Операция завершена: {state}.",
  cleanupStreamInterrupted:
    "Поток обновлений прерван; клиент продолжает ждать актуальный снимок операции.",
  cleanupResults: "Результаты очистки",
  unknownRemoval:
    "Сервер не вернул результаты по отдельным копиям; состояние удаления неизвестно.",
  confirmCleanup: "Очистить выбранные копии?",
  confirmationIds:
    "Будут отправлены только эти {count} идентификаторов из показанного снимка списка:",
  sourceFilesNotDeleted: "Файлы источника не удаляются.",
  confirmCleanupAction: "Подтвердить очистку",
  cleanupEligible: "Ожидает очистки",
  cleanupFailed: "Предыдущая очистка не удалась",
  deleted: "Удалён",
  missing: "Файл уже отсутствует",
  deletionFailed: "Не удалось удалить",
  claimed: "Выполняется",
  listUnavailable: "Не удалось загрузить каталоги.",
  readUnavailable: "Не удалось прочитать каталог.",
  createUnavailable: "Не удалось зарегистрировать каталог.",
  editUnavailable: "Не удалось обновить каталог.",
  deleteUnavailable: "Не удалось удалить каталог.",
  sourceConflict:
    "Каталог участвует в активном сканировании. Изменение пути, отключение и удаление отклонены сервером: повторите после завершения сканирования.",
  deleteConfirmationStale:
    "Подтверждение удаления не совпало с текущим каталогом: обновите страницу и повторите.",
  sourcePathRejected:
    "Сервер отклонил каталог: путь должен быть существующим читаемым каталогом на сервере, не пересекаться с каталогами MeloTrove и не повторять уже зарегистрированный путь.",
  requestFailed: "Не удалось выполнить запрос.",
  sourceAvailable: "Доступен",
  sourceUnavailable: "Недоступен",
  sourceUnchecked: "Не проверен",
  neverScanned: "Не выполнялось",
  scannedInventoryUnavailable:
    "Каталог недоступен: показан инвентарь последнего успешного сканирования.",
  oldInventoryPath: "Инвентарь относится к прежнему пути {path}.",
  emptyInventory:
    "Последнее успешное сканирование не нашло файлов с поддерживаемым аудиорасширением.",
  fileCountOne: "{count} файл",
  fileCountOther: "{count} файла",
  fileCountFew: "{count} файла",
  fileCountMany: "{count} файлов",
  inventorySummary: "В инвентаре последнего успешного сканирования {count}.",
  streamAudio: "Аудио",
  details: "Подробнее",
};

export type SourceMessageKey = keyof typeof en;
export type SourceMessage = {
  key: SourceMessageKey;
  values?: Record<string, string | number>;
};

export function sourceMessage(
  locale: Locale,
  key: SourceMessageKey,
  values: Record<string, string | number> = {},
) {
  return (locale === "ru" ? ru[key] : en[key]).replace(
    /\{([^}]+)\}/g,
    (_, name: string) => String(values[name] ?? `{${name}}`),
  );
}

export function numberFormat(
  locale: Locale,
  options?: Intl.NumberFormatOptions,
) {
  return new Intl.NumberFormat(locale === "ru" ? "ru-RU" : "en-US", options);
}

export function dateTimeFormat(locale: Locale, date: string | number | Date) {
  return new Intl.DateTimeFormat(locale === "ru" ? "ru-RU" : "en-US", {
    dateStyle: "short",
    timeStyle: "short",
  }).format(new Date(date));
}
