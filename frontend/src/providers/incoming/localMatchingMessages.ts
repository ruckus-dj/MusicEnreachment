function russianCount(count: number, forms: [string, string, string]) {
  const lastTwoDigits = count % 100;
  if (lastTwoDigits >= 11 && lastTwoDigits <= 14) return forms[2];
  const lastDigit = count % 10;
  if (lastDigit === 1) return forms[0];
  if (lastDigit >= 2 && lastDigit <= 4) return forms[1];
  return forms[2];
}

export type LocalMatchingNotice =
  | { key: "confirmed" }
  | { key: "scopeChangedAfterConfirm" }
  | { key: "scopeChangedBeforeConfirm" }
  | { key: "confirmedRefreshError"; reason?: string };

export const localMatchingMessages = {
  en: {
    heading: "Match with a Local release",
    sectionSummary:
      "Match incoming files with a release in the Local provider catalog.",
    intro:
      "Choose an existing Local release and explicitly assign files to its tracks. Create and edit catalog records in",
    ownCatalog: "Own Catalog",
    releaseLegend: "Local release",
    loadReleases: "Load Local releases",
    loadingReleases: "Loading Local releases…",
    loadReleasesError: "Could not load Local releases.",
    releaseLabel: "Local release",
    chooseRelease: "Choose a Local release",
    loadingTracks: "Loading release tracks…",
    releaseTrackCount: (title: string | undefined, count: number) =>
      `${title || "Local release"} · ${count} ${count === 1 ? "track" : "tracks"}`,
    assignmentsLegend: "Assign files to tracks",
    trackForFile: (file: string) => `Track for file ${file}`,
    unassigned: "Unassigned",
    authorityNotReady:
      "Matching authority is not loaded yet. Refresh the screen and try again.",
    selectReleaseFirst:
      "Choose a Local release and wait for its tracks to load.",
    assignAtLeastOne:
      "Assign at least one file to a track in the selected release.",
    releaseReadError: "Could not read the Local release.",
    previewAction: "Preview assignments",
    previewing: "Previewing…",
    previewTitle: "Preview — read only",
    previewSummary: (title: string, count: number) =>
      `${title} · ${count} ${count === 1 ? "track" : "tracks"}`,
    omittedFiles: (count: number) => `Files not included: ${count}`,
    previewError: "Preview failed.",
    previewConflict:
      "The snapshot is stale. Refresh the group data and create a new preview.",
    staleNotice:
      "The snapshot changed. Create a new preview after the data is refreshed.",
    confirmAction: "Confirm matching",
    confirmRetry: "Retry the same confirmation request",
    confirmError: "Confirmation failed.",
    confirmConflict:
      "The snapshot is stale. Get a new preview before confirming.",
    confirmRetryNotice: "Retry the request to safely check the same result.",
    notice: (notice: LocalMatchingNotice) => {
      if (notice.key === "confirmedRefreshError")
        return `Matching was confirmed, but the screen could not be refreshed: ${notice.reason || "refresh error"}`;
      return {
        confirmed: "Local matching confirmed.",
        scopeChangedAfterConfirm:
          "Local matching was confirmed; group data has been refreshed.",
        scopeChangedBeforeConfirm:
          "The group or authority snapshot changed. Select a release and create a new preview.",
      }[notice.key];
    },
    receipt: (releaseTitle: string) =>
      `Matching confirmed for release ${releaseTitle}.`,
    httpError: (fallback: string, status: number) =>
      `${fallback} (HTTP ${status}).`,
  },
  ru: {
    heading: "Сопоставить с локальным релизом",
    sectionSummary:
      "Сопоставить входящие файлы с релизом каталога провайдера Local.",
    intro:
      "Выберите существующий локальный релиз и явно назначьте файлы его дорожкам. Создавать и редактировать записи каталога можно в",
    ownCatalog: "собственном каталоге",
    releaseLegend: "Локальный релиз",
    loadReleases: "Загрузить локальные релизы",
    loadingReleases: "Загрузка локальных релизов…",
    loadReleasesError: "Не удалось загрузить локальные релизы.",
    releaseLabel: "Локальный релиз",
    chooseRelease: "Выберите локальный релиз",
    loadingTracks: "Загрузка дорожек релиза…",
    releaseTrackCount: (title: string | undefined, count: number) =>
      `${title || "Локальный релиз"} · ${count} ${russianCount(count, ["дорожка", "дорожки", "дорожек"])}`,
    assignmentsLegend: "Назначение файлов дорожкам",
    trackForFile: (file: string) => `Дорожка для файла ${file}`,
    unassigned: "Не назначен",
    authorityNotReady:
      "Полномочия ещё не загружены. Обновите экран и повторите предпросмотр.",
    selectReleaseFirst:
      "Выберите локальный релиз и дождитесь загрузки его дорожек.",
    assignAtLeastOne: "Назначьте хотя бы один файл дорожке выбранного релиза.",
    releaseReadError: "Не удалось прочитать локальный релиз.",
    previewAction: "Предпросмотр назначения",
    previewing: "Предпросмотр…",
    previewTitle: "Предпросмотр — только чтение",
    previewSummary: (title: string, count: number) =>
      `${title} · ${count} ${russianCount(count, ["дорожка", "дорожки", "дорожек"])}`,
    omittedFiles: (count: number) => `Не включённые файлы: ${count}`,
    previewError: "Предпросмотр не выполнен.",
    previewConflict:
      "Снимок устарел. Обновите данные группы и создайте новый предпросмотр.",
    staleNotice:
      "Снимок изменился. После обновления данных создайте новый предпросмотр.",
    confirmAction: "Подтвердить сопоставление",
    confirmRetry: "Повторить тот же запрос подтверждения",
    confirmError: "Подтверждение не выполнено.",
    confirmConflict:
      "Снимок устарел. Получите новый предпросмотр перед подтверждением.",
    confirmRetryNotice:
      "Повторите запрос, чтобы безопасно проверить тот же результат.",
    notice: (notice: LocalMatchingNotice) => {
      if (notice.key === "confirmedRefreshError")
        return `Сопоставление подтверждено, но обновить экран не удалось: ${notice.reason || "ошибка обновления"}`;
      return {
        confirmed: "Локальное сопоставление подтверждено.",
        scopeChangedAfterConfirm:
          "Локальное сопоставление подтверждено; данные группы обновлены.",
        scopeChangedBeforeConfirm:
          "Снимок группы или полномочий изменился. Выберите релиз и создайте новый предпросмотр.",
      }[notice.key];
    },
    receipt: (releaseTitle: string) =>
      `Сопоставление подтверждено для релиза ${releaseTitle}.`,
    httpError: (fallback: string, status: number) =>
      `${fallback} (HTTP ${status}).`,
  },
} as const;

export type LocalMatchingMessages =
  (typeof localMatchingMessages)[keyof typeof localMatchingMessages];
