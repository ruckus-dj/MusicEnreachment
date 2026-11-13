import { useCallback, useEffect, useId, useRef, useState } from "react";
import { apiResponseError, errorMessage } from "../../api/client/errors";
import {
  getLibraryPositionMetadata,
  getLibraryRecordingMetadata,
  getLibraryReleaseMetadata,
  getSearchLibraryArtistReferencesQueryKey,
  patchLibraryPositionMetadata,
  patchLibraryRecordingMetadata,
  patchLibraryReleaseMetadata,
  useSearchLibraryArtistReferences,
} from "../../api/generated/client";
import type {
  LibraryMetadataGetResponse,
  LibraryMetadataResponse,
} from "../../api/generated/client.schemas";
import { AppButton } from "../../components/AppButton";
import { useI18n } from "../../i18n/I18nProvider";
import { ArtistCreditRowsEditor } from "./ArtistCreditRowsEditor";
import {
  type ArtistCreditDraft,
  type ArtistCreditResolutionStatus,
  type ArtistCreditRow,
  adaptArtistCreditMetadata,
  serializeArtistCreditChange,
} from "./artistCreditDraft";
import { artistCreditMessages } from "./artistCreditMessages";
import {
  createMetadataDraftRowKey,
  type MetadataMovementDraft,
  type MetadataWorkDraft,
  readMetadataMovementDraft,
  readMetadataWorkDrafts,
  StructuredMetadataField,
} from "./StructuredMetadataFields";

type FieldState = "absent" | "value" | "explicit_empty";
type ContributorDraft = {
  uiKey: string;
  name: string;
  role: string;
  instrument?: string;
  artist_id?: string;
  mbid?: string;
};
type MetadataValue =
  | string
  | string[]
  | number
  | boolean
  | MetadataWorkDraft[]
  | ContributorDraft[]
  | MetadataMovementDraft;
type ReleaseFieldKey =
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
type MetadataFieldKey = ReleaseFieldKey | "title" | "sort_title";
type DirtyFieldKey = MetadataFieldKey | "artist_credit";
type ReleaseField = {
  key: MetadataFieldKey;
  kind:
    | "text"
    | "date"
    | "list"
    | "boolean"
    | "integer"
    | "number"
    | "advisory"
    | "works"
    | "contributors"
    | "movement";
  group: string;
};

const releaseFields: ReleaseField[] = [
  { key: "title", kind: "text", group: "release" },
  { key: "sort_title", kind: "text", group: "release" },
  { key: "release_date", kind: "date", group: "release" },
  { key: "original_release_date", kind: "date", group: "release" },
  { key: "release_country", kind: "text", group: "release" },
  { key: "label", kind: "list", group: "release" },
  { key: "catalog_number", kind: "text", group: "release" },
  { key: "release_primary_type", kind: "text", group: "release" },
  { key: "release_secondary_types", kind: "list", group: "release" },
  { key: "release_version", kind: "text", group: "release" },
  { key: "asin", kind: "text", group: "release" },
  { key: "script", kind: "text", group: "release" },
  { key: "barcode", kind: "text", group: "release" },
  { key: "release_status", kind: "text", group: "release" },
  { key: "compilation", kind: "boolean", group: "release" },
  { key: "content_advisory", kind: "advisory", group: "release" },
  { key: "media", kind: "list", group: "release" },
  { key: "mood", kind: "list", group: "additional" },
  { key: "grouping", kind: "text", group: "additional" },
  { key: "comment", kind: "text", group: "additional" },
  { key: "copyright", kind: "text", group: "additional" },
  { key: "license", kind: "text", group: "additional" },
  { key: "website", kind: "text", group: "additional" },
];

const titleFields: ReleaseField[] = [
  { key: "title", kind: "text", group: "main" },
  { key: "sort_title", kind: "text", group: "main" },
];

const recordingFields: ReleaseField[] = [
  ...titleFields,
  { key: "contributors", kind: "contributors", group: "contributors" },
  { key: "works", kind: "works", group: "worksMovement" },
  { key: "movement", kind: "movement", group: "worksMovement" },
  { key: "recording_date", kind: "date", group: "recording" },
  { key: "isrc", kind: "list", group: "recording" },
  { key: "bpm", kind: "number", group: "recording" },
  { key: "key", kind: "text", group: "recording" },
  { key: "language", kind: "text", group: "additional" },
  { key: "content_advisory", kind: "advisory", group: "additional" },
  { key: "mood", kind: "list", group: "additional" },
  { key: "grouping", kind: "text", group: "additional" },
  { key: "comment", kind: "text", group: "additional" },
  { key: "copyright", kind: "text", group: "additional" },
  { key: "license", kind: "text", group: "additional" },
  { key: "website", kind: "text", group: "additional" },
  { key: "subtitle", kind: "text", group: "additional" },
];

const positionFields: ReleaseField[] = [
  ...recordingFields,
  { key: "disc_title", kind: "text", group: "position" },
  { key: "track_number", kind: "integer", group: "position" },
  { key: "track_total", kind: "integer", group: "position" },
  { key: "disc_number", kind: "integer", group: "position" },
  { key: "disc_total", kind: "integer", group: "position" },
];

