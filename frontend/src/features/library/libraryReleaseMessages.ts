import type { Locale } from "../../i18n/locale";

const messages = {
  en: {
    requestFailed: "Request failed (HTTP {status}).",
    loadCoverFailed: "Could not load release cover.",
    saveCoverFailed: "Could not save release cover.",
    applyCoverFailed: "Could not start applying the cover.",
    coverSaved: "Cover saved as pending. Publications were not changed.",
    coverApplyStarted:
      "Application started for {count} published tracks. Current covers will update only after publication validation.",
    coverApplyAdmitted:
      "Cover application started. Waiting for validation of the published data.",
    releaseCover: "Release cover",
    selectedCover: "Selected cover",
    noSelectedCover: "No cover selected.",
    pendingCover: "Pending cover — not published yet",
    pendingReleaseCover: "Pending release cover",
    unspecified: "not specified",
    choosePngJpeg: "Choose PNG or JPEG",
    savePendingCover: "Save pending cover",
    applyCoverToTracks: "Apply to all published tracks",
    retrySelectedCover: "Retry applying selected cover",
    coverWarning:
      "Cover warning: {warning}. This does not confirm successful embedding.",
    library: "Library",
    incoming: "Incoming",
    libraryNavigation: "Library navigation",
    libraryCatalog: "← Library catalog",
    selectedReleaseCover: "Selected release cover",
    primaryReleaseArtist: "Release primary artist",
    notSelected: "Not selected",
    refreshMusicBrainzRunning: "Refreshing MusicBrainz…",
    refreshMusicBrainz: "Refresh MusicBrainz data",
    waitingMusicBrainz: "Waiting for MusicBrainz refresh…",
    musicBrainzUpdated:
      "MusicBrainz data updated. Publication refresh runs separately.",
    musicBrainzFailed: "MusicBrainz refresh ended with status: {state}.",
    refreshPreviewFailed: "Could not refresh metadata preview.",
    refreshReleaseFailed: "Could not refresh release data.",
    checkMusicBrainzFailed: "Could not check the MusicBrainz refresh.",
    startMusicBrainzFailed: "Could not start the MusicBrainz refresh.",
    releaseLoadFailed: "Could not load release.",
    loadingRelease: "Loading release…",
    publicationLoadFailed: "Could not load publication data.",
    publicationRefreshFailed: "Could not refresh publication.",
    cleanupCheckFailed:
      "Could not check source cleanup. Refresh publication data.",
    cleanupStillWarning:
      "Cleanup retry finished; the source warning is still current.",
    cleanupRetryUnavailable:
      "Cleanup retry is no longer available. The source warning is still current.",
    operationCompleted:
      "Operation completed. Review the published data and receipt below.",
    operationEnded: "Operation ended with status: {state}.",
    changedOrLyricsNotReady:
      "The state changed or the lyrics are not ready to apply. Refresh the data before trying again.",
    lyricsSaved:
      "Lyrics saved. Apply them separately to the current publication.",
    saveLyricsFailed: "Could not save lyrics.",
    applyStarted: "Application started. Waiting for the operation update…",
    applyLyricsFailed: "Could not apply lyrics.",
    verifyPublicationFailed: "Could not verify publication.",
    retryCleanupFailed: "Could not retry source cleanup.",
    verifyBeforeRetry:
      "Could not verify publication. Refresh the data before trying again.",
    chooseArtistFailed: "Could not select the artist.",
    chooseReleaseArtistFailed: "Could not select the release artist.",
    cleanupRetryRunning: "Retry in progress…",
    retrySourceCleanup: "Retry source cleanup",
    cleanupRunning: "Source cleanup retry is in progress.",
    fullRelease: "← Full release",
    positionNotFound: "Position not found in this release.",
    actualCoverRegion: "Actual cover of the current publication",
    audio: "audio",
    cover: "cover",
    lyrics: "lyrics",
    primaryTrackArtist: "Track primary artist",
    savedLyrics: "Saved lyrics",
    saveLyrics: "Save lyrics",
    saveExplicitlyEmpty: "Save as explicitly empty",
    applySavedLyrics: "Apply saved lyrics",
    publication: "Publication",
    notPublishedYet: "not published yet",
    generation: "generation {generation}",
    actualCover: "Actual cover",
    verified: "verified",
    unverified: "not verified",
    lyricsPublicationResult: "Lyrics result for current publication",
    lyricsInCurrentPublication: "Lyrics in current publication:",
    lyricsSource: "source",
    lyricsSourceNone: "no source selected",
    lyricsSourceLibrary: "library source",
    lyricsSourceLrclib: "LRCLIB",
    lyricsOutcomeNotRequested: "lyrics not requested",
    lyricsOutcomeVerified: "lyrics included and verified",
    lyricsOutcomeRebuiltWithout: "publication rebuilt without lyrics",
    lyricsWarningLookup: "Lookup warning:",
    lyricsWarningPublication: "Publication warning:",
    warningSourceSelection: "a lyrics source must be selected",
    warningSettingsUnavailable: "lyrics lookup settings are unavailable",
    warningMetadataIncomplete: "metadata is insufficient for lyrics lookup",
    warningProviderUnavailable: "lyrics provider is unavailable",
    warningLookupFailed: "lyrics lookup failed",
    warningInvalidSyncedText: "found lyrics did not pass validation",
    warningReadbackMismatch: "lyrics did not pass validation after writing",
    coverEmbeddingOutcome: "Selected cover embedding:",
    operationRunning: "Operation in progress: {stage} ({state})",
    savedLyricsPending: "Saved lyrics are waiting to be applied.",
    noLyricsPending: "No lyrics application is pending.",
    currentPublicationSummary: "Current publication:",
    audioUnpublished: "not published",
    unknown: "unknown",
    lyricsUnknown: "unknown",
    receipt: "Receipt: lyrics",
    confirmedInGeneration: "confirmed in generation",
    publishedGeneration: "published generation",
    warning: "warning",
    refreshData: "Refresh data",
  },
  ru: {
    requestFailed: "Запрос не выполнен (HTTP {status}).",
    loadCoverFailed: "Не удалось загрузить обложку релиза.",
    saveCoverFailed: "Не удалось сохранить обложку.",
    applyCoverFailed: "Не удалось запустить применение обложки.",
    coverSaved: "Обложка сохранена как ожидающая. Публикации не изменены.",
    coverApplyStarted:
      "Применение запущено для {count} опубликованных треков. Текущие обложки обновятся только после проверки публикаций.",
    coverApplyAdmitted:
      "Применение обложки запущено. Ожидается проверка опубликованных данных.",
    releaseCover: "Обложка релиза",
    selectedCover: "Выбранная обложка",
    noSelectedCover: "Выбранная обложка не задана.",
    pendingCover: "Ожидающая обложка — ещё не опубликована",
    pendingReleaseCover: "Ожидающая обложка релиза",
    unspecified: "не указан",
    choosePngJpeg: "Выбрать PNG или JPEG",
    savePendingCover: "Сохранить ожидающую обложку",
    applyCoverToTracks: "Применить ко всем опубликованным трекам",
    retrySelectedCover: "Повторить применение выбранной обложки",
    coverWarning:
      "Предупреждение обложки: {warning}. Это не подтверждение успешного встраивания.",
    library: "Медиатека",
    incoming: "Входящие",
    libraryNavigation: "Навигация по медиатеке",
    libraryCatalog: "← Каталог медиатеки",
    selectedReleaseCover: "Выбранная обложка релиза",
    primaryReleaseArtist: "Основной исполнитель релиза",
    notSelected: "Не выбран",
    refreshMusicBrainzRunning: "Обновление MusicBrainz выполняется…",
    refreshMusicBrainz: "Обновить данные MusicBrainz",
    waitingMusicBrainz: "Ожидание обновления данных MusicBrainz…",
    musicBrainzUpdated:
      "Данные MusicBrainz обновлены. Обновление публикаций выполняется отдельно.",
    musicBrainzFailed:
      "Обновление MusicBrainz завершилось со статусом: {state}.",
    refreshPreviewFailed: "Не удалось обновить предпросмотр метаданных.",
    refreshReleaseFailed: "Не удалось обновить данные релиза.",
    checkMusicBrainzFailed: "Не удалось проверить обновление MusicBrainz.",
    startMusicBrainzFailed: "Не удалось запустить обновление MusicBrainz.",
    releaseLoadFailed: "Не удалось загрузить релиз.",
    loadingRelease: "Загрузка релиза…",
    publicationLoadFailed: "Не удалось загрузить данные публикации.",
    publicationRefreshFailed: "Не удалось обновить публикацию.",
    cleanupCheckFailed:
      "Не удалось проверить очистку источников. Обновите данные публикации.",
    cleanupStillWarning:
      "Повтор очистки завершён, предупреждение источника остаётся актуальным.",
    cleanupRetryUnavailable:
      "Повтор очистки больше недоступен. Предупреждение источника остаётся актуальным.",
    operationCompleted:
      "Операция завершена. Сверьте опубликованные данные и квитанцию ниже.",
    operationEnded: "Операция завершилась со статусом: {state}.",
    changedOrLyricsNotReady:
      "Состояние изменилось или текст не готов к применению. Обновите данные перед повторной попыткой.",
    lyricsSaved: "Текст сохранён. Примените его отдельно к текущей публикации.",
    saveLyricsFailed: "Не удалось сохранить текст.",
    applyStarted: "Применение запущено. Ожидание обновления операции…",
    applyLyricsFailed: "Не удалось применить текст.",
    verifyPublicationFailed: "Не удалось проверить публикацию.",
    retryCleanupFailed: "Не удалось повторить очистку источников.",
    verifyBeforeRetry:
      "Не удалось проверить публикацию. Обновите данные перед повторной попыткой.",
    chooseArtistFailed: "Не удалось выбрать исполнителя.",
    chooseReleaseArtistFailed: "Не удалось выбрать исполнителя релиза.",
    cleanupRetryRunning: "Повтор выполняется…",
    retrySourceCleanup: "Повторить очистку источников",
    cleanupRunning: "Повторная очистка выполняется.",
    fullRelease: "← Полный релиз",
    positionNotFound: "Позиция не найдена в этом релизе.",
    actualCoverRegion: "Фактическая обложка текущей публикации",
    audio: "аудио",
    cover: "обложка",
    lyrics: "текст",
    primaryTrackArtist: "Основной исполнитель композиции",
    savedLyrics: "Сохранённый текст песни",
    saveLyrics: "Сохранить текст",
    saveExplicitlyEmpty: "Сохранить как явно пустой",
    applySavedLyrics: "Применить сохранённый текст",
    publication: "Публикация",
    notPublishedYet: "ещё не опубликована",
    generation: "поколение {generation}",
    actualCover: "Фактическая обложка",
    verified: "проверено",
    unverified: "не подтверждено",
    lyricsPublicationResult: "Результат текста песни для текущей публикации",
    lyricsInCurrentPublication: "Текст песни в текущей публикации:",
    lyricsSource: "источник",
    lyricsSourceNone: "источник не выбран",
    lyricsSourceLibrary: "источник медиатеки",
    lyricsSourceLrclib: "LRCLIB",
    lyricsOutcomeNotRequested: "текст не запрашивался",
    lyricsOutcomeVerified: "текст включён и проверен",
    lyricsOutcomeRebuiltWithout: "публикация пересоздана без текста",
    lyricsWarningLookup: "Предупреждение поиска:",
    lyricsWarningPublication: "Предупреждение публикации:",
    warningSourceSelection: "необходимо выбрать источник текста",
    warningSettingsUnavailable: "настройки поиска текста недоступны",
    warningMetadataIncomplete: "недостаточно метаданных для поиска текста",
    warningProviderUnavailable: "провайдер текста недоступен",
    warningLookupFailed: "не удалось найти текст",
    warningInvalidSyncedText: "найденный текст не прошёл проверку",
    warningReadbackMismatch: "текст не прошёл проверку после записи",
    coverEmbeddingOutcome: "Встраивание выбранной обложки:",
    operationRunning: "Выполняется операция: {stage} ({state})",
    savedLyricsPending: "Сохранённый текст ожидает применения.",
    noLyricsPending: "Нет ожидающего применения текста.",
    currentPublicationSummary: "Текущая публикация:",
    audioUnpublished: "не опубликовано",
    unknown: "неизвестно",
    lyricsUnknown: "неизвестно",
    receipt: "Квитанция: текст",
    confirmedInGeneration: "подтверждён в поколении",
    publishedGeneration: "опубликованное поколение",
    warning: "предупреждение",
    refreshData: "Обновить данные",
  },
} as const;

