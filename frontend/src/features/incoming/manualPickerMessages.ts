import type { Locale } from "../../i18n/locale";

export const manualPickerMessages = {
  en: {
    invalidResults: "MusicBrainz returned invalid search results.",
    readResultsFailed: "Could not read search results.",
    searchParametersFailed: "Could not read search parameters.",
    searchNotAccepted: "Search was not accepted (HTTP {status}).",
    searchFailed: "Could not run the search.",
    releaseNotPrepared: "Could not prepare the release.",
    prepareReleaseNotAccepted: "Could not prepare the release (HTTP {status}).",
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
    selectReleaseTitle: "Select a release by MBID or URL",
    selectReleaseDescription:
      "Only the selected MusicBrainz release will be requested. No artist search will run.",
    releaseMBIDOrURL: "Release MBID or official URL",
    prepareRelease: "Prepare release",
    preparingRelease: "Preparing release…",
    releasePrepared: "Release prepared.",
    refreshOperation: "Refresh operation status",
    retryReading: "Retry reading results",
  },
  ru: {
    invalidResults: "MusicBrainz вернул некорректные результаты поиска.",
    readResultsFailed: "Не удалось прочитать результаты поиска.",
    searchParametersFailed: "Не удалось прочитать параметры поиска.",
    searchNotAccepted: "Поиск не принят (HTTP {status}).",
    searchFailed: "Не удалось выполнить поиск.",
    releaseNotPrepared: "Не удалось подготовить издание.",
    prepareReleaseNotAccepted:
      "Не удалось подготовить издание (HTTP {status}).",
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
    selectReleaseTitle: "Выбрать издание по MBID или ссылке",
    selectReleaseDescription:
      "Будет выполнен только запрос выбранного MusicBrainz release. Поиск по артисту не запускается.",
    releaseMBIDOrURL: "MBID или официальная ссылка на издание",
    prepareRelease: "Подготовить издание",
    preparingRelease: "Подготовка издания…",
    releasePrepared: "Издание подготовлено.",
    refreshOperation: "Обновить состояние операции",
    retryReading: "Повторить чтение",
  },
} as const;

export type ManualPickerMessageKey = keyof (typeof manualPickerMessages)["en"];

export function manualPickerMessage(
  locale: Locale,
  key: ManualPickerMessageKey,
  values: Record<string, string | number> = {},
) {
  return manualPickerMessages[locale][key].replace(
    /\{([^}]+)\}/g,
    (_, name: string) => String(values[name] ?? `{${name}}`),
  );
}
