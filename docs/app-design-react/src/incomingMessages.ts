import { createNamespace } from "./i18n/createNamespace";
import type { Locale } from "./i18n/types";

export const incomingMessages = createNamespace({
	en: {
		artistNotSpecified: "Artist not specified",
		incompleteReason: "Incomplete release; one track is uncertain",
		confidentReason: "{count} / {total}: all assignments are confident",
		compilationReason: "No provider release; tracks come from different albums",
		automaticNotice:
			"Automatically published {count} tracks. Incomplete groups remain in manual review. No file operations were performed.",
		noAutomaticNotice:
			"There are no new fully confident releases. Automatic publication does not repeat work or lower quality.",
		process: "Process automatically",
		workflow: "Incoming · source tags → matching → publication",
		title: "Incoming albums",
		introduction:
			"Groups from source tags → find a release → assign files → publish. Incomplete data is allowed.",
		stages: "Processing stages",
		groupFiles: "Group files",
		match: "Match",
		allConfidentAutomatic: "All assignments confident",
		automaticDirection: "automatic",
		otherwiseManual: "Otherwise",
		manualDirection: "manual review, including partial groups",
		currentGroups: "Current groups",
		groupsOne: "{count} group · source queue",
		groupsFew: "{count} groups · source queue",
		groupsMany: "{count} groups · source queue",
		groupsOther: "{count} groups · source queue",
		filesCountOne: "file",
		filesCountFew: "files",
		filesCountOther: "files",
		groupList: "Current incoming album groups",
		noGroups: "There are no incoming groups yet.",
		open: "Open",
		files: "Files:",
		assigned: "Assigned:",
		free: "Available:",
		published: "Published:",
		assignedHeading: "Assigned",
		freeHeading: "Available",
		publishedHeading: "Published",
		albumFolder: "Album / folder",
		incoming: "Incoming",
		mode: "Mode",
		reason: "Reason",
		action: "Action",
		publishedAutomatically: "Published automatically",
		readyForAutomatic: "Ready for automatic processing",
		manualProcessing: "Manual processing",
		manualScenario: "Follow the manual workflow",
		manualStep1:
			"Open Northern Lines: confident assignments are already filled in.",
		manualStep2: "Assign 03 — Northbound (live?) to track 03.",
		manualStep3:
			"Leave tracks 09–10 empty; the fragment will remain in incoming.",
		manualStep4: "Preview → confirm publication of the selected tracks.",
		missingProviderCompilation: "Compilation not listed by the provider",
		compilationDescription:
			"Road Notes keeps its own title and order. Its tracks can be found on other releases; a provider album does not replace this local compilation.",
		openCompilation: "Open local compilation",
	},
	ru: {
		artistNotSpecified: "Артист не указан",
		incompleteReason: "Неполный релиз; одна спорная композиция",
		confidentReason: "{count} / {total}: все назначения уверенные",
		compilationReason: "Релиза нет у провайдера; композиции из разных альбомов",
		automaticNotice:
			"Автоматически опубликовано позиций: {count}. Неполные группы остались в ручном разборе. Файловые операции не выполнялись.",
		noAutomaticNotice:
			"Нет новых полностью уверенных релизов. Автопубликация не повторяет и не понижает качество.",
		process: "Запустить обработку",
		workflow: "Входящие · исходные теги → сопоставление → публикация",
		title: "Входящие альбомы",
		introduction:
			"Группы из исходных тегов → поиск издания → назначения → публикация. Неполные данные допустимы.",
		stages: "Этапы обработки",
		groupFiles: "Сгруппировать файлы",
		match: "Сопоставить",
		allConfidentAutomatic: "Все назначения уверенные",
		automaticDirection: "автоматически",
		otherwiseManual: "Иначе",
		manualDirection: "ручной разбор, в том числе частичный",
		currentGroups: "Текущие группы",
		groupsOne: "Группа: {count} · очередь источников",
		groupsFew: "Группы: {count} · очередь источников",
		groupsMany: "Групп: {count} · очередь источников",
		groupsOther: "Групп: {count} · очередь источников",
		filesCountOne: "файл",
		filesCountFew: "файла",
		filesCountOther: "файлов",
		groupList: "Текущие группы входящих альбомов",
		noGroups: "Входящих групп пока нет.",
		open: "Открыть",
		files: "Файлы:",
		assigned: "Назначено:",
		free: "Свободно:",
		published: "Опубликовано:",
		assignedHeading: "Назначено",
		freeHeading: "Свободно",
		publishedHeading: "Опубликовано",
		albumFolder: "Альбом / папка",
		incoming: "Входящие",
		mode: "Режим",
		reason: "Причина",
		action: "Действие",
		publishedAutomatically: "Опубликовано автоматически",
		readyForAutomatic: "Готов к автоматике",
		manualProcessing: "Ручная обработка",
		manualScenario: "Как пройти ручной сценарий",
		manualStep1: "Откройте Northern Lines: уверенные назначения заполнены.",
		manualStep2: "Назначьте 03 — Northbound (live?) на позицию 03.",
		manualStep3:
			"Оставьте позиции 09–10 пустыми; фрагмент останется во входящих.",
		manualStep4: "Предпросмотр → подтвердить публикацию выбранных позиций.",
		missingProviderCompilation: "Сборник, которого нет у провайдера",
		compilationDescription:
			"Road Notes сохраняет собственное название и порядок. Композиции можно найти в других изданиях; альбом провайдера не заменяет локальный сборник.",
		openCompilation: "Открыть локальный сборник",
	},
});

export type IncomingMessage = keyof typeof incomingMessages.en;

export function incomingMessage(
	locale: Locale,
	key: IncomingMessage,
	values: Record<string, string | number> = {},
) {
	return incomingMessages[locale][key].replace(
		/\{([^}]+)\}/g,
		(_, name: string) => String(values[name] ?? `{${name}}`),
	);
}

export function incomingFilesCount(locale: Locale, count: number) {
	const category = new Intl.PluralRules(locale).select(count);
	const key =
		locale === "ru" && category === "few"
			? "filesCountFew"
			: category === "one"
				? "filesCountOne"
				: "filesCountOther";
	return `${count} ${incomingMessages[locale][key]}`;
}

export function incomingGroupsCount(locale: Locale, count: number) {
	const category = new Intl.PluralRules(locale).select(count);
	const key =
		category === "one"
			? "groupsOne"
			: locale === "ru" && category === "few"
				? "groupsFew"
				: locale === "ru"
					? "groupsMany"
					: "groupsOther";
	return incomingMessages[locale][key].replace("{count}", String(count));
}