export type LibraryReleaseMessage = keyof typeof messages.en;
export type LibraryReleaseText =
  | string
  | { key: LibraryReleaseMessage; values?: Record<string, string | number> };

export function libraryReleaseMessage(
  locale: Locale,
  key: LibraryReleaseMessage,
  values: Record<string, string | number> = {},
): string {
  return messages[locale][key].replace(/\{([^}]+)\}/g, (_, name: string) =>
    String(values[name] ?? `{${name}}`),
  );
}

export function renderLibraryReleaseText(
  locale: Locale,
  text: LibraryReleaseText,
) {
  if (typeof text === "string") return text;
  return libraryReleaseMessage(locale, text.key, text.values);
}

export function libraryReleaseText(
  key: LibraryReleaseMessage,
  values?: Record<string, string | number>,
): LibraryReleaseText {
  return { key, values };
}

export class LocalizedLibraryReleaseError extends Error {
  constructor(readonly text: LibraryReleaseText) {
    super("Localized library release error");
  }
}

export function libraryReleaseError(
  reason: unknown,
  fallback: LibraryReleaseMessage,
): LibraryReleaseText {
  if (reason instanceof LocalizedLibraryReleaseError) return reason.text;
  if (reason instanceof Error && reason.message) return reason.message;
  return libraryReleaseText(fallback);
}
