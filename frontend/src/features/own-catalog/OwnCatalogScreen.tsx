import { type FormEvent, useEffect, useRef, useState } from "react";
import { apiResponseError, errorMessage } from "../../api/client/errors";
import {
  createLocalProviderArtist,
  createLocalProviderRecording,
  createLocalProviderRelease,
  getGenreCatalog,
  listLocalProviderArtists,
  listLocalProviderRecordings,
  listLocalProviderReleases,
  readLocalProviderArtist,
  readLocalProviderRecording,
  readLocalProviderRelease,
  searchGenreCatalog,
  updateLocalProviderArtist,
  updateLocalProviderRecording,
  updateLocalProviderRelease,
} from "../../api/generated/client";
import type {
  GenreCatalogItem,
  GenreCatalogStatus,
  LocalProviderItem,
  LocalProviderWrite,
} from "../../api/generated/client.schemas";
import { AppButton } from "../../components/AppButton";
import { useI18n } from "../../i18n/I18nProvider";
import { GenrePicker } from "../library/GenrePicker";
import { type GenreMessages, genreMessages } from "../library/genreMessages";
import { durationForSave, formatDuration, parseDuration } from "./duration";
import "./own-catalog.css";

type Kind = "artists" | "recordings" | "releases";
type CatalogEntry = LocalProviderItem;
type CreditDraft = {
  clientKey: string;
  artist_id: string;
  credit_name: string;
  join_phrase: string;
};
type TrackDraft = {
  clientKey: string;
  id?: string;
  recording_id: string;
  recording_title: string;
  recording_duration_ms: number | null;
  title_override: string;
  duration: string;
  originalDurationMS?: number | null;
  durationEdited: boolean;
};
type Draft = {
  name: string;
  sort_name: string;
  title: string;
  release_group_title: string;
  duration: string;
  originalDurationMS?: number;
  durationEdited: boolean;
  primary_type: string;
  secondary_types: string;
  language: string;
  script: string;
  tags: string;
  genres: GenreCatalogItem[];
  credits: CreditDraft[];
  tracks: TrackDraft[];
};

const emptyDraft = (): Draft => ({
  name: "",
  sort_name: "",
  title: "",
  release_group_title: "",
  duration: "",
  durationEdited: false,
  primary_type: "",
  secondary_types: "",
  language: "",
  script: "",
  tags: "",
  genres: [],
  credits: [],
  tracks: [],
});

function asGenre(item: GenreCatalogItem): GenreCatalogItem {
  return { id: item.id, name: item.name, source_name: item.source_name };
}

function draftFromItem(item: CatalogEntry): Draft {
  return {
    ...emptyDraft(),
    name: item.name ?? "",
    sort_name: item.sort_name ?? "",
    title: item.title ?? "",
    release_group_title: item.release_group_title ?? "",
    duration: item.duration_ms == null ? "" : formatDuration(item.duration_ms),
    originalDurationMS: item.duration_ms,
    durationEdited: false,
    primary_type: item.release_group_primary_type ?? "",
    secondary_types: (item.release_group_secondary_types ?? []).join(", "),
    language: item.language ?? "",
    script: item.script ?? "",
    tags: (item.tags ?? []).join(", "),
    genres: (item.genres ?? []).map(asGenre),
    credits: (item.credits ?? []).map((credit) => ({
      clientKey: crypto.randomUUID(),
      artist_id: credit.artist_id,
      credit_name: credit.credit_name,
      join_phrase: credit.join_phrase,
    })),
    tracks: (item.tracks ?? []).map((track) => ({
      clientKey: crypto.randomUUID(),
      id: track.id,
      recording_id: track.recording_id,
      recording_title: track.recording_title,
      recording_duration_ms: track.recording_duration_ms,
      title_override: track.title_override ?? "",
      originalDurationMS: track.duration_override_ms,
      durationEdited: false,
      duration:
        track.duration_override_ms == null
          ? ""
          : formatDuration(track.duration_override_ms),
    })),
  };
}

function writeBody(kind: Kind, draft: Draft): LocalProviderWrite {
  const body: LocalProviderWrite = {
    genres: draft.genres,
    tags: kind === "artists" || kind === "recordings" ? split(draft.tags) : [],
    credits:
      kind === "recordings" || kind === "releases"
        ? draft.credits.map(({ artist_id, credit_name, join_phrase }) => ({
            artist_id,
            credit_name,
            join_phrase,
          }))
        : [],
    tracks:
      kind === "releases"
        ? draft.tracks.map((track) => {
            const duration = durationForSave(
              track.originalDurationMS,
              track.durationEdited,
              track.duration,
            );
            return {
              ...(track.id ? { id: track.id } : {}),
              recording_id: track.recording_id,
              ...(track.title_override
                ? { title_override: track.title_override }
                : {}),
              ...(duration !== undefined
                ? { duration_override_ms: duration }
                : {}),
            };
          })
        : [],
  };
  if (kind === "artists") {
    body.name = draft.name;
    if (draft.sort_name) body.sort_name = draft.sort_name;
  } else {
    body.title = draft.title;
    if (kind === "recordings") {
      const duration = durationForSave(
        draft.originalDurationMS,
        draft.durationEdited,
        draft.duration,
      );
      if (duration !== undefined) body.duration_ms = duration;
    } else {
      body.release_group_title = draft.release_group_title;
      if (draft.primary_type)
        body.release_group_primary_type = draft.primary_type;
      body.release_group_secondary_types = split(draft.secondary_types);
      if (draft.language) body.language = draft.language;
      if (draft.script) body.script = draft.script;
    }
  }
  return body;
}

