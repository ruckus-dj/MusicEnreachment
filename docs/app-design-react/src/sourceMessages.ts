import { createNamespace } from "./i18n/createNamespace";
import type { Locale } from "./i18n/types";

export const sourcesMessages = createNamespace({
	en: {
		title: "Sources",
		description:
			"Server paths and read-only source policy. Choose processing mode separately; files are never uploaded by drag and drop.",
		addSource: "Add source",
		newSource: "New source",
		name: "Source name",
		namePlaceholder: "For example, Music library",
		serverDirectory: "Server directory",
		chooseFolder: "Choose folder",
		onlyDirectory: "Choose a directory; files are not uploaded.",
		processingMode: "Processing mode · choose explicitly",
		inPlace: "In place",
		inPlaceHelp:
			"Analysis reads the original file; the source itself remains read-only.",
		staged: "Using a work copy",
		stagedHelp:
			"Processing uses an application-owned audio copy in the shared server work directory. The directory is required for processing, not for adding or scanning a source.",
		sourcePolicy: "Source policy: read-only in either mode.",
		nameRequired: "Enter a source name.",
		modeRequired:
			"Choose a processing mode explicitly: In place or Using a work copy.",
		sourceAdded:
			"Source added to the prototype. No server directory was opened or scanned; a work directory is required only before staged processing.",
		cancel: "Cancel",
		connectedFolders: "Connected directories",
		nameColumn: "Name",
		pathColumn: "Path",
		modeColumn: "Processing mode",
		policyColumn: "Source policy",
		availabilityColumn: "Availability",
		checkedColumn: "Last checked",
		actionsColumn: "Actions",
		modeFor: "Processing mode: {name}",
		futureMode: "Changes apply to future processing.",
		workDirectory: "Shared work directory",
		configure: "Configure",
		configureInSettings: "Configure in Settings",
		notChecked: "Not checked · fixture",
		available: "Available",
		unavailable: "Unavailable",
		reviewGroups: "Review groups",
		checkUnavailable:
			"Source unavailable in the fixed fixture; no filesystem check was performed.",
		scan: "Scan and process",
		scanDisabled: "Scanning is available only for a source marked available.",
		scanStarted:
			"Inventory scan completed for {path}; processing started for {count} file records.",
		noInventory:
			"The fixed inventory contains no file records for this source.",
		workDirectoryRequired:
			"Files found. Choose a work directory in Settings to process working copies.",
		outputResetCancelled:
			"Output reset cancelled this queued work. It will remain paused until you explicitly retry it.",
		processingTitle: "File processing",
		processingDescription:
			"Files are processed independently. Failed steps can be retried separately.",
		freshness: "Rescanning detects changes by file size and modification time.",
		noFiles: "No inventory records have been queued for processing.",
		fileStatus: "File status: {status}",
		stepAudio: "Audio inspection",
		stepFingerprint: "Fingerprint",
		statusQueued: "Queued",
		statusRunning: "Running",
		statusSucceeded: "Succeeded",
		statusFailed: "Failed",
		statusCompleted: "Completed",
		statusBlocked: "Blocked · work directory required",
		fingerprintFailed:
			"Could not calculate this file’s audio fingerprint. Retry this step.",
		retry: "Retry",
		retryCancelled: "Retry cancelled work",
		retryStep: "Retry {step}",
		retainedCopies: "Retained work copies",
		copyOwnership:
			"These copies belong to the application. Removing a copy never changes the original source or current publications.",
		noCopies: "No retained work copies.",
		copyProtected: "Protected until processing succeeds",
		copyEligible: "Eligible for cleanup",
		removeCopy: "Remove retained copy",
		copyRemoved:
			"The application-owned work copy was removed from the prototype state.",
		removeRoot: "Remove source",
		rootRemoved: "Source removed from the prototype inventory.",
		rootActive:
			"Removal blocked: this source has queued or running processing. Wait for it to finish, then try again.",
		rootChanged:
			"Confirmation is out of date because this source inventory changed. Review the current source and try again.",
		rootConfirmTitle: "Remove source {path}?",
		rootConfirmCount: "File records in inventory: {count}.",
		rootConfirmScope:
			"Only this source's inventory records and application-owned retained work copies are removed. Original files on the host, library objects, and current publications remain untouched.",
		rootConfirmRecheck:
			"Before confirmation, the source inventory and any active processing are checked again. The prototype does not access the real filesystem.",
		confirm: "Confirm removal",
		unavailablePublications:
			"An unavailable source does not remove existing managed publications.",
		rootActionLabel: "Remove source {name}",
		deleteDialogLabel: "Remove source confirmation",
	},
	ru: {
		title: "Источники",
		description:
			"Серверные пути и политика источника только для чтения. Режим обработки выбирается отдельно; файлы не загружаются перетаскиванием.",
		addSource: "Добавить источник",
		newSource: "Новый источник",
		name: "Имя источника",
		namePlaceholder: "Например, Музыкальная библиотека",
		serverDirectory: "Каталог на сервере",
		chooseFolder: "Выбрать папку",
		onlyDirectory: "Выберите каталог; файлы не загружаются.",
		processingMode: "Режим обработки · выберите явно",
		inPlace: "На месте",
		inPlaceHelp:
			"Анализ выполняется по исходному файлу; сам источник остаётся только для чтения.",
		staged: "Через рабочую копию",
		stagedHelp:
			"Для обработки используется принадлежащая приложению рабочая копия аудио в общей рабочей папке сервера. Папка требуется для обработки, но не для добавления или сканирования источника.",
		sourcePolicy: "Политика источника: только чтение в обоих режимах.",
		nameRequired: "Укажите имя источника.",
		modeRequired:
			"Явно выберите режим обработки: «На месте» или «Через рабочую копию».",
		sourceAdded:
			"Источник добавлен в макет. Серверный каталог не открывался и не сканировался; рабочая папка требуется только перед обработкой через копию.",
		cancel: "Отмена",
		connectedFolders: "Подключённые каталоги",
		nameColumn: "Имя",
		pathColumn: "Путь",
		modeColumn: "Режим обработки",
		policyColumn: "Политика источника",
		availabilityColumn: "Доступность",
		checkedColumn: "Последняя проверка",
		actionsColumn: "Действия",
		modeFor: "Режим обработки: {name}",
		futureMode: "Изменение влияет на будущую обработку.",
		workDirectory: "Общая рабочая папка",
		configure: "Настроить",
		configureInSettings: "Настроить в Настройках",
		notChecked: "Не проверен · фикстура",
		available: "Доступен",
		unavailable: "Недоступен",
		reviewGroups: "Разобрать группы",
		checkUnavailable:
			"Источник недоступен в фиксированных данных; файловая система не проверялась.",
		scan: "Сканировать и обработать",
		scanDisabled:
			"Сканирование доступно только для отмеченного доступным источника.",
		scanStarted:
			"Сканирование инвентаря {path} завершено; обработка начата для записей о файлах: {count}.",
		noInventory:
			"В фиксированном инвентаре для этого источника нет записей о файлах.",
		workDirectoryRequired:
			"Файлы найдены. Для обработки через копию выберите рабочую папку в Настройках.",
		outputResetCancelled:
			"Обработка в очереди отменена при смене каталога публикации. Чтобы продолжить, явно повторите обработку.",
		processingTitle: "Обработка файлов",
		processingDescription:
			"Файлы обрабатываются независимо. Неудачный шаг можно повторить отдельно.",
		freshness:
			"Повторное сканирование определяет изменения по размеру и времени изменения файла.",
		noFiles: "В очереди нет записей инвентаря для обработки.",
		fileStatus: "Состояние файла: {status}",
		stepAudio: "Проверка аудио",
		stepFingerprint: "Fingerprint",
		statusQueued: "В очереди",
		statusRunning: "Выполняется",
		statusSucceeded: "Завершено",
		statusFailed: "Ошибка",
		statusCompleted: "Завершено",
		statusBlocked: "Заблокировано · требуется рабочая папка",
		fingerprintFailed:
			"Не удалось рассчитать аудиоотпечаток файла. Повторите этот шаг.",
		retry: "Повторить",
		retryCancelled: "Повторить отменённую обработку",
		retryStep: "Повторить: {step}",
		retainedCopies: "Сохранённые рабочие копии",
		copyOwnership:
			"Эти копии принадлежат приложению. Их удаление не меняет исходники и текущие публикации.",
		noCopies: "Сохранённых рабочих копий нет.",
		copyProtected: "Защищена до успешного завершения обработки",
		copyEligible: "Можно очистить",
		removeCopy: "Удалить рабочую копию",
		copyRemoved: "Рабочая копия приложения удалена из состояния макета.",
		removeRoot: "Удалить источник",
		rootRemoved: "Источник удалён из инвентаря макета.",
		rootActive:
			"Удаление заблокировано: для этого источника есть записи в очереди или выполняется обработка. Дождитесь завершения и повторите попытку.",
		rootChanged:
			"Подтверждение устарело: инвентарь источника изменился. Проверьте текущее состояние и повторите попытку.",
		rootConfirmTitle: "Удалить источник {path}?",
		rootConfirmCount: "Записей о файлах в инвентаре: {count}.",
		rootConfirmScope:
			"Удаляются только записи инвентаря этого источника и принадлежащие приложению сохранённые рабочие копии. Исходные файлы на сервере, объекты медиатеки и текущие публикации не затрагиваются.",
		rootConfirmRecheck:
			"Перед подтверждением инвентарь и активная обработка будут проверены повторно. Макет не обращается к реальной файловой системе.",
		confirm: "Подтвердить удаление",
		unavailablePublications:
			"Недоступность источника не удаляет существующие управляемые публикации.",
		rootActionLabel: "Удалить источник {name}",
		deleteDialogLabel: "Подтверждение удаления источника",
	},
});

export type SourceMessageKey = keyof typeof sourcesMessages.en;

export function sourceMessage(
	locale: Locale,
	key: SourceMessageKey,
	values: Record<string, string | number> = {},
): string {
	return sourcesMessages[locale][key].replace(
		/\{([^}]+)\}/g,
		(_, name: string) => String(values[name] ?? `{${name}}`),
	);
}
