import { useCallback, useEffect, useRef, useState } from "react";
import { apiResponseError, errorMessage } from "../../api/client/errors";
import {
  getLibraryReleaseMetadata,
  patchLibraryReleaseMetadata,
} from "../../api/generated/client";
import type { LibraryMetadataResponse } from "../../api/generated/client.schemas";
import { AppButton } from "../../components/AppButton";
import { useI18n } from "../../i18n/I18nProvider";

type FieldState = "absent" | "value" | "explicit_empty";
type MetadataValue = string | string[];
type ReleaseFieldKey =
  | "title"
  | "sort_title"
  | "release_date"
  | "original_release_date"
  | "release_country"
  | "label"
  | "catalog_number"
  | "release_primary_type"
  | "release_secondary_types";
type ReleaseField = {
  key: ReleaseFieldKey;
  kind: "text" | "date" | "list";
};

const releaseFields: ReleaseField[] = [
  { key: "title", kind: "text" },
  { key: "sort_title", kind: "text" },
  { key: "release_date", kind: "date" },
  { key: "original_release_date", kind: "date" },
  { key: "release_country", kind: "text" },
  { key: "label", kind: "list" },
  { key: "catalog_number", kind: "text" },
  { key: "release_primary_type", kind: "text" },
  { key: "release_secondary_types", kind: "list" },
];

const copy = {
  en: {
    title: "Release metadata",
    labels: {
      title: "Title",
      sort_title: "Sort title",
      release_date: "Release date",
      original_release_date: "Original release date",
      release_country: "Country",
      label: "Labels",
      catalog_number: "Catalog number",
      release_primary_type: "Primary release type",
      release_secondary_types: "Secondary release types",
    } satisfies Record<ReleaseFieldKey, string>,
    noOverride: "Inherited · no local override",
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
  },
  ru: {
    title: "Метаданные релиза",
    labels: {
      title: "Название",
      sort_title: "Сортировочное название",
      release_date: "Дата выпуска",
      original_release_date: "Дата первого выпуска",
      release_country: "Страна",
      label: "Лейблы",
      catalog_number: "Номер каталога",
      release_primary_type: "Основной тип релиза",
      release_secondary_types: "Дополнительные типы релиза",
    } satisfies Record<ReleaseFieldKey, string>,
    noOverride: "Наследуется · локальное значение не задано",
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
  },
} as const;

type EditorValues = Record<
  ReleaseFieldKey,
  { state: FieldState; value: MetadataValue }
>;

function emptyValues(): EditorValues {
  return Object.fromEntries(
    releaseFields.map(({ key, kind }) => [
      key,
      { state: "absent", value: kind === "list" ? [] : "" },
    ]),
  ) as EditorValues;
}

function readValues(metadata: LibraryMetadataResponse): EditorValues {
  const values = emptyValues();
  for (const { key, kind } of releaseFields) {
    const field = metadata.envelope.desired[key];
    if (!field || !["absent", "value", "explicit_empty"].includes(field.state))
      continue;
    const state = field.state as FieldState;
    let value: MetadataValue = kind === "list" ? [] : "";
    if (state === "value") {
      if (kind === "list" && Array.isArray(field.value))
        value = field.value.filter(
          (item): item is string => typeof item === "string",
        );
      else if (kind !== "list" && typeof field.value === "string")
        value = field.value;
    }
    values[key] = { state, value };
  }
  return values;
}

function displayValue(
  state: FieldState,
  value: MetadataValue,
  text: (typeof copy)[keyof typeof copy],
) {
  if (state === "absent") return text.noOverride;
  if (state === "explicit_empty") return text.explicitEmpty;
  const rendered = Array.isArray(value) ? value.join("\n") : value;
  return rendered || text.emptyValue;
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
    <ReleaseMetadataEditorSession
      key={releaseId}
      releaseId={releaseId}
      refreshToken={refreshToken}
      onSaved={onSaved}
    />
  );
}

