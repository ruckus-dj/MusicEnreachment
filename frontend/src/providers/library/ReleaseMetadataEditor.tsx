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
  ProviderGenreBaseline,
} from "../../api/generated/client.schemas";
import { useI18n } from "../../app/i18n/host";
import { AppButton } from "../../ui/components/AppButton";
import { genreMessages } from "../../ui/features/library/genreMessages";
import {
  libraryTagComparisonFieldLabel,
  navidromeFields,
} from "../../ui/features/library/libraryTagComparisonFields";
import {
  type MetadataEditorLabels,
  type MetadataEditorRolesView,
  type MetadataEditorRoleView,
  MetadataEditorView,
  type MetadataEditorWorksMovementView,
} from "../../ui/features/library/MetadataEditorView";
import {
  type MetadataFieldDescriptor,
  metadataFilledSummary,
  metadataListActions,
} from "../../ui/features/library/MetadataFieldPresentation";
import { ArtistCreditRowsEditorView } from "../../ui/features/library/metadata/ArtistCreditRowsEditorView";
import type {
  ArtistCreditDraft,
  ArtistCreditResolutionStatus,
  ArtistCreditRow,
} from "../../ui/features/library/metadata/artistCreditDraft";
import { artistCreditMessages } from "../../ui/features/library/metadata/artistCreditMessages";
import {
  focusMetadataAction,
  focusMetadataKey,
} from "../../ui/features/library/metadata/metadataEditorFocus";
import { ProviderCatalogGenreField } from "../local-release/ProviderCatalogGenreField";
import {
  adaptArtistCreditMetadata,
  serializeArtistCreditChange,
} from "./artistCreditDraft";
import type { GenreReference } from "./GenreMetadataEditor";
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
  | MetadataMovementDraft
  | GenreReference[];
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
    | "movement"
    | "genres";
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
  { key: "genre", kind: "genres", group: "release" },
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

const contributorRoleOrder = navidromeFields.flatMap((field) =>
  field.kind === "contributors" && field.role ? [field.role] : [],
);

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
          kind === "list" ||
          kind === "works" ||
          kind === "contributors" ||
          kind === "genres"
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
      kind === "list" ||
      kind === "works" ||
      kind === "contributors" ||
      kind === "genres"
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
      else if (kind === "genres") value = readGenreReferences(field.value);
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

