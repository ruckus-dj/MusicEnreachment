export type ReleaseFieldKey =
  | "title"
  | "sort_title"
  | "release_date"
  | "original_release_date"
  | "recording_date"
  | "release_country"
  | "label"
  | "catalog_number"
  | "release_primary_type"
  | "release_secondary_types"
  | "release_version"
  | "asin"
  | "script"
  | "subtitle"
  | "barcode"
  | "release_status"
  | "compilation"
  | "content_advisory"
  | "mood"
  | "grouping"
  | "comment"
  | "copyright"
  | "license"
  | "website"
  | "media"
  | "genre"
  | "isrc"
  | "bpm"
  | "key"
  | "language"
  | "works"
  | "contributors"
  | "movement"
  | "disc_title"
  | "track_number"
  | "track_total"
  | "disc_number"
  | "disc_total";

export type ReleaseScalarFieldKind =
  | "text"
  | "date"
  | "list"
  | "boolean"
  | "advisory";

export type ReleaseScalarField = {
  /** Backend metadata key and label catalog key. */
  key: Exclude<ReleaseFieldKey, "genre">;
  /** Portable registry key used by the design host. */
  metadataKey: string;
  kind: ReleaseScalarFieldKind;
  /** Catalog message key for an edit-time hint. */
  hint?: "dateHint";
};

/** Shared order, kind and transport identity for release basics in both hosts. */
export const releaseBasicsFields = [
  { key: "title", metadataKey: "album", kind: "text" },
  {
    key: "release_date",
    metadataKey: "releasedate",
    kind: "date",
    hint: "dateHint",
  },
  { key: "release_country", metadataKey: "releasecountry", kind: "text" },
  { key: "label", metadataKey: "recordlabel", kind: "list" },
  { key: "catalog_number", metadataKey: "catalognumber", kind: "text" },
  {
    key: "release_primary_type",
    metadataKey: "release_primary_type",
    kind: "text",
  },
  {
    key: "release_secondary_types",
    metadataKey: "release_secondary_types",
    kind: "list",
  },
] as const satisfies readonly ReleaseScalarField[];

/** Shared order, kind and transport identity for release detail fields in both hosts. */
export const releaseDetailFields = [
  { key: "sort_title", metadataKey: "albumsort", kind: "text" },
  {
    key: "original_release_date",
    metadataKey: "originaldate",
    kind: "date",
    hint: "dateHint",
  },
  { key: "release_version", metadataKey: "albumversion", kind: "text" },
  { key: "asin", metadataKey: "asin", kind: "text" },
  { key: "script", metadataKey: "script", kind: "text" },
  { key: "barcode", metadataKey: "barcode", kind: "text" },
  { key: "release_status", metadataKey: "release_status", kind: "text" },
  { key: "compilation", metadataKey: "compilation", kind: "boolean" },
  {
    key: "content_advisory",
    metadataKey: "explicitstatus",
    kind: "advisory",
  },
  { key: "media", metadataKey: "media", kind: "list" },
] as const satisfies readonly ReleaseScalarField[];

/** Shared order, kind and transport identity for additional release fields in both hosts. */
export const releaseAdditionalFields = [
  { key: "mood", metadataKey: "mood", kind: "list" },
  { key: "grouping", metadataKey: "grouping", kind: "text" },
  { key: "comment", metadataKey: "comment", kind: "text" },
  { key: "copyright", metadataKey: "copyright", kind: "text" },
  { key: "license", metadataKey: "license", kind: "text" },
  { key: "website", metadataKey: "website", kind: "text" },
] as const satisfies readonly ReleaseScalarField[];

export const releaseScalarFields = [
  ...releaseBasicsFields,
  ...releaseDetailFields,
  ...releaseAdditionalFields,
] as const;

export const recordingBasicsFields = [
  { key: "title", metadataKey: "title", kind: "text" },
  { key: "isrc", metadataKey: "isrc", kind: "list" },
  { key: "content_advisory", metadataKey: "explicitstatus", kind: "advisory" },
] as const satisfies readonly ReleaseScalarField[];

export function releaseScalarFieldLabel(
  locale: keyof typeof releaseMetadataEditorMessages,
  field: ReleaseScalarField,
): string {
  return releaseMetadataEditorMessages[locale].labels[field.key];
}

export function releaseScalarFieldHint(
  locale: keyof typeof releaseMetadataEditorMessages,
  field: ReleaseScalarField,
): string | undefined {
  return field.hint
    ? releaseMetadataEditorMessages[locale][field.hint]
    : undefined;
}

