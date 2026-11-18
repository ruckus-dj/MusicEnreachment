/** Copyable contract shared byte-for-byte by production and the design prototype. */
export type ManualSearchViewLocale = "en" | "ru";

export type ManualSearchViewItem = {
  id: string;
  title: string;
  artistCredit?: string;
  disambiguation?: string;
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

export type ManualSearchViewProps = {
  locale: ManualSearchViewLocale;
  query: string;
  limit: string;
  offset: number;
  disabled?: boolean;
  pending?: boolean;
  results?: ManualSearchViewResults;
  errors?: ManualSearchViewError[];
  selectedItemId?: string | null;
  onQueryChange: (query: string) => void;
  onLimitChange: (limit: string) => void;
  onSearch: () => void;
  onSelectRelease: (releaseId: string) => void;
  onPrevious: () => void;
  onNext: () => void;
};

export const manualSearchViewMessages = {
  en: {
    invalidResults: "MusicBrainz returned invalid search results.",
    readResultsFailed: "Could not read search results.",
    searchParametersFailed: "Could not read search parameters.",
    searchNotAccepted: "Search was not accepted (HTTP {status}).",
    searchFailed: "Could not run the search.",
    searchTitle: "Search MusicBrainz",
    searchDescription:
      "Enter an artist and release title together in one query. Search runs only after you submit a request; results do not start preparation automatically.",
    searchQuery: "Artist and release title",
    resultLimit: "Number of results",
    search: "Search",
    searching: "Searching MusicBrainz…",
    noMatches: "No matches found.",
    score: "MusicBrainz score: {score}",
    releaseDate: "Released: {date}",
    releaseCountry: "Country: {country}",
    releaseTracks: "Tracks: {count}",
    releaseFormats: "Format: {formats}",
    discCount: "{count} discs",
    releaseLabels: "Labels: {labels}",
    selectRelease: "Choose",
    selectReleaseDescribed: "Choose “{title}” ({details})",
    selectReleaseTitled: "Choose “{title}”",
    resultRange: "Results {start}–{end} of {count}",
    previous: "Previous",
    next: "Next",
    retryReading: "Retry reading results",
  },
  ru: {
    invalidResults: "MusicBrainz вернул некорректные результаты поиска.",
    readResultsFailed: "Не удалось прочитать результаты поиска.",
    searchParametersFailed: "Не удалось прочитать параметры поиска.",
    searchNotAccepted: "Поиск не принят (HTTP {status}).",
    searchFailed: "Не удалось выполнить поиск.",
    searchTitle: "Поиск MusicBrainz",
    searchDescription:
      "Введите артиста и название издания одним поисковым запросом. Поиск выполняется только после отправки; результаты не запускают подготовку автоматически.",
    searchQuery: "Артист и название издания",
    resultLimit: "Количество результатов",
    search: "Искать",
    searching: "Поиск MusicBrainz…",
    noMatches: "Совпадений нет.",
    score: "Оценка MusicBrainz: {score}",
    releaseDate: "Дата релиза: {date}",
    releaseCountry: "Страна: {country}",
    releaseTracks: "Треков: {count}",
    releaseFormats: "Формат: {formats}",
    discCount: "носителей: {count}",
    releaseLabels: "Лейблы: {labels}",
    selectRelease: "Выбрать",
    selectReleaseDescribed: "Выбрать «{title}» ({details})",
    selectReleaseTitled: "Выбрать «{title}»",
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
