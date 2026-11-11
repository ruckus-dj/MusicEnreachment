import { useCallback, useEffect, useRef, useState } from "react";
import { apiResponseError, errorMessage } from "../../api/client/errors";
import {
  getLibraryArtistMetadata,
  patchLibraryArtistMetadata,
} from "../../api/generated/client";
import type { LibraryMetadataResponse } from "../../api/generated/client.schemas";
import { AppButton } from "../../components/AppButton";
import { useI18n } from "../../i18n/I18nProvider";

type SortName = { present: boolean; value: string };

function savedSortName(metadata: LibraryMetadataResponse): SortName {
  const field = metadata.envelope.desired.sort_name;
  if (field?.state === "value" && typeof field.value === "string")
    return { present: true, value: field.value };
  return { present: false, value: "" };
}

const copy = {
  en: {
    label: "Artist sort name",
    noOverride: "No sort name override is set.",
    edit: "Edit",
    save: "Save",
    cancel: "Cancel",
    clear: "Clear sort name",
    loading: "Loading saved sort name…",
    saving: "Saving sort name…",
    readError: "Could not load the saved sort name.",
    saveError: "Could not save the sort name.",
    conflict:
      "Metadata changed while you were editing. Your draft is preserved; cancel and reopen to load the latest revision.",
    draftRetained: "Your draft is retained. Retry or cancel your changes.",
  },
  ru: {
    label: "Сортировочное имя артиста",
    noOverride: "Сортировочное имя не задано.",
    edit: "Изменить",
    save: "Сохранить",
    cancel: "Отмена",
    clear: "Очистить сортировочное имя",
    loading: "Загрузка сортировочного имени…",
    saving: "Сохранение сортировочного имени…",
    readError: "Не удалось загрузить сортировочное имя.",
    saveError: "Не удалось сохранить сортировочное имя.",
    conflict:
      "Метаданные изменились во время редактирования. Черновик сохранён; отмените изменения и откройте редактор снова, чтобы загрузить новую версию.",
    draftRetained:
      "Черновик сохранён. Повторите попытку или отмените изменения.",
  },
} as const;

