import { createNamespace } from "./i18n/createNamespace";
import type { Locale } from "./i18n/types";

export const catalogMessages = createNamespace({
	en: {
		library: "Library",
		localCatalog: "Local catalogue",
		artistNotSpecified: "Artist not specified",
		localReleaseSelected:
			"Local release “{title}” selected for incoming matching.",
		backToLibrary: "Back to library",
		libraryView: "Library view",
		artists: "Artists",
		allAlbums: "All albums",
		allTracks: "All tracks",
		searchLibrary: "Search the library",
		searchPlaceholder: "Artist, album, track…",
		chooseEditionFor: "Choose an edition for {title}",
		chooseEdition: "Choose an edition",
		previewDoesNotAdd: "Previewing does not add the album to the library.",
		libraryNotFound: "Release not found in the library",
		redirectingArtist: "Opening artist…",
		artistMissing: "Release has no artist",
		trackNotFound: "Track not found",
		previewTitle: "Publication preview",
		noSavedPreview: "There is no saved metadata preview.",
		goToLibrary: "Go to library",
		backToTrack: "Back to track",
		screenNotMoved: "This screen has not been migrated yet",
		routeNotMoved:
			"Route: {route}. This flow has not yet been migrated to the React prototype.",
		goToReactLibrary: "Go to the React library",
		missingAlbum: "album is missing from the library",
		documentTitleSuffix: "MeloTrove · React prototype",
	},
	ru: {
		library: "Медиатека",
		localCatalog: "Локальный каталог",
		artistNotSpecified: "Артист не указан",
		localReleaseSelected:
			"Для сопоставления входящих выбрано издание Local «{title}».",
		backToLibrary: "К медиатеке",
		libraryView: "Вид медиатеки",
		artists: "Артисты",
		allAlbums: "Все альбомы",
		allTracks: "Все треки",
		searchLibrary: "Поиск в медиатеке",
		searchPlaceholder: "Артист, альбом, трек…",
		chooseEditionFor: "Выберите издание для «{title}»",
		chooseEdition: "Выберите издание",
		previewDoesNotAdd: "Просмотр не добавляет альбом в медиатеку.",
		libraryNotFound: "Издание не найдено в медиатеке",
		redirectingArtist: "Переход к артисту…",
		artistMissing: "Для издания не указан артист",
		trackNotFound: "Позиция не найдена",
		previewTitle: "Предпросмотр публикации",
		noSavedPreview: "Нет сохранённого предпросмотра метаданных.",
		goToLibrary: "К медиатеке",
		backToTrack: "Вернуться к треку",
		screenNotMoved: "Экран ещё не перенесён",
		routeNotMoved:
			"Маршрут: {route}. Этот сценарий пока не перенесён в React-макет.",
		goToReactLibrary: "К медиатеке React",
		missingAlbum: "альбом отсутствует в медиатеке",
		documentTitleSuffix: "MeloTrove · React-макет",
	},
});

export type CatalogMessage = keyof typeof catalogMessages.en;

export function catalogMessage(
	locale: Locale,
	key: CatalogMessage,
	values: Record<string, string | number> = {},
) {
	return catalogMessages[locale][key].replace(
		/\{([^}]+)\}/g,
		(_, name: string) => String(values[name] ?? `{${name}}`),
	);
}