const copy = {
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
      label: "Labels",
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
      media: "Media",
      isrc: "ISRCs",
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
    } satisfies Record<ReleaseFieldKey, string>,
    groups: {
      release: "Release details",
      recording: "Recording details",
      position: "Position details",
      additional: "Additional metadata",
      worksMovement: "Works and movement",
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
    listHint: "Enter one value per line; order is preserved.",
    edit: "Edit",
    save: "Save",
    cancel: "Cancel",
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
    workName: "Work name",
    workMBID: "MusicBrainz work ID",
    addWork: "Add work",
    moveWorkUp: (index: number) => `Move work up ${index}`,
    moveWorkDown: (index: number) => `Move work down ${index}`,
    removeWork: (index: number) => `Remove work ${index}`,
    movementName: "Movement name",
    movementNumber: "Movement number",
    movementCount: "Movement total",
    contributorName: "Contributor name",
    contributorRole: "Role",
    contributorInstrument: "Instrument",
    contributorMusicBrainzID: "Contributor MusicBrainz ID",
    artistLinkRetained: "Local artist link retained",
    addContributor: "Add contributor",
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
      label: "Лейблы",
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
      media: "Носители",
      isrc: "Коды ISRC",
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
    } satisfies Record<ReleaseFieldKey, string>,
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
    listHint: "Введите каждое значение с новой строки; порядок сохраняется.",
    edit: "Изменить",
    save: "Сохранить",
    cancel: "Отмена",
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
    workName: "Название произведения",
    workMBID: "MusicBrainz ID произведения",
    addWork: "Добавить произведение",
    moveWorkUp: (index: number) => `Переместить произведение вверх ${index}`,
    moveWorkDown: (index: number) => `Переместить произведение вниз ${index}`,
    removeWork: (index: number) => `Удалить произведение ${index}`,
    movementName: "Название части",
    movementNumber: "Номер части",
    movementCount: "Всего частей",
    contributorName: "Имя участника",
    contributorRole: "Роль",
    contributorInstrument: "Инструмент",
    contributorMusicBrainzID: "MusicBrainz ID участника",
    artistLinkRetained: "Связь с локальным артистом сохранена",
    addContributor: "Добавить участника",
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

type EditorValues = Record<
  MetadataFieldKey,
  { state: FieldState; value: MetadataValue }
>;

function emptyValues(fields: ReleaseField[]): EditorValues {
  return Object.fromEntries(
    fields.map(({ key, kind }) => [
      key,
      {
        state: "absent",
        value:
          kind === "list" || kind === "works" || kind === "contributors"
            ? []
            : kind === "movement"
              ? { name: "", number: "", count: "" }
              : kind === "boolean"
                ? false
                : kind === "advisory"
                  ? "explicit"
                  : "",
      },
    ]),
  ) as EditorValues;
}

function emptyArtistCreditDraft(): ArtistCreditDraft {
  return { hasDesiredOverride: false, state: "absent", rows: [] };
}

function readValues(
  metadata: LibraryMetadataGetResponse | LibraryMetadataResponse,
  fields: ReleaseField[],
): EditorValues {
  const values = emptyValues(fields);
  for (const { key, kind } of fields) {
    const field = metadata.envelope.desired[key];
    if (!field || !["absent", "value", "explicit_empty"].includes(field.state))
      continue;
    const state = field.state as FieldState;
    let value: MetadataValue =
      kind === "list" || kind === "works" || kind === "contributors"
        ? []
        : kind === "movement"
          ? { name: "", number: "", count: "" }
          : kind === "boolean"
            ? false
            : kind === "advisory"
              ? "explicit"
              : "";
    if (state === "value") {
      if (kind === "works") value = readMetadataWorkDrafts(field.value);
      else if (kind === "contributors") value = readContributors(field.value);
      else if (kind === "movement")
        value = readMetadataMovementDraft(field.value);
      else if (kind === "list" && Array.isArray(field.value))
        value = field.value.filter(
          (item): item is string => typeof item === "string",
        );
      else if (kind === "boolean" && typeof field.value === "boolean")
        value = field.value;
      else if (
        ["integer", "number"].includes(kind) &&
        typeof field.value === "number"
      )
        value = String(field.value);
      else if (kind !== "list" && typeof field.value === "string")
        value = field.value;
    }
    values[key] = { state, value };
  }
  return values;
}

function readContributors(value: unknown): ContributorDraft[] {
  if (!Array.isArray(value)) return [];
  return value.flatMap((item) => {
    if (!item || typeof item !== "object" || Array.isArray(item)) return [];
    const contributor = item as Record<string, unknown>;
    if (
      typeof contributor.name !== "string" ||
      typeof contributor.role !== "string"
    )
      return [];
    return [
      {
        uiKey: createMetadataDraftRowKey(),
        name: contributor.name,
        role: contributor.role,
        ...(typeof contributor.instrument === "string"
          ? { instrument: contributor.instrument }
          : {}),
        ...(typeof contributor.artist_id === "string"
          ? { artist_id: contributor.artist_id }
          : {}),
        ...(typeof contributor.mbid === "string"
          ? { mbid: contributor.mbid }
          : {}),
      },
    ];
  });
}

function displayValue(
  state: FieldState,
  value: MetadataValue,
  text: (typeof copy)[keyof typeof copy],
) {
  if (state === "absent") return text.noOverride;
  if (state === "explicit_empty") return text.explicitEmpty;
  const rendered = Array.isArray(value) ? value.join("\n") : String(value);
  return rendered || text.emptyValue;
}

