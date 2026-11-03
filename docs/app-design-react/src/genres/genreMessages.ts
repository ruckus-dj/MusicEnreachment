import { createNamespace } from "../i18n/createNamespace";

export const genreMessages = createNamespace({
	en: {
		heading: "MusicBrainz genre catalogue",
		offline: "Available offline",
		loaded: "Marked as loaded",
		notLoaded: "Not marked as loaded",
		description:
			"Search the bundled catalogue snapshot by display name, source name, or MusicBrainz ID. The snapshot is searchable offline; this prototype does not contact a server.",
		search: "Search genres",
		searchPlaceholder: "Name or MusicBrainz ID",
		update: "Update catalogue",
		results: "Results",
		resultCount: "Results",
		sourceName: "Source name",
		noResults: "No genres match this search.",
	},
	ru: {
		heading: "Каталог жанров MusicBrainz",
		offline: "Доступен офлайн",
		loaded: "Отмечен как загруженный",
		notLoaded: "Не отмечен как загруженный",
		description:
			"Ищите в сохранённом снимке каталога по отображаемому или исходному названию либо MusicBrainz ID. Поиск работает офлайн; макет не обращается к серверу.",
		search: "Поиск жанров",
		searchPlaceholder: "Название или MusicBrainz ID",
		update: "Обновить каталог",
		results: "Результаты",
		resultCount: "Найдено жанров",
		sourceName: "Исходное название",
		noResults: "По этому запросу жанры не найдены.",
	},
});
