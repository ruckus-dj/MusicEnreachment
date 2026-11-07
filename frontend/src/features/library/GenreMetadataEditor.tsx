import { useCallback, useEffect, useRef, useState } from "react";
import { apiResponseError, errorMessage } from "../../api/client/errors";
import {
  getGenreCatalog,
  getLibraryArtistMetadata,
  getLibraryRecordingMetadata,
  getLibraryReleaseMetadata,
  patchLibraryArtistMetadata,
  patchLibraryRecordingMetadata,
  patchLibraryReleaseMetadata,
  searchGenreCatalog,
} from "../../api/generated/client";
import type {
  GenreCatalogItem,
  GenreCatalogStatus,
  LibraryMetadataResponse,
  ProviderGenreBaseline,
} from "../../api/generated/client.schemas";
import { AppButton } from "../../components/AppButton";
import { useI18n } from "../../i18n/I18nProvider";
import { GenrePicker } from "./GenrePicker";
import { genreMessages } from "./genreMessages";

export type GenreReference = {
  id: string;
  source_name: string;
  name: string;
};

export type GenreOwner = "artist" | "release" | "recording";
type GenreState = "absent" | "explicit_empty" | "value";

function savedGenres(metadata: LibraryMetadataResponse): {
  state: GenreState;
  value: GenreReference[];
} {
  const field = metadata.envelope.desired.genre;
  if (!field || field.state === "absent") return { state: "absent", value: [] };
  if (field.state === "explicit_empty")
    return { state: "explicit_empty", value: [] };
  const value = field.value;
  if (!Array.isArray(value)) return { state: "value", value: [] };
  return {
    state: "value",
    value: value.filter(
      (item): item is GenreReference =>
        !!item &&
        typeof item === "object" &&
        typeof (item as GenreReference).id === "string" &&
        typeof (item as GenreReference).name === "string" &&
        typeof (item as GenreReference).source_name === "string",
    ),
  };
}

function catalogGenre(item: GenreCatalogItem): GenreReference {
  return { id: item.id, source_name: item.source_name, name: item.name };
}

export function GenreMetadataEditor({
  owner,
  ownerId,
  refreshToken,
  onSaved,
}: {
  owner: GenreOwner;
  ownerId: string;
  refreshToken?: number | string;
  onSaved?: (
    owner: GenreOwner,
    ownerId: string,
    metadata: LibraryMetadataResponse,
  ) => void | Promise<void>;
}) {
  return (
    <GenreMetadataEditorSession
      key={`${owner}:${ownerId}`}
      owner={owner}
      ownerId={ownerId}
      refreshToken={refreshToken}
      onSaved={onSaved}
    />
  );
}

