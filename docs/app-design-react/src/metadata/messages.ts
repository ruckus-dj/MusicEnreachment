import { createNamespace } from "../i18n/createNamespace";
import { fieldLabels } from "../features/library/libraryTagComparisonFields";
import type { Locale } from "../i18n/types";

type FieldLabelKey = keyof typeof fieldLabels.en;

export type MetadataFieldMessageKey = `field.${FieldLabelKey}`;

const fieldMessageKeys = {} as Record<FieldLabelKey, MetadataFieldMessageKey>;
for (const key of Object.keys(fieldLabels.en) as FieldLabelKey[]) {
	fieldMessageKeys[key] = `field.${key}`;
}

function fieldMessages(
	labels: Record<FieldLabelKey, string>,
): Record<MetadataFieldMessageKey, string> {
	const messages = {} as Record<MetadataFieldMessageKey, string>;
	for (const key of Object.keys(labels) as FieldLabelKey[]) {
		messages[fieldMessageKeys[key]] = labels[key];
	}
	return messages;
}

const ui = createNamespace({
	en: {
		"action.edit": "Edit",
		"action.save": "Save",
		"action.saving": "Saving…",
		"action.cancel": "Cancel",
		"action.remove": "Remove",
		"action.addArtist": "Add artist",
		"action.addContributor": "Add contributor",
		"action.done": "Done",
		"action.retry": "Retry loading",
		"action.loadCatalog": "Load catalog",
		"action.settings": "Go to catalog settings",
		"state.unspecified": "Not set",
		"state.notCalculated": "Not calculated",
		"state.filled": "filled",
		"state.empty": "Not filled",
		"state.noCredits": "Not specified",
		"state.noArtist": "Artist not specified",
		"group.release": "Release type and details",
		"group.recording": "Dates and musical details",
		"group.track": "Additional position details",
		"group.audio": "Audio",
		"group.recordingArtists": "Recording artists",
		"group.releaseArtists": "Release artists",
		"group.roles": "Contributors and roles",
		"group.identifiers": "Identifiers",
		"group.works": "Works",
		"group.movement": "Movement",
		"group.worksMovement": "Works and movements",
		"group.audioMeasurements": "Audio measurements",
		"group.lyrics": "Lyrics",
		"group.extraTags": "Additional tags",
		"group.extraDetails": "Additional details",
		"label.artistComposition": "Artists and order",
		"label.linkedArtist": "Linked artist",
		"label.unlinked": "Not linked",
		"label.creditName": "Credited name",
		"label.artistMusicBrainz": "Artist MusicBrainz ID",
		"label.joinPhrase": "Join phrase",
		"label.artistSort": "Artist sort",
		"label.contributorName": "Contributor name",
		"label.contributorMusicBrainz": "Contributor MusicBrainz ID",
		"label.instrument": "Instrument",
		"label.addRole": "Add role",
		"label.selectRole": "Select a role…",
		"label.foreignFields":
			"Metadata authorization is pending confirmation; values from other scopes cannot be edited.",
		"action.moveUp": "Move up",
		"action.moveDown": "Move down",
		"value.yes": "Yes",
		"value.no": "No",
		"value.unrated": "Not rated",
		"value.clean": "Clean",
		"value.explicit": "Explicit content",
		"value.explicitLegacy": "Explicit content (legacy)",
		"hint.datePrecision": "Allowed precision: YYYY, YYYY-MM, or YYYY-MM-DD.",
		"hint.artistIdentity":
			"Identifiers and sort names come from linked artist cards.",
		"error.save": "Could not save metadata.",
		"error.required": "{field} is required.",
		"error.unknownField": "Unknown metadata field: {key}",
		"error.scope": "{key} belongs to {actual}, not {expected}",
		"error.list": "{key} must be a list of strings",
		"error.date": "{key} must be YYYY, YYYY-MM, or YYYY-MM-DD",
		"error.integer": "{key} must be a non-negative integer",
		"error.number": "{key} must be a number",
		"error.boolean": "{key} must be true or false",
		"error.advisory": "{key} must be an explicit/clean advisory value",
		"error.uuid": "{key} must be a UUID",
		"error.projectionUnknown": "Unknown metadata field is not published.",
		"error.projectionScope": "Field belongs to scope {actual}, not {expected}.",
		"error.projectionList": "Field value must be an ordered list.",
		"error.projectionIdentity":
			"Invalid identifier or advisory value is not published.",
		"error.projectionDate":
			"Navidrome accepts partial dates YYYY, YYYY-MM, or YYYY-MM-DD.",
		"error.projectionCredit":
			"Ordered credit has a gap; values were not shifted.",
		"genre.label": "Genres",
		"genre.more": "Show more",
		"genre.choose": "Choose genres…",
		"genre.chooseDialog": "Choose",
		"genre.selected": "Selected genres",
		"genre.search": "Search genres",
		"genre.searchPlaceholder": "Search genres…",
		"genre.notLoaded":
			"The genre catalog is not loaded. The saved selection will remain unchanged.",
		"genre.empty": "The genre catalog is empty.",
		"genre.noMatches": "No matches found.",
		"genre.loadError": "Could not load the genre catalog.",
		"genre.available": "Available genres",
		"state.loading": "Loading…",
		"scope.release": "release",
		"scope.recording": "recording",
		"scope.track": "track",
		"scope.audio": "audio",
		"extra.disambiguation": "Disambiguation",
		"extra.tags": "Tags",
		"extra.titleOverride": "Position title override",
	},
	ru: {
		"action.edit": "Редактировать",
		"action.save": "Сохранить",
		"action.saving": "Сохранение…",
		"action.cancel": "Отменить",
		"action.remove": "Убрать",
		"action.addArtist": "Добавить исполнителя",
		"action.addContributor": "Добавить участника",
		"action.done": "Готово",
		"action.retry": "Повторить загрузку",
		"action.loadCatalog": "Загрузить каталог",
		"action.settings": "Перейти к настройкам каталога",
		"state.unspecified": "Не задано",
		"state.notCalculated": "Не рассчитано",
		"state.filled": "заполнено",
		"state.empty": "Не заполнено",
		"state.noCredits": "Не указаны",
		"state.noArtist": "Исполнитель не указан",
		"group.release": "Тип и параметры издания",
		"group.recording": "Даты и музыкальные сведения",
		"group.track": "Дополнительно о позиции",
		"group.audio": "Аудио",
		"group.recordingArtists": "Исполнители записи",
		"group.releaseArtists": "Исполнители издания",
		"group.roles": "Участники и роли",
		"group.identifiers": "Идентификаторы",
		"group.works": "Произведения",
		"group.movement": "Часть произведения",
		"group.worksMovement": "Произведения и части",
		"group.audioMeasurements": "Измерения аудио",
		"group.lyrics": "Текст песни",
		"group.extraTags": "Дополнительные теги",
		"group.extraDetails": "Дополнительные сведения",
		"label.artistComposition": "Состав исполнителей и порядок",
		"label.linkedArtist": "Связанный исполнитель",
		"label.unlinked": "Не связан",
		"label.creditName": "Имя в зачёте",
		"label.artistMusicBrainz": "MusicBrainz ID исполнителя",
		"label.joinPhrase": "Фраза соединения",
		"label.artistSort": "Сортировка исполнителя",
		"label.contributorName": "Имя участника",
		"label.contributorMusicBrainz": "MusicBrainz ID участника",
		"label.instrument": "Инструмент",
		"label.addRole": "Добавить роль",
		"label.selectRole": "Выберите роль…",
		"label.foreignFields":
			"Разрешение метаданных ожидает подтверждения; значения других областей не редактируются.",
		"action.moveUp": "Выше",
		"action.moveDown": "Ниже",
		"value.yes": "Да",
		"value.no": "Нет",
		"value.unrated": "Не указано",
		"value.clean": "Чистая версия",
		"value.explicit": "Ненормативное содержание",
		"value.explicitLegacy": "Ненормативное содержание (legacy)",
		"hint.datePrecision": "Допустимая точность: YYYY, YYYY-MM или YYYY-MM-DD.",
		"hint.artistIdentity":
			"Идентификаторы и имена сортировки берутся из связанных карточек артистов.",
		"error.save": "Не удалось сохранить метаданные.",
		"error.required": "Поле «{field}» обязательно для заполнения.",
		"error.unknownField": "Неизвестное поле метаданных: {key}",
		"error.scope": "{key} относится к области {actual}, а не {expected}",
		"error.list": "Поле «{key}» должно быть упорядоченным списком строк",
		"error.date":
			"Некорректное значение поля «{key}»: ожидается формат YYYY, YYYY-MM или YYYY-MM-DD",
		"error.integer": "Поле «{key}» должно быть неотрицательным целым числом",
		"error.number": "Поле «{key}» должно быть числом",
		"error.boolean": "Поле «{key}» должно содержать true или false",
		"error.advisory":
			"Поле «{key}» должно содержать явное значение advisory/clean",
		"error.uuid": "Поле «{key}» должно содержать UUID",
		"error.projectionUnknown": "Неизвестное поле метаданных не опубликовано.",
		"error.projectionScope":
			"Поле относится к области {actual}, а не {expected}.",
		"error.projectionList": "Значение поля должно быть упорядоченным списком.",
		"error.projectionIdentity":
			"Некорректное значение идентификатора или advisory не опубликовано.",
		"error.projectionDate":
			"Navidrome принимает частичные даты YYYY, YYYY-MM или YYYY-MM-DD.",
		"error.projectionCredit":
			"В упорядоченном credit есть пропуск; значения не сдвинуты.",
		"genre.label": "Жанры",
		"genre.more": "Показать ещё",
		"genre.choose": "Выбрать жанры…",
		"genre.chooseDialog": "выбор",
		"genre.selected": "Выбранные жанры",
		"genre.search": "Поиск жанров",
		"genre.searchPlaceholder": "Поиск жанров…",
		"genre.notLoaded":
			"Каталог жанров не загружен. Сохранённый выбор останется без изменений.",
		"genre.empty": "Каталог жанров пуст.",
		"genre.noMatches": "Совпадений не найдено.",
		"genre.loadError": "Не удалось загрузить каталог жанров.",
		"genre.available": "Доступные жанры",
		"state.loading": "Загрузка…",
		"scope.release": "издания",
		"scope.recording": "записи",
		"scope.track": "позиции",
		"scope.audio": "аудио",
		"extra.disambiguation": "Уточнение",
		"extra.tags": "Теги",
		"extra.titleOverride": "Название позиции (переопределение)",
	},
});

