/** Copyable contract shared byte-for-byte by production and the design prototype. */
export type ManualReleasePreparationLocale = "en" | "ru";

export type ManualReleasePreparationError = {
  id: string;
  message: string;
  onRetry?: () => void;
};

export type ManualReleasePreparationViewProps = {
  locale: ManualReleasePreparationLocale;
  value: string;
  disabled?: boolean;
  pending?: boolean;
  prepared?: boolean;
  /**
   * Extra provider-side reason to keep the action disabled, such as a failed
   * operation that only an edited input may replace. The input stays editable.
   */
  prepareDisabled?: boolean;
  errors?: ManualReleasePreparationError[];
  onValueChange: (value: string) => void;
  onPrepare: () => void;
};

export const manualReleasePreparationMessages = {
  en: {
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
    releaseNotPrepared: "Не удалось подготовить издание.",
    prepareReleaseNotAccepted:
      "Не удалось подготовить издание (HTTP {status}).",
    selectReleaseTitle: "Выбрать издание по MBID или ссылке",
    selectReleaseDescription:
      "Будет выполнен только запрос выбранного издания MusicBrainz. Поиск по артисту не запускается.",
    releaseMBIDOrURL: "MBID или официальная ссылка на издание",
    prepareRelease: "Подготовить издание",
    preparingRelease: "Подготовка издания…",
    releasePrepared: "Издание подготовлено.",
    refreshOperation: "Обновить состояние операции",
  },
} as const;

export type ManualReleasePreparationMessageKey =
  keyof (typeof manualReleasePreparationMessages)["en"];

export function manualReleasePreparationMessage(
  locale: ManualReleasePreparationLocale,
  key: ManualReleasePreparationMessageKey,
  values: Record<string, string | number> = {},
) {
  return manualReleasePreparationMessages[locale][key].replace(
    /\{([^}]+)\}/g,
    (_, name: string) => String(values[name] ?? `{${name}}`),
  );
}