function validPartialDate(value: string) {
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

function positiveIntegerDraft(value: string): number | undefined {
  if (!/^\d+$/.test(value)) return undefined;
  const number = Number(value);
  return Number.isSafeInteger(number) && number > 0 ? number : undefined;
}

export function ReleaseMetadataEditor({
  releaseId,
  refreshToken,
  onSaved,
}: {
  releaseId: string;
  refreshToken?: number | string;
  onSaved?: (metadata: LibraryMetadataResponse) => void | Promise<void>;
}) {
  return (
    <LibraryScopedMetadataEditorSession
      key={releaseId}
      scope="release"
      fields={releaseFields}
      ownerId={releaseId}
      refreshToken={refreshToken}
      onSaved={onSaved}
    />
  );
}

type ScopedLibraryMetadataEditorProps = {
  refreshToken?: number | string;
  onSaved?: (metadata: LibraryMetadataResponse) => void | Promise<void>;
} & (
  | { scope: "recording"; ownerId: string; releaseId?: never }
  | { scope: "position"; ownerId: string; releaseId: string }
);

export function ScopedLibraryMetadataEditor(
  props: ScopedLibraryMetadataEditorProps,
) {
  const ownerId = props.ownerId;
  const refreshToken = props.refreshToken;
  const onSaved = props.onSaved;
  if (props.scope === "position") {
    return (
      <LibraryScopedMetadataEditorSession
        key={`position:${props.releaseId}:${ownerId}`}
        scope="position"
        fields={positionFields}
        ownerId={ownerId}
        releaseId={props.releaseId}
        refreshToken={refreshToken}
        onSaved={onSaved}
      />
    );
  }
  return (
    <LibraryScopedMetadataEditorSession
      key={`recording::${ownerId}`}
      scope="recording"
      fields={recordingFields}
      ownerId={ownerId}
      refreshToken={refreshToken}
      onSaved={onSaved}
    />
  );
}

type LibraryScopedMetadataEditorSessionProps = {
  fields: ReleaseField[];
  ownerId: string;
  refreshToken?: number | string;
  onSaved?: (metadata: LibraryMetadataResponse) => void | Promise<void>;
} & (
  | { scope: "release"; releaseId?: never }
  | { scope: "recording"; releaseId?: never }
  | { scope: "position"; releaseId: string }
);

function LibraryScopedMetadataEditorSession({
  scope,
  fields,
  ownerId,
  releaseId,
  refreshToken,
  onSaved,
}: LibraryScopedMetadataEditorSessionProps) {
  const { locale } = useI18n();
  const contributorControlPrefix = useId();
  const text = copy[locale];
  const title =
    scope === "release"
      ? text.title
      : scope === "recording"
        ? text.recordingTitle
        : text.positionTitle;
  const [metadata, setMetadata] = useState<
    LibraryMetadataGetResponse | LibraryMetadataResponse
  >();
  const metadataRevision = useRef<number | undefined>(undefined);
  const readGeneration = useRef(0);
  const readController = useRef<AbortController | undefined>(undefined);
  const sessionGeneration = useRef(0);
  const sessionIdentity = `${scope}:${releaseId ?? ""}:${ownerId}`;
  const sessionIdentityRef = useRef(sessionIdentity);
  if (sessionIdentityRef.current !== sessionIdentity) {
    sessionIdentityRef.current = sessionIdentity;
    sessionGeneration.current += 1;
  }
  const writePending = useRef(false);
  const onSavedRef = useRef(onSaved);
  onSavedRef.current = onSaved;
  const dirtyRef = useRef(false);
  const [saved, setSaved] = useState<EditorValues>(() => emptyValues(fields));
  const [draft, setDraft] = useState<EditorValues>(() => emptyValues(fields));
  const [dirtyFields, setDirtyFields] = useState<Set<DirtyFieldKey>>(
    () => new Set(),
  );
  const [savedArtistCredit, setSavedArtistCredit] = useState<ArtistCreditDraft>(
    emptyArtistCreditDraft,
  );
  const [artistCreditDraft, setArtistCreditDraft] = useState<ArtistCreditDraft>(
    emptyArtistCreditDraft,
  );
  const [artistCreditBaseline, setArtistCreditBaseline] = useState<
    ArtistCreditRow[]
  >([]);
  const [effectiveArtistCredit, setEffectiveArtistCredit] = useState<{
    status: ArtistCreditResolutionStatus;
    origin: string | null;
  }>({ status: "unclassified", origin: null });
  const [effectiveKey, setEffectiveKey] =
    useState<LibraryMetadataGetResponse["effective_key"]>();
  const [artistSearchQuery, setArtistSearchQuery] = useState("");
  const [editing, setEditing] = useState(false);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [readError, setReadError] = useState("");
  const [saveError, setSaveError] = useState("");
  const [refreshConflict, setRefreshConflict] = useState(false);
  const previousRefreshToken = useRef(refreshToken);

  const artistSearchTerm = artistSearchQuery.trim();
  const artistSearchParams = artistSearchTerm
    ? { q: artistSearchTerm, limit: 20, offset: 0 }
    : undefined;
  const artistSearch = useSearchLibraryArtistReferences(artistSearchParams, {
    query: {
      enabled:
        editing && artistCreditDraft.state === "value" && !!artistSearchTerm,
      queryKey: [
        ...getSearchLibraryArtistReferencesQueryKey(artistSearchParams),
        sessionIdentity,
      ],
    },
  });
  const artistSearchData =
    artistSearch.data?.status === 200 ? artistSearch.data.data.items : [];
  const artistCreditSearchResults = artistSearchData.map((artist) => ({
    id: artist.id,
    name: artist.name,
    ...(artist.disambiguation ? { disambiguation: artist.disambiguation } : {}),
    ...(artist.provider_links.length
      ? {
          providerFacts: artist.provider_links.map((link) => ({
            label: `${link.provider_code} · ${link.method}`,
            value: artistCreditMessages[locale].providerLinked,
          })),
        }
      : {}),
  }));
  const artistSearchError =
    artistSearch.isError ||
    (artistSearch.data !== undefined && artistSearch.data.status !== 200);

  const loadMetadata = useCallback(
    async (force = false) => {
      const generation = ++readGeneration.current;
      readController.current?.abort();
      const controller = new AbortController();
      readController.current = controller;
      setLoading(true);
      try {
        const options: RequestInit = {
          cache: "no-store",
          signal: controller.signal,
        };
        const response =
          scope === "release"
            ? await getLibraryReleaseMetadata(ownerId, options)
            : scope === "recording"
              ? await getLibraryRecordingMetadata(ownerId, options)
              : await getLibraryPositionMetadata(releaseId, ownerId, options);
        if (controller.signal.aborted || generation !== readGeneration.current)
          return;
        if (response.status !== 200) throw apiResponseError(response);
        if (
          !force &&
          dirtyRef.current &&
          metadataRevision.current !== undefined &&
          metadataRevision.current !== response.data.revision
        ) {
          setRefreshConflict(true);
          return;
        }
        const next = readValues(response.data, fields);
        const creditDisplay = adaptArtistCreditMetadata(response.data);
        metadataRevision.current = response.data.revision;
        setMetadata(response.data);
        setSaved(next);
        setSavedArtistCredit(creditDisplay.draft);
        setArtistCreditBaseline(creditDisplay.baseline);
        setEffectiveArtistCredit({
          status: creditDisplay.effective.status,
          origin: creditDisplay.effective.origin,
        });
        setEffectiveKey(
          "effective_key" in response.data
            ? response.data.effective_key
            : undefined,
        );
        if (!dirtyRef.current) {
          setDraft(next);
          setArtistCreditDraft(creditDisplay.draft);
          setDirtyFields(new Set());
          setRefreshConflict(false);
        }
        setReadError("");
      } catch (reason) {
        if (!controller.signal.aborted && generation === readGeneration.current)
          setReadError(errorMessage(reason, ""));
      } finally {
        if (!controller.signal.aborted && generation === readGeneration.current)
          setLoading(false);
      }
    },
    [fields, ownerId, releaseId, scope],
  );

  useEffect(() => {
    const generation = ++sessionGeneration.current;
    void loadMetadata(true);
    return () => {
      if (sessionGeneration.current === generation)
        sessionGeneration.current += 1;
      readGeneration.current += 1;
      readController.current?.abort();
    };
  }, [loadMetadata]);

  useEffect(() => {
    if (Object.is(previousRefreshToken.current, refreshToken)) return;
    previousRefreshToken.current = refreshToken;
    void loadMetadata();
  }, [loadMetadata, refreshToken]);

  function beginEdit() {
    setDraft(saved);
    setArtistCreditDraft(savedArtistCredit);
    setArtistSearchQuery("");
    dirtyRef.current = false;
    setDirtyFields(new Set());
    setSaveError("");
    setRefreshConflict(false);
    setEditing(true);
  }

  function cancelEdit() {
    dirtyRef.current = false;
    setDirtyFields(new Set());
    setDraft(saved);
    setArtistCreditDraft(savedArtistCredit);
    setArtistSearchQuery("");
    setEditing(false);
    setSaveError("");
    if (refreshConflict) void loadMetadata(true);
    setRefreshConflict(false);
  }

  function updateField(
    key: MetadataFieldKey,
    next: { state: FieldState; value: MetadataValue },
  ) {
    if (writePending.current) return;
    dirtyRef.current = true;
    setDraft((current) => ({ ...current, [key]: next }));
    setDirtyFields((current) => new Set(current).add(key));
    setSaveError("");
  }

  function updateArtistCreditDraft(next: ArtistCreditDraft) {
    if (writePending.current) return;
    dirtyRef.current = true;
    setArtistCreditDraft(next);
    setDirtyFields((current) => new Set(current).add("artist_credit"));
    setSaveError("");
  }

  function notifySaved(response: LibraryMetadataResponse) {
    try {
      const refresh = onSavedRef.current?.(response);
      if (refresh) void Promise.resolve(refresh).catch(() => undefined);
    } catch {
      // A parent refresh callback cannot undo a committed metadata save.
    }
  }

  async function saveDraft() {
    if (!metadata || !dirtyFields.size || writePending.current) return;
    const generation = sessionGeneration.current;
    const isCurrentSession = () =>
      sessionGeneration.current === generation &&
      sessionIdentityRef.current === sessionIdentity;
    writePending.current = true;
    setSaving(true);
    setSaveError("");
    try {
      const changesEntries: [string, unknown][] = [...dirtyFields].flatMap(
        (key): [string, unknown][] => {
          if (key === "artist_credit") {
            const creditChange = serializeArtistCreditChange(artistCreditDraft);
            return creditChange ? [[key, creditChange]] : [];
          }
          const field = draft[key];
          const definition = fields.find((candidate) => candidate.key === key);
          let wireValue: unknown = field.value;
          if (field.state === "value" && definition) {
            if (definition.kind === "integer" || definition.kind === "number") {
              const number = Number(field.value);
              const rawValue = String(field.value);
              const validNumberSyntax =
                definition.kind === "integer"
                  ? /^\d+$/.test(rawValue)
                  : /^(?:\d+\.?\d*|\.\d+)(?:[eE][+-]?\d+)?$/.test(rawValue);
              if (
                !validNumberSyntax ||
                !Number.isFinite(number) ||
                number <= 0 ||
                (definition.kind === "integer" && !Number.isSafeInteger(number))
              ) {
                throw new Error(
                  definition.kind === "integer"
                    ? text.invalidPositiveInteger
                    : text.invalidPositiveNumber,
                );
              }
              wireValue = number;
            } else if (
              definition.kind === "date" &&
              !validPartialDate(String(field.value))
            ) {
              throw new Error(text.invalidDate);
            } else if (definition.kind === "works") {
              wireValue = (field.value as MetadataWorkDraft[]).map((work) => ({
                name: work.name,
                ...(work.mbid ? { mbid: work.mbid } : {}),
              }));
            } else if (definition.kind === "contributors") {
              wireValue = (field.value as ContributorDraft[]).map(
                (contributor) => ({
                  name: contributor.name,
                  role: contributor.role,
                  ...(contributor.instrument !== undefined
                    ? { instrument: contributor.instrument }
                    : {}),
                  ...(contributor.artist_id !== undefined
                    ? { artist_id: contributor.artist_id }
                    : {}),
                  ...(contributor.mbid !== undefined
                    ? { mbid: contributor.mbid }
                    : {}),
                }),
              );
            } else if (definition.kind === "movement") {
              const movement = field.value as MetadataMovementDraft;
              const number = positiveIntegerDraft(movement.number);
              const count = positiveIntegerDraft(movement.count);
              if (movement.name === "")
                throw new Error(text.invalidMovementName);
              if (number === undefined || count === undefined)
                throw new Error(text.invalidPositiveInteger);
              wireValue = { name: movement.name, number, count };
            }
          }
          return [
            [
              key,
              field.state === "value"
                ? { state: field.state, value: wireValue }
                : { state: field.state },
            ],
          ];
        },
      );
      const changes = Object.fromEntries(changesEntries);
      if (!Object.keys(changes).length) {
        dirtyRef.current = false;
        setDirtyFields(new Set());
        setDraft(saved);
        setArtistCreditDraft(savedArtistCredit);
        setArtistSearchQuery("");
        setEditing(false);
        return;
      }
      const body = {
        expected_revision: metadata.revision,
        changes,
      };
      const response =
        scope === "release"
          ? await patchLibraryReleaseMetadata(ownerId, body)
          : scope === "recording"
            ? await patchLibraryRecordingMetadata(ownerId, body)
            : await patchLibraryPositionMetadata(releaseId, ownerId, body);
      if (!isCurrentSession()) {
        // The PATCH may have committed after this editor was replaced. Notify
        // the parent so it can invalidate previews only if this release is
        // still active; never update the stale editor or start another read.
        if (response.status === 200) notifySaved(response.data);
        return;
      }
      if (response.status !== 200) {
        if (response.status === 409) setRefreshConflict(true);
        throw apiResponseError(response);
      }
      readController.current?.abort();
      readGeneration.current += 1;
      setLoading(false);
      const next = readValues(response.data, fields);
      metadataRevision.current = response.data.revision;
      setMetadata(response.data);
      setSaved(next);
      setDraft(next);
      setSavedArtistCredit(artistCreditDraft);
      setArtistCreditDraft(artistCreditDraft);
      dirtyRef.current = false;
      setDirtyFields(new Set());
      setRefreshConflict(false);
      setEditing(false);
      setArtistSearchQuery("");
      notifySaved(response.data);
      // The mutation is committed. A follow-up read must not turn it into a
      // reported save failure, and must not overwrite the committed revision.
      void loadMetadata(true);
    } catch (reason) {
      if (isCurrentSession()) setSaveError(errorMessage(reason, ""));
    } finally {
      if (isCurrentSession()) {
        writePending.current = false;
        setSaving(false);
      }
    }
  }

  return (
    <section
      aria-label={title}
      className="grid min-w-0 gap-2 rounded-[var(--radius)] border border-[var(--line)] bg-[var(--surface)] p-3 text-[var(--ink)]"
      onKeyDown={(event) => {
        if (event.key !== "Escape" || !editing || saving) return;
        event.preventDefault();
        event.stopPropagation();
        cancelEdit();
      }}
    >
      <div className="flex min-w-0 flex-wrap items-center justify-between gap-2">
        <h2 className="m-0 text-sm font-semibold">{title}</h2>
        <div className="grid grid-cols-[7.5rem_7.5rem] gap-2">
          <AppButton
            type="button"
            isDisabled={
              loading ||
              !!readError ||
              saving ||
              refreshConflict ||
              (editing && !dirtyFields.size)
            }
            onPress={() => (editing ? void saveDraft() : beginEdit())}
            className="min-w-0 justify-center"
          >
            {editing ? text.save : text.edit}
          </AppButton>
          <AppButton
            type="button"
            isDisabled={!editing || saving}
            aria-hidden={!editing}
            excludeFromTabOrder={!editing}
            onPress={cancelEdit}
            className={`min-w-0 justify-center ${editing ? "" : "invisible"}`}
          >
            {text.cancel}
          </AppButton>
        </div>
      </div>
      <div className="grid min-w-0 gap-2">
        {[...new Set(fields.map(({ group }) => group))].map((group) => (
          <details
            key={group}
            className="min-w-0 rounded border border-[var(--line)]"
          >
            <summary className="cursor-pointer px-3 py-2 text-sm font-medium">
              {text.groups[group as keyof typeof text.groups]}
            </summary>
            <dl className="m-0 grid min-w-0 grid-cols-1 gap-2 p-2 sm:grid-cols-2 lg:grid-cols-3">
              {fields
                .filter((field) => field.group === group)
                .map(({ key, kind }) => {
                  const field = editing ? draft[key] : saved[key];
                  const label = text.labels[key];
                  const rendered = Array.isArray(field.value)
                    ? field.value.join("\n")
                    : String(field.value);
                  const providerIdentifier =
                    scope === "release" &&
                    metadata &&
                    "provider_release_identifier_baseline" in metadata
                      ? key === "asin"
                        ? metadata.provider_release_identifier_baseline?.asin
                        : key === "script"
                          ? metadata.provider_release_identifier_baseline
                              ?.script
                          : undefined
                      : undefined;
                  const hasDesiredIdentifier =
                    metadata && Object.hasOwn(metadata.envelope.desired, key);
                  const inheritedKey =
                    key === "key" && field.state === "absent"
                      ? effectiveKey
                      : undefined;
                  return (
                    <div
                      key={key}
                      className="grid min-w-0 gap-1 rounded border border-[var(--line)] p-2"
                    >
                      <dt className="text-xs text-[var(--muted)]">{label}</dt>
                      <dd className="m-0 min-w-0 text-sm">
                        {kind === "contributors" ? (
                          <div className="grid min-w-0 gap-2">
                            {field.state === "absent" ? (
                              <span className="text-sm text-[var(--muted)]">
                                {text.noOverride}
                              </span>
                            ) : field.state === "explicit_empty" ? (
                              <span className="text-sm text-[var(--muted)]">
                                {text.explicitEmpty}
                              </span>
                            ) : (
                              <>
                                {(field.value as ContributorDraft[]).map(
                                  (contributor, index) => (
                                    <div
                                      key={contributor.uiKey}
                                      className="grid min-w-0 gap-2 rounded border border-[var(--line)] p-2"
                                    >
                                      {editing ? (
                                        <div className="grid min-w-0 gap-1 text-xs text-[var(--muted)]">
                                          <label
                                            htmlFor={`${contributorControlPrefix}-${contributor.uiKey}-name`}
                                          >
                                            {`${text.contributorName} ${index + 1}`}
                                          </label>
                                          <input
                                            id={`${contributorControlPrefix}-${contributor.uiKey}-name`}
                                            className="min-h-9 w-full min-w-0 rounded-[var(--control-radius)] border border-[var(--control)] bg-[var(--surface)] px-2 text-sm text-[var(--ink)]"
                                            value={contributor.name}
                                            disabled={saving}
                                            onChange={(event) => {
                                              const next = [
                                                ...(field.value as ContributorDraft[]),
                                              ];
                                              next[index] = {
                                                ...contributor,
                                                name: event.currentTarget.value,
                                              };
                                              updateField(key, {
                                                state: "value",
                                                value: next,
                                              });
                                            }}
                                          />
                                        </div>
                                      ) : (
                                        <div className="grid min-w-0 gap-1 text-xs text-[var(--muted)]">
                                          <span>{`${text.contributorName} ${index + 1}`}</span>
                                          <span className="min-h-9 break-words py-2 text-sm text-[var(--ink)]">
                                            {contributor.name ||
                                              text.emptyValue}
                                          </span>
                                        </div>
                                      )}
                                      {editing ? (
                                        <div className="grid min-w-0 gap-1 text-xs text-[var(--muted)]">
                                          <label
                                            htmlFor={`${contributorControlPrefix}-${contributor.uiKey}-role`}
                                          >
                                            {`${text.contributorRole} ${index + 1}`}
                                          </label>
                                          <select
                                            id={`${contributorControlPrefix}-${contributor.uiKey}-role`}
                                            className="min-h-9 w-full rounded-[var(--control-radius)] border border-[var(--control)] bg-[var(--surface)] px-2 text-sm text-[var(--ink)]"
                                            value={contributor.role}
                                            disabled={saving}
                                            onChange={(event) => {
                                              const role =
                                                event.currentTarget.value;
                                              const next = [
                                                ...(field.value as ContributorDraft[]),
                                              ];
                                              next[index] = {
                                                ...contributor,
                                                role,
                                              };
                                              updateField(key, {
                                                state: "value",
                                                value: next,
                                              });
                                            }}
                                          >
                                            <option value="">
                                              {text.contributorRole}
                                            </option>
                                            {contributor.role &&
                                            !Object.hasOwn(
                                              text.contributorRoles,
                                              contributor.role,
                                            ) ? (
                                              <option value={contributor.role}>
                                                {contributor.role}
                                              </option>
                                            ) : null}
                                            {Object.entries(
                                              text.contributorRoles,
                                            ).map(([role, roleLabel]) => (
                                              <option key={role} value={role}>
                                                {roleLabel}
                                              </option>
                                            ))}
                                          </select>
                                        </div>
                                      ) : (
                                        <div className="grid min-w-0 gap-1 text-xs text-[var(--muted)]">
                                          <span>{`${text.contributorRole} ${index + 1}`}</span>
                                          <span className="min-h-9 py-2 text-sm text-[var(--ink)]">
                                            {text.contributorRoles[
                                              contributor.role as keyof typeof text.contributorRoles
                                            ] ?? contributor.role}
                                          </span>
                                        </div>
                                      )}
                                      {editing ? (
                                        <div className="grid min-w-0 gap-1 text-xs text-[var(--muted)]">
                                          <label
                                            htmlFor={`${contributorControlPrefix}-${contributor.uiKey}-instrument`}
                                          >
                                            {`${text.contributorInstrument} ${index + 1}`}
                                          </label>
                                          <input
                                            id={`${contributorControlPrefix}-${contributor.uiKey}-instrument`}
                                            className="min-h-9 w-full min-w-0 rounded-[var(--control-radius)] border border-[var(--control)] bg-[var(--surface)] px-2 text-sm text-[var(--ink)]"
                                            value={contributor.instrument ?? ""}
                                            disabled={saving}
                                            onChange={(event) => {
                                              const next = [
                                                ...(field.value as ContributorDraft[]),
                                              ];
                                              next[index] = {
                                                ...contributor,
                                                instrument:
                                                  event.currentTarget.value ||
                                                  undefined,
                                              };
                                              updateField(key, {
                                                state: "value",
                                                value: next,
                                              });
                                            }}
                                          />
                                        </div>
                                      ) : (
                                        <div className="grid min-w-0 gap-1 text-xs text-[var(--muted)]">
                                          <span>{`${text.contributorInstrument} ${index + 1}`}</span>
                                          <span className="min-h-9 py-2 text-sm text-[var(--ink)]">
                                            {contributor.instrument ||
                                              text.emptyValue}
                                          </span>
                                        </div>
                                      )}
                                      {contributor.artist_id ? (
                                        <div className="grid min-w-0 gap-1 text-xs text-[var(--muted)]">
                                          <span className="text-sm text-[var(--ink)]">
                                            {text.artistLinkRetained}
                                          </span>
                                        </div>
                                      ) : null}
                                      {editing ? (
                                        <div className="grid min-w-0 gap-1 text-xs text-[var(--muted)]">
                                          <label
                                            htmlFor={`${contributorControlPrefix}-${contributor.uiKey}-mbid`}
                                          >
                                            {`${text.contributorMusicBrainzID} ${index + 1}`}
                                          </label>
                                          <input
                                            id={`${contributorControlPrefix}-${contributor.uiKey}-mbid`}
                                            className="min-h-9 w-full min-w-0 rounded-[var(--control-radius)] border border-[var(--control)] bg-[var(--surface)] px-2 text-sm text-[var(--ink)]"
                                            value={contributor.mbid ?? ""}
                                            disabled={saving}
                                            onChange={(event) => {
                                              const next = [
                                                ...(field.value as ContributorDraft[]),
                                              ];
                                              next[index] = {
                                                ...contributor,
                                                mbid:
                                                  event.currentTarget.value ||
                                                  undefined,
                                              };
                                              updateField(key, {
                                                state: "value",
                                                value: next,
                                              });
                                            }}
                                          />
                                        </div>
                                      ) : (
                                        <div className="grid min-w-0 gap-1 text-xs text-[var(--muted)]">
                                          <span>{`${text.contributorMusicBrainzID} ${index + 1}`}</span>
                                          <span className="min-h-9 break-words py-2 text-sm text-[var(--ink)]">
                                            {contributor.mbid ||
                                              text.emptyValue}
                                          </span>
                                        </div>
                                      )}
                                      {editing ? (
                                        <div className="flex flex-wrap items-end gap-1">
                                          <AppButton
                                            type="button"
                                            aria-label={text.moveContributorUp(
                                              index + 1,
                                            )}
                                            isDisabled={saving || index === 0}
                                            onPress={() => {
                                              const contributors = [
                                                ...(field.value as ContributorDraft[]),
                                              ];
                                              [
                                                contributors[index - 1],
                                                contributors[index],
                                              ] = [
                                                contributors[index],
                                                contributors[index - 1],
                                              ];
                                              updateField(key, {
                                                state: "value",
                                                value: contributors,
                                              });
                                            }}
                                            className="min-w-0 justify-center"
                                          >
                                            ↑
                                          </AppButton>
                                          <AppButton
                                            type="button"
                                            aria-label={text.moveContributorDown(
                                              index + 1,
                                            )}
                                            isDisabled={
                                              saving ||
                                              index ===
                                                (
                                                  field.value as ContributorDraft[]
                                                ).length -
                                                  1
                                            }
                                            onPress={() => {
                                              const contributors = [
                                                ...(field.value as ContributorDraft[]),
                                              ];
                                              [
                                                contributors[index],
                                                contributors[index + 1],
                                              ] = [
                                                contributors[index + 1],
                                                contributors[index],
                                              ];
                                              updateField(key, {
                                                state: "value",
                                                value: contributors,
                                              });
                                            }}
                                            className="min-w-0 justify-center"
                                          >
                                            ↓
                                          </AppButton>
                                          <AppButton
                                            type="button"
                                            isDisabled={saving}
                                            onPress={() =>
                                              updateField(key, {
                                                state: "value",
                                                value: (
                                                  field.value as ContributorDraft[]
                                                ).filter(
                                                  (_, row) => row !== index,
                                                ),
                                              })
                                            }
                                            className="min-w-0 justify-center"
                                          >
                                            {text.removeContributor(index + 1)}
                                          </AppButton>
                                        </div>
                                      ) : null}
                                    </div>
                                  ),
                                )}
                                {editing ? (
                                  <AppButton
                                    type="button"
                                    isDisabled={saving}
                                    onPress={() =>
                                      updateField(key, {
                                        state: "value",
                                        value: [
                                          ...(field.value as ContributorDraft[]),
                                          {
                                            uiKey: createMetadataDraftRowKey(),
                                            name: "",
                                            role: "",
                                          },
                                        ],
                                      })
                                    }
                                    className="min-w-0 justify-center justify-self-start"
                                  >
                                    {text.addContributor}
                                  </AppButton>
                                ) : (field.value as ContributorDraft[])
                                    .length === 0 ? (
                                  <span className="text-sm text-[var(--muted)]">
                                    {text.emptyValue}
                                  </span>
                                ) : null}
                              </>
                            )}
                            {editing ? (
                              <div className="grid min-w-0 gap-1 text-xs text-[var(--muted)]">
                                <label
                                  htmlFor={`${contributorControlPrefix}-${key}-state`}
                                >
                                  {text.mode}
                                </label>
                                <select
                                  id={`${contributorControlPrefix}-${key}-state`}
                                  aria-label={`${label} — ${text.mode}`}
                                  className="min-h-9 w-full rounded-[var(--control-radius)] border border-[var(--control)] bg-[var(--surface)] px-2 text-[var(--ink)]"
                                  value={field.state}
                                  disabled={saving}
                                  onChange={(event) =>
                                    updateField(key, {
                                      state: event.currentTarget
                                        .value as FieldState,
                                      value: field.value,
                                    })
                                  }
                                >
                                  <option value="absent">{text.absent}</option>
                                  <option value="value">{text.value}</option>
                                  <option value="explicit_empty">
                                    {text.explicitEmpty}
                                  </option>
                                </select>
                              </div>
                            ) : null}
                          </div>
                        ) : kind === "works" ? (
                          <StructuredMetadataField
                            kind="works"
                            label={label}
                            state={field.state}
                            value={field.value as MetadataWorkDraft[]}
                            editing={editing}
                            saving={saving}
                            modeLabel={text.mode}
                            absentLabel={text.absent}
                            valueLabel={text.value}
                            explicitEmptyLabel={text.explicitEmpty}
                            noOverrideLabel={text.noOverride}
                            emptyValueLabel={text.emptyValue}
                            workNameLabel={text.workName}
                            workMBIDLabel={text.workMBID}
                            movementNameLabel={text.movementName}
                            movementNumberLabel={text.movementNumber}
                            movementCountLabel={text.movementCount}
                            addWorkLabel={text.addWork}
                            moveWorkUpLabel={text.moveWorkUp}
                            moveWorkDownLabel={text.moveWorkDown}
                            removeWorkLabel={text.removeWork}
                            onStateChange={(state) =>
                              updateField(key, { state, value: field.value })
                            }
                            onValueChange={(value) =>
                              updateField(key, { state: "value", value })
                            }
                          />
                        ) : kind === "movement" ? (
                          <StructuredMetadataField
                            kind="movement"
                            label={label}
                            state={field.state}
                            value={field.value as MetadataMovementDraft}
                            editing={editing}
                            saving={saving}
                            modeLabel={text.mode}
                            absentLabel={text.absent}
                            valueLabel={text.value}
                            explicitEmptyLabel={text.explicitEmpty}
                            noOverrideLabel={text.noOverride}
                            emptyValueLabel={text.emptyValue}
                            workNameLabel={text.workName}
                            workMBIDLabel={text.workMBID}
                            movementNameLabel={text.movementName}
                            movementNumberLabel={text.movementNumber}
                            movementCountLabel={text.movementCount}
                            addWorkLabel={text.addWork}
                            moveWorkUpLabel={text.moveWorkUp}
                            moveWorkDownLabel={text.moveWorkDown}
                            removeWorkLabel={text.removeWork}
                            onStateChange={(state) =>
                              updateField(key, { state, value: field.value })
                            }
                            onValueChange={(value) =>
                              updateField(key, { state: "value", value })
                            }
                          />
                        ) : editing ? (
                          <div className="grid min-w-0 gap-1.5">
                            <label className="grid min-w-0 gap-1 text-xs text-[var(--muted)]">
                              <span>{text.mode}</span>
                              <select
                                aria-label={`${label} — ${text.mode}`}
                                className="min-h-9 w-full rounded-[var(--control-radius)] border border-[var(--control)] bg-[var(--surface)] px-2 text-[var(--ink)] focus-visible:outline-2 focus-visible:outline-[var(--focus)]"
                                value={field.state}
                                disabled={saving}
                                onChange={(event) =>
                                  updateField(key, {
                                    state: event.currentTarget
                                      .value as FieldState,
                                    value: field.value,
                                  })
                                }
                              >
                                <option value="absent">{text.absent}</option>
                                <option value="value">{text.value}</option>
                                <option value="explicit_empty">
                                  {text.explicitEmpty}
                                </option>
                              </select>
                            </label>
                            {field.state === "value" ? (
                              kind === "list" ? (
                                <textarea
                                  aria-label={label}
                                  className="min-h-20 w-full resize-y rounded-[var(--control-radius)] border border-[var(--control)] bg-[var(--surface)] p-2 text-sm text-[var(--ink)] focus-visible:outline-2 focus-visible:outline-[var(--focus)]"
                                  value={rendered}
                                  disabled={saving}
                                  onChange={(event) =>
                                    updateField(key, {
                                      state: "value",
                                      value:
                                        event.currentTarget.value.split("\n"),
                                    })
                                  }
                                />
                              ) : kind === "boolean" || kind === "advisory" ? (
                                <select
                                  aria-label={label}
                                  className="min-h-9 w-full rounded-[var(--control-radius)] border border-[var(--control)] bg-[var(--surface)] px-2 text-[var(--ink)] focus-visible:outline-2 focus-visible:outline-[var(--focus)]"
                                  value={String(field.value)}
                                  disabled={saving}
                                  onChange={(event) =>
                                    updateField(key, {
                                      state: "value",
                                      value:
                                        kind === "boolean"
                                          ? event.currentTarget.value === "true"
                                          : event.currentTarget.value,
                                    })
                                  }
                                >
                                  {kind === "boolean" ? (
                                    <>
                                      <option value="true">
                                        {text.value}: true
                                      </option>
                                      <option value="false">
                                        {text.value}: false
                                      </option>
                                    </>
                                  ) : (
                                    <>
                                      <option value="explicit">
                                        {text.explicit}
                                      </option>
                                      <option value="clean">
                                        {text.clean}
                                      </option>
                                    </>
                                  )}
                                </select>
                              ) : (
                                <input
                                  aria-label={label}
                                  className="min-h-9 w-full min-w-0 rounded-[var(--control-radius)] border border-[var(--control)] bg-[var(--surface)] px-2 text-sm text-[var(--ink)] focus-visible:outline-2 focus-visible:outline-[var(--focus)]"
                                  type="text"
                                  inputMode={
                                    kind === "integer"
                                      ? "numeric"
                                      : kind === "number"
                                        ? "decimal"
                                        : kind === "date"
                                          ? "text"
                                          : undefined
                                  }
                                  value={rendered}
                                  disabled={saving}
                                  onChange={(event) =>
                                    updateField(key, {
                                      state: "value",
                                      value: event.currentTarget.value,
                                    })
                                  }
                                />
                              )
                            ) : null}
                            {kind === "date" && field.state === "value" ? (
                              <span className="text-xs text-[var(--muted)]">
                                {text.dateHint}
                              </span>
                            ) : null}
                            {kind === "list" && field.state === "value" ? (
                              <span className="text-xs text-[var(--muted)]">
                                {text.listHint}
                              </span>
                            ) : null}
                          </div>
                        ) : (
                          <>
                            <span className="block whitespace-pre-wrap break-words">
                              {inheritedKey?.field.state === "value"
                                ? String(inheritedKey.field.value)
                                : inheritedKey?.field.state === "explicit_empty"
                                  ? text.explicitEmpty
                                  : field.state === "absent" &&
                                      !hasDesiredIdentifier &&
                                      providerIdentifier
                                    ? providerIdentifier
                                    : displayValue(
                                        field.state,
                                        field.value,
                                        text,
                                      )}
                            </span>
                            {inheritedKey?.scope === "recording" ? (
                              <span className="mt-1 block text-xs text-[var(--muted)]">
                                {text.inheritedFromRecording}
                              </span>
                            ) : null}
                            {field.state === "absent" &&
                            !hasDesiredIdentifier &&
                            providerIdentifier ? (
                              <span className="mt-1 block text-xs text-[var(--muted)]">
                                {text.providerBaseline}
                              </span>
                            ) : null}
                          </>
                        )}
                      </dd>
                    </div>
                  );
                })}
            </dl>
          </details>
        ))}
        <details className="min-w-0 rounded border border-[var(--line)]">
          <summary className="cursor-pointer px-3 py-2 text-sm font-medium">
            {text.groups.artistCredits}
          </summary>
          <div className="grid min-w-0 gap-2 p-2">
            <ArtistCreditRowsEditor
              draft={editing ? artistCreditDraft : savedArtistCredit}
              savedHasDesiredOverride={savedArtistCredit.hasDesiredOverride}
              baseline={artistCreditBaseline}
              effective={effectiveArtistCredit}
              searchQuery={artistSearchQuery}
              searchResults={artistCreditSearchResults}
              searchLoading={artistSearch.isFetching}
              searchError={artistSearchError}
              editing={editing}
              disabled={!editing || saving}
              onDraftChange={updateArtistCreditDraft}
              onSearchQueryChange={setArtistSearchQuery}
              onRetrySearch={() => void artistSearch.refetch()}
            />
          </div>
        </details>
      </div>
      {loading ? (
        <p role="status" className="m-0 text-sm text-[var(--muted)]">
          {text.loading}
        </p>
      ) : null}
      {saving ? (
        <p role="status" className="m-0 text-sm text-[var(--muted)]">
          {text.saving}
        </p>
      ) : null}
      {readError ? (
        <div className="flex flex-wrap items-center gap-2">
          <p role="alert" className="m-0 text-sm text-[var(--error)]">
            {text.readError} {readError}
          </p>
          <AppButton
            type="button"
            isDisabled={loading || saving}
            onPress={() => void loadMetadata(true)}
            className="min-w-0 justify-center"
          >
            {text.retry}
          </AppButton>
        </div>
      ) : null}
      {saveError ? (
        <p role="alert" className="m-0 text-sm text-[var(--error)]">
          {refreshConflict ? text.conflict : text.saveError} {saveError}{" "}
          {text.draftRetained}
        </p>
      ) : null}
      {refreshConflict && !saveError ? (
        <p role="alert" className="m-0 text-sm text-[var(--error)]">
          {text.conflict}
        </p>
      ) : null}
    </section>
  );
}