function readGenreReferences(value: unknown): GenreReference[] {
  if (!Array.isArray(value)) return [];
  return value.flatMap((item) => {
    if (!item || typeof item !== "object" || Array.isArray(item)) return [];
    const genre = item as Record<string, unknown>;
    if (
      typeof genre.id !== "string" ||
      typeof genre.name !== "string" ||
      typeof genre.source_name !== "string"
    )
      return [];
    return [{ id: genre.id, name: genre.name, source_name: genre.source_name }];
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
  visible = true,
}: {
  releaseId: string;
  refreshToken?: number | string;
  onSaved?: (metadata: LibraryMetadataResponse) => void | Promise<void>;
  visible?: boolean;
}) {
  return (
    <LibraryScopedMetadataEditorSession
      key={releaseId}
      scope="release"
      fields={releaseFields}
      ownerId={releaseId}
      refreshToken={refreshToken}
      onSaved={onSaved}
      visible={visible}
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
  visible?: boolean;
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
  visible = true,
}: LibraryScopedMetadataEditorSessionProps) {
  const { locale } = useI18n();
  const contributorControlPrefix = useId();
  const [newContributorKeys, setNewContributorKeys] = useState<Set<string>>(
    () => new Set(),
  );
  const [contributorFocus, setContributorFocus] = useState<{
    uiKey: string;
    field: "name" | "role";
  } | null>(null);
  const contributorAddBaseline = useRef<{
    state: FieldState;
    value: ContributorDraft[];
    dirty: boolean;
  } | null>(null);
  const contributorModeTouched = useRef(false);
  const text = copy[locale];
  const genreCopy = genreMessages[locale];
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
  const actionsRef = useRef<HTMLDivElement>(null);
  const editorRef = useRef<HTMLElement>(null);
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
  const [providerGenreBaseline, setProviderGenreBaseline] =
    useState<ProviderGenreBaseline>();
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

  const loadMetadata = useCallback(async () => {
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
        metadataRevision.current !== undefined &&
        response.data.revision < metadataRevision.current
      ) {
        return;
      }
      if (
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
      setProviderGenreBaseline(response.data.provider_genre_baseline);
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
  }, [fields, ownerId, releaseId, scope]);

  useEffect(() => {
    const generation = ++sessionGeneration.current;
    void loadMetadata();
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
    setNewContributorKeys(new Set());
    setContributorFocus(null);
    contributorAddBaseline.current = null;
    contributorModeTouched.current = false;
    setSaveError("");
    setRefreshConflict(false);
    setEditing(true);
  }

  function cancelEdit() {
    dirtyRef.current = false;
    setDirtyFields(new Set());
    setNewContributorKeys(new Set());
    setContributorFocus(null);
    contributorAddBaseline.current = null;
    contributorModeTouched.current = false;
    setDraft(saved);
    setArtistCreditDraft(savedArtistCredit);
    setArtistSearchQuery("");
    setEditing(false);
    setSaveError("");
    if (refreshConflict) void loadMetadata();
    setRefreshConflict(false);
    focusMetadataAction(actionsRef, editorRef);
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
    const nameless = activeContributorRoles
      .flatMap(({ indices }) => indices)
      .map((index) => contributors[index])
      .find(
        (contributor) =>
          newContributorKeys.has(contributor.uiKey) && !contributor.name.trim(),
      );
    if (nameless) {
      setSaveError("");
      focusMetadataKey(
        editorRef,
        `${contributorControlPrefix}-${nameless.uiKey}-name`,
      );
      return;
    }
    const generation = sessionGeneration.current;
    let invalidKey: string | undefined;
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
                invalidKey = key;
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
              invalidKey = key;
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
                  ...(contributor.instrument?.trim()
                    ? { instrument: contributor.instrument }
                    : {}),
                  ...(contributor.artist_id !== undefined
                    ? { artist_id: contributor.artist_id }
                    : {}),
                  ...(contributor.mbid?.trim()
                    ? { mbid: contributor.mbid }
                    : {}),
                }),
              );
            } else if (definition.kind === "movement") {
              const movement = field.value as MetadataMovementDraft;
              const number = positiveIntegerDraft(movement.number);
              const count = positiveIntegerDraft(movement.count);
              if (movement.name === "") {
                invalidKey = "movementname";
                throw new Error(text.invalidMovementName);
              }
              if (number === undefined || count === undefined) {
                invalidKey =
                  number === undefined ? "movement" : "movementtotal";
                throw new Error(text.invalidPositiveInteger);
              }
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
        setNewContributorKeys(new Set());
        setContributorFocus(null);
        contributorAddBaseline.current = null;
        contributorModeTouched.current = false;
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
      setNewContributorKeys(new Set());
      setContributorFocus(null);
      contributorAddBaseline.current = null;
      contributorModeTouched.current = false;
      setRefreshConflict(false);
      setEditing(false);
      setArtistSearchQuery("");
      notifySaved(response.data);
      // The mutation is committed. A follow-up read must not turn it into a
      // reported save failure, and must not overwrite the committed revision.
      void loadMetadata();
    } catch (reason) {
      if (isCurrentSession()) {
        setSaveError(errorMessage(reason, ""));
        if (invalidKey && invalidKey !== "contributors")
          focusMetadataKey(editorRef, invalidKey);
      }
    } finally {
      if (isCurrentSession()) {
        writePending.current = false;
        setSaving(false);
      }
    }
  }

  const basicFieldKeys: MetadataFieldKey[] =
    scope === "release"
      ? [
          "title",
          "release_date",
          "release_country",
          "label",
          "catalog_number",
          "release_primary_type",
          "release_secondary_types",
        ]
      : scope === "recording"
        ? ["title", "content_advisory", "isrc"]
        : ["track_number", "disc_number"];
  const basicFieldKeySet = new Set(basicFieldKeys);
  const scalarFieldKinds = new Set<ReleaseField["kind"]>([
    "text",
    "date",
    "list",
    "boolean",
    "integer",
    "number",
    "advisory",
  ]);
  const fieldDescriptor = ({
    key,
    kind,
  }: ReleaseField):
    | (MetadataFieldDescriptor & { populated: boolean })
    | undefined => {
    if (!scalarFieldKinds.has(kind)) return undefined;
    const field = editing ? draft[key] : saved[key];
    const label = text.labels[key as Exclude<ReleaseFieldKey, "genre">];
    const providerIdentifier =
      scope === "release" &&
      metadata &&
      "provider_release_identifier_baseline" in metadata
        ? key === "asin"
          ? metadata.provider_release_identifier_baseline?.asin
          : key === "script"
            ? metadata.provider_release_identifier_baseline?.script
            : undefined
        : undefined;
    const hasDesiredIdentifier =
      metadata && Object.hasOwn(metadata.envelope.desired, key);
    const inheritedKey =
      key === "key" && field.state === "absent" ? effectiveKey : undefined;
    const shownProviderIdentifier =
      field.state === "absent" && !hasDesiredIdentifier
        ? providerIdentifier || undefined
        : undefined;
    const notices: string[] = [];
    if (inheritedKey?.scope === "recording") {
      notices.push(text.inheritedFromRecording);
    }
    if (shownProviderIdentifier) {
      notices.push(text.providerBaseline);
    }
    const display =
      inheritedKey?.field.state === "value"
        ? String(inheritedKey.field.value)
        : inheritedKey?.field.state === "explicit_empty"
          ? text.explicitEmpty
          : (shownProviderIdentifier ??
            displayValue(field.state, field.value, text));
    const populated = inheritedKey
      ? inheritedKey.field.state === "value" &&
        Boolean(inheritedKey.field.value)
      : Boolean(shownProviderIdentifier) ||
        (field.state === "value" &&
          (Array.isArray(field.value)
            ? field.value.some(Boolean)
            : typeof field.value === "boolean" || Boolean(field.value)));
    const value =
      kind === "list"
        ? (field.value as string[])
        : kind === "boolean"
          ? String(field.value)
          : String(field.value);
    const choices =
      kind === "boolean"
        ? [
            { value: "true", label: `${text.value}: true` },
            { value: "false", label: `${text.value}: false` },
          ]
        : kind === "advisory"
          ? [
              { value: "explicit", label: text.explicit },
              { value: "clean", label: text.clean },
            ]
          : undefined;
    return {
      key,
      label,
      kind:
        kind === "advisory" ? kind : (kind as MetadataFieldDescriptor["kind"]),
      value,
      editing,
      disabled: saving,
      displayValue: display,
      populated,
      state: field.state,
      mode: {
        label: text.mode,
        absent: text.absent,
        value: text.value,
        explicitEmpty: text.explicitEmpty,
      },
      choices,
      hint:
        editing && field.state === "value" && kind === "date"
          ? text.dateHint
          : undefined,
      inputMode:
        kind === "integer"
          ? "numeric"
          : kind === "number"
            ? "decimal"
            : kind === "date"
              ? "text"
              : undefined,
      dataKeys: [key],
      notices,
      listActions:
        kind === "list" ? metadataListActions(locale, label) : undefined,
      onChange: (next) => {
        const value =
          kind === "boolean" ? next === "true" : (next as MetadataValue);
        updateField(key, { state: "value", value });
      },
      onStateChange: (state) => updateField(key, { state, value: field.value }),
    };
  };
  const contributorsField = fields.find(
    (field) => field.kind === "contributors",
  );
  const contributorsState = contributorsField
    ? (editing ? draft : saved)[contributorsField.key]
    : undefined;
  const contributors =
    contributorsState?.state === "value"
      ? (contributorsState.value as ContributorDraft[])
      : [];
  const contributorsByRole = new Map<string, number[]>();
  for (const [index, { role }] of contributors.entries()) {
    const indices = contributorsByRole.get(role);
    if (indices) indices.push(index);
    else contributorsByRole.set(role, [index]);
  }
  const activeContributorRoles = Array.from(
    new Set([...contributorRoleOrder, ...contributorsByRole.keys()]),
  ).flatMap((role) => {
    const indices = contributorsByRole.get(role);
    return indices ? [{ role, indices }] : [];
  });
  const contributorRoleLabel = (role: string) =>
    Object.hasOwn(text.contributorRoles, role)
      ? libraryTagComparisonFieldLabel(role, locale)
      : role || text.contributorRole;
  const updateContributors = (value: ContributorDraft[]) => {
    if (!contributorsField) return;
    updateField(contributorsField.key, { state: "value", value });
  };
  const sameContributorRows = (
    left: ContributorDraft[],
    right: ContributorDraft[],
  ) =>
    left.length === right.length &&
    left.every((row, index) => {
      const other = right[index];
      return (
        other !== undefined &&
        row.uiKey === other.uiKey &&
        row.name === other.name &&
        row.role === other.role &&
        row.instrument === other.instrument &&
        row.artist_id === other.artist_id &&
        row.mbid === other.mbid
      );
    });
  const restoreContributorBaseline = () => {
    if (!contributorsField) return;
    const baseline = contributorAddBaseline.current;
    if (!baseline) return;
    contributorAddBaseline.current = null;
    contributorModeTouched.current = false;
    setNewContributorKeys(new Set());
    setContributorFocus(null);
    setDraft((current) => ({
      ...current,
      [contributorsField.key]: {
        state: baseline.state,
        value: baseline.value,
      },
    }));
    setDirtyFields((current) => {
      const next = new Set(current);
      if (baseline.dirty) next.add(contributorsField.key);
      else next.delete(contributorsField.key);
      dirtyRef.current = next.size > 0;
      return next;
    });
    setSaveError("");
  };
  const replaceContributor = (
    index: number,
    changes: Partial<ContributorDraft>,
  ) =>
    updateContributors(
      contributors.map((row, rowIndex) =>
        rowIndex === index ? { ...row, ...changes } : row,
      ),
    );
  const moveContributorWithinRole = (
    indices: number[],
    roleIndex: number,
    delta: number,
  ) => {
    const source = indices[roleIndex];
    const target = indices[roleIndex + delta];
    if (source === undefined || target === undefined) return;
    const next = [...contributors];
    [next[source], next[target]] = [next[target], next[source]];
    updateContributors(next);
  };
  const addContributor = (role: string) => {
    if (writePending.current || !contributorsField) return;
    if (!contributorAddBaseline.current) {
      contributorAddBaseline.current = {
        state: contributorsState?.state ?? "absent",
        value: contributors.map((contributor) => ({ ...contributor })),
        dirty: dirtyFields.has(contributorsField.key),
      };
      contributorModeTouched.current = false;
    }
    const uiKey = createMetadataDraftRowKey();
    setNewContributorKeys((current) => new Set(current).add(uiKey));
    setContributorFocus({ uiKey, field: "name" });
    updateContributors([...contributors, { uiKey, name: "", role }]);
  };
  const removeContributor = (index: number) => {
    if (writePending.current || !contributorsField) return;
    const uiKey = contributors[index]?.uiKey;
    if (uiKey) {
      setNewContributorKeys((current) => {
        const next = new Set(current);
        next.delete(uiKey);
        return next;
      });
    }
    const next = contributors.filter((_, row) => row !== index);
    const baseline = contributorAddBaseline.current;
    if (
      baseline &&
      !contributorModeTouched.current &&
      sameContributorRows(next, baseline.value)
    ) {
      restoreContributorBaseline();
      return;
    }
    updateContributors(next);
  };
  const contributorRoleControl = (
    contributor: ContributorDraft,
    index: number,
  ) => (
    <div className="grid min-w-0 gap-1 text-xs text-[var(--muted)]">
      <label htmlFor={`${contributorControlPrefix}-${contributor.uiKey}-role`}>
        {`${text.contributorRole} ${index + 1}`}
      </label>
      <select
        id={`${contributorControlPrefix}-${contributor.uiKey}-role`}
        className="min-h-9 w-full rounded-[var(--control-radius)] border border-[var(--control)] bg-[var(--surface)] px-2 text-sm text-[var(--ink)]"
        value={contributor.role}
        disabled={saving}
        onChange={(event) => {
          const role = event.currentTarget.value;
          if (role === contributor.role) return;
          replaceContributor(index, { role });
          setContributorFocus({ uiKey: contributor.uiKey, field: "role" });
        }}
      >
        <option value="">{text.contributorRole}</option>
        {contributor.role &&
        !Object.hasOwn(text.contributorRoles, contributor.role) ? (
          <option value={contributor.role}>{contributor.role}</option>
        ) : null}
        {Object.entries(text.contributorRoles).map(([role, roleLabel]) => (
          <option key={role} value={role}>
            {roleLabel}
          </option>
        ))}
      </select>
    </div>
  );
  const rolesView: MetadataEditorRolesView | undefined = contributorsField
    ? {
        label: text.groups.contributors,
        summary: metadataFilledSummary(
          locale,
          contributors.filter((contributor) => contributor.name.trim()).length,
        ),
        dataKeys: [contributorsField.key],
        roles: activeContributorRoles.map(
          ({ role, indices }): MetadataEditorRoleView => ({
            role,
            label: contributorRoleLabel(role),
            contributorCount: indices.length,
            editing,
            contributors: indices.map((index) => {
              const contributor = contributors[index];
              const required = newContributorKeys.has(contributor.uiKey);
              const nameInputId = `${contributorControlPrefix}-${contributor.uiKey}-name`;
              const nameError =
                required && !contributor.name.trim()
                  ? text.contributorNameRequired
                  : undefined;
              return {
                uiKey: contributor.uiKey,
                name: contributor.name,
                ...(contributor.mbid !== undefined
                  ? { mbid: contributor.mbid }
                  : {}),
                ...(contributor.instrument !== undefined
                  ? { instrument: contributor.instrument }
                  : {}),
                nameKey: contributorsField.key,
                nameInputId,
                ...(required ? { nameRequired: true } : {}),
                ...(nameError ? { nameError } : {}),
                mbidKey: contributorsField.key,
                showInstrument:
                  editing ||
                  role === "performer" ||
                  contributor.instrument !== undefined,
                roleControl: editing
                  ? contributorRoleControl(contributor, index)
                  : undefined,
                artistLink: contributor.artist_id ? (
                  <div className="grid min-w-0 gap-1 text-xs text-[var(--muted)]">
                    <span className="text-sm text-[var(--ink)]">
                      {text.artistLinkRetained}
                    </span>
                  </div>
                ) : undefined,
              };
            }),
            onChange: (roleIndex, patch) =>
              replaceContributor(indices[roleIndex], patch),
            onMove: (roleIndex, delta) =>
              moveContributorWithinRole(indices, roleIndex, delta),
            onRemove: (roleIndex) => removeContributor(indices[roleIndex]),
            onAdd: () => addContributor(role),
          }),
        ),
        roleOptions: contributorRoleOrder
          .filter((role) => !contributorsByRole.has(role))
          .map((role) => ({ role, label: contributorRoleLabel(role) })),
        addRoleLabel: text.addRole,
        selectRoleLabel: text.selectRole,
        editing,
        onAddRole: (role) => addContributor(role),
        modeControl: editing ? (
          <div className="grid min-w-0 gap-1 text-xs text-[var(--muted)]">
            <label
              htmlFor={`${contributorControlPrefix}-${contributorsField.key}-state`}
            >
              {text.mode}
            </label>
            <select
              id={`${contributorControlPrefix}-${contributorsField.key}-state`}
              aria-label={`${text.groups.contributors} — ${text.mode}`}
              className="min-h-9 w-full rounded-[var(--control-radius)] border border-[var(--control)] bg-[var(--surface)] px-2 text-[var(--ink)]"
              value={contributorsState?.state ?? "absent"}
              disabled={saving}
              onChange={(event) => {
                contributorModeTouched.current = true;
                updateField(contributorsField.key, {
                  state: event.currentTarget.value as FieldState,
                  value: contributorsState?.value ?? [],
                });
              }}
            >
              <option value="absent">{text.absent}</option>
              <option value="value">{text.value}</option>
              <option value="explicit_empty">{text.explicitEmpty}</option>
            </select>
          </div>
        ) : undefined,
      }
    : undefined;
  const rolesViewWithEmpty = rolesView
    ? {
        ...rolesView,
        emptyText:
          contributorsState?.state === "explicit_empty"
            ? text.explicitEmpty
            : contributorsState?.state === "absent"
              ? text.noOverride
              : text.emptyValue,
      }
    : undefined;

  const renderWorksMovementField = (key: "works" | "movement") => {
    const field = editing ? draft[key] : saved[key];
    const props = {
      locale,
      label: text.labels[key],
      state: field.state,
      editing,
      saving,
      modeLabel: text.mode,
      absentLabel: text.absent,
      valueLabel: text.value,
      explicitEmptyLabel: text.explicitEmpty,
      noOverrideLabel: text.noOverride,
      emptyValueLabel: text.emptyValue,
      onStateChange: (state: FieldState) =>
        updateField(key, { state, value: field.value }),
      onValueChange: (value: MetadataWorkDraft[] | MetadataMovementDraft) =>
        updateField(key, { state: "value", value }),
    };
    return key === "works" ? (
      <StructuredMetadataField
        {...props}
        kind="works"
        value={field.value as MetadataWorkDraft[]}
      />
    ) : (
      <StructuredMetadataField
        {...props}
        kind="movement"
        value={field.value as MetadataMovementDraft}
      />
    );
  };

  const genres = scope === "release" ? (editing ? draft : saved).genre : null;
  const genreValue = genres?.value as GenreReference[] | undefined;
  const genreFields = fields.filter(({ key }) => key !== "genre");
  const scalarDescriptors = (items: ReleaseField[]) =>
    items.flatMap((field) => {
      const descriptor = fieldDescriptor(field);
      return descriptor ? [descriptor] : [];
    });
  const groupNames = [
    ...new Set(
      genreFields
        .filter(({ key }) => !basicFieldKeySet.has(key))
        .map(({ group }) => group),
    ),
  ];
  function worksMovementSummary() {
    const values = editing ? draft : saved;
    let populated = 0;
    if (values.works?.state === "value") {
      const works = values.works.value as MetadataWorkDraft[];
      if (works.some((work) => Boolean(work.name))) populated += 1;
      if (works.some((work) => Boolean(work.mbid))) populated += 1;
    }
    if (values.movement?.state === "value") {
      const movement = values.movement.value as MetadataMovementDraft;
      if (movement.name) populated += 1;
      if (movement.count) populated += 1;
      if (movement.number) populated += 1;
    }
    return metadataFilledSummary(locale, populated);
  }
  const worksMovementFields = genreFields.filter(
    ({ group }) => group === "worksMovement",
  );
  const worksMovementView: MetadataEditorWorksMovementView | undefined =
    worksMovementFields.length
      ? {
          label: text.groups.worksMovement,
          summary: worksMovementSummary(),
          worksLabel: text.labels.works,
          movementLabel: text.labels.movement,
          worksDataKeys: worksMovementFields.some(({ key }) => key === "works")
            ? ["work", "musicbrainz_workid"]
            : undefined,
          movementDataKeys: worksMovementFields.some(
            ({ key }) => key === "movement",
          )
            ? ["movementname", "movementtotal", "movement"]
            : [],
          worksContent: worksMovementFields.some(({ key }) => key === "works")
            ? renderWorksMovementField("works")
            : undefined,
          movementContent: worksMovementFields.some(
            ({ key }) => key === "movement",
          )
            ? renderWorksMovementField("movement")
            : undefined,
        }
      : undefined;
  const sections = groupNames
    .filter((group) => group !== "worksMovement" && group !== "contributors")
    .flatMap((group) => {
      const groupFields = genreFields.filter(
        (field) => field.group === group && !basicFieldKeySet.has(field.key),
      );
      const descriptors = scalarDescriptors(groupFields);
      if (!descriptors.length) return [];
      return [
        {
          key: group,
          label: text.groups[group as keyof typeof text.groups],
          summary: metadataFilledSummary(
            locale,
            descriptors.filter((field) => field.populated).length,
          ),
          fields: descriptors,
        },
      ];
    });
  const editorLabels: MetadataEditorLabels = {
    foreignFields: "",
    noCredits: "",
    linkedArtist: "",
    unlinked: "",
    creditName: "",
    artistMusicBrainz: "",
    joinPhrase: "",
    artistSort: "",
    moveUp: text.moveUp,
    moveDown: text.moveDown,
    remove: text.remove,
    addArtist: "",
    contributorName: text.contributorName,
    contributorMusicBrainz: text.contributorMusicBrainzID,
    instrument: text.contributorInstrument,
    addContributor: text.addContributor,
    addRole: text.addRole,
    selectRole: text.selectRole,
    nameRequired: text.contributorNameRequired,
  };

  useEffect(() => {
    if (!visible || !contributorFocus) return;
    const element = document.getElementById(
      `${contributorControlPrefix}-${contributorFocus.uiKey}-${contributorFocus.field}`,
    );
    if (element instanceof HTMLElement) {
      element.focus();
      setContributorFocus(null);
    }
  }, [contributorControlPrefix, contributorFocus, visible]);

  // Keep revisions and drafts route-scoped, but detach portaled pickers when
  // this editor is not on the active surface.
  if (!visible) return null;

  return (
    <MetadataEditorView
      locale={locale}
      labels={editorLabels}
      label={title}
      editing={editing}
      saving={saving}
      readOnly={false}
      editOrSaveDisabled={
        loading ||
        !!readError ||
        refreshConflict ||
        (editing && !dirtyFields.size)
      }
      hideActions={false}
      editorRef={editorRef}
      actionsRef={actionsRef}
      onEditOrSave={() => (editing ? void saveDraft() : beginEdit())}
      onCancel={cancelEdit}
      onKeyDown={(event) => {
        if (event.key !== "Escape" || !editing || saving) return;
        event.preventDefault();
        event.stopPropagation();
        cancelEdit();
      }}
      showForeignFieldsNotice={false}
      basics={{
        fields: scalarDescriptors(
          genreFields.filter(({ key }) => basicFieldKeySet.has(key)),
        ),
        genreKey: "genre",
        genreControl:
          genres && genreValue ? (
            <section
              aria-label={genreCopy.label.release}
              className="grid min-w-0 gap-2 rounded border border-[var(--line)] p-2"
            >
              <h4 className="m-0 text-sm font-semibold">
                {genreCopy.label.release}
              </h4>
              {editing ? (
                <ProviderCatalogGenreField
                  locale={locale}
                  owner="release"
                  value={genreValue}
                  onChange={(value) =>
                    updateField("genre", {
                      state: value.length ? "value" : "explicit_empty",
                      value,
                    })
                  }
                  disabled={saving}
                />
              ) : (
                genreValue.length > 0 && (
                  <ul className="m-0 flex list-none flex-wrap gap-2 p-0 text-sm">
                    {genreValue.map((genre) => (
                      <li
                        className="rounded-full border border-[var(--line)] px-2 py-0.5"
                        key={genre.id}
                      >
                        {genre.name}
                      </li>
                    ))}
                  </ul>
                )
              )}
              {!genreValue.length && (
                <p className="m-0 text-sm text-[var(--muted)]">
                  {genres.state === "explicit_empty"
                    ? genreCopy.explicitlyCleared
                    : genreCopy.noOverride}
                </p>
              )}
              {editing && (
                <div className="flex flex-wrap gap-2">
                  <AppButton
                    type="button"
                    isDisabled={saving}
                    onPress={() =>
                      updateField("genre", {
                        state: "explicit_empty",
                        value: [],
                      })
                    }
                  >
                    {genreCopy.clear}
                  </AppButton>
                  <AppButton
                    type="button"
                    isDisabled={saving}
                    onPress={() =>
                      updateField("genre", { state: "absent", value: [] })
                    }
                  >
                    {genreCopy.inherit}
                  </AppButton>
                </div>
              )}
            </section>
          ) : undefined,
      }}
      providerGenreBaseline={
        scope === "release" ? (providerGenreBaseline ?? null) : undefined
      }
      artistCredits={
        <details className="min-w-0 rounded border border-[var(--line)]">
          <summary className="cursor-pointer px-3 py-2 text-sm font-medium">
            {text.groups.artistCredits}
          </summary>
          <div className="grid min-w-0 gap-2 p-2">
            <ArtistCreditRowsEditorView
              locale={locale}
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
      }
      roles={rolesViewWithEmpty}
      worksMovement={worksMovementView}
      sections={sections}
      lyrics={[]}
      notices={
        <>
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
                onPress={() => void loadMetadata()}
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
        </>
      }
    />
  );
}
