import { createNamespace } from "./i18n/createNamespace";

export const matchingSearchPickerMessages = createNamespace({
	en: {
		searchType: "Search type",
		searchMethod: "Search method",
		releases: "Releases",
		artists: "Artists",
		byName: "By name",
		exactIdUrl: "Exact ID / URL",
		identityLabel: "ID or MusicBrainz MBID / URL",
		artistNameLabel: "Artist name",
		releaseSearchLabel: "Artist, release, catalog, or ID",
		identityAriaLabel: "ID or MusicBrainz URL",
		artistSearchAriaLabel: "Search artists",
		releaseSearchAriaLabel: "Search releases",
		identityPlaceholder: "MBID, official URL, or ID",
		artistPlaceholder: "Artist name…",
		releasePlaceholder: "Artist, album, catalog…",
		search: "Search",
		availableRecords:
			"Search available catalogue records. Page size: {pageSize}.",
		releaseTrackCount: "Tracks: {count}",
		releasesForArtist: "Releases by {artist}:",
		artistResults: "Back to artist results",
		searching: "Searching…",
		enterIdentity: "Enter an identifier or official URL.",
		identityNotFound:
			"No exact identity match was found. A similar name is not used in place of an MBID.",
		enterQuery:
			"Enter a query and select Search; results are not filtered until you submit.",
		noArtistReleases: "No available releases for this artist.",
		noMatches: "No matches in the available records.",
		artistDisambiguation: "No disambiguation",
		artistId: "Artist ID: {id}",
		showArtistReleases: "Select this artist to show their releases",
		previous: "Previous",
		page: "Page {current} of {total}",
		next: "Next",
		searchError:
			"Could not complete the search. Change the query and try again.",
		identityNoMatch: "No record with this MBID or URL is available.",
		identityNoData:
			"No MusicBrainz MBID/URL is available for an exact check; no record was matched.",
		identityNameNoMatch:
			"No exact identity match was found in the available records. Name search was not used.",
	},
	ru: {
		searchType: "Тип поиска",
		searchMethod: "Способ поиска",
		releases: "Издания",
		artists: "Артисты",
		byName: "По названию",
		exactIdUrl: "Точный ID / URL",
		identityLabel: "Идентификатор или MusicBrainz MBID / URL",
		artistNameLabel: "Имя артиста",
		releaseSearchLabel: "Артист, издание, каталог или ID",
		identityAriaLabel: "Идентификатор или MusicBrainz URL",
		artistSearchAriaLabel: "Поиск артистов",
		releaseSearchAriaLabel: "Поиск изданий",
		identityPlaceholder: "MBID, официальный URL или ID",
		artistPlaceholder: "Имя артиста…",
		releasePlaceholder: "Артист, альбом, каталог…",
		search: "Найти",
		availableRecords:
			"Поиск по доступным записям каталога. На странице: {pageSize}.",
		releaseTrackCount: "Позиций: {count}",
		releasesForArtist: "Издания артиста {artist}:",
		artistResults: "К результатам артистов",
		searching: "Поиск…",
		enterIdentity: "Введите идентификатор или официальный URL.",
		identityNotFound:
			"Точное совпадение по идентификатору не найдено. Похожее имя не используется вместо MBID.",
		enterQuery:
			"Введите запрос и нажмите «Найти»; результаты не фильтруются до отправки.",
		noArtistReleases: "Нет доступных изданий этого артиста.",
		noMatches: "Совпадений среди доступных записей нет.",
		artistDisambiguation: "Без уточнения",
		artistId: "ID артиста: {id}",
		showArtistReleases: "Выбрать артиста и показать его издания",
		previous: "Назад",
		page: "Страница {current} из {total}",
		next: "Дальше",
		searchError:
			"Не удалось выполнить поиск. Измените запрос и попробуйте снова.",
		identityNoMatch:
			"Среди доступных записей нет сущности с этим MBID или URL.",
		identityNoData:
			"Среди доступных записей нет MusicBrainz MBID/URL для точной проверки; запись не сопоставлена.",
		identityNameNoMatch:
			"Точное совпадение по идентификатору среди доступных записей не найдено. Поиск по имени не выполнялся.",
	},
});
