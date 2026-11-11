import { useCallback, useEffect, useId, useRef, useState } from "react";
import { apiResponseError, errorMessage } from "../../api/client/errors";
import {
  getLibraryArtistDetails,
  patchLibraryArtistDetails,
} from "../../api/generated/client";
import type { LibraryArtistDetails } from "../../api/generated/client.schemas";
import { AppButton } from "../../components/AppButton";
import { useI18n } from "../../i18n/I18nProvider";

const copy = {
  en: {
    name: "Artist name",
    disambiguation: "Disambiguation",
    noDisambiguation: "\u00a0",
    edit: "Edit",
    save: "Save",
    cancel: "Cancel",
    clearDisambiguation: "Clear",
    loading: "Loading artist details…",
    saving: "Saving artist details…",
    readError: "Could not load artist details.",
    retry: "Retry",
    saveError: "Could not save artist details.",
    blankName:
      "Enter an artist name containing at least one non-space character.",
    notFound: "This artist is no longer available.",
    conflict:
      "Artist details changed while you were editing. Your draft is preserved; cancel and reopen to load the latest revision.",
    draftRetained: "Your draft is retained. Retry or cancel your changes.",
  },
  ru: {
    name: "Имя артиста",
    disambiguation: "Уточнение",
    noDisambiguation: "\u00a0",
    edit: "Изменить",
    save: "Сохранить",
    cancel: "Отмена",
    clearDisambiguation: "Очистить",
    loading: "Загрузка сведений об артисте…",
    saving: "Сохранение сведений об артисте…",
    readError: "Не удалось загрузить сведения об артисте.",
    retry: "Повторить",
    saveError: "Не удалось сохранить сведения об артисте.",
    blankName:
      "Укажите имя артиста, содержащее хотя бы один непробельный символ.",
    notFound: "Этот артист больше недоступен.",
    conflict:
      "Сведения об артисте изменились во время редактирования. Черновик сохранён; отмените изменения и откройте редактор снова, чтобы загрузить новую версию.",
    draftRetained:
      "Черновик сохранён. Повторите попытку или отмените изменения.",
  },
} as const;

type Draft = { name: string; disambiguation: string };
type DirtyFields = { name: boolean; disambiguation: boolean };

export function ArtistDetailsEditor({
  artistId,
  fallbackName,
  fallbackDisambiguation,
  refreshToken,
  onSaved,
}: {
  artistId: string;
  fallbackName: string;
  fallbackDisambiguation: string | null;
  refreshToken?: number | string;
  onSaved?: () => void;
}) {
  return (
    <ArtistDetailsEditorSession
      key={artistId}
      artistId={artistId}
      fallbackName={fallbackName}
      fallbackDisambiguation={fallbackDisambiguation}
      refreshToken={refreshToken}
      onSaved={onSaved}
    />
  );
}

