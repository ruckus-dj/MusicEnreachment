import type { Locale } from "../../i18n/locale";

export const evidenceOverrideMessages = {
  en: {
    missingValue: "not provided",
    emptyValue: "empty value",
    evidenceLabel: "Current evaluation evidence",
    evidenceHeading: "Current evaluation evidence",
    evidenceExplanation:
      "Saved values and their provenance are shown. The interface does not calculate scores or their contributions.",
    releaseConfidence: "Release confidence (saved)",
    positionConfidence: (medium: number, track: string | number) =>
      `Position confidence · Disc ${medium} · track ${track}`,
    missingCandidate: "No candidate exists for this file and release.",
    rawTags: "Raw file tags",
    factorsAndProvenance: "Factors and provenance",
    factor: (index: number) => `Factor ${index}`,
    edge: (index: number) => `Edge ${index}`,
    noFactors:
      "Factors, weights, and contributions were not saved for this candidate.",
    missingExplanation:
      "Missing values are not replaced with calculations; inapplicable factors are shown only when explicitly present in the evaluation data.",
    releaseProfile: "Release profile",
    assignmentProfile: "Assignment profile",
    tagOverridesLegend: (title: string) => `Track tags · ${title}`,
    tagOverridesExplanation:
      "Provider tags are used unless overridden. Changes here are local metadata edits, not changes to file assignments.",
    overrideTitle: (title: string) => `Override title for ${title}`,
    title: "Title",
    overrideArtists: (title: string) => `Override artist list for ${title}`,
    artist: (index: number) => `Artist ${index}`,
    removeArtist: (index: number, title: string) =>
      `Remove artist ${index} for ${title}`,
    remove: "Remove",
    addArtist: (title: string) => `Add artist for ${title}`,
    emptyArtists: "An empty list is an explicit override with no artists.",
  },
  ru: {
    missingValue: "не указано",
    emptyValue: "пустое значение",
    evidenceLabel: "Доказательства текущей оценки",
    evidenceHeading: "Доказательства текущей оценки",
    evidenceExplanation:
      "Показаны сохранённые значения и сведения об их происхождении. Числовые оценки и вклад в них интерфейс не рассчитывает.",
    releaseConfidence: "Уверенность издания (сохранённая)",
    positionConfidence: (medium: number, track: string | number) =>
      `Уверенность позиции · Диск ${medium} · трек ${track}`,
    missingCandidate: "Кандидат для этого файла и издания отсутствует.",
    rawTags: "Исходные теги файла",
    factorsAndProvenance: "Факторы и происхождение",
    factor: (index: number) => `Фактор ${index}`,
    edge: (index: number) => `Связь ${index}`,
    noFactors: "Факторы, веса и вклад не сохранены для этого кандидата.",
    missingExplanation:
      "Отсутствующие значения не заменяются вычислениями; неприменимые факторы отображаются только если они явно указаны в данных оценки.",
    releaseProfile: "Профиль издания",
    assignmentProfile: "Профиль назначения",
    tagOverridesLegend: (title: string) => `Теги позиции · ${title}`,
    tagOverridesExplanation:
      "Без переопределения используются теги провайдера. Изменения здесь — локальная ручная правка метаданных, не изменение связей файлов.",
    overrideTitle: (title: string) => `Переопределить название для ${title}`,
    title: "Название",
    overrideArtists: (title: string) =>
      `Переопределить список исполнителей для ${title}`,
    artist: (index: number) => `Исполнитель ${index}`,
    removeArtist: (index: number, title: string) =>
      `Удалить исполнителя ${index} для ${title}`,
    remove: "Удалить",
    addArtist: (title: string) => `Добавить исполнителя для ${title}`,
    emptyArtists: "Пустой список — явное переопределение без исполнителей.",
  },
} as const;

export type EvidenceOverrideMessages =
  (typeof evidenceOverrideMessages)[Locale];
