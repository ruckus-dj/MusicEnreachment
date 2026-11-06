import { createNamespace } from "./i18n/createNamespace";
import type { Locale } from "./i18n/types";

export const operationMessages = createNamespace({
	en: {
		title: "Current operations",
		attention: "Needs attention",
		currentOperations: "current operations",
		object: "Object",
		operation: "Operation",
		phase: "Phase",
		reason: "Reason",
		consequence: "Consequence",
		action: "Action",
		signalsTrack: "Signals · position 06",
		publishPositions: "Publish positions",
		failed: "Failed",
		retry: "Retry",
		dismiss: "Dismiss",
		retryQueued: "Retry queued",
		retrySucceeded: "Retry succeeded; the resolved operation was removed.",
		dismissed: "Operation dismissed.",
		prepareAgain:
			"A retry prepares the result again. It does not force a republish.",
		recoveryTrack: "Northern Lines · position 01",
		interruptedPublication: "Interrupted publication",
		resultNotRegistered: "Result not registered",
		knownTargetMissingResult:
			"The known target file is present, but its result record is missing.",
		checkKnownTarget:
			"Check the known target. If it does not match, use a full retry.",
		checkResult: "Check result",
		confirmResult: "Register result",
		recoveryRejected:
			"Recovery was rejected because the current object is incompatible with the checked result.",
		recoveryFailed:
			"Recovery validation failed. Use an ordinary full retry instead.",
		recoveryRegistered:
			"The checked result was registered as the current publication.",
		deletionTitle: "Library removal requests",
		deletionDescription: "Review and complete the confirmed removal scope.",
		state: "State",
		confirmedScope: "Confirmed scope",
		filesAndPublications: "Files / publications",
		result: "Result",
		removePublications: "Remove publications",
		removeArtist: "Remove artist",
		removeLibrary: "Remove library",
		staleScope: "Scope is out of date",
		queued: "Queued",
		publicationCount:
			"{count} publication records; {files} application-owned files in the confirmed scope",
		completeRemoval: "Complete removal",
		retryRemoval: "Retry removal",
		returnAndConfirm:
			"Return to the library and confirm the removal scope again",
		removalSucceeded: "The confirmed removal scope was completed.",
		removalStale:
			"Removal stopped because the confirmed scope is out of date. Review and confirm the current scope again.",
		removalRetry:
			"Removal is incomplete. Retry to verify the managed path and finish removal; an unavailable path is not treated as an absent file.",
		removalConflict:
			"The file changed or the managed path is occupied by another file or publication. It was not touched; review the publication before retrying.",
		removalStateRetry: "Removal is still pending",
		removalStateConflict: "Path conflict · review required",
		currentRequestsEmpty: "No current requests.",
		checkSnapshot: "Known frozen path: ",
		tagsMatch: "Tags match",
		technicalMatch: "Technical properties match",
		authorityCurrent: "Authority is current",
		yes: "yes",
		no: "no",
		physicalTargetNotChecked: "The target file itself is not inspected here.",
		cancel: "Cancel",
		confirm: "Confirm",
		deleteKindPublication: "Remove publications",
		deleteKindArtist: "Remove artist",
		deleteKindLibrary: "Remove library",
		unknownTarget: "Unknown target",
		noCurrentOperations: "No current operations.",
		writeAccessFailure: "EACCES: no write access to the temporary directory",
		otherTrackActions: "Other actions",
		editLocalTitle: "Edit local title",
		refreshProviderCache: "Refresh provider cache",
		providerCacheRefreshed:
			"Provider cache refreshed separately. The publication and metadata snapshot are unchanged.",
		upgradeAudio: "Improve audio quality only",
		upgradeAudioTitle: "Improve audio only?",
		audioUpgraded:
			"Audio quality was updated. Publication tags, artwork, lyrics, and metadata snapshot are preserved.",
		republishExplicit: "Republish explicitly",
		chooseSource:
			"Choose a source before explicitly republishing. No alternative source is selected automatically.",
		republishTitle: "Explicitly republish from the selected source?",
		republishDescription:
			"This explicitly replaces the current publication using the selected source, even when metadata is unchanged. It is separate from retry.",
		republished:
			"The current publication snapshot was replaced using the selected source.",
		downgradeRequired: "The downgrade was not confirmed.",
		confirmDowngrade: "I confirm the quality downgrade",
		editTagsOnly: "Edit local tags only",
		trackTitle: "Position title",
		sourceAndFormat: "{title} · {path} · {codec}",
		renamePreserves:
			"The source, provider cache, and current publication remain unchanged.",
		publicationPreserves:
			"The current publication's tags, embedded artwork, and lyrics remain unchanged.",
		cancelAction: "Cancel",
		confirmAction: "Confirm",
	},
	ru: {
		title: "Текущие операции",
		attention: "Требует внимания",
		currentOperations: "текущие операции",
		object: "Объект",
		operation: "Операция",
		phase: "Фаза",
		reason: "Причина",
		consequence: "Последствие",
		action: "Действие",
		signalsTrack: "Signals · позиция 06",
		publishPositions: "Публикация набора позиций",
		failed: "Ошибка",
		retry: "Повторить",
		dismiss: "Скрыть",
		retryQueued: "Повтор поставлен в очередь",
		retrySucceeded: "Повтор завершён; устранённая операция удалена.",
		dismissed: "Операция скрыта.",
		prepareAgain:
			"Повтор заново подготавливает результат и не является принудительной перепубликацией.",
		recoveryTrack: "Northern Lines · позиция 01",
		interruptedPublication: "Прерванная публикация",
		resultNotRegistered: "Результат не зарегистрирован",
		knownTargetMissingResult:
			"Известный целевой файл установлен, но запись результата отсутствует.",
		checkKnownTarget:
			"Проверить известный целевой файл; при несовпадении выполнить полный повтор.",
		checkResult: "Проверить результат",
		confirmResult: "Зарегистрировать результат",
		recoveryRejected:
			"Восстановление отклонено: текущий объект несовместим с проверенным результатом.",
		recoveryFailed:
			"Проверка восстановления не пройдена. Выполните обычный полный повтор.",
		recoveryRegistered:
			"Проверенный результат зарегистрирован как текущая публикация.",
		deletionTitle: "Запросы на удаление из медиатеки",
		deletionDescription:
			"Проверьте и выполните удаление в подтверждённой области.",
		state: "Состояние",
		confirmedScope: "Подтверждённая область",
		filesAndPublications: "Файлы / публикации",
		result: "Результат",
		removePublications: "Удаление публикаций",
		removeArtist: "Удаление артиста",
		removeLibrary: "Удаление медиатеки",
		staleScope: "Область устарела",
		queued: "В очереди",
		publicationCount:
			"{count} строк публикаций; {files} принадлежащих приложению файлов в подтверждённой области",
		completeRemoval: "Выполнить удаление",
		retryRemoval: "Повторить удаление",
		returnAndConfirm:
			"Вернуться в медиатеку и подтвердить область удаления заново",
		removalSucceeded: "Удаление в подтверждённой области выполнено.",
		removalStale:
			"Удаление остановлено: подтверждённая область устарела. Проверьте и подтвердите актуальную область заново.",
		removalRetry:
			"Удаление не завершено. Повторите попытку для проверки управляемого пути и завершения удаления; недоступный путь не считается отсутствующим файлом.",
		removalConflict:
			"Файл изменился или управляемый путь занят другим файлом или публикацией. Он не затронут; проверьте публикацию перед повтором.",
		removalStateRetry: "Удаление не завершено",
		removalStateConflict: "Конфликт пути · требуется проверка",
		currentRequestsEmpty: "Нет текущих запросов.",
		checkSnapshot: "Известный зафиксированный путь: ",
		tagsMatch: "Теги совпадают",
		technicalMatch: "Технические параметры совпадают",
		authorityCurrent: "Разрешение актуально",
		yes: "да",
		no: "нет",
		physicalTargetNotChecked: "Сам целевой файл здесь не проверяется.",
		cancel: "Отмена",
		confirm: "Подтвердить",
		deleteKindPublication: "Удаление публикаций",
		deleteKindArtist: "Удаление артиста",
		deleteKindLibrary: "Удаление медиатеки",
		unknownTarget: "Неизвестный объект",
		noCurrentOperations: "Нет текущих операций.",
		writeAccessFailure: "EACCES: нет записи во временном каталоге",
		otherTrackActions: "Другие действия",
		editLocalTitle: "Изменить локальное название",
		refreshProviderCache: "Обновить кэш провайдера",
		providerCacheRefreshed:
			"Кэш провайдера обновлён отдельно. Публикация и снимок метаданных не изменились.",
		upgradeAudio: "Улучшить только качество аудио",
		upgradeAudioTitle: "Улучшить только аудио?",
		audioUpgraded:
			"Качество аудио обновлено. Теги публикации, обложка, текст песни и снимок метаданных сохранены.",
		republishExplicit: "Перепубликовать явно",
		chooseSource:
			"Для явной перепубликации выберите исходник. Альтернативный вариант автоматически не выбирается.",
		republishTitle: "Явно перепубликовать из выбранного исходника?",
		republishDescription:
			"Это действие явно заменяет текущую публикацию из выбранного исходника, даже если метаданные не изменились. Оно отличается от повтора.",
		republished:
			"Снимок текущей публикации заменён с использованием выбранного исходника.",
		downgradeRequired: "Понижение качества не подтверждено.",
		confirmDowngrade: "Подтверждаю понижение качества",
		editTagsOnly: "Изменить только локальные теги",
		trackTitle: "Название позиции",
		sourceAndFormat: "{title} · {path} · {codec}",
		renamePreserves:
			"Исходник, кэш провайдера и текущая публикация останутся прежними.",
		publicationPreserves:
			"Теги, встроенная обложка и текст песни текущей публикации останутся без изменений.",
		cancelAction: "Отмена",
		confirmAction: "Подтвердить",
	},
});

export type OperationMessage = keyof typeof operationMessages.en;

export function operationMessage(
	locale: Locale,
	key: OperationMessage,
	values: Record<string, string | number> = {},
) {
	return operationMessages[locale][key].replace(
		/\{([^}]+)\}/g,
		(_, name: string) => String(values[name] ?? `{${name}}`),
	);
}