export const metadataMessages = createNamespace({
	en: { ...ui.en, ...fieldMessages(fieldLabels.en) },
	ru: { ...ui.ru, ...fieldMessages(fieldLabels.ru) },
});

export type MetadataMessageKey =
	| keyof typeof metadataMessages.en
	| MetadataFieldMessageKey;

/** Narrows a runtime string to a catalog key without widening the catalog type. */
export function metadataMessageKeyFor<
	Keys extends Record<string, MetadataMessageKey>,
>(keys: Keys, candidate: string): Keys[keyof Keys] | undefined {
	return Object.hasOwn(keys, candidate)
		? keys[candidate as keyof Keys]
		: undefined;
}

export function metadataFieldMessageKey(
	key: string,
): MetadataFieldMessageKey | undefined {
	return metadataMessageKeyFor(fieldMessageKeys, key);
}

export function metadataMessage(
	locale: Locale,
	key: MetadataMessageKey,
	values?: Record<string, string>,
): string {
	const template = (metadataMessages[locale] as Record<string, string>)[key];
	if (template === undefined) return key;
	return template.replace(
		/\{(\w+)\}/g,
		(_, name: string) => values?.[name] ?? `{${name}}`,
	);
}

const extraFieldMessageKeys = {
	disambiguation: "extra.disambiguation",
	tags: "extra.tags",
	titleOverride: "extra.titleOverride",
} as const satisfies Record<string, MetadataMessageKey>;

export function metadataExtraFieldLabel(
	key: string,
	fallback: string,
	locale: Locale,
): string {
	const messageKey = metadataMessageKeyFor(extraFieldMessageKeys, key);
	return messageKey ? metadataMessage(locale, messageKey) : fallback;
}
