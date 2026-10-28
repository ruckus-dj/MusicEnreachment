import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import { apiResponseError } from "../../api/client/errors";
import { useOperationFollower } from "../../api/client/useOperationFollower";
import {
  createLibraryRelease,
  getGetLibraryCoverCandidateImageUrl,
  getLibraryRecordingSearch,
  listLibraryArtists,
  listLibraryCoverCandidates,
  listLibraryRecordings,
  listLibrarySourceVariants,
  startLibraryRecordingSearch,
} from "../../api/generated/client";
import type {
  LibraryArtistSummary,
  LibraryCoverCandidateOption,
  LibraryRecordingOption,
  LibraryRecordingSearchResultOutputBody,
  LibrarySourceVariantOption,
  OperationResponse,
} from "../../api/generated/client.schemas";
import "./newLibraryRelease.css";

type Credit = {
  key: string;
  name: string;
  artistId: string;
  credit: string;
  role: string;
  join: string;
};
type Source = {
  media_variant_id: string;
  metadata_result_id: string | null;
  title: string;
  filename: string;
  codec: string;
  container: string;
  duration_ms: number | null;
  locations: { relative_path: string; root_display_name: string }[];
};
type Position = {
  key: string;
  number: string;
  title: string;
  duration: string;
  recording: "new" | "local" | "provider";
  recordingId: string;
  recordingTitle: string;
  recordingLabel: string;
  provider?: Record<string, unknown>;
  sources: Source[];
  selectedSource: string;
};
type Medium = {
  key: string;
  title: string;
  format: string;
  positions: Position[];
};
type PickerKind = "artist" | "recording" | "source";
type PickerItem =
  | {
      kind: "artist";
      id: string;
      name: string;
      artist: LibraryArtistSummary;
    }
  | {
      kind: "recording";
      id: string;
      title: string;
      recording: LibraryRecordingOption;
    }
  | {
      kind: "source";
      id: string;
      title: string;
      locations: LibrarySourceVariantOption["locations"];
      source: LibrarySourceVariantOption;
    };
type PickerState = {
  kind: PickerKind;
  key: string;
  query: string;
  page: number;
  items: PickerItem[];
  total: number;
  loading: boolean;
  error: string;
};
type Draft = {
  releaseID: string;
  title: string;
  date: string;
  country: string;
  label: string;
  catalog: string;
  type: string;
  credits: Credit[];
  media: Medium[];
  cover: { metadata_result_id: string; expected_sha256: string } | null;
};
const key = () => crypto.randomUUID();
const blankPosition = (): Position => ({
  key: key(),
  number: "",
  title: "",
  duration: "",
  recording: "new",
  recordingId: "",
  recordingTitle: "",
  recordingLabel: "",
  sources: [],
  selectedSource: "",
});
const initialDraft = (): Draft => ({
  releaseID: key(),
  title: "",
  date: "",
  country: "",
  label: "",
  catalog: "",
  type: "",
  credits: [],
  media: [],
  cover: null,
});
// Deliberately in-memory only: the editable draft survives screen unmounts without persistence.
let draftMemory = initialDraft();
let pendingCreate: { releaseID: string; payload: unknown } | undefined;
const pageSize = 25;
function errorText(data: unknown, status: number) {
  return apiResponseError(
    { status, data },
    `Запрос не выполнен (HTTP ${status}).`,
  ).message;
}
function millis(value: string) {
  if (!value.trim()) return null;
  const match = /^(\d+):([0-5]?\d)$/.exec(value.trim());
  return match ? (Number(match[1]) * 60 + Number(match[2])) * 1000 : null;
}