function split(value: string): string[] {
  return value
    .split(",")
    .map((part) => part.trim())
    .filter(Boolean);
}

function formatEffectiveDuration(
  track: TrackDraft,
  notSpecified: string,
): string {
  const override = durationForSave(
    track.originalDurationMS,
    track.durationEdited,
    track.duration,
  );
  const duration =
    override == null || Number.isNaN(override)
      ? track.recording_duration_ms
      : override;
  return duration == null ? notSpecified : formatDuration(duration);
}

const apiFor = {
  artists: {
    list: listLocalProviderArtists,
    read: readLocalProviderArtist,
    create: createLocalProviderArtist,
    update: updateLocalProviderArtist,
  },
  recordings: {
    list: listLocalProviderRecordings,
    read: readLocalProviderRecording,
    create: createLocalProviderRecording,
    update: updateLocalProviderRecording,
  },
  releases: {
    list: listLocalProviderReleases,
    read: readLocalProviderRelease,
    create: createLocalProviderRelease,
    update: updateLocalProviderRelease,
  },
} as const;

export function OwnCatalogScreen() {
  const { locale } = useI18n();
  const copy = ownCatalogMessages[locale];
  const genreCopy = genreMessages[locale];
  const [kind, setKind] = useState<Kind>("artists");
  const [searchInput, setSearchInput] = useState("");
  const [query, setQuery] = useState("");
  const [items, setItems] = useState<CatalogEntry[]>([]);
  const [selected, setSelected] = useState<CatalogEntry>();
  const [draft, setDraft] = useState<Draft>(emptyDraft);
  const [creating, setCreating] = useState(false);
  const [editing, setEditing] = useState(false);
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [genreStatus, setGenreStatus] = useState<GenreCatalogStatus>();
  const [genreQuery, setGenreQuery] = useState("");
  const [genreOptions, setGenreOptions] = useState<GenreCatalogItem[]>([]);
  const [genreLoading, setGenreLoading] = useState(false);
  const [genreError, setGenreError] = useState("");
  const [genreStatusLoading, setGenreStatusLoading] = useState(false);
  const [genreStatusError, setGenreStatusError] = useState("");
  const [genreRetry, setGenreRetry] = useState(0);
  const [artistQuery, setArtistQuery] = useState("");
  const [artists, setArtists] = useState<CatalogEntry[]>([]);
  const [recordingQuery, setRecordingQuery] = useState("");
  const [recordings, setRecordings] = useState<CatalogEntry[]>([]);
  const genreRetryRef = useRef(genreRetry);
  genreRetryRef.current = genreRetry;
  const detailReadGeneration = useRef(0);
  const currentKind = useRef(kind);
  currentKind.current = kind;

  useEffect(() => {
    if (!query) {
      setItems([]);
      setSelected(undefined);
      setLoading(false);
      return;
    }
    const controller = new AbortController();
    setLoading(true);
    setError("");
    void apiFor[kind]
      .list(
        { q: query, provider: "all", limit: 100, offset: 0 },
        { signal: controller.signal, cache: "no-store" },
      )
      .then((response) => {
        if (response.status !== 200) throw apiResponseError(response);
        setItems(response.data.items ?? []);
        setSelected(undefined);
      })
      .catch((reason: unknown) => {
        if (!controller.signal.aborted)
          setError(errorMessage(reason, copy.loadError));
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
    return () => controller.abort();
  }, [copy.loadError, kind, query]);

  useEffect(() => {
    if (!editing && !creating) return;
    const controller = new AbortController();
    const timer = window.setTimeout(() => {
      if (kind === "recordings" || kind === "releases") {
        void listLocalProviderArtists(
          { q: artistQuery, provider: "all", limit: 20, offset: 0 },
          { signal: controller.signal, cache: "no-store" },
        )
          .then((response) => {
            if (response.status === 200) setArtists(response.data.items ?? []);
          })
          .catch(() => undefined);
      }
      if (kind === "releases") {
        void listLocalProviderRecordings(
          { q: recordingQuery, provider: "all", limit: 20, offset: 0 },
          { signal: controller.signal, cache: "no-store" },
        )
          .then((response) => {
            if (response.status === 200)
              setRecordings(response.data.items ?? []);
          })
          .catch(() => undefined);
      }
    }, 180);
    return () => {
      window.clearTimeout(timer);
      controller.abort();
    };
  }, [artistQuery, creating, editing, kind, recordingQuery]);

  useEffect(() => {
    const retryAttempt = genreRetry;
    if (!genreQuery.trim() || !genreStatus?.updated_at || !genreStatus.count) {
      setGenreOptions([]);
      return;
    }
    const controller = new AbortController();
    setGenreLoading(true);
    setGenreError("");
    void searchGenreCatalog(
      { query: genreQuery.trim(), limit: 40 },
      { signal: controller.signal, cache: "no-store" },
    )
      .then((response) => {
        if (retryAttempt !== genreRetryRef.current) return;
        if (response.status !== 200) throw apiResponseError(response);
        setGenreOptions(response.data.items ?? []);
      })
      .catch((reason: unknown) => {
        if (
          !controller.signal.aborted &&
          retryAttempt === genreRetryRef.current
        )
          setGenreError(errorMessage(reason, copy.loadError));
      })
      .finally(() => {
        if (
          !controller.signal.aborted &&
          retryAttempt === genreRetryRef.current
        )
          setGenreLoading(false);
      });
    return () => controller.abort();
  }, [copy.loadError, genreQuery, genreRetry, genreStatus]);

  const hasLocalMatch = items.some((item) => item.provider_code === "local");
  const genreAvailable = Boolean(
    genreStatus?.updated_at && genreStatus.count > 0,
  );
  const selectedTitle =
    selected?.kind === "artist" ? selected.name : selected?.title;
  const savingAllowed =
    !saving &&
    (!creating && selected ? selected.provider_code === "local" : true);

  async function search(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (saving) return;
    detailReadGeneration.current += 1;
    setLoading(false);
    setQuery(searchInput.trim());
    setNotice("");
    setError("");
    setSelected(undefined);
    setCreating(false);
    setEditing(false);
  }

  async function select(item: CatalogEntry) {
    if (saving) return;
    const requestKind = kind;
    const generation = ++detailReadGeneration.current;
    setError("");
    setNotice("");
    setCreating(false);
    setEditing(false);
    setSelected(undefined);
    if (item.provider_code !== "local") {
      setSelected(item);
      setLoading(false);
      return;
    }
    setLoading(true);
    try {
      const response = await apiFor[requestKind].read(item.id, {
        cache: "no-store",
      });
      if (
        generation !== detailReadGeneration.current ||
        currentKind.current !== requestKind
      )
        return;
      if (response.status !== 200) throw apiResponseError(response);
      setSelected(response.data);
    } catch (reason) {
      if (
        generation === detailReadGeneration.current &&
        currentKind.current === requestKind
      )
        setError(errorMessage(reason, copy.loadError));
    } finally {
      if (
        generation === detailReadGeneration.current &&
        currentKind.current === requestKind
      )
        setLoading(false);
    }
  }

  function beginCreate() {
    if (saving) return;
    detailReadGeneration.current += 1;
    setLoading(false);
    setSelected(undefined);
    setDraft(emptyDraft());
    setCreating(true);
    setEditing(true);
    setError("");
    setNotice("");
  }

  function beginEdit() {
    if (!selected || selected.provider_code !== "local") return;
    setDraft(draftFromItem(selected));
    setEditing(true);
    setError("");
    setNotice("");
  }

  function cancel() {
    if (saving) return;
    setEditing(false);
    setCreating(false);
    setDraft(emptyDraft());
    setError("");
  }

  async function save(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (saving) return;
    if (!formIsValid(kind, draft)) {
      setError(copy.invalidDuration);
      return;
    }
    setSaving(true);
    setError("");
    try {
      const body = writeBody(kind, draft);
      const response = creating
        ? await apiFor[kind].create(body)
        : selected
          ? await apiFor[kind].update(selected.id, body)
          : undefined;
      if (!response) return;
      if (response.status !== 200) throw apiResponseError(response);
      setSelected(response.data);
      setItems((current) => [
        response.data,
        ...current.filter((item) => item.id !== response.data.id),
      ]);
      setCreating(false);
      setEditing(false);
      setNotice(creating ? copy.created : copy.updated);
    } catch (reason) {
      setError(errorMessage(reason, copy.saveError));
    } finally {
      setSaving(false);
    }
  }

  async function loadGenreStatus() {
    if (genreStatus || genreStatusLoading) return;
    setGenreStatusLoading(true);
    setGenreStatusError("");
    try {
      const response = await getGenreCatalog({ cache: "no-store" });
      if (response.status !== 200) throw apiResponseError(response);
      setGenreStatus(response.data);
    } catch (reason) {
      setGenreStatusError(errorMessage(reason, copy.loadError));
    } finally {
      setGenreStatusLoading(false);
    }
  }

  return (
    <main
      className="library-catalog own-catalog"
      aria-labelledby="own-catalog-title"
    >
      <header className="catalog-header">
        <div>
          <h1 id="own-catalog-title">{copy.title}</h1>
          <p className="catalog-muted">{copy.description}</p>
        </div>
      </header>
      <nav
        className="catalog-segments own-catalog-tabs"
        aria-label={copy.entityTypes}
      >
        {(["artists", "recordings", "releases"] as const).map((tab) => (
          <button
            key={tab}
            type="button"
            aria-pressed={kind === tab}
            disabled={saving}
            onClick={() => {
              detailReadGeneration.current += 1;
              setLoading(false);
              setKind(tab);
              setSearchInput("");
              setQuery("");
              setItems([]);
              setSelected(undefined);
              setCreating(false);
              setEditing(false);
              setError("");
              setNotice("");
            }}
          >
            {copy[tab]}
          </button>
        ))}
      </nav>
      <div className="own-catalog-workspace">
        <section
          className="catalog-state own-catalog-list"
          aria-labelledby="own-catalog-search-title"
        >
          <h2 id="own-catalog-search-title">{copy.searchTitle}</h2>
          <p>{copy.searchBeforeCreate}</p>
          <form className="catalog-search" onSubmit={search}>
            <label className="catalog-sr-only" htmlFor="own-catalog-search">
              {copy.searchLabel}
            </label>
            <input
              id="own-catalog-search"
              type="search"
              value={searchInput}
              onChange={(event) => setSearchInput(event.currentTarget.value)}
              placeholder={copy.searchPlaceholder}
              disabled={saving}
            />
            <button type="submit" disabled={saving}>
              {copy.search}
            </button>
          </form>
          {loading && <p role="status">{copy.loading}</p>}
          {error && (
            <p className="own-catalog-error" role="alert">
              {error}
            </p>
          )}
          {query && !loading && !error && (
            <div className="own-catalog-empty">
              {items.length === 0 ? <p>{copy.noResults}</p> : null}
              <AppButton isDisabled={saving} onPress={beginCreate}>
                {copy.createNoMatch[kind]}
              </AppButton>
            </div>
          )}
          <ul className="own-catalog-results">
            {items.map((item) => (
              <li key={item.id}>
                <button
                  type="button"
                  disabled={saving}
                  className={selected?.id === item.id ? "is-selected" : ""}
                  onClick={() => void select(item)}
                >
                  <span>
                    <strong>
                      {item.kind === "artist" ? item.name : item.title}
                    </strong>
                    <small>
                      {item.provider_code}
                      {item.kind === "release" && item.release_group_title
                        ? ` · ${item.release_group_title}`
                        : ""}
                    </small>
                  </span>
                  <span className="own-catalog-provider">
                    {item.provider_code}
                  </span>
                </button>
              </li>
            ))}
          </ul>
          {query && items.length > 0 && !hasLocalMatch && (
            <p className="catalog-muted">{copy.externalCandidate}</p>
          )}
        </section>

        <section
          className="catalog-state own-catalog-detail"
          aria-labelledby="own-catalog-detail-title"
        >
          <h2 id="own-catalog-detail-title">{copy.details}</h2>
          {selected && !editing ? (
            <div>
              <h3>{selectedTitle || copy.untitled}</h3>
              <p className="catalog-muted">
                {selected.provider_code} · {selected.provider_key}
              </p>
              {selected.provider_code === "local" ? (
                <>
                  {selected.kind === "artist" && (
                    <p>{selected.sort_name || copy.noSortName}</p>
                  )}
                  {selected.kind !== "artist" && <p>{selected.title}</p>}
                  {selected.genres?.length ? (
                    <p>
                      {selected.genres.map((genre) => genre.name).join(", ")}
                    </p>
                  ) : null}
                  {selected.kind === "recording" &&
                    selected.duration_ms != null && (
                      <p>{formatDuration(selected.duration_ms)}</p>
                    )}
                  {selected.kind === "release" && (
                    <ol className="own-catalog-positions">
                      {(selected.tracks ?? []).map((track) => {
                        const duration =
                          track.duration_override_ms ??
                          track.recording_duration_ms;
                        return (
                          <li key={track.id}>
                            {track.displayed_number}.{" "}
                            {track.title_override || track.recording_title}{" "}
                            <small>
                              {duration == null
                                ? copy.durationNotSpecified
                                : formatDuration(duration)}
                            </small>
                          </li>
                        );
                      })}
                    </ol>
                  )}
                  <AppButton onPress={beginEdit}>{copy.edit}</AppButton>
                </>
              ) : (
                <p>{copy.externalReadOnly}</p>
              )}
            </div>
          ) : creating || editing ? (
            <form
              className="own-catalog-form"
              onSubmit={(event) => void save(event)}
            >
              <h3>{creating ? copy.create[kind] : copy.editing}</h3>
              <fieldset disabled={saving} className="own-catalog-edit-fields">
                {kind === "artists" ? (
                  <>
                    <Field
                      label={copy.name}
                      value={draft.name}
                      required
                      onChange={(name) => setDraft({ ...draft, name })}
                    />
                    <Field
                      label={copy.sortName}
                      value={draft.sort_name}
                      onChange={(sort_name) =>
                        setDraft({ ...draft, sort_name })
                      }
                    />
                    <GenreField
                      kind={kind}
                      saving={saving}
                      draft={draft}
                      setDraft={setDraft}
                      genreCopy={genreCopy}
                      locale={locale}
                      genreStatus={genreStatus}
                      genreQuery={genreQuery}
                      setGenreQuery={setGenreQuery}
                      genreOptions={genreOptions}
                      genreLoading={genreLoading}
                      genreError={genreError}
                      genreStatusLoading={genreStatusLoading}
                      genreStatusError={genreStatusError}
                      genreAvailable={genreAvailable}
                      onLoad={loadGenreStatus}
                      onRetry={() => setGenreRetry((retry) => retry + 1)}
                    />
                    <Field
                      label={copy.tags}
                      value={draft.tags}
                      onChange={(tags) => setDraft({ ...draft, tags })}
                    />
                  </>
                ) : kind === "recordings" ? (
                  <>
                    <Field
                      label={copy.titleField}
                      value={draft.title}
                      required
                      onChange={(title) => setDraft({ ...draft, title })}
                    />
                    <Field
                      label={copy.duration}
                      value={draft.duration}
                      placeholder="3:42"
                      onChange={(duration) =>
                        setDraft({ ...draft, duration, durationEdited: true })
                      }
                    />
                    <ReferenceSearch
                      label={copy.performers}
                      query={artistQuery}
                      onQuery={setArtistQuery}
                      results={artists}
                      copy={copy}
                      onChoose={(item) => {
                        const credit = {
                          clientKey: crypto.randomUUID(),
                          artist_id: item.id,
                          credit_name: item.name ?? "",
                          join_phrase: draft.credits.length ? ", " : "",
                        };
                        setDraft({
                          ...draft,
                          credits: [...draft.credits, credit],
                        });
                      }}
                    />
                    <CreditList
                      credits={draft.credits}
                      artists={artists}
                      copy={copy}
                      onChange={(credits) => setDraft({ ...draft, credits })}
                    />
                    <GenreField
                      kind={kind}
                      saving={saving}
                      draft={draft}
                      setDraft={setDraft}
                      genreCopy={genreCopy}
                      locale={locale}
                      genreStatus={genreStatus}
                      genreQuery={genreQuery}
                      setGenreQuery={setGenreQuery}
                      genreOptions={genreOptions}
                      genreLoading={genreLoading}
                      genreError={genreError}
                      genreStatusLoading={genreStatusLoading}
                      genreStatusError={genreStatusError}
                      genreAvailable={genreAvailable}
                      onLoad={loadGenreStatus}
                      onRetry={() => setGenreRetry((retry) => retry + 1)}
                    />
                    <Field
                      label={copy.tags}
                      value={draft.tags}
                      onChange={(tags) => setDraft({ ...draft, tags })}
                    />
                  </>
                ) : (
                  <>
                    <Field
                      label={copy.releaseGroupTitle}
                      value={draft.release_group_title}
                      required
                      onChange={(release_group_title) =>
                        setDraft({ ...draft, release_group_title })
                      }
                    />
                    <Field
                      label={copy.titleField}
                      value={draft.title}
                      required
                      onChange={(title) => setDraft({ ...draft, title })}
                    />
                    <div className="own-catalog-grid">
                      <Field
                        label={copy.primaryType}
                        value={draft.primary_type}
                        onChange={(primary_type) =>
                          setDraft({ ...draft, primary_type })
                        }
                      />
                      <Field
                        label={copy.secondaryTypes}
                        value={draft.secondary_types}
                        onChange={(secondary_types) =>
                          setDraft({ ...draft, secondary_types })
                        }
                      />
                      <Field
                        label={copy.language}
                        value={draft.language}
                        onChange={(language) =>
                          setDraft({ ...draft, language })
                        }
                      />
                      <Field
                        label={copy.script}
                        value={draft.script}
                        onChange={(script) => setDraft({ ...draft, script })}
                      />
                    </div>
                    <GenreField
                      kind={kind}
                      saving={saving}
                      draft={draft}
                      setDraft={setDraft}
                      genreCopy={genreCopy}
                      locale={locale}
                      genreStatus={genreStatus}
                      genreQuery={genreQuery}
                      setGenreQuery={setGenreQuery}
                      genreOptions={genreOptions}
                      genreLoading={genreLoading}
                      genreError={genreError}
                      genreStatusLoading={genreStatusLoading}
                      genreStatusError={genreStatusError}
                      genreAvailable={genreAvailable}
                      onLoad={loadGenreStatus}
                      onRetry={() => setGenreRetry((retry) => retry + 1)}
                    />
                    <ReferenceSearch
                      label={copy.performers}
                      query={artistQuery}
                      onQuery={setArtistQuery}
                      results={artists}
                      copy={copy}
                      onChoose={(item) => {
                        setDraft({
                          ...draft,
                          credits: [
                            ...draft.credits,
                            {
                              clientKey: crypto.randomUUID(),
                              artist_id: item.id,
                              credit_name: item.name ?? "",
                              join_phrase: draft.credits.length ? ", " : "",
                            },
                          ],
                        });
                      }}
                    />
                    <CreditList
                      credits={draft.credits}
                      artists={artists}
                      copy={copy}
                      onChange={(credits) => setDraft({ ...draft, credits })}
                    />
                    <fieldset className="own-catalog-track-editor">
                      <legend>{copy.positions}</legend>
                      <ReferenceSearch
                        label={copy.existingRecording}
                        query={recordingQuery}
                        onQuery={setRecordingQuery}
                        results={recordings}
                        copy={copy}
                        onChoose={(item) =>
                          setDraft({
                            ...draft,
                            tracks: [
                              ...draft.tracks,
                              {
                                clientKey: crypto.randomUUID(),
                                recording_id: item.id,
                                recording_title: item.title ?? "",
                                recording_duration_ms: item.duration_ms ?? null,
                                title_override: "",
                                duration: "",
                                originalDurationMS: null,
                                durationEdited: false,
                              },
                            ],
                          })
                        }
                      />
                      {draft.tracks.map((track, index) => (
                        <div
                          className="own-catalog-track"
                          key={track.clientKey}
                        >
                          <span>
                            {index + 1}.{" "}
                            {track.title_override || track.recording_title}
                            <small className="catalog-muted">
                              {copy.recordingDefault}:{" "}
                              {track.recording_title || copy.recordingSelected}{" "}
                              ·{" "}
                              {track.recording_duration_ms == null
                                ? copy.durationNotSpecified
                                : formatDuration(track.recording_duration_ms)}
                            </small>
                            <small className="catalog-muted">
                              {copy.effectivePosition}:{" "}
                              {track.title_override ||
                                track.recording_title ||
                                copy.recordingSelected}{" "}
                              ·{" "}
                              {formatEffectiveDuration(
                                track,
                                copy.durationNotSpecified,
                              )}
                            </small>
                          </span>
                          <Field
                            label={copy.positionTitle}
                            value={track.title_override}
                            onChange={(title_override) =>
                              setDraft({
                                ...draft,
                                tracks: draft.tracks.map((item, i) =>
                                  i === index
                                    ? { ...item, title_override }
                                    : item,
                                ),
                              })
                            }
                          />
                          <Field
                            label={copy.durationOverride}
                            value={track.duration}
                            placeholder="3:42"
                            onChange={(duration) =>
                              setDraft({
                                ...draft,
                                tracks: draft.tracks.map((item, i) =>
                                  i === index
                                    ? {
                                        ...item,
                                        duration,
                                        durationEdited: true,
                                      }
                                    : item,
                                ),
                              })
                            }
                          />
                          <div className="own-catalog-track-actions">
                            <button
                              type="button"
                              disabled={index === 0}
                              aria-label={`${copy.moveUp} ${index + 1}`}
                              onClick={() => reorderTracks(index, -1)}
                            >
                              ↑
                            </button>
                            <button
                              type="button"
                              disabled={index === draft.tracks.length - 1}
                              aria-label={`${copy.moveDown} ${index + 1}`}
                              onClick={() => reorderTracks(index, 1)}
                            >
                              ↓
                            </button>
                            <button
                              type="button"
                              onClick={() =>
                                setDraft({
                                  ...draft,
                                  tracks: draft.tracks.filter(
                                    (_, i) => i !== index,
                                  ),
                                })
                              }
                            >
                              {copy.remove}
                            </button>
                          </div>
                        </div>
                      ))}
                    </fieldset>
                  </>
                )}
              </fieldset>
              {error && (
                <p role="alert" className="own-catalog-error">
                  {error}
                </p>
              )}
              <div className="own-catalog-actions">
                <button type="submit" disabled={!savingAllowed}>
                  {saving
                    ? copy.saving
                    : creating
                      ? copy.create[kind]
                      : copy.save}
                </button>
                <button type="button" disabled={saving} onClick={cancel}>
                  {copy.cancel}
                </button>
              </div>
              <p className="catalog-muted">{copy.localOnly}</p>
            </form>
          ) : (
            <p className="catalog-muted">
              {query ? copy.selectResult : copy.searchPrompt}
            </p>
          )}
        </section>
      </div>
      {notice && (
        <p className="own-catalog-notice" role="status">
          {notice}
        </p>
      )}
    </main>
  );

  function reorderTracks(index: number, direction: -1 | 1) {
    const next = [...draft.tracks];
    const target = index + direction;
    [next[index], next[target]] = [next[target], next[index]];
    setDraft({ ...draft, tracks: next });
  }
}

function formIsValid(kind: Kind, draft: Draft): boolean {
  if (kind === "artists") return Boolean(draft.name.trim());
  if (!draft.title.trim()) return false;
  if (kind === "recordings")
    return !Number.isNaN(parseDuration(draft.duration));
  return (
    Boolean(draft.release_group_title.trim()) &&
    draft.tracks.every((track) => !Number.isNaN(parseDuration(track.duration)))
  );
}

function Field({
  label,
  value,
  onChange,
  required,
  placeholder,
}: {
  label: string;
  value: string;
  onChange: (value: string) => void;
  required?: boolean;
  placeholder?: string;
}) {
  return (
    <label className="own-catalog-field">
      <span>
        {label}
        {required ? " *" : ""}
      </span>
      <input
        required={required}
        value={value}
        placeholder={placeholder}
        onChange={(event) => onChange(event.currentTarget.value)}
      />
    </label>
  );
}

function ReferenceSearch({
  label,
  query,
  onQuery,
  results,
  copy,
  onChoose,
}: {
  label: string;
  query: string;
  onQuery: (value: string) => void;
  results: CatalogEntry[];
  copy: OwnCatalogMessages;
  onChoose: (item: CatalogEntry) => void;
}) {
  return (
    <div className="own-catalog-reference-search">
      <label className="own-catalog-field">
        <span>{label}</span>
        <input
          value={query}
          onChange={(event) => onQuery(event.currentTarget.value)}
          placeholder={copy.referenceSearch}
        />
      </label>
      {query.trim() && results.length > 0 && (
        <ul>
          {results.map((item) => (
            <li key={item.id}>
              <button type="button" onClick={() => onChoose(item)}>
                {item.name || item.title} <small>{item.provider_code}</small>
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

function CreditList({
  credits,
  artists,
  copy,
  onChange,
}: {
  credits: CreditDraft[];
  artists: CatalogEntry[];
  copy: OwnCatalogMessages;
  onChange: (credits: CreditDraft[]) => void;
}) {
  return credits.length ? (
    <ol className="own-catalog-credit-list">
      {credits.map((credit, index) => (
        <li key={credit.clientKey}>
          <span>
            {artists.find((artist) => artist.id === credit.artist_id)?.name ||
              credit.credit_name}
          </span>
          <label>
            {copy.creditName}
            <input
              value={credit.credit_name}
              onChange={(event) =>
                onChange(
                  credits.map((item, i) =>
                    i === index
                      ? { ...item, credit_name: event.currentTarget.value }
                      : item,
                  ),
                )
              }
            />
          </label>
          <label>
            {copy.joinPhrase}
            <input
              value={credit.join_phrase}
              onChange={(event) =>
                onChange(
                  credits.map((item, i) =>
                    i === index
                      ? { ...item, join_phrase: event.currentTarget.value }
                      : item,
                  ),
                )
              }
            />
          </label>
          <button
            type="button"
            onClick={() => onChange(credits.filter((_, i) => i !== index))}
          >
            {copy.remove}
          </button>
        </li>
      ))}
    </ol>
  ) : null;
}

function GenreField({
  kind,
  saving,
  draft,
  setDraft,
  genreCopy,
  locale,
  genreStatus,
  genreQuery,
  setGenreQuery,
  genreOptions,
  genreLoading,
  genreError,
  genreStatusLoading,
  genreStatusError,
  genreAvailable,
  onLoad,
  onRetry,
}: {
  kind: Kind;
  saving: boolean;
  draft: Draft;
  setDraft: (draft: Draft) => void;
  genreCopy: GenreMessages;
  locale: "en" | "ru";
  genreStatus?: GenreCatalogStatus;
  genreQuery: string;
  setGenreQuery: (value: string) => void;
  genreOptions: GenreCatalogItem[];
  genreLoading: boolean;
  genreError: string;
  genreStatusLoading: boolean;
  genreStatusError: string;
  genreAvailable: boolean;
  onLoad: () => void;
  onRetry: () => void;
}) {
  const owner =
    kind === "artists"
      ? "artist"
      : kind === "releases"
        ? "release"
        : "recording";
  return (
    <div className="own-catalog-genre">
      <span>{genreCopy.label[owner]}</span>
      <GenrePicker
        value={draft.genres}
        onChange={(genres) =>
          setDraft({ ...draft, genres: genres.map(asGenre) })
        }
        editing
        label={genreCopy.label[owner]}
        copy={genreCopy}
        options={genreOptions}
        query={genreQuery}
        onQueryChange={setGenreQuery}
        loading={genreLoading}
        catalogError={genreError}
        onClearCatalogError={() => undefined}
        onRetryCatalog={onRetry}
        catalogStatus={genreStatus}
        catalogStatusLoading={genreStatusLoading}
        catalogStatusError={genreStatusError}
        onOpenCatalog={onLoad}
        onRetryStatus={onLoad}
        disabled={saving}
      />
      {!genreAvailable && !genreStatusLoading && (
        <small>
          {locale === "ru"
            ? "Каталог жанров недоступен; запись без жанров можно сохранить."
            : "The genre catalog is unavailable; the record can still be saved without genres."}
        </small>
      )}
    </div>
  );
}

type OwnCatalogMessages =
  (typeof ownCatalogMessages)[keyof typeof ownCatalogMessages];
const ownCatalogMessages = {
  en: {
    title: "Own catalog",
    description:
      "Search provider records, then create or edit records owned by Local.",
    entityTypes: "Provider record types",
    artists: "Artists",
    recordings: "Recordings",
    releases: "Releases",
    searchTitle: "Find a provider record",
    searchBeforeCreate:
      "Search existing providers before creating a Local record.",
    searchLabel: "Search provider records",
    searchPlaceholder: "Enter a name or title",
    search: "Search",
    loading: "Searching…",
    loadError: "Could not load provider records.",
    noResults: "No provider records match this search.",
    createNoMatch: {
      artists: "None of these — create a Local artist",
      recordings: "None of these — create a Local recording",
      releases: "None of these — create a Local release",
    },
    externalCandidate:
      "Existing external provider records are shown first; select one to inspect it instead of creating a duplicate.",
    create: {
      artists: "Create Local artist",
      recordings: "Create Local recording",
      releases: "Create Local release",
    },
    details: "Record details",
    untitled: "Untitled record",
    noSortName: "No sort name",
    externalReadOnly:
      "External provider records are read-only here. They can be selected as references in Local records.",
    edit: "Edit Local record",
    editing: "Edit record",
    name: "Name",
    sortName: "Sort name",
    titleField: "Title",
    duration: "Duration (seconds or M:SS)",
    tags: "Tags (comma-separated)",
    releaseGroupTitle: "Release group title",
    primaryType: "Primary type",
    secondaryTypes: "Secondary types (comma-separated)",
    language: "Language",
    script: "Script",
    performers: "Artists",
    referenceSearch: "Search existing providers",
    creditName: "Credit name",
    joinPhrase: "Join phrase",
    positions: "Medium 1 positions",
    existingRecording: "Existing recording",
    recordingSelected: "Recording",
    recordingDefault: "Recording default",
    effectivePosition: "Position values",
    durationNotSpecified: "duration not specified",
    positionTitle: "Title override",
    durationOverride: "Duration override (seconds or M:SS)",
    moveUp: "Move up",
    moveDown: "Move down",
    remove: "Remove",
    createAction: "Create",
    save: "Save changes",
    cancel: "Cancel",
    saving: "Saving…",
    invalidDuration:
      "Check the required fields and enter duration as whole seconds or M:SS.",
    saveError: "Could not save the provider record.",
    created: "Local provider record created.",
    updated: "Local provider record updated.",
    localOnly:
      "Only Local records can be changed here. Saving provider metadata does not publish audio or create library records.",
    selectResult: "Select a result to view its details.",
    searchPrompt:
      "Search all providers to view records or create a Local record.",
  },
  ru: {
    title: "Собственный каталог",
    description:
      "Ищите записи провайдеров, создавайте и редактируйте записи Local.",
    entityTypes: "Типы записей провайдера",
    artists: "Артисты",
    recordings: "Композиции",
    releases: "Релизы",
    searchTitle: "Найти запись провайдера",
    searchBeforeCreate:
      "Перед созданием записи Local поищите её у существующих провайдеров.",
    searchLabel: "Поиск записей провайдера",
    searchPlaceholder: "Введите имя или название",
    search: "Найти",
    loading: "Поиск…",
    loadError: "Не удалось загрузить записи провайдера.",
    noResults: "По этому запросу ничего не найдено.",
    createNoMatch: {
      artists: "Нет подходящего — создать артиста Local",
      recordings: "Нет подходящей — создать композицию Local",
      releases: "Нет подходящего — создать релиз Local",
    },
    externalCandidate:
      "Сначала показаны записи внешних провайдеров: выберите запись, чтобы проверить её и не создавать дубликат.",
    create: {
      artists: "Создать артиста Local",
      recordings: "Создать композицию Local",
      releases: "Создать релиз Local",
    },
    details: "Данные записи",
    untitled: "Без названия",
    noSortName: "Имя для сортировки не задано",
    externalReadOnly:
      "Внешние записи доступны только для чтения. Их можно выбирать как ссылки в записях Local.",
    edit: "Изменить запись Local",
    editing: "Изменение записи",
    name: "Имя",
    sortName: "Имя для сортировки",
    titleField: "Название",
    duration: "Длительность (секунды или М:СС)",
    tags: "Теги (через запятую)",
    releaseGroupTitle: "Название группы релизов",
    primaryType: "Основной тип",
    secondaryTypes: "Дополнительные типы (через запятую)",
    language: "Язык",
    script: "Письменность",
    performers: "Артисты",
    referenceSearch: "Поиск существующих провайдеров",
    creditName: "Имя в кредите",
    joinPhrase: "Связка имени",
    positions: "Позиции носителя 1",
    existingRecording: "Существующая композиция",
    recordingSelected: "Композиция",
    recordingDefault: "Данные композиции",
    effectivePosition: "Данные позиции",
    durationNotSpecified: "длительность не указана",
    positionTitle: "Переопределение названия",
    durationOverride: "Переопределение длительности (секунды или М:СС)",
    moveUp: "Переместить выше",
    moveDown: "Переместить ниже",
    remove: "Убрать",
    createAction: "Создать",
    save: "Сохранить изменения",
    cancel: "Отмена",
    saving: "Сохранение…",
    invalidDuration:
      "Проверьте обязательные поля и введите длительность в секундах или формате М:СС.",
    saveError: "Не удалось сохранить запись провайдера.",
    created: "Запись Local создана.",
    updated: "Запись Local обновлена.",
    localOnly:
      "Здесь можно менять только записи Local. Сохранение метаданных не публикует аудио и не создаёт записи медиатеки.",
    selectResult: "Выберите результат, чтобы просмотреть подробности.",
    searchPrompt:
      "Ищите во всех провайдерах, чтобы просмотреть записи или создать запись Local.",
  },
} as const;