function ReleaseMetadataEditorSession({
  releaseId,
  refreshToken,
  onSaved,
}: {
  releaseId: string;
  refreshToken?: number | string;
  onSaved?: (metadata: LibraryMetadataResponse) => void | Promise<void>;
}) {
  const { locale } = useI18n();
  const text = copy[locale];
  const [metadata, setMetadata] = useState<LibraryMetadataResponse>();
  const metadataRevision = useRef<number | undefined>(undefined);
  const readGeneration = useRef(0);
  const readController = useRef<AbortController | undefined>(undefined);
  const writePending = useRef(false);
  const dirtyRef = useRef(false);
  const [saved, setSaved] = useState<EditorValues>(emptyValues);
  const [draft, setDraft] = useState<EditorValues>(emptyValues);
  const [dirtyFields, setDirtyFields] = useState<Set<ReleaseFieldKey>>(
    () => new Set(),
  );
  const [editing, setEditing] = useState(false);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [readError, setReadError] = useState("");
  const [saveError, setSaveError] = useState("");
  const [refreshConflict, setRefreshConflict] = useState(false);
  const previousRefreshToken = useRef(refreshToken);

  const loadMetadata = useCallback(
    async (force = false) => {
      const generation = ++readGeneration.current;
      readController.current?.abort();
      const controller = new AbortController();
      readController.current = controller;
      setLoading(true);
      try {
        const response = await getLibraryReleaseMetadata(releaseId, {
          cache: "no-store",
          signal: controller.signal,
        });
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
        const next = readValues(response.data);
        metadataRevision.current = response.data.revision;
        setMetadata(response.data);
        setSaved(next);
        if (!dirtyRef.current) {
          setDraft(next);
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
    [releaseId],
  );

  useEffect(() => {
    void loadMetadata(true);
    return () => {
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
    setEditing(false);
    setSaveError("");
    if (refreshConflict) void loadMetadata(true);
    setRefreshConflict(false);
  }

  function updateField(
    key: ReleaseFieldKey,
    next: { state: FieldState; value: MetadataValue },
  ) {
    if (writePending.current) return;
    dirtyRef.current = true;
    setDraft((current) => ({ ...current, [key]: next }));
    setDirtyFields((current) => new Set(current).add(key));
    setSaveError("");
  }

  async function saveDraft() {
    if (!metadata || !dirtyFields.size || writePending.current) return;
    writePending.current = true;
    setSaving(true);
    setSaveError("");
    const changes = Object.fromEntries(
      [...dirtyFields].map((key) => {
        const field = draft[key];
        return [
          key,
          field.state === "value"
            ? { state: field.state, value: field.value }
            : { state: field.state },
        ];
      }),
    );
    try {
      const response = await patchLibraryReleaseMetadata(releaseId, {
        expected_revision: metadata.revision,
        changes,
      });
      if (response.status !== 200) {
        if (response.status === 409) setRefreshConflict(true);
        throw apiResponseError(response);
      }
      readController.current?.abort();
      readGeneration.current += 1;
      setLoading(false);
      const next = readValues(response.data);
      metadataRevision.current = response.data.revision;
      setMetadata(response.data);
      setSaved(next);
      setDraft(next);
      dirtyRef.current = false;
      setDirtyFields(new Set());
      setRefreshConflict(false);
      setEditing(false);
      try {
        const refresh = onSaved?.(response.data);
        if (refresh) void Promise.resolve(refresh).catch(() => undefined);
      } catch {
        // A parent refresh callback cannot undo a committed metadata save.
      }
      // The mutation is committed. A follow-up read must not turn it into a
      // reported save failure, and must not overwrite the committed revision.
      void loadMetadata(true);
    } catch (reason) {
      setSaveError(errorMessage(reason, ""));
    } finally {
      writePending.current = false;
      setSaving(false);
    }
  }

  return (
    <section
      aria-label={text.title}
      className="grid min-w-0 gap-2 rounded-[var(--radius)] border border-[var(--line)] bg-[var(--surface)] p-3 text-[var(--ink)]"
      onKeyDown={(event) => {
        if (event.key !== "Escape" || !editing || saving) return;
        event.preventDefault();
        event.stopPropagation();
        cancelEdit();
      }}
    >
      <div className="flex min-w-0 flex-wrap items-center justify-between gap-2">
        <h2 className="m-0 text-sm font-semibold">{text.title}</h2>
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
      <dl className="m-0 grid min-w-0 grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-3">
        {releaseFields.map(({ key, kind }) => {
          const field = editing ? draft[key] : saved[key];
          const label = text.labels[key];
          const rendered = Array.isArray(field.value)
            ? field.value.join("\n")
            : field.value;
          return (
            <div
              key={key}
              className="grid min-w-0 gap-1 rounded border border-[var(--line)] p-2"
            >
              <dt className="text-xs text-[var(--muted)]">{label}</dt>
              <dd className="m-0 min-w-0 text-sm">
                {editing ? (
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
                            state: event.currentTarget.value as FieldState,
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
                              value: event.currentTarget.value.split("\n"),
                            })
                          }
                        />
                      ) : (
                        <input
                          aria-label={label}
                          className="min-h-9 w-full min-w-0 rounded-[var(--control-radius)] border border-[var(--control)] bg-[var(--surface)] px-2 text-sm text-[var(--ink)] focus-visible:outline-2 focus-visible:outline-[var(--focus)]"
                          type="text"
                          inputMode={kind === "date" ? "text" : undefined}
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
                  <span className="block whitespace-pre-wrap break-words">
                    {displayValue(field.state, field.value, text)}
                  </span>
                )}
              </dd>
            </div>
          );
        })}
      </dl>
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
