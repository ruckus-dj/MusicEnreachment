import type { Locale } from "../../i18n/locale";
import { manualSearchViewMessages } from "../matchingWorkspace/manualSearchContract";

export const manualPickerMessages = {
  en: {
    ...manualSearchViewMessages.en,
    releaseNotPrepared: "Could not prepare the release.",
    prepareReleaseNotAccepted: "Could not prepare the release (HTTP {status}).",
    selectReleaseTitle: "Select a release by MBID or URL",
    selectReleaseDescription:
      "Only the selected MusicBrainz release will be requested. No artist search will run.",
    releaseMBIDOrURL: "Release MBID or official URL",
    prepareRelease: "Prepare release",
    preparingRelease: "Preparing release…",
    releasePrepared: "Release prepared.",
    refreshOperation: "Refresh operation status",
  },
  ru: {
    ...manualSearchViewMessages.ru,
    releaseNotPrepared: "Не удалось подготовить издание.",
    prepareReleaseNotAccepted:
      "Не удалось подготовить издание (HTTP {status}).",
    selectReleaseTitle: "Выбрать издание по MBID или ссылке",
    selectReleaseDescription:
      "Будет выполнен только запрос выбранного MusicBrainz release. Поиск по артисту не запускается.",
    releaseMBIDOrURL: "MBID или официальная ссылка на издание",
    prepareRelease: "Подготовить издание",
    preparingRelease: "Подготовка издания…",
    releasePrepared: "Издание подготовлено.",
    refreshOperation: "Обновить состояние операции",
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