export function ArtistSortNameEditor({
  artistId,
  refreshToken,
  onSaved,
}: {
  artistId: string;
  refreshToken?: number | string;
  onSaved?: () => void;
}) {
  const { locale } = useI18n();
  const text = copy[locale];
  const [metadata, setMetadata] = useState<LibraryMetadataResponse>();
  const metadataRef = useRef<LibraryMetadataResponse | undefined>(undefined);
  const generation = useRef(0);
  const controller = useRef<AbortController | undefined>(undefined);
  const previousRefreshToken = useRef(refreshToken);
  const skipNextRefresh = useRef(false);
  const [saved, setSaved] = useState<SortName>({ present: false, value: "" });
  const [draft, setDraft] = useState<SortName>({ present: false, value: "" });
  const [editing, setEditing] = useState(false);
  const [dirty, setDirty] = useState(false);
  const dirtyRef = useRef(false);
  dirtyRef.current = dirty;
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [readError, setReadError] = useState("");
  const [saveError, setSaveError] = useState("");
  const [refreshConflict, setRefreshConflict] = useState(false);

  const loadMetadata = useCallback(
    async (force = false) => {
      const currentGeneration = ++generation.current;
      controller.current?.abort();
      const request = new AbortController();
      controller.current = request;
      setLoading(true);
      try {
        const response = await getLibraryArtistMetadata(artistId, {
          cache: "no-store",
          signal: request.signal,
        });
        if (request.signal.aborted || currentGeneration !== generation.current)
          return;
        if (response.status !== 200) throw apiResponseError(response);
        if (
          !force &&
          dirtyRef.current &&
          metadataRef.current &&
          metadataRef.current.revision !== response.data.revision
        ) {
          setRefreshConflict(true);
          return;
        }
        const next = savedSortName(response.data);
        metadataRef.current = response.data;
        setMetadata(response.data);
        setSaved(next);
        setDraft(next);
        setDirty(false);
        setRefreshConflict(false);
        setReadError("");
      } catch (reason) {
        if (!request.signal.aborted && currentGeneration === generation.current)
          setReadError(errorMessage(reason, ""));
      } finally {
        if (!request.signal.aborted && currentGeneration === generation.current)
          setLoading(false);
      }
    },
    [artistId],
  );

  useEffect(() => {
    void loadMetadata(true);
    return () => {
      generation.current += 1;
      controller.current?.abort();
    };
  }, [loadMetadata]);

  useEffect(() => {
    if (Object.is(previousRefreshToken.current, refreshToken)) return;
    previousRefreshToken.current = refreshToken;
    if (skipNextRefresh.current) {
      skipNextRefresh.current = false;
      return;
    }
    void loadMetadata();
  }, [loadMetadata, refreshToken]);

  function beginEdit() {
    setDraft({ ...saved });
    dirtyRef.current = false;
    setDirty(false);
    setSaveError("");
    setRefreshConflict(false);
    setEditing(true);
  }

  function cancelEdit() {
    setEditing(false);
    setDraft({ ...saved });
    dirtyRef.current = false;
    setDirty(false);
    setSaveError("");
    if (refreshConflict) void loadMetadata(true);
    setRefreshConflict(false);
  }

  function changeDraft(value: string) {
    setDraft({ present: true, value });
    dirtyRef.current = true;
    setDirty(true);
  }

  function clearDraft() {
    setDraft({ present: false, value: "" });
    dirtyRef.current = true;
    setDirty(true);
  }

  async function saveDraft() {
    if (!metadata || !dirty || saving) return;
    setSaving(true);
    setSaveError("");
    try {
      const response = await patchLibraryArtistMetadata(artistId, {
        expected_revision: metadata.revision,
        changes: {
          sort_name: draft.present
            ? { state: "value", value: draft.value }
            : { state: "absent" },
        },
      });
      if (response.status !== 200) {
        if (response.status === 409) setRefreshConflict(true);
        throw apiResponseError(response);
      }
      const next = savedSortName(response.data);
      metadataRef.current = response.data;
      setMetadata(response.data);
      setSaved(next);
      setDraft(next);
      dirtyRef.current = false;
      setDirty(false);
      setRefreshConflict(false);
      setEditing(false);
      if (onSaved) {
        skipNextRefresh.current = true;
        try {
          onSaved();
        } catch {
          // The metadata PATCH is committed; cache refresh errors do not undo it.
        }
      }
    } catch (reason) {
      setSaveError(errorMessage(reason, ""));
    } finally {
      setSaving(false);
    }
  }

  return (
    <section
      className="grid gap-2 rounded-[var(--radius)] border border-[var(--line)] bg-[var(--surface)] p-3 text-[var(--ink)]"
      aria-label={text.label}
      onKeyDown={(event) => {
        if (event.key !== "Escape" || !editing) return;
        event.preventDefault();
        cancelEdit();
      }}
    >
      <div className="grid min-w-0 gap-1.5 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-start">
        <div className="min-w-0">
          <h2 className="mb-1 text-sm font-semibold">{text.label}</h2>
          {editing ? (
            <input
              aria-label={text.label}
              className="min-h-10 w-full rounded-[var(--control-radius)] border border-[var(--control)] bg-[var(--surface)] px-2 text-[var(--ink)] focus-visible:outline-2 focus-visible:outline-[var(--focus)] focus-visible:outline-offset-2"
              type="text"
              value={draft.present ? draft.value : ""}
              onChange={(event) => changeDraft(event.currentTarget.value)}
              disabled={saving}
            />
          ) : (
            <p className="m-0 min-h-10 py-2 text-sm">
              {saved.present ? saved.value : text.noOverride}
            </p>
          )}
        </div>
        <div className="grid grid-cols-[7.5rem_7.5rem] gap-2">
          <AppButton
            type="button"
            isDisabled={
              loading ||
              !!readError ||
              saving ||
              refreshConflict ||
              (editing && !dirty)
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
      {editing ? (
        <AppButton
          type="button"
          isDisabled={saving || (!draft.present && !saved.present)}
          onPress={clearDraft}
          className="justify-self-start"
        >
          {text.clear}
        </AppButton>
      ) : null}
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
        <p role="alert" className="m-0 text-sm text-[var(--error)]">
          {text.readError} {readError}
        </p>
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