function ArtistDetailsEditorSession({
  artistId,
  fallbackName,
  fallbackDisambiguation,
  refreshToken,
  onSaved,
}: {
  artistId: string;
  fallbackName: string;
  fallbackDisambiguation: string | null;
  refreshToken?: number | string;
  onSaved?: () => void;
}) {
  const { locale } = useI18n();
  const text = copy[locale];
  const textRef = useRef(text);
  textRef.current = text;
  const fallbackNameRef = useRef(fallbackName);
  fallbackNameRef.current = fallbackName;
  const fallbackDisambiguationRef = useRef(fallbackDisambiguation);
  fallbackDisambiguationRef.current = fallbackDisambiguation;
  const errorId = useId();
  const nameInput = useRef<HTMLInputElement | null>(null);
  const detailsRef = useRef<LibraryArtistDetails | undefined>(undefined);
  const readGeneration = useRef(0);
  const saveGeneration = useRef(0);
  const mounted = useRef(false);
  const writePending = useRef(false);
  const dirtyRef = useRef<DirtyFields>({ name: false, disambiguation: false });
  const previousRefreshToken = useRef(refreshToken);
  const skipNextRefresh = useRef(false);
  const readController = useRef<AbortController | undefined>(undefined);
  const saveController = useRef<AbortController | undefined>(undefined);
  const [details, setDetails] = useState<LibraryArtistDetails>();
  const [draft, setDraft] = useState<Draft>({
    name: fallbackName,
    disambiguation: fallbackDisambiguation ?? "",
  });
  const [dirty, setDirty] = useState<DirtyFields>({
    name: false,
    disambiguation: false,
  });
  const [editing, setEditing] = useState(false);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [readError, setReadError] = useState("");
  const [saveError, setSaveError] = useState("");
  const [nameError, setNameError] = useState("");
  const [refreshConflict, setRefreshConflict] = useState(false);
  const hasDirtyDraft = dirty.name || dirty.disambiguation;

  const adopt = useCallback((value: LibraryArtistDetails) => {
    detailsRef.current = value;
    setDetails(value);
    setDraft({
      name: value.name,
      disambiguation: value.disambiguation ?? "",
    });
    const clean = { name: false, disambiguation: false };
    dirtyRef.current = clean;
    setDirty(clean);
    setSaveError("");
    setNameError("");
    setRefreshConflict(false);
    setReadError("");
  }, []);

  const isCurrent = useCallback(
    (generation: number) =>
      mounted.current && generation === saveGeneration.current,
    [],
  );

  const loadDetails = useCallback(
    async (force = false) => {
      const generation = ++readGeneration.current;
      readController.current?.abort();
      const controller = new AbortController();
      readController.current = controller;
      setLoading(true);
      try {
        const response = await getLibraryArtistDetails(artistId, {
          cache: "no-store",
          signal: controller.signal,
        });
        if (
          controller.signal.aborted ||
          !mounted.current ||
          generation !== readGeneration.current
        )
          return;
        if (response.status !== 200) throw apiResponseError(response);
        setReadError("");
        const current = detailsRef.current;
        if (
          !force &&
          (dirtyRef.current.name || dirtyRef.current.disambiguation) &&
          current &&
          current.revision !== response.data.revision
        ) {
          setRefreshConflict(true);
          return;
        }
        if (
          !force &&
          (dirtyRef.current.name || dirtyRef.current.disambiguation)
        )
          return;
        adopt(response.data);
      } catch (reason) {
        if (!controller.signal.aborted && mounted.current)
          setReadError(errorMessage(reason, textRef.current.readError));
      } finally {
        if (
          !controller.signal.aborted &&
          mounted.current &&
          generation === readGeneration.current
        )
          setLoading(false);
      }
    },
    [adopt, artistId],
  );

  useEffect(() => {
    mounted.current = true;
    void loadDetails(true);
    return () => {
      mounted.current = false;
      readGeneration.current += 1;
      saveGeneration.current += 1;
      readController.current?.abort();
      saveController.current?.abort();
    };
  }, [loadDetails]);

  useEffect(() => {
    if (Object.is(previousRefreshToken.current, refreshToken)) return;
    previousRefreshToken.current = refreshToken;
    if (skipNextRefresh.current) {
      skipNextRefresh.current = false;
      return;
    }
    void loadDetails();
  }, [loadDetails, refreshToken]);

  function beginEdit() {
    const saved = detailsRef.current;
    if (!saved) return;
    setDraft({
      name: saved.name,
      disambiguation: saved.disambiguation ?? "",
    });
    const clean = { name: false, disambiguation: false };
    dirtyRef.current = clean;
    setDirty(clean);
    setSaveError("");
    setNameError("");
    setRefreshConflict(false);
    setEditing(true);
  }

  function cancelEdit() {
    const saved = detailsRef.current;
    if (saved) {
      setDraft({
        name: saved.name,
        disambiguation: saved.disambiguation ?? "",
      });
    }
    const clean = { name: false, disambiguation: false };
    dirtyRef.current = clean;
    setDirty(clean);
    setEditing(false);
    setSaveError("");
    setNameError("");
    if (refreshConflict) {
      setRefreshConflict(false);
      void loadDetails(true);
    }
  }

  function updateName(value: string) {
    const next = {
      ...dirtyRef.current,
      name: value !== (detailsRef.current?.name ?? fallbackNameRef.current),
    };
    dirtyRef.current = next;
    setDirty(next);
    setDraft((current) => ({ ...current, name: value }));
    setNameError("");
  }

  function updateDisambiguation(value: string) {
    const saved = detailsRef.current?.disambiguation;
    const next = {
      ...dirtyRef.current,
      disambiguation: value !== (saved ?? ""),
    };
    dirtyRef.current = next;
    setDirty(next);
    setDraft((current) => ({ ...current, disambiguation: value }));
  }

  function clearDisambiguation() {
    const isAlreadyExplicitlyEmpty = detailsRef.current?.disambiguation === "";
    const next = {
      ...dirtyRef.current,
      disambiguation: !isAlreadyExplicitlyEmpty,
    };
    dirtyRef.current = next;
    setDirty(next);
    setDraft((current) => ({ ...current, disambiguation: "" }));
  }

  async function saveDraft() {
    const saved = detailsRef.current;
    const dirtySnapshot = dirtyRef.current;
    if (!saved || !hasDirtyDraft || writePending.current || refreshConflict)
      return;
    if (dirtySnapshot.name && !draft.name.trim()) {
      setNameError(text.blankName);
      nameInput.current?.focus();
      return;
    }

    const body = {
      expected_revision: saved.revision,
      ...(dirtySnapshot.name ? { name: draft.name } : {}),
      ...(dirtySnapshot.disambiguation
        ? { disambiguation: draft.disambiguation }
        : {}),
    };
    if (body.name === undefined && body.disambiguation === undefined) return;

    writePending.current = true;
    setSaving(true);
    setSaveError("");
    const generation = ++saveGeneration.current;
    const controller = new AbortController();
    saveController.current?.abort();
    saveController.current = controller;
    try {
      const response = await patchLibraryArtistDetails(artistId, body, {
        signal: controller.signal,
      });
      if (!isCurrent(generation)) return;
      if (response.status !== 200) {
        if (response.status === 409) setRefreshConflict(true);
        if (response.status === 404) setReadError(text.notFound);
        throw apiResponseError(response);
      }
      adopt(response.data);
      setEditing(false);
      if (onSaved) {
        skipNextRefresh.current = true;
        try {
          onSaved();
        } catch {
          // The PATCH is committed; cache refresh errors do not undo it.
        }
      }
    } catch (reason) {
      if (isCurrent(generation) && !controller.signal.aborted)
        setSaveError(errorMessage(reason, text.saveError));
    } finally {
      if (isCurrent(generation)) {
        writePending.current = false;
        setSaving(false);
      }
    }
  }

  const displayedName = editing ? draft.name : (details?.name ?? fallbackName);
  const displayedDisambiguation = editing
    ? draft.disambiguation
    : details
      ? (details.disambiguation ?? "")
      : (fallbackDisambiguation ?? "");
  const canEdit = Boolean(details) && !loading && !readError;

  return (
    <section
      className="grid min-w-0 gap-1"
      aria-label={text.name}
      onKeyDown={(event) => {
        if (event.key !== "Escape" || !editing || saving) return;
        event.preventDefault();
        cancelEdit();
      }}
    >
      <div className="grid min-w-0 gap-1 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-start">
        <div className="grid min-w-0 gap-1">
          {editing ? (
            <>
              <h1 id="artist-title" className="sr-only">
                {displayedName}
              </h1>
              <input
                ref={nameInput}
                aria-label={text.name}
                aria-invalid={Boolean(nameError) || undefined}
                aria-describedby={nameError ? errorId : undefined}
                className="min-h-7 w-full min-w-0 border border-transparent bg-transparent p-0 text-[22px] font-semibold leading-7 text-[var(--ink)] focus-visible:rounded-[var(--control-radius)] focus-visible:border-[var(--control)] focus-visible:px-2 focus-visible:outline-2 focus-visible:outline-[var(--focus)]"
                value={draft.name}
                onChange={(event) => updateName(event.currentTarget.value)}
                disabled={saving}
              />
            </>
          ) : (
            <h1
              id="artist-title"
              className="m-0 text-[22px] font-semibold leading-7"
            >
              {displayedName}
            </h1>
          )}
          <div className="flex min-h-5 min-w-0 items-center gap-2 text-sm text-[var(--muted)]">
            {editing ? (
              <>
                <input
                  aria-label={text.disambiguation}
                  className="min-h-7 w-full min-w-0 rounded-[var(--control-radius)] border border-[var(--control)] bg-[var(--surface)] px-2 py-1 text-sm text-[var(--ink)] focus-visible:outline-2 focus-visible:outline-[var(--focus)]"
                  value={draft.disambiguation}
                  onChange={(event) =>
                    updateDisambiguation(event.currentTarget.value)
                  }
                  disabled={saving}
                />
                <AppButton
                  type="button"
                  isDisabled={
                    saving ||
                    (draft.disambiguation === "" &&
                      detailsRef.current?.disambiguation === "")
                  }
                  onPress={clearDisambiguation}
                  className="shrink-0"
                >
                  {text.clearDisambiguation}
                </AppButton>
              </>
            ) : displayedDisambiguation ? (
              <p className="m-0 min-h-5 overflow-wrap-anywhere">
                {displayedDisambiguation}
              </p>
            ) : (
              <p className="m-0 min-h-5">{text.noDisambiguation}</p>
            )}
          </div>
          {nameError ? (
            <p
              id={errorId}
              role="alert"
              className="m-0 text-sm text-[var(--error)]"
            >
              {nameError}
            </p>
          ) : null}
        </div>
        <div className="grid grid-cols-[7.5rem_7.5rem] gap-2">
          <AppButton
            type="button"
            isDisabled={
              !canEdit ||
              saving ||
              refreshConflict ||
              (editing && !hasDirtyDraft)
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
      {loading ? (
        <p role="status" className="m-0 min-h-5 text-sm text-[var(--muted)]">
          {text.loading}
        </p>
      ) : null}
      {saving ? (
        <p role="status" className="m-0 min-h-5 text-sm text-[var(--muted)]">
          {text.saving}
        </p>
      ) : null}
      {readError ? (
        <div
          role="alert"
          className="flex flex-wrap items-center gap-2 text-sm text-[var(--error)]"
        >
          <span>
            {text.readError} {readError}
          </span>
          <AppButton type="button" onPress={() => void loadDetails()}>
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
