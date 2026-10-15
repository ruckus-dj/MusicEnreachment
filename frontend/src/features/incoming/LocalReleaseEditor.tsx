import { useEffect, useRef, useState } from "react";
import {
  confirmIncomingGroupLocalMatching,
  listLocalReleases,
  previewIncomingGroupLocalMatching,
} from "../../api/generated/client";
import type {
  IncomingFileResponse,
  IncomingGroupResponse,
  MatchingLocalConfirmBody,
  MatchingLocalPreview,
  MatchingLocalPreviewBody,
  MatchingLocalRelease,
  MatchingLocalTrack,
} from "../../api/generated/client.schemas";
import { AppButton } from "../../components/AppButton";

type DraftTrack = {
  id: string;
  medium: string;
  position: string;
  displayedNumber: string;
  title: string;
  artists: DraftArtist[];
  fileMemberIds: string[];
  localTrackID?: string;
  sourceMediaIDs: string[];
};

type DraftArtist = { id: string; name: string };

type LocalDraft = {
  releaseTitle: string;
  releaseArtists: DraftArtist[];
  tracks: DraftTrack[];
};

type PreviewState = {
  scope: string;
  body: MatchingLocalPreviewBody;
  response: MatchingLocalPreview;
};

type PendingConfirmation = {
  scope: string;
  body: MatchingLocalConfirmBody;
};

type GroupState = {
  draft: LocalDraft;
  preview?: PreviewState;
  pending?: PendingConfirmation;
  receipt?: MatchingLocalPreview;
  busy?: "preview" | "confirm";
  busyRequest?: number;
  error?: string;
  notice?: string;
  stale?: boolean;
};

type SourceSelection = {
  sourceID: string;
  album?: string;
  title?: string;
};

type LocalReleaseCatalogState = {
  releases?: MatchingLocalRelease[];
  busy?: boolean;
  error?: string;
  request?: number;
};

export interface LocalReleaseEditorProps {
  group: IncomingGroupResponse;
  files: IncomingFileResponse[];
  authorityRevision: number | undefined;
  onConfirmed?: (receipt: MatchingLocalPreview) => void | Promise<void>;
}

const emptyDraft = (): LocalDraft => ({
  releaseTitle: "",
  releaseArtists: [],
  tracks: [],
});

const emptyGroupState = (): GroupState => ({ draft: emptyDraft() });

function message(reason: unknown, fallback: string) {
  return reason instanceof Error && reason.message ? reason.message : fallback;
}

function responseMessage(
  response: { data: { detail?: string }; status: number },
  fallback: string,
) {
  return response.data.detail || `${fallback} (HTTP ${response.status}).`;
}

function tagValues(file: IncomingFileResponse | undefined, name: string) {
  if (!file) return [];
  const key = Object.keys(file.tags).find(
    (tag) => tag.toUpperCase().replace(/[ _-]/g, "") === name,
  );
  return key ? (file.tags[key] ?? []) : [];
}

function fileLabel(file: IncomingFileResponse) {
  return file.locations?.[0]?.relative_path || file.variant_id;
}

function freshRequestID() {
  return crypto.randomUUID();
}

function localID() {
  return crypto.randomUUID();
}

function toPayload(draft: LocalDraft): MatchingLocalTrack[] {
  return draft.tracks
    .filter((track) => track.fileMemberIds.length > 0)
    .map((track) => ({
      ...(track.localTrackID ? { local_track_id: track.localTrackID } : {}),
      medium: Number(track.medium),
      position: Number(track.position),
      displayed_number: track.displayedNumber,
      title: track.title,
      artists: track.artists.map(({ name }) => ({ name })),
      file_member_ids: track.fileMemberIds,
    }));
}

