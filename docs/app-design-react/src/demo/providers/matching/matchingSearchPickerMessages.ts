import { createNamespace } from "../../../ui/i18n/createNamespace";

export const matchingSearchPickerMessages = createNamespace({
	en: {
		search: "Search",
		releaseSearchLabel: "Artist and release title",
		releaseSearchAriaLabel: "Search releases by artist and title",
		releasePlaceholder: "Artist and release title…",
		availableRecords:
			"Search available catalogue records. Page size: {pageSize}.",
		releaseDate: "Released: {date}",
		releaseCountry: "Country: {country}",
		releaseTracks: "Tracks: {count}",
		releaseFormats: "Format: {formats}",
		discCount: "{count} discs",
		releaseLabels: "Labels: {labels}",
		searching: "Searching…",
		enterQuery:
			"Enter an artist and release title, then select Search; results are not filtered until you submit.",
		noMatches: "No matches in the available records.",
		previous: "Previous",
		page: "Page {current} of {total}",
		next: "Next",
		searchError:
			"Could not complete the search. Change the query and try again.",
	},
	ru: {
		search: "Найти",
		releaseSearchLabel: "Артист и название издания",
		releaseSearchAriaLabel: "Поиск изданий по артисту и названию",
		releasePlaceholder: "Артист и название издания…",
		availableRecords:
			"Поиск по доступным записям каталога. На странице: {pageSize}.",
		releaseDate: "Дата релиза: {date}",
		releaseCountry: "Страна: {country}",
		releaseTracks: "Треков: {count}",
		releaseFormats: "Формат: {formats}",
		discCount: "носителей: {count}",
		releaseLabels: "Лейблы: {labels}",
		searching: "Поиск…",
		enterQuery:
			"Введите артиста и название издания, затем нажмите «Найти»; результаты не фильтруются до отправки.",
		noMatches: "Совпадений среди доступных записей нет.",
		previous: "Назад",
		page: "Страница {current} из {total}",
		next: "Дальше",
		searchError:
			"Не удалось выполнить поиск. Измените запрос и попробуйте снова.",
	},
});