export function libraryMetadataScalarChoices(
  locale: keyof typeof releaseMetadataEditorMessages,
  kind: string,
) {
  const text = releaseMetadataEditorMessages[locale];
  if (kind === "boolean") {
    return [
      { value: "true", label: `${text.value}: true` },
      { value: "false", label: `${text.value}: false` },
    ];
  }
  if (kind === "advisory") {
    return [
      { value: "explicit", label: text.explicit },
      { value: "clean", label: text.clean },
    ];
  }
  return undefined;
}

export function validLibraryMetadataPartialDate(value: string): boolean {
  if (!/^(?!0000)\d{4}(?:-\d{2}(?:-\d{2})?)?$/.test(value)) return false;
  if (value.length === 4) return true;
  const date = new Date(
    `${value.length === 7 ? `${value}-01` : value}T00:00:00Z`,
  );
  return (
    !Number.isNaN(date.valueOf()) &&
    (value.length === 7
      ? date.toISOString().slice(0, 7) === value
      : date.toISOString().slice(0, 10) === value)
  );
}

export const releaseMetadataEditorMessages = {
  en: {
    title: "Release metadata",
    recordingTitle: "Recording metadata",
    positionTitle: "Position metadata",
    labels: {
      title: "Title",
      sort_title: "Sort title",
      release_date: "Release date",
      original_release_date: "Original release date",
      recording_date: "Recording date",
      release_country: "Country",
      label: "Label",
      catalog_number: "Catalog number",
      release_primary_type: "Primary release type",
      release_secondary_types: "Secondary release types",
      release_version: "Release version",
      asin: "ASIN",
      script: "Script",
      subtitle: "Track subtitle",
      barcode: "Barcode",
      release_status: "Release status",
      compilation: "Compilation",
      content_advisory: "Content advisory",
      mood: "Mood",
      grouping: "Grouping",
      comment: "Comment",
      copyright: "Copyright",
      license: "License",
      website: "Website",
      media: "Release medium",
      isrc: "ISRC",
      bpm: "BPM",
      key: "Musical key",
      language: "Language",
      works: "Works",
      contributors: "Contributors",
      movement: "Movement",
      disc_title: "Disc title",
      track_number: "Track number",
      track_total: "Track total",
      disc_number: "Disc number",
      disc_total: "Disc total",
    } satisfies Record<Exclude<ReleaseFieldKey, "genre">, string>,
    groups: {
      release: "Release details",
      recording: "Recording details",
      position: "Position details",
      additional: "Additional metadata",
      worksMovement: "Works and movements",
      main: "Titles",
      artistCredits: "Artist credits",
      contributors: "Contributors",
    },
    noOverride: "Inherited · no local override",
    providerBaseline: "MusicBrainz effective value",
    inheritedFromRecording: "Inherited from recording",
    explicitEmpty: "Explicitly cleared",
    emptyValue: "Empty value",
    dateHint: "Enter the date in the format accepted by the server.",
    inherit: "Inherit",
    clear: "Clear",
    loading: "Loading saved metadata…",
    saving: "Saving metadata…",
    readError: "Could not load saved metadata.",
    retry: "Retry",
    saveError: "Could not save metadata.",
    conflict:
      "Metadata changed while you were editing. Your draft is preserved; cancel and reopen to load the latest revision.",
    draftRetained: "Your draft is retained. Retry or cancel your changes.",
    mode: "Override state",
    absent: "Inherit",
    value: "Value",
    invalidDate: "Enter a valid date as YYYY, YYYY-MM, or YYYY-MM-DD.",
    invalidPositiveInteger: "Enter a positive whole number.",
    invalidPositiveNumber: "Enter a positive finite number.",
    invalidMovementName: "Enter a movement name.",
    explicit: "Explicit",
    clean: "Clean",
    contributorName: "Contributor name",
    contributorRole: "Role",
    contributorInstrument: "Instrument",
    contributorMusicBrainzID: "Contributor MusicBrainz ID",
    artistLinkRetained: "Local artist link retained",
    addContributor: "Add contributor",
    addRole: "Add role",
    selectRole: "Select a role…",
    moveUp: "Move up",
    moveDown: "Move down",
    remove: "Remove",
    contributorNameRequired: "Contributor name is required.",
    moveContributorUp: (index: number) => `Move contributor up ${index}`,
    moveContributorDown: (index: number) => `Move contributor down ${index}`,
    removeContributor: (index: number) => `Remove contributor ${index}`,
    contributorRoles: {
      composer: "Composer",
      lyricist: "Lyricist",
      conductor: "Conductor",
      director: "Director",
      djmixer: "DJ mixer",
      mixer: "Mixer",
      engineer: "Engineer",
      arranger: "Arranger",
      producer: "Producer",
      remixer: "Remixer",
      performer: "Performer",
    },
  },
  ru: {
    title: "Метаданные релиза",
    recordingTitle: "Метаданные записи",
    positionTitle: "Метаданные позиции",
    labels: {
      title: "Название",
      sort_title: "Сортировочное название",
      release_date: "Дата выпуска",
      original_release_date: "Дата первого выпуска",
      recording_date: "Дата записи",
      release_country: "Страна",
      label: "Лейбл",
      catalog_number: "Номер каталога",
      release_primary_type: "Основной тип релиза",
      release_secondary_types: "Дополнительные типы релиза",
      release_version: "Версия релиза",
      asin: "ASIN",
      script: "Письменность",
      subtitle: "Подзаголовок трека",
      barcode: "Штрихкод",
      release_status: "Статус релиза",
      compilation: "Сборник",
      content_advisory: "Предупреждение о содержании",
      mood: "Настроение",
      grouping: "Группа",
      comment: "Комментарий",
      copyright: "Авторское право",
      license: "Лицензия",
      website: "Веб-сайт",
      media: "Носитель издания",
      isrc: "ISRC",
      bpm: "Темп (BPM)",
      key: "Тональность",
      language: "Язык",
      works: "Произведения",
      contributors: "Участники",
      movement: "Часть произведения",
      disc_title: "Название диска",
      track_number: "Номер дорожки",
      track_total: "Всего дорожек",
      disc_number: "Номер диска",
      disc_total: "Всего дисков",
    } satisfies Record<Exclude<ReleaseFieldKey, "genre">, string>,
    groups: {
      release: "Сведения о релизе",
      recording: "Сведения о записи",
      position: "Сведения о позиции",
      additional: "Дополнительные метаданные",
      worksMovement: "Произведения и части",
      main: "Названия",
      artistCredits: "Исполнители",
      contributors: "Участники",
    },
    noOverride: "Наследуется · локальное значение не задано",
    providerBaseline: "Эффективное значение MusicBrainz",
    inheritedFromRecording: "Унаследовано от записи",
    explicitEmpty: "Явно очищено",
    emptyValue: "Пустое значение",
    dateHint: "Введите дату в формате, который принимает сервер.",
    inherit: "Наследовать",
    clear: "Очистить",
    loading: "Загрузка сохранённых метаданных…",
    saving: "Сохранение метаданных…",
    readError: "Не удалось загрузить сохранённые метаданные.",
    retry: "Повторить",
    saveError: "Не удалось сохранить метаданные.",
    conflict:
      "Метаданные изменились во время редактирования. Черновик сохранён; отмените изменения и откройте редактор снова, чтобы загрузить новую версию.",
    draftRetained:
      "Черновик сохранён. Повторите попытку или отмените изменения.",
    mode: "Состояние переопределения",
    absent: "Наследовать",
    value: "Значение",
    invalidDate:
      "Введите корректную дату в формате ГГГГ, ГГГГ-ММ или ГГГГ-ММ-ДД.",
    invalidPositiveInteger: "Введите положительное целое число.",
    invalidPositiveNumber: "Введите положительное конечное число.",
    invalidMovementName: "Введите название части.",
    explicit: "С ненормативной лексикой",
    clean: "Без ненормативной лексики",
    contributorName: "Имя участника",
    contributorRole: "Роль",
    contributorInstrument: "Инструмент",
    contributorMusicBrainzID: "MusicBrainz ID участника",
    artistLinkRetained: "Связь с локальным артистом сохранена",
    addContributor: "Добавить участника",
    addRole: "Добавить роль",
    selectRole: "Выберите роль…",
    moveUp: "Выше",
    moveDown: "Ниже",
    remove: "Убрать",
    contributorNameRequired: "Укажите имя участника.",
    moveContributorUp: (index: number) =>
      `Переместить участника вверх ${index}`,
    moveContributorDown: (index: number) =>
      `Переместить участника вниз ${index}`,
    removeContributor: (index: number) => `Удалить участника ${index}`,
    contributorRoles: {
      composer: "Композитор",
      lyricist: "Автор текста",
      conductor: "Дирижёр",
      director: "Режиссёр",
      djmixer: "Диджей-микшер",
      mixer: "Микшер",
      engineer: "Звукорежиссёр",
      arranger: "Аранжировщик",
      producer: "Продюсер",
      remixer: "Ремиксер",
      performer: "Исполнитель",
    },
  },
} as const;
