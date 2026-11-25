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
