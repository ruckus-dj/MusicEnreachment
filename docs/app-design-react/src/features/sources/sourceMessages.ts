import { createNamespace } from "../../i18n/createNamespace";
import type { Locale } from "../../i18n/types";

export const sourcesMessages = createNamespace({
	en: {
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
		workDirectory: "Shared work directory",
		configure: "Configure",
		configureInSettings: "Configure in Settings",
		notChecked: "Not checked · fixture",
		available: "Available",
		unavailable: "Unavailable",
		checkUnavailable:
			"Source unavailable in the fixed fixture; no filesystem check was performed.",
		scan: "Scan and process",
		scanDisabled: "Scanning is available only for a source marked available.",
		scanStarted:
			"Inventory scan completed for {path}; processing started for {count} file records.",
		noInventory:
			"The fixed inventory contains no file records for this source.",
		outputResetCancelled:
			"Changing the publication directory cancelled this step. Retry it to continue.",
		statusBlocked: "Blocked · work directory required",
		fingerprintFailed:
			"Could not calculate this file’s audio fingerprint. Retry this step.",
		copyPathUnavailable: "Source path unavailable",
		cleanupSelectionStale:
			"The selected copies are no longer all eligible. Refresh the selection and choose again.",
		cleanupCopyFailed: "The temporary copy could not be cleaned up.",
		cleanupBatchFailed: "One or more temporary copies could not be cleaned up.",
		rootRemoved: "Source removed from the prototype inventory.",
		rootActive:
			"Removal blocked: this source has queued or running processing. Wait for it to finish, then try again.",
		pathScanActive:
			"The folder cannot be changed while this source is being scanned.",
		rootChanged:
			"Confirmation is out of date because this source inventory changed. Review the current source and try again.",
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
		workDirectory: "Общая рабочая папка",
		configure: "Настроить",
		configureInSettings: "Настроить в Настройках",
		notChecked: "Не проверен · фикстура",
		available: "Доступен",
		unavailable: "Недоступен",
		checkUnavailable:
			"Источник недоступен в фиксированных данных; файловая система не проверялась.",
		scan: "Сканировать и обработать",
		scanDisabled:
			"Сканирование доступно только для отмеченного доступным источника.",
		scanStarted:
			"Сканирование инвентаря {path} завершено; обработка начата для записей о файлах: {count}.",
		noInventory:
			"В фиксированном инвентаре для этого источника нет записей о файлах.",
		outputResetCancelled:
			"Шаг отменён при смене каталога публикации. Чтобы продолжить, повторите этот шаг.",
		statusBlocked: "Заблокировано · требуется рабочая папка",
		fingerprintFailed:
			"Не удалось рассчитать аудиоотпечаток файла. Повторите этот шаг.",
		copyPathUnavailable: "Путь в источнике недоступен",
		cleanupSelectionStale:
			"Выбранные копии больше не все доступны для очистки. Обновите подборку и выберите заново.",
		cleanupCopyFailed: "Не удалось очистить временную копию.",
		cleanupBatchFailed:
			"Не удалось очистить одну или несколько временных копий.",
		rootRemoved: "Источник удалён из инвентаря макета.",
		rootActive:
			"Удаление заблокировано: для этого источника есть записи в очереди или выполняется обработка. Дождитесь завершения и повторите попытку.",
		pathScanActive:
			"Каталог нельзя изменить, пока выполняется сканирование источника.",
		rootChanged:
			"Подтверждение устарело: инвентарь источника изменился. Проверьте текущее состояние и повторите попытку.",
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
