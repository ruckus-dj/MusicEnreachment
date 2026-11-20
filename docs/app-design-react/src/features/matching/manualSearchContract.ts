/** Search presentation contract. Provider loading and preparation stay in the controller. */
export type ManualSearchViewLocale = "en" | "ru";

export type ManualSearchProvider = "Local" | "MusicBrainz";

/** Unified edition search across providers, or the MusicBrainz-only consumer. */
export type ManualSearchVariant = "editions" | "musicbrainz";

export type ManualSearchViewItem = {
	id: string;
	title: string;
	provider: ManualSearchProvider;
	artistCredit?: string;
	disambiguation?: string;
	releaseGroupTitle?: string;
	date?: string;
	country?: string;
	trackCount?: number;
	formats: string[];
	mediaCount: number;
	labels: { name?: string; catalogNumber?: string }[];
	score?: number;
};

export type ManualSearchViewResults = {
	count: number;
	items: ManualSearchViewItem[];
};

export type ManualSearchViewError = {
	id: string;
	message: string;
	onRetry?: () => void;
};

/** Selection is keyed by provider: Local and MusicBrainz identities may collide. */
export type ManualSearchSelection = {
	provider: ManualSearchProvider;
	id: string;
};

export type ManualSearchViewProps = {
	locale: ManualSearchViewLocale;
	variant?: ManualSearchVariant;
	query: string;
	limit: string;
	offset: number;
	disabled?: boolean;
	pending?: boolean;
	results?: ManualSearchViewResults;
	errors?: ManualSearchViewError[];
	selectedItem?: ManualSearchSelection | null;
	onQueryChange: (query: string) => void;
	onLimitChange: (limit: string) => void;
	onSearch: () => void;
	onSelectItem: (selection: ManualSearchSelection) => void;
	onPrevious: () => void;
	onNext: () => void;
};

export const manualSearchViewMessages = {
	en: {
		invalidResults: "MusicBrainz returned invalid search results.",
		invalidLocalResults: "Local returned invalid search results.",
		readResultsFailed: "Could not read search results.",
		localResultsFailed: "Could not read Local results.",
		searchParametersFailed: "Could not read search parameters.",
		searchNotAccepted: "Search was not accepted (HTTP {status}).",
		localNotAccepted: "Local search was not accepted (HTTP {status}).",
		searchFailed: "Could not run the search.",
		searchTitle: "Search MusicBrainz",
		searchTitleEditions: "Search editions",
		searchDescription:
			"Enter an artist and release title together in one query. Search runs only after you submit a request; results do not start preparation automatically.",
		searchDescriptionEditions:
			"Enter an artist, a release title, or both to search Local and MusicBrainz.",
		searchQuery: "Artist and release title",
		resultLimit: "Number of results",
		search: "Search",
		searching: "Searching MusicBrainz…",
		searchingEditions: "Searching editions…",
		noMatches: "No matches found.",
		score: "MusicBrainz score: {score}",
		releaseArtist: "Artist: {artist}",
		releaseDate: "Released: {date}",
		releaseCountry: "Country: {country}",
		releaseGroup: "Release group: {title}",
		releaseTracks: "Tracks: {count}",
		releaseFormats: "Format: {formats}",
		discCount: "{count} discs",
		releaseLabels: "Labels: {labels}",
		catalogNumbers: "Catalog no.: {numbers}",
		selectRelease: "Choose",
		selectReleaseDescribed: "Choose “{title}” ({details})",
		selectReleaseTitled: "Choose “{title}”",
		selectedRelease: "Selected",
		selectedReleaseDescribed: "Selected: “{title}” ({details})",
		selectedReleaseTitled: "Selected: “{title}”",
		prepareFailed: "Could not prepare the release.",
		resultRange: "Results {start}–{end} of {count}",
		previous: "Previous",
		next: "Next",
		retryReading: "Retry reading results",
	},
	ru: {
		invalidResults: "MusicBrainz вернул некорректные результаты поиска.",
		invalidLocalResults: "Local вернул некорректные результаты поиска.",
		readResultsFailed: "Не удалось прочитать результаты поиска.",
		localResultsFailed: "Не удалось прочитать результаты Local.",
		searchParametersFailed: "Не удалось прочитать параметры поиска.",
		searchNotAccepted: "Поиск не принят (HTTP {status}).",
		localNotAccepted: "Поиск Local не принят (HTTP {status}).",
		searchFailed: "Не удалось выполнить поиск.",
		searchTitle: "Поиск MusicBrainz",
		searchTitleEditions: "Поиск изданий",
		searchDescription:
			"Введите артиста и название издания одним поисковым запросом. Поиск выполняется только после отправки; результаты не запускают подготовку автоматически.",
		searchDescriptionEditions:
			"Введите артиста, название издания или оба поля в одной строке. Поиск идёт по каталогам Local и MusicBrainz.",
		searchQuery: "Артист и название издания",
		resultLimit: "Количество результатов",
		search: "Искать",
		searching: "Поиск MusicBrainz…",
		searchingEditions: "Ищем издания…",
		noMatches: "Совпадений нет.",
		score: "Оценка MusicBrainz: {score}",
		releaseArtist: "Исполнитель: {artist}",
		releaseDate: "Дата релиза: {date}",
		releaseCountry: "Страна: {country}",
		releaseGroup: "Группа релиза: {title}",
		releaseTracks: "Треков: {count}",
		releaseFormats: "Формат: {formats}",
		discCount: "носителей: {count}",
		releaseLabels: "Лейблы: {labels}",
		catalogNumbers: "Кат. №: {numbers}",
		selectRelease: "Выбрать",
		selectReleaseDescribed: "Выбрать «{title}» ({details})",
		selectReleaseTitled: "Выбрать «{title}»",
		selectedRelease: "Выбрано",
		selectedReleaseDescribed: "Выбрано: «{title}» ({details})",
		selectedReleaseTitled: "Выбрано: «{title}»",
		prepareFailed: "Не удалось подготовить издание.",
		resultRange: "Результаты {start}–{end} из {count}",
		previous: "Предыдущая",
		next: "Следующая",
		retryReading: "Повторить чтение",
	},
} as const;

export type ManualSearchMessageKey =
	keyof (typeof manualSearchViewMessages)["en"];

export function manualSearchMessage(
	locale: ManualSearchViewLocale,
	key: ManualSearchMessageKey,
	values: Record<string, string | number> = {},
) {
	return manualSearchViewMessages[locale][key].replace(
		/\{([^}]+)\}/g,
		(_, name: string) => String(values[name] ?? `{${name}}`),
	);
}
