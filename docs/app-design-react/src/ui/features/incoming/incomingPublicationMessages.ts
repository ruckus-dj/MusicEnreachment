import type { Locale } from "../../i18n/types";
import type {
  IncomingPublicationNextStepKind,
  IncomingPublicationReason,
  IncomingPublicationTrackAttention,
} from "./incomingPublicationModel";

function russianCountForm(count: number, forms: [string, string, string]) {
  const lastTwo = count % 100;
  if (lastTwo >= 11 && lastTwo <= 14) return forms[2];
  const last = count % 10;
  if (last === 1) return forms[0];
  if (last >= 2 && last <= 4) return forms[1];
  return forms[2];
}

function trackCount(locale: Locale, count: number) {
  return locale === "en"
    ? `${count} ${count === 1 ? "track" : "tracks"}`
    : `${count} ${russianCountForm(count, ["трек", "трека", "треков"])}`;
}

const copy = {
  en: {
    sectionTitle: "Publication queue",
    sectionDescription:
      "Sample publication diagnoses. No audio files are published in this demo.",
    demo: "Demo",
    sectionTabList: "Publication queue status",
    attention: "Needs attention",
    ready: "Ready to publish",
    attentionCaption: "Editions blocked from publication",
    readyCaption: "Editions ready for manual publication",
    edition: "Edition",
    tracks: "Tracks",
    reason: "Reason",
    nextStep: "Next step",
    number: "No.",
    title: "Track",
    source: "Source",
    state: "State",
    emptyAttention: "No editions need attention.",
    emptyReady: "No editions are ready to publish.",
    noSource: "Not selected",
    missingArtist: "Primary artist not specified",
    expand: (title: string) => `Tracks in ${title}`,
    reasonLabel: (reason: IncomingPublicationReason) => {
      switch (reason.code) {
        case "missingPrimaryArtist":
          return "Release primary artist is missing";
        case "unsafeOutputFilename":
          return `Unsafe output filename: ${reason.filename}`;
        case "sourceNeedsSelection":
          return `Source needs selection: ${reason.trackTitle}`;
        case "automaticSkip":
          return "Auto-publish not started · manual publication available";
        case "manualReady":
          return "Primary artist selected · ready for manual publication";
      }
    },
    nextStepLabel: (kind: IncomingPublicationNextStepKind) => {
      switch (kind) {
        case "release":
          return "Open release";
        case "track":
          return "Open track";
        case "matching":
          return "Choose source";
        case "primaryArtist":
          return "Choose primary artist";
      }
    },
    trackAttentionLabel: (
      attention: IncomingPublicationTrackAttention | undefined,
      editionReason: IncomingPublicationReason,
    ) => {
      if (attention === "unsafeOutputFilename") return "Unsafe filename";
      if (attention === "sourceNeedsSelection") return "Source needed";
      return editionReason.code === "missingPrimaryArtist"
        ? "Source ready"
        : "Ready";
    },
    attentionTab: (count: number) => `Needs attention: ${count}`,
    readyTab: (count: number) => `Ready to publish: ${count}`,
    trackCount: (count: number) => trackCount("en", count),
  },
  ru: {
    sectionTitle: "Очередь публикации",
    sectionDescription:
      "Примеры причин на демо-данных. Макет не публикует аудиофайлы.",
    demo: "Демо",
    sectionTabList: "Состояние очереди публикации",
    attention: "Требуют внимания",
    ready: "Готовы к публикации",
    attentionCaption: "Издания, заблокированные для публикации",
    readyCaption: "Издания, готовые к ручной публикации",
    edition: "Издание",
    tracks: "Треки",
    reason: "Причина",
    nextStep: "Следующий шаг",
    number: "№",
    title: "Трек",
    source: "Источник",
    state: "Состояние",
    emptyAttention: "Нет изданий, требующих внимания.",
    emptyReady: "Нет изданий, готовых к публикации.",
    noSource: "Не выбран",
    missingArtist: "Основной исполнитель не указан",
    expand: (title: string) => `Треки издания «${title}»`,
    reasonLabel: (reason: IncomingPublicationReason) => {
      switch (reason.code) {
        case "missingPrimaryArtist":
          return "Не указан основной исполнитель издания";
        case "unsafeOutputFilename":
          return `Небезопасное имя файла: ${reason.filename}`;
        case "sourceNeedsSelection":
          return `Нужно выбрать источник: ${reason.trackTitle}`;
        case "automaticSkip":
          return "Автопубликация не запускалась · можно опубликовать вручную";
        case "manualReady":
          return "Основной исполнитель выбран · можно опубликовать вручную";
      }
    },
    nextStepLabel: (kind: IncomingPublicationNextStepKind) => {
      switch (kind) {
        case "release":
          return "Открыть издание";
        case "track":
          return "Открыть трек";
        case "matching":
          return "Выбрать источник";
        case "primaryArtist":
          return "Выбрать артиста";
      }
    },
    trackAttentionLabel: (
      attention: IncomingPublicationTrackAttention | undefined,
      editionReason: IncomingPublicationReason,
    ) => {
      if (attention === "unsafeOutputFilename") return "Небезопасное имя";
      if (attention === "sourceNeedsSelection") return "Нужен источник";
      return editionReason.code === "missingPrimaryArtist"
        ? "Источник готов"
        : "Готов";
    },
    attentionTab: (count: number) => `Требуют внимания: ${count}`,
    readyTab: (count: number) => `Готовы к публикации: ${count}`,
    trackCount: (count: number) => trackCount("ru", count),
  },
} as const;

export const incomingPublicationMessages = copy;
