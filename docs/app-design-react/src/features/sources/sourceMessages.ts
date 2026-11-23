import { createNamespace } from "../../i18n/createNamespace";
import type { Locale } from "../../i18n/types";

export const sourcesMessages = createNamespace({
	en: {
		title: "Sources",
		description:
			"Choose server directories and their processing mode. Analysis does not modify originals; source deletion is a separate setting and requires filesystem permission. Files are not uploaded by drag and drop.",
		sourceAdded:
			"Source added to the prototype. No server directory was opened or scanned; a work directory is required only before staged processing.",
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
		audioNotChecked: "Not checked",
		audioCheckFailed: "Check failed",
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
		copyPathUnavailable: "Source path unavailable",
		cleanupSelectionStale:
			"The selected copies are no longer all eligible. Refresh the selection and choose again.",
		cleanupCopyFailed: "The temporary copy could not be cleaned up.",
		cleanupBatchFailed: "One or more temporary copies could not be cleaned up.",
		rootRemoved: "Source removed from the prototype inventory.",
		rootActive:
			"Removal blocked: this source has queued or running processing. Wait for it to finish, then try again.",
		rootChanged:
			"Confirmation is out of date because this source inventory changed. Review the current source and try again.",
		unavailablePublications:
			"An unavailable source does not remove existing managed publications.",
		backToSources: "Back to sources",
		sourceNotFound: "Source not found",
		lastSuccessfulScan: "Last successful scan",
		inventory: "Inventory",
		neverScanned: "Not scanned yet",
		inventoryCount: "Inventory records: {count}",
		scanFailedFixture:
			"This demo source fails its first scan to show retry behavior. Retry to scan it again.",
	},
	ru: {
		title: "Источники",
		description:
			"Выберите серверные каталоги и режим их обработки. Анализ не изменяет исходники; удаление — отдельная настройка, требующая разрешения файловой системы. Файлы не загружаются перетаскиванием.",
		sourceAdded:
			"Источник добавлен в макет. Серверный каталог не открывался и не сканировался; рабочая папка требуется только перед обработкой через копию.",
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
		audioNotChecked: "Не проверен",
		audioCheckFailed: "Проверка не удалась",
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
		copyPathUnavailable: "Путь в источнике недоступен",
		cleanupSelectionStale:
			"Выбранные копии больше не все доступны для очистки. Обновите подборку и выберите заново.",
		cleanupCopyFailed: "Не удалось очистить временную копию.",
		cleanupBatchFailed:
			"Не удалось очистить одну или несколько временных копий.",
		rootRemoved: "Источник удалён из инвентаря макета.",
		rootActive:
			"Удаление заблокировано: для этого источника есть записи в очереди или выполняется обработка. Дождитесь завершения и повторите попытку.",
		rootChanged:
			"Подтверждение устарело: инвентарь источника изменился. Проверьте текущее состояние и повторите попытку.",
		unavailablePublications:
			"Недоступность источника не удаляет существующие управляемые публикации.",
		backToSources: "К списку источников",
		sourceNotFound: "Источник не найден",
		lastSuccessfulScan: "Последнее успешное сканирование",
		inventory: "Инвентарь",
		neverScanned: "Ещё не сканировался",
		inventoryCount: "Записей в инвентаре: {count}",
		scanFailedFixture:
			"Первый скан этого демонстрационного источника завершается ошибкой, чтобы показать повтор. Запустите сканирование ещё раз.",
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
