import { createNamespace } from "../../i18n/createNamespace";
import type { Locale } from "../../i18n/types";

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
		overviewCaption:
			"Files, paths, source readiness, and saved group evaluations",
		columnNumber: "No.",
		columnTitle: "Title",
		columnArtist: "Artist",
		columnAlbum: "Album",
		columnActions: "Actions",
		noValue: "—",
		filesIn: "Files in {name}",
		group: "Group",
		uniqueFilesPaths: "Unique files / paths",
		readyTotal: "Ready / total",
		evaluationState: "Evaluation status",
		openGroup: "Open group “{name}”",
		filePaths: "File paths",
		sourcePathUnavailable: "Source path unavailable",
		repeatedGroupName: "{name} · group {number}",
		missingFileTitle: "No title tag",
		remainingFilesOne: "and {count} more file",
		remainingFilesFew: "and {count} more files",
		remainingFilesMany: "and {count} more files",
		remainingFilesOther: "and {count} more files",
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
		overviewCaption:
			"Файлы, пути, готовность источников и сохранённые оценки групп",
		columnNumber: "№",
		columnTitle: "Название",
		columnArtist: "Исполнитель",
		columnAlbum: "Альбом",
		columnActions: "Действия",
		noValue: "—",
		filesIn: "Файлы группы «{name}»",
		group: "Группа",
		uniqueFilesPaths: "Уникальные файлы / пути",
		readyTotal: "Готово / всего",
		evaluationState: "Состояние оценки",
		openGroup: "Открыть группу «{name}»",
		filePaths: "Пути файлов",
		sourcePathUnavailable: "Путь к файлам недоступен",
		repeatedGroupName: "{name} · группа {number}",
		missingFileTitle: "Нет тега названия",
		remainingFilesOne: "и ещё {count} файл",
		remainingFilesFew: "и ещё {count} файла",
		remainingFilesMany: "и ещё {count} файлов",
		remainingFilesOther: "и ещё {count} файлов",
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