export function NewLibraryReleaseScreen() {
  const [draft, setDraft] = useState<Draft>(() => structuredClone(draftMemory));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [conflictReleaseID, setConflictReleaseID] = useState("");
  const [notice, setNotice] = useState("");
  const [replaceTarget, setReplaceTarget] = useState<{
    medium: string;
    position: string;
  } | null>(null);
  const [replaceSelection, setReplaceSelection] = useState(false);
  const [picker, setPicker] = useState<PickerState | null>(null);
  const [operation, setOperation] = useState<OperationResponse>();
  const [providerResult, setProviderResult] =
    useState<LibraryRecordingSearchResultOutputBody>();
  const [providerPositionKey, setProviderPositionKey] = useState("");
  const [providerQueries, setProviderQueries] = useState<
    Record<string, string>
  >({});
  const [coverItems, setCoverItems] = useState<LibraryCoverCandidateOption[]>(
    [],
  );
  const [coverPage, setCoverPage] = useState(0);
  const [coverTotal, setCoverTotal] = useState(0);
  const [coverState, setCoverState] = useState({
    loading: false,
    error: "",
    loaded: false,
  });
  const active = useRef(true);
  const pickerGeneration = useRef(0);
  const coverGeneration = useRef(0);
  const providerGeneration = useRef(0);
  const queryClient = useQueryClient();
  useEffect(() => {
    active.current = true;
    return () => {
      active.current = false;
    };
  }, []);
  function update(change: (current: Draft) => Draft) {
    setDraft((current) => {
      const next = change(current);
      draftMemory = next;
      return next;
    });
  }
  const terminal =
    !!operation &&
    ["succeeded", "failed", "cancelled"].includes(operation.state);
  useOperationFollower(operation?.id, {
    enabled: !!operation && !terminal,
    initialSnapshot: operation,
    onSnapshot: setOperation,
    onError: () =>
      setNotice("Связь с операцией прервана; повторное чтение продолжится."),
    onHttpError: (response) => {
      setNotice("Не удалось обновить состояние поиска.");
      return apiResponseError(
        response,
        "Не удалось получить состояние поиска.",
      );
    },
    onStreamError: () =>
      setNotice("Ожидается восстановление соединения с поиском."),
  });
  useEffect(() => {
    if (!operation || !terminal || operation.state === "succeeded") return;
    setNotice(
      operation.safe_error ||
        (operation.state === "cancelled"
          ? "Поиск MusicBrainz отменён."
          : "Поиск MusicBrainz завершился с ошибкой."),
    );
  }, [operation, terminal]);
  useEffect(() => {
    if (!operation || !terminal || operation.state !== "succeeded") return;
    let current = true;
    const generation = providerGeneration.current;
    void getLibraryRecordingSearch(operation.id)
      .then((response) => {
        if (!current || generation !== providerGeneration.current) return;
        if (response.status !== 200)
          throw new Error(errorText(response.data, response.status));
        setProviderResult(response.data);
      })
      .catch((reason: unknown) => {
        if (current && generation === providerGeneration.current)
          setNotice(
            reason instanceof Error
              ? reason.message
              : "Не удалось получить результаты MusicBrainz.",
          );
      });
    return () => {
      current = false;
    };
  }, [operation, terminal]);

  async function loadPicker(
    kind: PickerKind,
    id: string,
    query: string,
    offset: number,
  ) {
    const generation = ++pickerGeneration.current;
    setPicker({
      kind,
      key: id,
      query,
      page: offset,
      items: [],
      total: 0,
      loading: true,
      error: "",
    });
    try {
      let items: PickerItem[] = [];
      let total = 0;
      if (kind === "artist") {
        const response = await listLibraryArtists({
          limit: pageSize,
          offset,
          q: query,
          publication: "all",
        });
        if (!active.current || generation !== pickerGeneration.current) return;
        if (response.status !== 200)
          throw new Error(errorText(response.data, response.status));
        items = response.data.items.map((artist) => ({
          kind: "artist",
          id: artist.id,
          name: artist.name,
          artist,
        }));
        total = response.data.total;
      } else if (kind === "recording") {
        const response = await listLibraryRecordings({
          limit: pageSize,
          offset,
          q: query,
        });
        if (!active.current || generation !== pickerGeneration.current) return;
        if (response.status !== 200)
          throw new Error(errorText(response.data, response.status));
        items = response.data.items.map((recording) => ({
          kind: "recording",
          id: recording.id,
          title: recording.title,
          recording,
        }));
        total = response.data.total;
      } else {
        const response = await listLibrarySourceVariants({
          limit: pageSize,
          offset,
          q: query,
        });
        if (!active.current || generation !== pickerGeneration.current) return;
        if (response.status !== 200)
          throw new Error(errorText(response.data, response.status));
        items = response.data.items.map((source) => ({
          kind: "source",
          id: source.media_variant_id,
          title: source.title,
          locations: source.locations,
          source,
        }));
        total = response.data.total;
      }
      setPicker({
        kind,
        key: id,
        query,
        page: offset,
        items,
        total,
        loading: false,
        error: "",
      });
    } catch (reason) {
      if (active.current && generation === pickerGeneration.current)
        setPicker({
          kind,
          key: id,
          query,
          page: offset,
          items: [],
          total: 0,
          loading: false,
          error:
            reason instanceof Error
              ? reason.message
              : "Не удалось загрузить варианты.",
        });
    }
  }
  async function loadCovers(offset = 0) {
    const generation = ++coverGeneration.current;
    setCoverState({ loading: true, error: "", loaded: true });
    try {
      const r = await listLibraryCoverCandidates({
        limit: pageSize,
        offset,
      });
      if (r.status !== 200) throw new Error(errorText(r.data, r.status));
      if (!active.current || generation !== coverGeneration.current) return;
      setCoverItems(r.data.items);
      setCoverPage(offset);
      setCoverTotal(r.data.total);
      setCoverState({ loading: false, error: "", loaded: true });
    } catch (e) {
      if (!active.current || generation !== coverGeneration.current) return;
      setCoverState({
        loading: false,
        error: e instanceof Error ? e.message : "Не удалось загрузить обложки.",
        loaded: true,
      });
    }
  }
  function addCredit() {
    update((d) => ({
      ...d,
      credits: [
        ...d.credits,
        { key: key(), name: "", artistId: "", credit: "", role: "", join: "" },
      ],
    }));
  }
  function moveCredit(id: string, direction: -1 | 1) {
    update((d) => {
      const index = d.credits.findIndex((credit) => credit.key === id);
      const target = index + direction;
      if (index < 0 || target < 0 || target >= d.credits.length) return d;
      const credits = [...d.credits];
      [credits[index], credits[target]] = [credits[target], credits[index]];
      return { ...d, credits };
    });
  }
  function addMedium() {
    update((d) => ({
      ...d,
      media: [...d.media, { key: key(), title: "", format: "", positions: [] }],
    }));
  }
  function changeCredit(id: string, field: keyof Credit, value: string) {
    update((d) => ({
      ...d,
      credits: d.credits.map((c) =>
        c.key === id
          ? {
              ...c,
              [field]: value,
              ...(field === "name" ? { artistId: "" } : {}),
            }
          : c,
      ),
    }));
  }
  function selectArtist(id: string, artist: LibraryArtistSummary) {
    update((d) => ({
      ...d,
      credits: d.credits.map((c) =>
        c.key === id ? { ...c, name: artist.name, artistId: artist.id } : c,
      ),
    }));
    pickerGeneration.current += 1;
    setPicker(null);
  }
  function modifyPosition(
    mediumKey: string,
    positionKey: string,
    change: (p: Position) => Position,
  ) {
    update((d) => ({
      ...d,
      media: d.media.map((m) =>
        m.key === mediumKey
          ? {
              ...m,
              positions: m.positions.map((p) =>
                p.key === positionKey ? change(p) : p,
              ),
            }
          : m,
      ),
    }));
  }
  async function searchProvider(position: Position, query: string, offset = 0) {
    if (!query.trim()) return;
    const generation = ++providerGeneration.current;
    const targetKey = position.key;
    setNotice("");
    setProviderResult(undefined);
    setOperation(undefined);
    try {
      const r = await startLibraryRecordingSearch({
        query,
        offset,
        limit: pageSize,
      });
      if (
        !active.current ||
        generation !== providerGeneration.current ||
        !draftMemory.media.some((medium) =>
          medium.positions.some(
            (item) => item.key === targetKey && item.recording === "provider",
          ),
        )
      )
        return;
      if (r.status !== 202 && r.status !== 200)
        throw new Error(errorText(r.data, r.status));
      setProviderPositionKey(position.key);
      setOperation(r.data as OperationResponse);
    } catch (e) {
      if (active.current && generation === providerGeneration.current)
        setNotice(
          e instanceof Error
            ? e.message
            : "Не удалось начать MusicBrainz-поиск.",
        );
    }
  }
  async function submit() {
    if (busy) return;
    setBusy(true);
    setError("");
    setNotice("");
    const positions = draft.media.flatMap((medium) => medium.positions);
    if (
      positions.some(
        (position) =>
          position.duration.trim() && millis(position.duration) === null,
      )
    ) {
      setError(
        "Укажите длительность в формате м:с, например 3:42, или оставьте поле пустым.",
      );
      setBusy(false);
      return;
    }
    const credits = draft.credits.filter(
      (credit) => credit.artistId || credit.name.trim(),
    );
    let payload: Record<string, unknown> = {
      release_id: draft.releaseID,
      title: draft.title,
      release_date: draft.date,
      country: draft.country,
      label: draft.label,
      catalog_number: draft.catalog,
      release_type: draft.type,
      new_artists: credits
        .filter((c) => !c.artistId)
        .map((c) => ({ key: c.key, name: c.name })),
      new_recordings: draft.media.flatMap((m) =>
        m.positions
          .filter((p) => p.recording === "new")
          .map((p) => ({ key: p.key, title: p.recordingTitle })),
      ),
      credits: credits.map((c) => ({
        artist_ref: c.artistId
          ? { kind: "existing", id: c.artistId }
          : { kind: "new", key: c.key },
        credit_name: c.credit,
        role: c.role,
        join_phrase: c.join,
      })),
      media: draft.media.map((m) => ({
        key: m.key,
        title: m.title,
        format: m.format,
        positions: m.positions.map((p) => ({
          key: p.key,
          displayed_number: p.number,
          title: p.title,
          duration_ms: millis(p.duration),
          recording_ref:
            p.recording === "new"
              ? { kind: "new", key: p.key }
              : p.recording === "local"
                ? { kind: "existing", id: p.recordingId }
                : p.provider,
          sources: p.sources.map((s) => ({
            media_variant_id: s.media_variant_id,
            ...(s.metadata_result_id
              ? { metadata_result_id: s.metadata_result_id }
              : {}),
          })),
          selected_media_variant_id: p.selectedSource || null,
        })),
      })),
      cover: draft.cover,
    };
    let pendingID = draft.releaseID;
    const fingerprint = (value: unknown) => JSON.stringify(value);
    if (pendingCreate?.releaseID === pendingID) {
      if (fingerprint(pendingCreate.payload) !== fingerprint(payload)) {
        pendingID = key();
        payload = { ...payload, release_id: pendingID };
        update((current) => ({ ...current, releaseID: pendingID }));
        setNotice(
          `Изменения сохранены в новом запросе. Предыдущий запрос ${draft.releaseID} завершился неопределённо; его результат следует проверить отдельно.`,
        );
        pendingCreate = undefined;
      } else {
        payload = pendingCreate.payload as Record<string, unknown>;
      }
    }
    if (!pendingCreate || pendingCreate.releaseID !== pendingID)
      pendingCreate = {
        releaseID: pendingID,
        payload: structuredClone(payload),
      };
    setConflictReleaseID("");
    try {
      const response = await createLibraryRelease(
        payload as Parameters<typeof createLibraryRelease>[0],
      );
      if (response.status === 409) {
        setConflictReleaseID(pendingID);
        throw new Error(
          "Этот идентификатор релиза уже существует. Существующий релиз не подтверждает создание текущего черновика; черновик сохранён для проверки.",
        );
      }
      if (response.status !== 201)
        throw new Error(errorText(response.data, response.status));
      if (!active.current) return;
      await queryClient.invalidateQueries({ queryKey: ["/api/library"] });
      window.location.hash = `#/library/releases/${response.data.release_id}`;
      draftMemory = initialDraft();
      pendingCreate = undefined;
    } catch (e) {
      if (active.current)
        setError(
          e instanceof Error
            ? e.message
            : "Не удалось создать релиз. Черновик сохранён в памяти.",
        );
    } finally {
      if (active.current) setBusy(false);
    }
  }
  const selectedPosition =
    picker?.kind === "source"
      ? draft.media
          .flatMap((m) => m.positions.map((p) => ({ m, p })))
          .find(({ p }) => p.key === picker.key)
      : undefined;
  return (
    <main className="new-release" aria-labelledby="new-release-title">
      <header className="new-release-header">
        <div>
          <p className="new-release-kicker">МЕДИАТЕКА / РУЧНОЕ СОЗДАНИЕ</p>
          <h1 id="new-release-title">Создать локальный релиз</h1>
          <p>Необязательные данные; создание не запускает публикацию.</p>
        </div>
        <a className="nr-button" href="#/library">
          Вернуться в медиатеку
        </a>
      </header>
      {error && (
        <div className="nr-alert" role="alert">
          {error}
          {conflictReleaseID && (
            <>
              {" "}
              <a href={`#/library/releases/${conflictReleaseID}`}>
                Открыть существующий релиз
              </a>
            </>
          )}
        </div>
      )}
      {notice && (
        <div className="nr-note" role="status">
          {notice}
        </div>
      )}
      <section className="nr-panel">
        <h2>Релиз</h2>
        <div className="nr-grid">
          {(
            [
              ["title", "Название релиза"],
              ["date", "Дата релиза"],
              ["country", "Страна"],
              ["label", "Лейбл"],
              ["catalog", "Номер каталога"],
              ["type", "Тип / уточнение"],
            ] as const
          ).map(([field, label]) => (
            <label key={field}>
              {label}
              <input
                value={draft[field]}
                onChange={(e) =>
                  update((d) => ({ ...d, [field]: e.target.value }))
                }
              />
            </label>
          ))}
        </div>
      </section>
      <section className="nr-panel">
        <div className="nr-heading">
          <div>
            <h2>Артисты и credits</h2>
            <p>Артист выбирается явно; текст credit и роль независимы.</p>
          </div>
          <button type="button" className="nr-button" onClick={addCredit}>
            Добавить credit
          </button>
        </div>
        {draft.credits.length === 0 && (
          <p className="nr-empty">Credits не добавлены.</p>
        )}
        {draft.credits.map((c, i) => (
          <article className="nr-card" key={c.key}>
            <div className="nr-heading">
              <strong>Credit {i + 1}</strong>
              <button
                type="button"
                className="nr-link"
                disabled={i === 0}
                onClick={() => moveCredit(c.key, -1)}
              >
                ↑
              </button>
              <button
                type="button"
                className="nr-link"
                disabled={i === draft.credits.length - 1}
                onClick={() => moveCredit(c.key, 1)}
              >
                ↓
              </button>
              <button
                type="button"
                className="nr-link"
                onClick={() =>
                  update((d) => ({
                    ...d,
                    credits: d.credits.filter((x) => x.key !== c.key),
                  }))
                }
              >
                Удалить
              </button>
            </div>
            <div className="nr-grid">
              <label>
                Артист — существующий или новое имя
                <input
                  value={c.name}
                  onChange={(e) => changeCredit(c.key, "name", e.target.value)}
                />
              </label>
              <button
                type="button"
                className="nr-button"
                onClick={() => loadPicker("artist", c.key, c.name, 0)}
              >
                Найти существующего
              </button>
              <label>
                Отображаемый credit
                <input
                  value={c.credit}
                  onChange={(e) =>
                    changeCredit(c.key, "credit", e.target.value)
                  }
                />
              </label>
              <label>
                Роль
                <input
                  value={c.role}
                  onChange={(e) => changeCredit(c.key, "role", e.target.value)}
                />
              </label>
              <label>
                Join phrase
                <input
                  value={c.join}
                  onChange={(e) => changeCredit(c.key, "join", e.target.value)}
                />
              </label>
            </div>
            {c.artistId && (
              <small>Выбран существующий артист · {c.artistId}</small>
            )}
          </article>
        ))}
      </section>
      <section className="nr-panel">
        <div className="nr-heading">
          <div>
            <h2>Носители и позиции</h2>
            <p>Позиции и порядок можно менять независимо.</p>
          </div>
          <button type="button" className="nr-button" onClick={addMedium}>
            Добавить носитель
          </button>
        </div>
        {!draft.media.length && (
          <p className="nr-empty">Носители не добавлены.</p>
        )}
        {draft.media.map((m, mi) => (
          <article className="nr-card" key={m.key}>
            <div className="nr-heading">
              <h3>Носитель {mi + 1}</h3>
              <div>
                <button
                  type="button"
                  className="nr-link"
                  disabled={mi === 0}
                  onClick={() =>
                    update((d) => {
                      const a = [...d.media];
                      [a[mi - 1], a[mi]] = [a[mi], a[mi - 1]];
                      return { ...d, media: a };
                    })
                  }
                >
                  ↑
                </button>
                <button
                  type="button"
                  className="nr-link"
                  disabled={mi === draft.media.length - 1}
                  onClick={() =>
                    update((d) => {
                      const a = [...d.media];
                      [a[mi + 1], a[mi]] = [a[mi], a[mi + 1]];
                      return { ...d, media: a };
                    })
                  }
                >
                  ↓
                </button>
                <button
                  type="button"
                  className="nr-link"
                  onClick={() =>
                    update((d) => ({
                      ...d,
                      media: d.media.filter((x) => x.key !== m.key),
                    }))
                  }
                >
                  Удалить
                </button>
              </div>
            </div>
            <div className="nr-grid">
              <label>
                Название носителя
                <input
                  value={m.title}
                  onChange={(e) =>
                    update((d) => ({
                      ...d,
                      media: d.media.map((x) =>
                        x.key === m.key ? { ...x, title: e.target.value } : x,
                      ),
                    }))
                  }
                />
              </label>
              <label>
                Тип / формат
                <input
                  value={m.format}
                  onChange={(e) =>
                    update((d) => ({
                      ...d,
                      media: d.media.map((x) =>
                        x.key === m.key ? { ...x, format: e.target.value } : x,
                      ),
                    }))
                  }
                />
              </label>
            </div>
            <div className="nr-heading">
              <h4>Позиции</h4>
              <button
                type="button"
                className="nr-button"
                onClick={() =>
                  update((d) => ({
                    ...d,
                    media: d.media.map((x) =>
                      x.key === m.key
                        ? { ...x, positions: [...x.positions, blankPosition()] }
                        : x,
                    ),
                  }))
                }
              >
                Добавить позицию
              </button>
            </div>
            {!m.positions.length && (
              <p className="nr-empty">Позиции не добавлены.</p>
            )}
            {m.positions.map((p, pi) => (
              <article className="nr-position" key={p.key}>
                <div className="nr-heading">
                  <strong>Позиция {pi + 1}</strong>
                  <div>
                    <button
                      type="button"
                      className="nr-link"
                      disabled={pi === 0}
                      onClick={() =>
                        update((d) => ({
                          ...d,
                          media: d.media.map((x) => {
                            if (x.key !== m.key) return x;
                            const a = [...x.positions];
                            [a[pi - 1], a[pi]] = [a[pi], a[pi - 1]];
                            return { ...x, positions: a };
                          }),
                        }))
                      }
                    >
                      ↑
                    </button>
                    <button
                      type="button"
                      className="nr-link"
                      disabled={pi === m.positions.length - 1}
                      onClick={() =>
                        update((d) => ({
                          ...d,
                          media: d.media.map((x) => {
                            if (x.key !== m.key) return x;
                            const a = [...x.positions];
                            [a[pi + 1], a[pi]] = [a[pi], a[pi + 1]];
                            return { ...x, positions: a };
                          }),
                        }))
                      }
                    >
                      ↓
                    </button>
                    <button
                      type="button"
                      className="nr-link"
                      onClick={() =>
                        update((d) => ({
                          ...d,
                          media: d.media.map((x) =>
                            x.key === m.key
                              ? {
                                  ...x,
                                  positions: x.positions.filter(
                                    (y) => y.key !== p.key,
                                  ),
                                }
                              : x,
                          ),
                        }))
                      }
                    >
                      Удалить
                    </button>
                  </div>
                </div>
                <div className="nr-grid">
                  <label>
                    Отображаемый номер
                    <input
                      value={p.number}
                      onChange={(e) =>
                        modifyPosition(m.key, p.key, (v) => ({
                          ...v,
                          number: e.target.value,
                        }))
                      }
                    />
                  </label>
                  <label>
                    Название позиции
                    <input
                      value={p.title}
                      onChange={(e) =>
                        modifyPosition(m.key, p.key, (v) => ({
                          ...v,
                          title: e.target.value,
                        }))
                      }
                    />
                  </label>
                  <label>
                    Длительность (м:с)
                    <input
                      placeholder="3:42"
                      value={p.duration}
                      onChange={(e) =>
                        modifyPosition(m.key, p.key, (v) => ({
                          ...v,
                          duration: e.target.value,
                        }))
                      }
                    />
                  </label>
                  <label>
                    Recording
                    <select
                      value={p.recording}
                      onChange={(e) => {
                        providerGeneration.current += 1;
                        modifyPosition(m.key, p.key, (v) => ({
                          ...v,
                          recording: e.target.value as Position["recording"],
                          recordingId: "",
                          recordingLabel: "",
                          provider: undefined,
                        }));
                      }}
                    >
                      <option value="new">Новый recording</option>
                      <option value="local">Существующий в медиатеке</option>
                      <option value="provider">MusicBrainz</option>
                    </select>
                  </label>
                  {p.recording === "new" ? (
                    <label>
                      Название нового recording
                      <input
                        value={p.recordingTitle}
                        onChange={(e) =>
                          modifyPosition(m.key, p.key, (v) => ({
                            ...v,
                            recordingTitle: e.target.value,
                          }))
                        }
                      />
                    </label>
                  ) : p.recording === "local" ? (
                    <div>
                      <button
                        type="button"
                        className="nr-button"
                        onClick={() =>
                          loadPicker("recording", p.key, p.title, 0)
                        }
                      >
                        {p.recordingLabel
                          ? `Выбран recording: ${p.recordingLabel}`
                          : "Найти recording"}
                      </button>
                      {p.recordingId && (
                        <button
                          type="button"
                          className="nr-link"
                          onClick={() =>
                            modifyPosition(m.key, p.key, (v) => ({
                              ...v,
                              recordingId: "",
                              recordingLabel: "",
                            }))
                          }
                        >
                          Снять выбор
                        </button>
                      )}
                    </div>
                  ) : (
                    <div className="nr-provider">
                      <label>
                        Поиск MusicBrainz
                        <input
                          aria-label={`Поиск MusicBrainz позиции ${pi + 1}`}
                          value={providerQueries[p.key] ?? ""}
                          onChange={(e) =>
                            setProviderQueries((queries) => ({
                              ...queries,
                              [p.key]: e.target.value,
                            }))
                          }
                        />
                      </label>
                      <button
                        type="button"
                        className="nr-button"
                        onClick={() =>
                          searchProvider(p, providerQueries[p.key] ?? "")
                        }
                      >
                        Искать
                      </button>
                      {p.recordingLabel && (
                        <p>
                          Выбран MusicBrainz recording: {p.recordingLabel}{" "}
                          <button
                            type="button"
                            className="nr-link"
                            onClick={() =>
                              modifyPosition(m.key, p.key, (v) => ({
                                ...v,
                                provider: undefined,
                                recordingLabel: "",
                              }))
                            }
                          >
                            Снять выбор
                          </button>
                        </p>
                      )}
                      {(providerPositionKey === p.key
                        ? providerResult?.items
                        : []
                      )?.map((item) => (
                        <button
                          type="button"
                          key={item.provider_recording_id}
                          className="nr-result"
                          onClick={() =>
                            modifyPosition(m.key, p.key, (v) => ({
                              ...v,
                              recordingLabel:
                                item.title?.trim() || item.provider_key,
                              provider: {
                                kind: "provider",
                                provider_id: item.provider_id,
                                provider_source_id: item.provider_source_id,
                                provider_recording_id:
                                  item.provider_recording_id,
                                configuration_identity:
                                  providerResult?.configuration_identity,
                              },
                            }))
                          }
                        >
                          {item.title} ·{" "}
                          {item.artists.map((a) => a.name).join(", ")}
                        </button>
                      ))}
                      {providerPositionKey === p.key &&
                        providerResult &&
                        providerResult.total > pageSize && (
                          <div className="nr-heading">
                            <button
                              type="button"
                              className="nr-button"
                              disabled={providerResult.offset === 0}
                              onClick={() =>
                                void searchProvider(
                                  p,
                                  providerQueries[p.key] ?? "",
                                  Math.max(0, providerResult.offset - pageSize),
                                )
                              }
                            >
                              Назад
                            </button>
                            <span>
                              {providerResult.offset + 1}–
                              {Math.min(
                                providerResult.offset + pageSize,
                                providerResult.total,
                              )}{" "}
                              из {providerResult.total}
                            </span>
                            <button
                              type="button"
                              className="nr-button"
                              disabled={
                                providerResult.offset + pageSize >=
                                providerResult.total
                              }
                              onClick={() =>
                                void searchProvider(
                                  p,
                                  providerQueries[p.key] ?? "",
                                  providerResult.offset + pageSize,
                                )
                              }
                            >
                              Далее
                            </button>
                          </div>
                        )}
                    </div>
                  )}
                </div>
                <h4>Связанные исходники</h4>
                <div className="nr-source-list">
                  {p.sources.map((s) => (
                    <div className="nr-source" key={s.media_variant_id}>
                      <label>
                        <input
                          type="radio"
                          name={`source-${p.key}`}
                          checked={p.selectedSource === s.media_variant_id}
                          onChange={() =>
                            modifyPosition(m.key, p.key, (v) => ({
                              ...v,
                              selectedSource: s.media_variant_id,
                            }))
                          }
                        />
                        {s.title || s.filename} ·{" "}
                        {s.locations
                          .map(
                            (l) => `${l.root_display_name}/${l.relative_path}`,
                          )
                          .join(", ")}
                      </label>
                      <button
                        type="button"
                        className="nr-link"
                        onClick={() =>
                          modifyPosition(m.key, p.key, (v) => ({
                            ...v,
                            sources: v.sources.filter(
                              (x) => x.media_variant_id !== s.media_variant_id,
                            ),
                            selectedSource:
                              v.selectedSource === s.media_variant_id
                                ? ""
                                : v.selectedSource,
                          }))
                        }
                      >
                        Снять
                      </button>
                    </div>
                  ))}
                  {!p.sources.length && (
                    <p className="nr-empty">Исходники не выбраны.</p>
                  )}
                </div>
                <button
                  type="button"
                  className="nr-button"
                  onClick={() => loadPicker("source", p.key, "", 0)}
                >
                  Добавить исходник
                </button>{" "}
                <button
                  type="button"
                  className="nr-button"
                  onClick={() =>
                    setReplaceTarget({ medium: m.key, position: p.key })
                  }
                >
                  Заменить набор…
                </button>
              </article>
            ))}
          </article>
        ))}
      </section>
      <section className="nr-panel">
        <div className="nr-heading">
          <div>
            <h2>Обложка</h2>
            <p>Выбор только из сохранённых кандидатов.</p>
          </div>
          <button
            type="button"
            className="nr-button"
            onClick={() =>
              draft.cover
                ? update((d) => ({ ...d, cover: null }))
                : void loadCovers()
            }
          >
            {draft.cover ? "Снять выбор" : "Показать обложки"}
          </button>
        </div>
        {coverState.error && (
          <p role="alert">
            {coverState.error}{" "}
            <button type="button" onClick={() => void loadCovers()}>
              Повторить
            </button>
          </p>
        )}
        {coverState.loading && <p>Загрузка…</p>}
        {coverItems.map((c) => (
          <label className="nr-cover" key={c.metadata_result_id}>
            <input
              type="radio"
              name="cover"
              checked={draft.cover?.metadata_result_id === c.metadata_result_id}
              onChange={() =>
                update((d) => ({
                  ...d,
                  cover: {
                    metadata_result_id: c.metadata_result_id,
                    expected_sha256: c.sha256,
                  },
                }))
              }
            />
            <img
              src={getGetLibraryCoverCandidateImageUrl(c.metadata_result_id, {
                expected_sha256: c.sha256,
              })}
              alt=""
            />
            <span>
              {c.label} · {c.width ?? "—"} × {c.height ?? "—"}
            </span>
          </label>
        ))}
        {coverTotal > pageSize && (
          <div className="nr-heading">
            <button
              type="button"
              className="nr-button"
              disabled={coverState.loading || coverPage === 0}
              onClick={() => void loadCovers(Math.max(0, coverPage - pageSize))}
            >
              Назад
            </button>
            <span>
              {coverPage + 1}–{Math.min(coverPage + pageSize, coverTotal)} из{" "}
              {coverTotal}
            </span>
            <button
              type="button"
              className="nr-button"
              disabled={
                coverState.loading || coverPage + pageSize >= coverTotal
              }
              onClick={() => void loadCovers(coverPage + pageSize)}
            >
              Далее
            </button>
          </div>
        )}
      </section>
      <footer className="nr-footer">
        <button
          type="button"
          className="nr-button nr-primary"
          disabled={busy}
          onClick={() => void submit()}
        >
          {busy ? "Создание…" : "Создать локальный релиз"}
        </button>
      </footer>
      {picker && (
        <div className="nr-modal-backdrop" role="presentation">
          <section
            className="nr-modal"
            role="dialog"
            aria-modal="true"
            aria-labelledby="picker-title"
          >
            <div className="nr-heading">
              <h2 id="picker-title">
                {picker.kind === "artist"
                  ? "Выбрать артиста"
                  : picker.kind === "recording"
                    ? "Выбрать recording"
                    : "Выбрать исходник"}
              </h2>
              <button
                type="button"
                className="nr-link"
                onClick={() => {
                  pickerGeneration.current += 1;
                  setPicker(null);
                  setReplaceSelection(false);
                }}
              >
                Закрыть
              </button>
            </div>
            <form
              onSubmit={(e) => {
                e.preventDefault();
                void loadPicker(
                  picker.kind,
                  picker.key,
                  (e.currentTarget.elements.namedItem("q") as HTMLInputElement)
                    .value,
                  0,
                );
              }}
            >
              <label>
                Поиск
                <input name="q" defaultValue={picker.query} />
              </label>
              <button type="submit" className="nr-button">
                Искать
              </button>
            </form>
            {picker.loading && <p>Загрузка…</p>}
            {picker.error && (
              <p role="alert">
                {picker.error}{" "}
                <button
                  type="button"
                  onClick={() =>
                    void loadPicker(
                      picker.kind,
                      picker.key,
                      picker.query,
                      picker.page,
                    )
                  }
                >
                  Повторить
                </button>
              </p>
            )}
            <div className="nr-picker-results">
              {picker.items.map((item) => (
                <button
                  type="button"
                  key={item.id}
                  className="nr-result"
                  onClick={() => {
                    if (item.kind === "artist")
                      selectArtist(picker.key, item.artist);
                    else if (item.kind === "recording") {
                      const position = draft.media
                        .flatMap((m) => m.positions.map((p) => ({ m, p })))
                        .find(({ p }) => p.key === picker.key);
                      if (position)
                        modifyPosition(position.m.key, position.p.key, (p) => ({
                          ...p,
                          recordingId: item.id,
                          recordingLabel: item.title,
                        }));
                      pickerGeneration.current += 1;
                      setPicker(null);
                    } else if (selectedPosition) {
                      const exists = selectedPosition.p.sources.some(
                        (s) => s.media_variant_id === item.id,
                      );
                      if (replaceSelection)
                        modifyPosition(
                          selectedPosition.m.key,
                          selectedPosition.p.key,
                          (p) => ({
                            ...p,
                            sources: [item.source],
                            selectedSource: "",
                          }),
                        );
                      else if (!exists)
                        modifyPosition(
                          selectedPosition.m.key,
                          selectedPosition.p.key,
                          (p) => ({
                            ...p,
                            sources: [...p.sources, item.source],
                          }),
                        );
                      setReplaceSelection(false);
                      pickerGeneration.current += 1;
                      setPicker(null);
                    }
                  }}
                >
                  {item.kind === "artist" ? item.name : item.title}{" "}
                  {item.kind === "source" &&
                    item.locations[0] &&
                    `· ${item.locations[0].root_display_name}/${item.locations[0].relative_path}`}
                </button>
              ))}
            </div>
            <div className="nr-heading">
              <button
                type="button"
                className="nr-button"
                disabled={picker.page === 0 || picker.loading}
                onClick={() =>
                  void loadPicker(
                    picker.kind,
                    picker.key,
                    picker.query,
                    Math.max(0, picker.page - pageSize),
                  )
                }
              >
                Назад
              </button>
              <span>
                {picker.total
                  ? `${picker.page + 1}–${Math.min(picker.page + pageSize, picker.total)} из ${picker.total}`
                  : "Нет результатов"}
              </span>
              <button
                type="button"
                className="nr-button"
                disabled={
                  picker.loading || picker.page + pageSize >= picker.total
                }
                onClick={() =>
                  void loadPicker(
                    picker.kind,
                    picker.key,
                    picker.query,
                    picker.page + pageSize,
                  )
                }
              >
                Далее
              </button>
            </div>
          </section>
        </div>
      )}
      {replaceTarget && (
        <div className="nr-modal-backdrop">
          <section className="nr-modal" role="alertdialog" aria-modal="true">
            <h2>Заменить связанные исходники?</h2>
            <p>Текущий набор ссылок будет заменён. Публикация не изменяется.</p>
            <button
              type="button"
              className="nr-button"
              onClick={() => setReplaceTarget(null)}
            >
              Отмена
            </button>
            <button
              type="button"
              className="nr-button nr-primary"
              onClick={() => {
                const p = draft.media
                  .find((m) => m.key === replaceTarget.medium)
                  ?.positions.find((x) => x.key === replaceTarget.position);
                setReplaceTarget(null);
                if (p) {
                  setReplaceSelection(true);
                  void loadPicker("source", p.key, "", 0);
                }
              }}
            >
              Выбрать набор
            </button>
          </section>
        </div>
      )}
    </main>
  );
}