export function LocalReleaseEditor({
  group,
  files,
  authorityRevision,
  onConfirmed,
}: LocalReleaseEditorProps) {
  const [states, setStates] = useState<Record<string, GroupState>>({});
  const [activeTargets, setActiveTargets] = useState<Record<string, string>>(
    {},
  );
  const [selectedCatalogIDs, setSelectedCatalogIDs] = useState<
    Record<string, string>
  >({});
  const [catalogs, setCatalogs] = useState<
    Record<string, LocalReleaseCatalogState>
  >({});
  const [sourceSelections, setSourceSelections] = useState<
    Record<string, SourceSelection>
  >({});
  const sequence = useRef(0);
  const catalogRequest = useRef(0);
  const currentScope = `${group.id}:${group.revision}:${authorityRevision === undefined ? "unknown" : authorityRevision}`;
  const currentScopeRef = useRef(currentScope);
  currentScopeRef.current = currentScope;
  const activeTarget = activeTargets[group.id] ?? "";
  const stateKey = `${group.id}:${activeTarget || "new"}`;
  const groupScopes = useRef<Record<string, string>>({});
  const scopedFiles = files.filter((file) =>
    group.members?.includes(file.variant_id),
  );
  const state = states[stateKey] ?? emptyGroupState();
  const draft = state.draft;
  const sourceSelection = sourceSelections[group.id] ?? { sourceID: "" };
  const {
    sourceID,
    album: selectedAlbum,
    title: selectedTitle,
  } = sourceSelection;
  const selectedSource = scopedFiles.find(
    (file) => file.variant_id === sourceID,
  );

  function updateSourceSelection(
    update: (current: SourceSelection) => SourceSelection,
  ) {
    setSourceSelections((current) => ({
      ...current,
      [group.id]: update(current[group.id] ?? { sourceID: "" }),
    }));
  }

  useEffect(() => {
    const previous = groupScopes.current[group.id];
    groupScopes.current[group.id] = currentScope;
    if (previous === undefined || previous === currentScope) return;
    sequence.current += 1;
    setStates((current) => {
      const existing = current[stateKey] ?? emptyGroupState();
      const confirmationOutstanding =
        existing.busy === "confirm" || existing.pending !== undefined;
      return {
        ...current,
        [stateKey]: {
          draft: existing.draft,
          receipt: existing.receipt,
          ...(confirmationOutstanding
            ? {
                pending: existing.pending,
                busy: existing.busy,
                busyRequest: existing.busyRequest,
                error: existing.error,
              }
            : {}),
          notice: existing.receipt
            ? "Локальный релиз подтверждён; данные группы обновлены."
            : "Снимок группы или полномочий изменился. Черновик сохранён; создайте новый предпросмотр.",
        },
      };
    });
  }, [currentScope, stateKey, group.id]);

  function update(update: (current: GroupState) => GroupState) {
    updateState(stateKey, update);
  }

  function updateState(
    key: string,
    update: (current: GroupState) => GroupState,
  ) {
    setStates((current) => {
      const existing = current[key] ?? emptyGroupState();
      return { ...current, [key]: update(existing) };
    });
  }

  function editDraft(next: LocalDraft) {
    sequence.current += 1;
    update((_current) => ({
      draft: next,
      notice: undefined,
      error: undefined,
      stale: false,
    }));
  }

  function setDraftField<K extends keyof LocalDraft>(
    key: K,
    value: LocalDraft[K],
  ) {
    editDraft({ ...draft, [key]: value });
  }

  function addArtist(
    artists: DraftArtist[],
    set: (next: DraftArtist[]) => void,
  ) {
    set([...artists, { id: localID(), name: "" }]);
  }

  function tagChoice(
    file: IncomingFileResponse | undefined,
    name: string,
    value: string,
  ) {
    if (!file) return;
    updateSourceSelection((current) => {
      const selection =
        current.sourceID === file.variant_id
          ? current
          : { sourceID: file.variant_id };
      if (name === "ALBUM") return { ...selection, album: value };
      if (name === "TITLE") return { ...selection, title: value };
      return selection;
    });
  }

  function copyReleaseTitle() {
    if (!selectedSource || selectedAlbum === undefined) {
      update((current) => ({
        ...current,
        notice: "Выберите значение ALBUM перед копированием.",
      }));
      return;
    }
    setDraftField("releaseTitle", selectedAlbum);
  }

  function copyReleaseArtists() {
    if (!selectedSource) return;
    const values = tagValues(selectedSource, "ALBUMARTIST");
    const artists = values.length
      ? values
      : tagValues(selectedSource, "ARTIST");
    if (!artists.length) {
      update((current) => ({
        ...current,
        notice: "В выбранном файле нет ALBUMARTIST или ARTIST.",
      }));
      return;
    }
    setDraftField(
      "releaseArtists",
      artists.map((name) => ({ id: localID(), name })),
    );
  }

  function addTrack() {
    editDraft({
      ...draft,
      tracks: [
        ...draft.tracks,
        {
          id: localID(),
          medium: "",
          position: "",
          displayedNumber: "",
          title: "",
          artists: [],
          fileMemberIds: [],
          sourceMediaIDs: [],
        },
      ],
    });
  }

  function editTrack(
    index: number,
    updateTrack: (track: DraftTrack) => DraftTrack,
  ) {
    const tracks = [...draft.tracks];
    tracks[index] = updateTrack(tracks[index]);
    editDraft({ ...draft, tracks });
  }

  function copyTrackSource(index: number) {
    if (!selectedSource) return;
    const titles = tagValues(selectedSource, "TITLE");
    const numbers = tagValues(selectedSource, "TRACKNUMBER");
    const discs = tagValues(selectedSource, "DISCNUMBER");
    const title =
      selectedTitle === undefined
        ? titles.length === 1
          ? titles[0]
          : undefined
        : selectedTitle;
    if (title === undefined) {
      update((current) => ({
        ...current,
        notice: titles.length
          ? "У источника несколько значений TITLE: выберите или введите название вручную."
          : "В источнике нет TITLE; введите название вручную.",
      }));
      return;
    }
    const validNumber = numbers.length === 1 && /^[1-9]\d*$/.test(numbers[0]);
    const validDisc = discs.length === 1 && /^[1-9]\d*$/.test(discs[0]);
    editTrack(index, (current) => ({
      ...current,
      title,
      ...(validNumber
        ? {
            ...(!current.localTrackID ? { position: numbers[0] } : {}),
            displayedNumber: numbers[0],
          }
        : {}),
      ...(validDisc && !current.localTrackID ? { medium: discs[0] } : {}),
    }));
    if (!validNumber || !validDisc) {
      update((current) => ({
        ...current,
        notice: `Название скопировано${selectedTitle !== undefined ? " из выбранного значения" : ""}. Номер дорожки и/или носителя не однозначен — укажите их вручную.`,
      }));
    }
  }

  function makePreviewBody(): MatchingLocalPreviewBody | undefined {
    if (authorityRevision === undefined) {
      update((current) => ({
        ...current,
        error:
          "Полномочия ещё не загружены. Обновите экран и повторите предпросмотр.",
      }));
      return undefined;
    }
    if (
      !draft.tracks.some((track) => track.fileMemberIds.length > 0) ||
      draft.tracks
        .filter((track) => track.fileMemberIds.length > 0)
        .some(
          (track) =>
            !/^[1-9]\d*$/.test(track.medium) ||
            !/^[1-9]\d*$/.test(track.position),
        )
    ) {
      update((current) => ({
        ...current,
        error:
          "Для каждой назначенной дорожки укажите носитель, позицию и хотя бы один файл. Текстовые теги могут быть пустыми.",
      }));
      return undefined;
    }
    return {
      expected_group_revision: group.revision,
      expected_authority_revision: authorityRevision,
      ...(activeTarget ? { local_release_id: activeTarget } : {}),
      release_title: draft.releaseTitle,
      release_artists: draft.releaseArtists.map(({ name }) => ({ name })),
      tracks: toPayload(draft),
    };
  }

  async function loadLocalReleases() {
    const request = ++catalogRequest.current;
    const requestedScope = currentScope;
    setCatalogs((current) => ({
      ...current,
      [group.id]: {
        ...current[group.id],
        busy: true,
        error: undefined,
        request,
      },
    }));
    try {
      const response = await listLocalReleases({ cache: "no-store" });
      if (
        request !== catalogRequest.current ||
        currentScopeRef.current !== requestedScope
      )
        return;
      if (response.status !== 200) {
        setCatalogs((current) => ({
          ...current,
          [group.id]: {
            ...current[group.id],
            busy: false,
            error: responseMessage(
              response,
              "Не удалось загрузить локальные релизы",
            ),
          },
        }));
        return;
      }
      setCatalogs((current) => ({
        ...current,
        [group.id]: {
          ...current[group.id],
          releases: response.data.releases ?? [],
        },
      }));
    } catch (reason) {
      if (
        request === catalogRequest.current &&
        currentScopeRef.current === requestedScope
      ) {
        setCatalogs((current) => ({
          ...current,
          [group.id]: {
            ...current[group.id],
            busy: false,
            error: message(reason, "Не удалось загрузить локальные релизы."),
          },
        }));
      }
    } finally {
      setCatalogs((current) => {
        const catalog = current[group.id];
        if (catalog?.request !== request) return current;
        return {
          ...current,
          [group.id]: { ...catalog, busy: false, request: undefined },
        };
      });
    }
  }

  function useSelectedLocalRelease() {
    const selected = catalogs[group.id]?.releases?.find(
      (release) => release.id === selectedCatalogIDs[group.id],
    );
    if (!selected) return;
    const targetKey = `${group.id}:${selected.id}`;
    setStates((current) => {
      if (current[targetKey]) return current;
      const tracks = (selected.tracks ?? []).map((track) => ({
        id: localID(),
        medium: String(track.medium),
        position: String(track.position),
        displayedNumber: track.displayed_number,
        title: track.title,
        artists: (track.artists ?? []).map(({ name }) => ({
          id: localID(),
          name,
        })),
        fileMemberIds: [],
        localTrackID: track.local_track_id,
        sourceMediaIDs: track.source_media_ids ?? [],
      }));
      return {
        ...current,
        [targetKey]: {
          draft: {
            releaseTitle: selected.title,
            releaseArtists: (selected.artists ?? []).map(({ name }) => ({
              id: localID(),
              name,
            })),
            tracks,
          },
        },
      };
    });
    setActiveTargets((current) => ({ ...current, [group.id]: selected.id }));
  }

  function useNewLocalRelease() {
    setActiveTargets((current) => ({ ...current, [group.id]: "" }));
  }

  async function preview() {
    const body = makePreviewBody();
    if (!body) return;
    const request = ++sequence.current;
    const originKey = stateKey;
    const originScope = currentScope;
    update((current) => ({
      ...current,
      busy: "preview",
      busyRequest: request,
      error: undefined,
      notice: undefined,
      stale: false,
    }));
    try {
      const response = await previewIncomingGroupLocalMatching(group.id, body);
      if (request !== sequence.current) return;
      if (response.status !== 200) {
        updateState(originKey, (current) => ({
          ...current,
          busy: undefined,
          preview: undefined,
          pending: response.status === 409 ? undefined : current.pending,
          stale: response.status === 409,
          error:
            response.status === 409
              ? "Снимок устарел. Черновик сохранён; обновите данные группы и создайте новый предпросмотр."
              : responseMessage(response, "Предпросмотр не выполнен"),
        }));
        return;
      }
      updateState(originKey, (current) => ({
        ...current,
        busy: undefined,
        preview: { scope: originScope, body, response: response.data },
        pending: undefined,
        receipt: undefined,
      }));
    } catch (reason) {
      if (request === sequence.current)
        updateState(originKey, (current) => ({
          ...current,
          busy: undefined,
          error: message(reason, "Предпросмотр не выполнен."),
        }));
    } finally {
      updateState(originKey, (current) =>
        current.busyRequest === request
          ? { ...current, busy: undefined, busyRequest: undefined }
          : current,
      );
    }
  }

  async function confirm() {
    const pending =
      state.pending ??
      (state.preview && state.preview.scope === currentScope
        ? {
            scope: currentScope,
            body: {
              ...state.preview.body,
              preview_digest: state.preview.response.preview_digest,
              request_id: freshRequestID(),
            } as MatchingLocalConfirmBody,
          }
        : undefined);
    if (!pending || state.busy) return;
    if (!state.pending) update((current) => ({ ...current, pending }));
    const request = ++sequence.current;
    const originKey = stateKey;
    update((current) => ({
      ...current,
      busy: "confirm",
      busyRequest: request,
      error: undefined,
      notice: undefined,
    }));
    try {
      const response = await confirmIncomingGroupLocalMatching(
        group.id,
        pending.body,
      );
      if (response.status !== 200) {
        updateState(originKey, (current) => ({
          ...current,
          busy: undefined,
          stale: response.status === 409,
          preview: response.status === 409 ? undefined : current.preview,
          pending: response.status === 409 ? undefined : current.pending,
          error:
            response.status === 409
              ? "Снимок устарел. Черновик сохранён; получите новый предпросмотр перед подтверждением."
              : responseMessage(response, "Подтверждение не выполнено"),
        }));
        return;
      }
      updateState(originKey, (current) => ({
        ...current,
        busy: undefined,
        receipt: response.data,
        pending: undefined,
        preview: undefined,
        notice: "Локальный релиз подтверждён.",
      }));
      try {
        await onConfirmed?.(response.data);
      } catch (reason) {
        updateState(originKey, (current) => ({
          ...current,
          notice: `Локальный релиз подтверждён, но обновить экран не удалось: ${message(reason, "ошибка обновления")}`,
        }));
      }
    } catch (reason) {
      updateState(originKey, (current) => ({
        ...current,
        busy: undefined,
        error: `${message(reason, "Подтверждение не выполнено.")} Повторите запрос, чтобы безопасно проверить тот же результат.`,
      }));
    } finally {
      updateState(originKey, (current) =>
        current.busyRequest === request
          ? { ...current, busy: undefined, busyRequest: undefined }
          : current,
      );
    }
  }

  const previewCurrent =
    state.preview?.scope === currentScope ? state.preview : undefined;
  const confirmationPending = Object.entries(states).some(
    ([key, value]) =>
      key.startsWith(`${group.id}:`) && value.busy === "confirm",
  );
  const canPreview =
    authorityRevision !== undefined &&
    state.busy === undefined &&
    !confirmationPending;

  return (
    <section
      aria-labelledby={`local-release-${group.id}`}
      className="space-y-4 rounded border border-stone-300 p-4"
    >
      <h2 id={`local-release-${group.id}`} className="text-lg font-semibold">
        {activeTarget ? "Изменить локальный релиз" : "Создать локальный релиз"}
      </h2>
      <p>
        {activeTarget
          ? "Будет изменён только локальный релиз."
          : "Будет создан только локальный релиз."}{" "}
        Подключения к музыкальным провайдерам не используются.
      </p>

      <fieldset
        disabled={confirmationPending}
        className="space-y-3 rounded border border-stone-300 p-3"
      >
        <legend className="font-medium">Целевой локальный релиз</legend>
        <div className="flex flex-wrap gap-2">
          <AppButton
            type="button"
            isDisabled={catalogs[group.id]?.busy}
            onPress={loadLocalReleases}
          >
            {catalogs[group.id]?.busy
              ? "Загрузка локальных релизов…"
              : "Загрузить локальные релизы"}
          </AppButton>
          <AppButton type="button" onPress={useNewLocalRelease}>
            Новый локальный релиз
          </AppButton>
        </div>
        {catalogs[group.id]?.error && (
          <p role="alert">{catalogs[group.id].error}</p>
        )}
        {catalogs[group.id]?.releases && (
          <>
            <label className="block">
              Локальный релиз
              <select
                className="block w-full rounded border p-2"
                value={selectedCatalogIDs[group.id] ?? ""}
                onChange={(event) =>
                  setSelectedCatalogIDs((current) => ({
                    ...current,
                    [group.id]: event.target.value,
                  }))
                }
              >
                <option value="">Выберите локальный релиз</option>
                {catalogs[group.id].releases?.map((release) => (
                  <option key={release.id} value={release.id}>
                    {release.title}
                  </option>
                ))}
              </select>
            </label>
            <AppButton
              type="button"
              isDisabled={!selectedCatalogIDs[group.id]}
              onPress={useSelectedLocalRelease}
            >
              Использовать выбранный релиз
            </AppButton>
          </>
        )}
        <p>
          {activeTarget
            ? `Изменения будут применены к существующему локальному релизу ${activeTarget}.`
            : "Будет создан новый локальный релиз; выбор из каталога не применяется автоматически."}
        </p>
      </fieldset>

      <fieldset disabled={confirmationPending} className="space-y-3">
        <legend className="font-medium">Данные релиза</legend>
        <label className="block">
          Название релиза
          <input
            className="block w-full rounded border p-2"
            value={draft.releaseTitle}
            onChange={(event) =>
              setDraftField("releaseTitle", event.target.value)
            }
          />
        </label>
        <div className="space-y-2">
          <span>Исполнители релиза (в порядке отображения)</span>
          {draft.releaseArtists.map((artist, index) => (
            <div className="flex gap-2" key={artist.id}>
              <label
                className="sr-only"
                htmlFor={`release-artist-${group.id}-${index}`}
              >
                Исполнитель релиза {index + 1}
              </label>
              <input
                id={`release-artist-${group.id}-${index}`}
                className="min-w-0 flex-1 rounded border p-2"
                value={artist.name}
                onChange={(event) =>
                  setDraftField(
                    "releaseArtists",
                    draft.releaseArtists.map((value, item) =>
                      item === index
                        ? { ...value, name: event.target.value }
                        : value,
                    ),
                  )
                }
              />
              <AppButton
                type="button"
                onPress={() =>
                  setDraftField(
                    "releaseArtists",
                    draft.releaseArtists.filter((_, item) => item !== index),
                  )
                }
              >
                Удалить
              </AppButton>
            </div>
          ))}
          <AppButton
            type="button"
            onPress={() =>
              addArtist(draft.releaseArtists, (artists) =>
                setDraftField("releaseArtists", artists),
              )
            }
          >
            Добавить исполнителя
          </AppButton>
        </div>
      </fieldset>

      <fieldset disabled={confirmationPending} className="space-y-3">
        <legend className="font-medium">
          Явное копирование метаданных из исходного файла
        </legend>
        <label className="block">
          Исходный файл
          <select
            className="block w-full rounded border p-2"
            value={sourceID}
            onChange={(event) => {
              updateSourceSelection(() => ({ sourceID: event.target.value }));
            }}
          >
            <option value="">Не выбран</option>
            {scopedFiles.map((file) => (
              <option key={file.variant_id} value={file.variant_id}>
                {fileLabel(file)}
              </option>
            ))}
          </select>
        </label>
        <div className="flex flex-wrap items-end gap-2">
          <label>
            Значение ALBUM
            <select
              className="block max-w-full rounded border p-2"
              value={
                selectedAlbum === undefined
                  ? ""
                  : String(
                      tagValues(selectedSource, "ALBUM").indexOf(
                        selectedAlbum,
                      ) + 1,
                    )
              }
              onChange={(event) => {
                const values = [...new Set(tagValues(selectedSource, "ALBUM"))];
                const index = Number(event.target.value) - 1;
                tagChoice(selectedSource, "ALBUM", values[index]);
              }}
            >
              <option value="">Выберите значение</option>
              {[...new Set(tagValues(selectedSource, "ALBUM"))].map(
                (value, index) => (
                  <option key={value} value={String(index + 1)}>
                    {value}
                  </option>
                ),
              )}
            </select>
          </label>
          <AppButton type="button" onPress={copyReleaseTitle}>
            Скопировать в название релиза
          </AppButton>
          <AppButton
            type="button"
            onPress={copyReleaseArtists}
            isDisabled={!selectedSource}
          >
            Скопировать ALBUMARTIST / ARTIST
          </AppButton>
        </div>
        <label>
          Значение TITLE для явного копирования в дорожку
          <select
            className="block max-w-full rounded border p-2"
            value={
              selectedTitle === undefined
                ? ""
                : String(
                    [...new Set(tagValues(selectedSource, "TITLE"))].indexOf(
                      selectedTitle,
                    ) + 1,
                  )
            }
            onChange={(event) => {
              const values = [...new Set(tagValues(selectedSource, "TITLE"))];
              tagChoice(
                selectedSource,
                "TITLE",
                values[Number(event.target.value) - 1],
              );
            }}
          >
            <option value="">
              Не выбрано (копирование разрешено только при одном значении)
            </option>
            {[...new Set(tagValues(selectedSource, "TITLE"))].map(
              (value, index) => (
                <option key={value} value={String(index + 1)}>
                  {value}
                </option>
              ),
            )}
          </select>
        </label>
        <p>
          Список исполнителей копируется целиком и по порядку; разделители не
          разбираются.
        </p>
      </fieldset>

      <div className="space-y-3">
        <h3 className="font-medium">Дорожки и членство файлов</h3>
        {draft.tracks.map((track, index) => (
          <fieldset
            disabled={confirmationPending}
            className="grid gap-3 rounded border border-stone-300 p-3 sm:grid-cols-2"
            key={track.id}
          >
            <legend>Дорожка {index + 1}</legend>
            <label>
              Носитель (с 1)
              <input
                inputMode="numeric"
                className="block w-full rounded border p-2"
                value={track.medium}
                readOnly={Boolean(track.localTrackID)}
                onChange={(event) =>
                  editTrack(index, (current) => ({
                    ...current,
                    medium: event.target.value,
                  }))
                }
              />
            </label>
            <label>
              Позиция (с 1)
              <input
                inputMode="numeric"
                className="block w-full rounded border p-2"
                value={track.position}
                readOnly={Boolean(track.localTrackID)}
                onChange={(event) =>
                  editTrack(index, (current) => ({
                    ...current,
                    position: event.target.value,
                  }))
                }
              />
            </label>
            <label>
              Отображаемый номер
              <input
                className="block w-full rounded border p-2"
                value={track.displayedNumber}
                onChange={(event) =>
                  editTrack(index, (current) => ({
                    ...current,
                    displayedNumber: event.target.value,
                  }))
                }
              />
            </label>
            <label>
              Название дорожки
              <input
                className="block w-full rounded border p-2"
                value={track.title}
                onChange={(event) =>
                  editTrack(index, (current) => ({
                    ...current,
                    title: event.target.value,
                  }))
                }
              />
            </label>
            <div className="space-y-2 sm:col-span-2">
              <span>Исполнители дорожки (по порядку)</span>
              {track.artists.map((artist, artistIndex) => (
                <label className="block" key={artist.id}>
                  Исполнитель {artistIndex + 1}
                  <span className="flex gap-2">
                    <input
                      className="min-w-0 flex-1 rounded border p-2"
                      value={artist.name}
                      onChange={(event) =>
                        editTrack(index, (current) => ({
                          ...current,
                          artists: current.artists.map((value, item) =>
                            item === artistIndex
                              ? { ...value, name: event.target.value }
                              : value,
                          ),
                        }))
                      }
                    />
                    <AppButton
                      type="button"
                      onPress={() =>
                        editTrack(index, (current) => ({
                          ...current,
                          artists: current.artists.filter(
                            (_, item) => item !== artistIndex,
                          ),
                        }))
                      }
                    >
                      Удалить
                    </AppButton>
                  </span>
                </label>
              ))}
              <AppButton
                type="button"
                onPress={() =>
                  editTrack(index, (current) => ({
                    ...current,
                    artists: [...current.artists, { id: localID(), name: "" }],
                  }))
                }
              >
                Добавить исполнителя дорожки
              </AppButton>
            </div>
            <div className="space-y-1 sm:col-span-2">
              <span>
                Файлы, относящиеся к этой позиции (можно выбрать несколько)
              </span>
              {track.sourceMediaIDs.length > 0 && (
                <p role="note">
                  Существующие источники (только просмотр):{" "}
                  {track.sourceMediaIDs.join(", ")}
                </p>
              )}
              {scopedFiles.map((file) => (
                <label
                  className="flex items-center gap-2"
                  key={file.variant_id}
                >
                  <input
                    type="checkbox"
                    checked={track.fileMemberIds.includes(file.variant_id)}
                    onChange={() =>
                      editTrack(index, (current) => ({
                        ...current,
                        fileMemberIds: current.fileMemberIds.includes(
                          file.variant_id,
                        )
                          ? current.fileMemberIds.filter(
                              (id) => id !== file.variant_id,
                            )
                          : [...current.fileMemberIds, file.variant_id],
                      }))
                    }
                  />
                  {fileLabel(file)}
                </label>
              ))}
            </div>
            <div className="flex flex-wrap gap-2 sm:col-span-2">
              <AppButton
                type="button"
                isDisabled={!selectedSource}
                onPress={() => copyTrackSource(index)}
              >
                Скопировать TITLE и однозначный номер из выбранного файла
              </AppButton>
              {!track.localTrackID && (
                <AppButton
                  type="button"
                  onPress={() =>
                    editDraft({
                      ...draft,
                      tracks: draft.tracks.filter((_, item) => item !== index),
                    })
                  }
                >
                  Удалить дорожку
                </AppButton>
              )}
            </div>
          </fieldset>
        ))}
        <AppButton
          type="button"
          isDisabled={confirmationPending}
          onPress={addTrack}
        >
          Добавить дорожку
        </AppButton>
      </div>

      {state.error && <p role="alert">{state.error}</p>}
      {state.notice && <p role="status">{state.notice}</p>}
      {state.stale && (
        <p role="status">
          Изменения группы не сбросили введённый черновик. После обновления
          снимка проверьте его и запросите новый предпросмотр.
        </p>
      )}
      <div className="flex flex-wrap gap-2">
        <AppButton type="button" isDisabled={!canPreview} onPress={preview}>
          {state.busy === "preview"
            ? "Предпросмотр…"
            : "Предпросмотр локального релиза"}
        </AppButton>
        {previewCurrent && (
          <AppButton
            type="button"
            isDisabled={state.busy !== undefined}
            onPress={confirm}
          >
            {state.pending
              ? "Повторить подтверждение"
              : "Подтвердить локальный релиз"}
          </AppButton>
        )}
        {state.pending && !previewCurrent && (
          <AppButton
            type="button"
            isDisabled={state.busy !== undefined}
            onPress={confirm}
          >
            Повторить тот же запрос подтверждения
          </AppButton>
        )}
      </div>
      {previewCurrent && (
        <section
          aria-label="Предпросмотр локального релиза"
          className="space-y-2 rounded bg-stone-100 p-3"
        >
          <h3 className="font-medium">Предпросмотр — только чтение</h3>
          <p>
            {previewCurrent.response.release_title} ·{" "}
            {previewCurrent.response.tracks?.length ?? 0} дорожек
          </p>
          <p>
            Не включённые файлы:{" "}
            {previewCurrent.response.omitted_file_member_ids?.length ?? 0}
          </p>
          <ol>
            {previewCurrent.response.tracks?.map((track) => (
              <li key={`${track.medium}-${track.position}`}>
                {track.medium}.{track.position} · {track.displayed_number} ·{" "}
                {track.title} · файлов: {track.file_member_ids?.length ?? 0}
              </li>
            ))}
          </ol>
        </section>
      )}
      {state.receipt && (
        <p role="status">
          Подтверждён локальный релиз {state.receipt.local_release_id ?? ""}.
        </p>
      )}
    </section>
  );
}