function GenreMetadataEditorSession({
  owner,
  ownerId,
  refreshToken,
  onSaved,
}: {
  owner: GenreOwner;
  ownerId: string;
  refreshToken?: number | string;
  onSaved?: (
    owner: GenreOwner,
    ownerId: string,
    metadata: LibraryMetadataResponse,
  ) => void | Promise<void>;
}) {
  const { locale } = useI18n();
  const copy = genreMessages[locale];
  const identity = `${owner}:${ownerId}`;
  const identityRef = useRef(identity);
  identityRef.current = identity;
  const mounted = useRef(false);
  const writePending = useRef(false);
  const dirtyRef = useRef(false);
  const metadataReadGeneration = useRef(0);
  const searchGeneration = useRef(0);
  const searchAttemptRef = useRef(0);
  const metadataController = useRef<AbortController | undefined>(undefined);
  const searchController = useRef<AbortController | undefined>(undefined);
  const statusController = useRef<AbortController | undefined>(undefined);
  const previousRefreshToken = useRef(refreshToken);
  const [metadata, setMetadata] = useState<LibraryMetadataResponse>();
  const metadataRevision = useRef<number | undefined>(undefined);
  const [providerGenreBaseline, setProviderGenreBaseline] =
    useState<ProviderGenreBaseline>();
  const [catalogStatus, setCatalogStatus] = useState<GenreCatalogStatus>();
  const [catalogStatusLoading, setCatalogStatusLoading] = useState(false);
  const [catalogStatusError, setCatalogStatusError] = useState("");
  const [selected, setSelected] = useState<GenreReference[]>([]);
  const [savedState, setSavedState] = useState<GenreState>("absent");
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState<GenreReference[]>([]);
  const [draftState, setDraftState] = useState<GenreState>("absent");
  const [draftDirty, setDraftDirty] = useState(false);
  const [query, setQuery] = useState("");
  const [retrySearch, setRetrySearch] = useState(0);
  const [options, setOptions] = useState<GenreReference[]>([]);
  const [loading, setLoading] = useState(false);
  const [loadingSaved, setLoadingSaved] = useState(true);
  const [saving, setSaving] = useState(false);
  const [readError, setReadError] = useState("");
  const [catalogError, setCatalogError] = useState("");
  const [saveError, setSaveError] = useState("");
  const [refreshConflict, setRefreshConflict] = useState(false);

  const isCurrent = useCallback(
    (capturedIdentity: string) =>
      mounted.current && identityRef.current === capturedIdentity,
    [],
  );

  const loadMetadata = useCallback(
    async (baselineOnly = false) => {
      const capturedIdentity = identity;
      const generation = ++metadataReadGeneration.current;
      metadataController.current?.abort();
      const controller = new AbortController();
      metadataController.current = controller;
      setLoadingSaved(true);
      try {
        const response =
          owner === "artist"
            ? await getLibraryArtistMetadata(ownerId, {
                cache: "no-store",
                signal: controller.signal,
              })
            : owner === "release"
              ? await getLibraryReleaseMetadata(ownerId, {
                  cache: "no-store",
                  signal: controller.signal,
                })
              : await getLibraryRecordingMetadata(ownerId, {
                  cache: "no-store",
                  signal: controller.signal,
                });
        if (
          !isCurrent(capturedIdentity) ||
          generation !== metadataReadGeneration.current
        )
          return;
        if (response.status !== 200) throw apiResponseError(response);
        setProviderGenreBaseline(response.data.provider_genre_baseline);
        if (baselineOnly) return;
        if (
          dirtyRef.current &&
          metadataRevision.current !== response.data.revision
        ) {
          setRefreshConflict(true);
          return;
        }
        const genres = savedGenres(response.data);
        metadataRevision.current = response.data.revision;
        setMetadata(response.data);
        setSelected(genres.value);
        setSavedState(genres.state);
        if (!dirtyRef.current) {
          setDraft(genres.value);
          setDraftState(genres.state);
        }
        setReadError("");
      } catch (reason) {
        if (
          !baselineOnly &&
          !controller.signal.aborted &&
          isCurrent(capturedIdentity) &&
          generation === metadataReadGeneration.current
        )
          setReadError(errorMessage(reason, ""));
      } finally {
        if (
          isCurrent(capturedIdentity) &&
          generation === metadataReadGeneration.current
        )
          setLoadingSaved(false);
      }
    },
    [identity, isCurrent, owner, ownerId],
  );

  useEffect(() => {
    mounted.current = true;
    void loadMetadata();
    return () => {
      mounted.current = false;
      metadataController.current?.abort();
      searchController.current?.abort();
      statusController.current?.abort();
      metadataReadGeneration.current += 1;
      searchGeneration.current += 1;
    };
  }, [loadMetadata]);

  useEffect(() => {
    if (Object.is(previousRefreshToken.current, refreshToken)) return;
    previousRefreshToken.current = refreshToken;
    void loadMetadata();
  }, [loadMetadata, refreshToken]);

  useEffect(() => {
    const search = query.trim();
    const generation = ++searchGeneration.current;
    const attempt = retrySearch;
    searchAttemptRef.current = attempt;
    searchController.current?.abort();
    const catalogAvailable = Boolean(
      catalogStatus?.updated_at && catalogStatus.count > 0,
    );
    if (!search || !catalogAvailable) {
      setOptions([]);
      setCatalogError("");
      setLoading(false);
      return;
    }
    const capturedIdentity = identity;
    const controller = new AbortController();
    searchController.current = controller;
    setOptions([]);
    setCatalogError("");
    setLoading(true);
    void searchGenreCatalog(
      { query: search, limit: 40 },
      { cache: "no-store", signal: controller.signal },
    )
      .then((response) => {
        if (
          controller.signal.aborted ||
          !isCurrent(capturedIdentity) ||
          generation !== searchGeneration.current ||
          attempt !== searchAttemptRef.current
        )
          return;
        if (response.status !== 200) throw apiResponseError(response);
        setOptions((response.data.items ?? []).map(catalogGenre));
      })
      .catch((reason: unknown) => {
        if (
          !controller.signal.aborted &&
          isCurrent(capturedIdentity) &&
          generation === searchGeneration.current &&
          attempt === searchAttemptRef.current
        )
          setCatalogError(errorMessage(reason, ""));
      })
      .finally(() => {
        if (
          !controller.signal.aborted &&
          isCurrent(capturedIdentity) &&
          generation === searchGeneration.current &&
          attempt === searchAttemptRef.current
        )
          setLoading(false);
      });
    return () => controller.abort();
  }, [query, retrySearch, catalogStatus, identity, isCurrent]);

  async function loadCatalogStatus() {
    statusController.current?.abort();
    const controller = new AbortController();
    statusController.current = controller;
    setCatalogStatusLoading(true);
    setCatalogStatusError("");
    try {
      const response = await getGenreCatalog({
        cache: "no-store",
        signal: controller.signal,
      });
      if (controller.signal.aborted || !isCurrent(identity)) return;
      if (response.status !== 200) throw apiResponseError(response);
      setCatalogStatus(response.data);
    } catch (reason) {
      if (!controller.signal.aborted && isCurrent(identity))
        setCatalogStatusError(errorMessage(reason, ""));
    } finally {
      if (!controller.signal.aborted && isCurrent(identity))
        setCatalogStatusLoading(false);
    }
  }

  function markDraftDirty() {
    dirtyRef.current = true;
    setDraftDirty(true);
  }

  function beginEdit() {
    setDraft(selected.map((item) => ({ ...item })));
    setDraftState(savedState);
    dirtyRef.current = false;
    setDraftDirty(false);
    setRefreshConflict(false);
    setSaveError("");
    setEditing(true);
  }

  function cancelEdit() {
    dirtyRef.current = false;
    setDraftDirty(false);
    setEditing(false);
    setDraft(selected.map((item) => ({ ...item })));
    setDraftState(savedState);
    setSaveError("");
    if (refreshConflict) {
      setRefreshConflict(false);
      void loadMetadata();
    }
  }

  async function saveDraft() {
    if (!metadata || !draftDirty || writePending.current) return;
    if (draftState === "value" && draft.length === 0) return;
    writePending.current = true;
    setSaving(true);
    const capturedIdentity = identity;
    setSaveError("");
    const field: { state: GenreState; value?: GenreReference[] } =
      draftState === "value"
        ? { state: "value", value: draft }
        : { state: draftState };
    try {
      const body = {
        expected_revision: metadata.revision,
        changes: { genre: field },
      };
      const response =
        owner === "artist"
          ? await patchLibraryArtistMetadata(ownerId, body)
          : owner === "release"
            ? await patchLibraryReleaseMetadata(ownerId, body)
            : await patchLibraryRecordingMetadata(ownerId, body);
      if (!isCurrent(capturedIdentity)) return;
      if (response.status !== 200) throw apiResponseError(response);
      // A read captured before this commit may still resolve after it. Abort it
      // and advance the generation before installing the committed revision so
      // the stale response cannot overwrite revision, draft, or baseline.
      metadataController.current?.abort();
      metadataReadGeneration.current += 1;
      setLoadingSaved(false);
      const genres = savedGenres(response.data);
      metadataRevision.current = response.data.revision;
      setMetadata(response.data);
      setSelected(genres.value);
      setSavedState(genres.state);
      setDraft(genres.value);
      setDraftState(genres.state);
      dirtyRef.current = false;
      setDraftDirty(false);
      setRefreshConflict(false);
      setEditing(false);
      void Promise.resolve()
        .then(() => onSaved?.(owner, ownerId, response.data))
        .catch(() => undefined);
      // PATCH responses intentionally contain only desired metadata, so always
      // reload the baseline independently. The parent callback may also trigger
      // a full read; generation/abort keeps the latest read authoritative, and
      // neither follow-up can turn the committed save into a reported failure.
      void loadMetadata(true);
    } catch (reason) {
      if (isCurrent(capturedIdentity)) setSaveError(errorMessage(reason, ""));
    } finally {
      if (isCurrent(capturedIdentity)) {
        writePending.current = false;
        setSaving(false);
      }
    }
  }

  function changeDraft(next: GenreReference[]) {
    if (writePending.current) return;
    setDraft(next);
    setDraftState(next.length ? "value" : "explicit_empty");
    markDraftDirty();
  }

  function setExplicitEmpty() {
    if (writePending.current) return;
    setDraft([]);
    setDraftState("explicit_empty");
    markDraftDirty();
  }

  function setAbsent() {
    if (writePending.current) return;
    setDraft([]);
    setDraftState("absent");
    markDraftDirty();
  }

  const label = copy.label[owner];
  const displayedValue = editing ? draft : selected;
  const emptyState = editing ? draftState : savedState;
  const emptyText =
    emptyState === "explicit_empty"
      ? copy.explicitlyCleared
      : emptyState === "absent"
        ? copy.noOverride
        : copy.empty;

  return (
    <section
      className="grid gap-2 rounded-[var(--radius)] border border-[var(--line)] bg-[var(--surface)] p-3 text-[var(--ink)]"
      aria-label={label}
      onKeyDown={(event) => {
        if (event.key !== "Escape" || !editing) return;
        event.preventDefault();
        cancelEdit();
      }}
    >
      <div className="grid min-w-0 gap-1.5 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-start">
        <div className="min-w-0">
          <h3 className="mb-1 text-sm font-semibold">{label}</h3>
          <GenrePicker
            value={displayedValue}
            onChange={changeDraft}
            editing={editing}
            label={label}
            copy={copy}
            options={options}
            query={query}
            onQueryChange={setQuery}
            loading={loading}
            catalogError={catalogError}
            onClearCatalogError={() => setCatalogError("")}
            onRetryCatalog={() => {
              searchAttemptRef.current += 1;
              setRetrySearch((value) => value + 1);
            }}
            disabled={saving}
            catalogStatus={catalogStatus}
            catalogStatusLoading={catalogStatusLoading}
            catalogStatusError={catalogStatusError}
            onOpenCatalog={() => void loadCatalogStatus()}
            onRetryStatus={() => void loadCatalogStatus()}
          />
          <p
            className="m-0 mt-1 min-h-5 text-sm text-[var(--muted)]"
            aria-hidden={Boolean(displayedValue.length)}
          >
            {displayedValue.length ? "\u00a0" : emptyText}
          </p>
        </div>
        <div className="grid grid-cols-[7.5rem_7.5rem] gap-2">
          <AppButton
            type="button"
            isDisabled={
              loadingSaved ||
              !!readError ||
              saving ||
              refreshConflict ||
              (editing &&
                (!draftDirty || (draftState === "value" && !draft.length)))
            }
            onPress={() => (editing ? void saveDraft() : beginEdit())}
            className="min-w-0 justify-center"
          >
            {editing ? copy.save : copy.edit}
          </AppButton>
          <AppButton
            type="button"
            isDisabled={!editing || saving}
            aria-hidden={!editing}
            excludeFromTabOrder={!editing}
            onPress={cancelEdit}
            className={`min-w-0 justify-center ${editing ? "" : "invisible"}`}
          >
            {copy.cancel}
          </AppButton>
        </div>
      </div>
      <section
        className="grid gap-1 border-t border-[var(--line)] pt-2 text-sm"
        aria-label={copy.providerGenres}
      >
        <h4 className="m-0 font-medium">{copy.providerGenres}</h4>
        {providerGenreBaseline ? (
          (providerGenreBaseline.genres ?? []).length ? (
            <ul className="m-0 flex list-none flex-wrap gap-2 p-0">
              {(providerGenreBaseline.genres ?? []).map((genre) => (
                <li
                  className="rounded-full border border-[var(--line)] px-2 py-0.5"
                  key={genre.id}
                >
                  {genre.name}
                </li>
              ))}
            </ul>
          ) : (
            <p className="m-0 text-[var(--muted)]">
              {copy.providerGenresEmpty}
            </p>
          )
        ) : (
          <p className="m-0 text-[var(--muted)]">
            {copy.providerGenresUnavailable}
          </p>
        )}
      </section>
      {loadingSaved ? (
        <p role="status" className="m-0 text-sm text-[var(--muted)]">
          {copy.loading}
        </p>
      ) : null}
      {readError ? (
        <p role="alert" className="m-0 text-sm text-[var(--error)]">
          {copy.readError} {readError}
        </p>
      ) : null}
      {editing ? (
        <div className="flex flex-wrap gap-2">
          <AppButton
            type="button"
            isDisabled={saving}
            onPress={setExplicitEmpty}
          >
            {copy.clear}
          </AppButton>
          <AppButton type="button" isDisabled={saving} onPress={setAbsent}>
            {copy.inherit}
          </AppButton>
        </div>
      ) : null}
      {saving ? (
        <p role="status" className="m-0 text-sm text-[var(--muted)]">
          {copy.saving}
        </p>
      ) : null}
      {saveError ? (
        <p role="alert" className="m-0 text-sm text-[var(--error)]">
          {copy.saveError} {saveError} {copy.draftRetained}
        </p>
      ) : null}
      {refreshConflict && draftDirty ? (
        <p role="alert" className="m-0 text-sm text-[var(--error)]">
          {copy.refreshConflict}
        </p>
      ) : null}
    </section>
  );
}
