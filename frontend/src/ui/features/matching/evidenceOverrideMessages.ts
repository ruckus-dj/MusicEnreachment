import type { MatchingWorkspaceLocale } from "./types";

export const evidenceOverrideMessages = {
  en: {
    missingValue: "not provided",
    evidenceLabel: "Current evaluation evidence",
    evidenceHeading: "Selected track match",
    evidenceExplanation:
      "One saved comparison for the selected file and track. Saved scores are shown as short percentages; nothing is recalculated here.",
    trackPosition: (disc: number, track: string | number, title?: string) =>
      `Disc ${disc} · track ${track}${title ? ` · ${title}` : ""}`,
    matchConfidence: "Match",
    scoreSummary: (used: number, total: number) =>
      `Based on ${used} of ${total} saved scoring factors.`,
    missingCandidate: "No candidate exists for this file and release.",
    noFactors:
      "Factors, weights, and contributions were not saved for this candidate.",
    noPositionEvidence: "No saved comparison exists for this track.",
    rawTags: "Raw file tags",
    originalTags: "Original values",
    readableTags: "Readable values",
    diagnostics: "Diagnostics for this track",
    releaseMatch: "Release match",
    assignmentMatch: "Track match",
    noAppliedFactors: "No scored factors were saved.",
    factorsNotApplied: (count: number) =>
      count === 1
        ? "1 factor was not scored."
        : `${count} factors were not scored.`,
    factorWeight: (weight: number) => `weight ${weight}`,
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
    evidenceLabel: "Доказательства текущей оценки",
    evidenceHeading: "Совпадение выбранной позиции",
    evidenceExplanation:
      "Одно сохранённое сравнение выбранного файла и трека. Сохранённые оценки показаны короткими процентами; здесь ничего не пересчитывается.",
    trackPosition: (disc: number, track: string | number, title?: string) =>
      `Диск ${disc} · трек ${track}${title ? ` · ${title}` : ""}`,
    matchConfidence: "Совпадение",
    scoreSummary: (used: number, total: number) =>
      `На основе ${used} из ${total} сохранённых факторов оценки.`,
    missingCandidate: "Кандидат для этого файла и издания отсутствует.",
    noFactors: "Факторы, веса и вклад не сохранены для этого кандидата.",
    noPositionEvidence: "Сохранённого сравнения для этого трека нет.",
    rawTags: "Исходные теги файла",
    originalTags: "Исходные значения",
    readableTags: "Читаемые значения",
    diagnostics: "Диагностика по этому треку",
    releaseMatch: "Совпадение издания",
    assignmentMatch: "Совпадение трека",
    noAppliedFactors: "Оценённые факторы не сохранены.",
    factorsNotApplied: (count: number) => {
      const remainder10 = count % 10;
      const remainder100 = count % 100;
      const noun =
        remainder10 === 1 && remainder100 !== 11
          ? "фактор"
          : remainder10 >= 2 &&
              remainder10 <= 4 &&
              (remainder100 < 12 || remainder100 > 14)
            ? "фактора"
            : "факторов";
      return `${count} ${noun} не участвовали в оценке.`;
    },
    factorWeight: (weight: number) => `вес ${weight}`,
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
  (typeof evidenceOverrideMessages)[MatchingWorkspaceLocale];
