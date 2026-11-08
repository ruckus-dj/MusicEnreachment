import { useEffect, useRef, useState } from "react";
import {
  apiResponseError,
  errorMessage as sharedErrorMessage,
} from "../../api/client/errors";
import { createUUID } from "../../api/client/uuid";
import {
  confirmIncomingGroupLocalMatching,
  listLocalProviderReleases,
  previewIncomingGroupLocalMatching,
  readLocalProviderRelease,
} from "../../api/generated/client";
import type {
  IncomingFileResponse,
  IncomingGroupResponse,
  LocalProviderItem,
  MatchingLocalPreview,
} from "../../api/generated/client.schemas";
import { AppButton } from "../../components/AppButton";

type Assignment = { file_member_id: string; provider_track_id: string };
type PreviewBody = {
  provider_release_id: string;
  expected_group_revision: string;
  expected_authority_revision: number;
  assignments: Assignment[];
};
type ConfirmBody = PreviewBody & {
  preview_digest: string;
  request_id: string;
};
type PreviewState = {
  scope: string;
  body: PreviewBody;
  response: MatchingLocalPreview;
};
type PendingConfirmation = { scope: string; body: ConfirmBody };
type EditorState = {
  selectedReleaseID: string;
  release?: LocalProviderItem;
  assignments: Record<string, string>;
  preview?: PreviewState;
  pending?: PendingConfirmation;
  receipt?: MatchingLocalPreview;
  busy?: "preview" | "confirm";
  busyRequest?: number;
  error?: string;
  notice?: string;
  stale?: boolean;
};
type CatalogState = {
  items?: LocalProviderItem[];
  busy?: boolean;
  error?: string;
};

const emptyEditorState = (): EditorState => ({
  selectedReleaseID: "",
  assignments: {},
});

function message(reason: unknown, fallback: string) {
  return sharedErrorMessage(reason, fallback) || fallback;
}

function responseMessage(
  response: { data: { detail?: string }; status: number },
  fallback: string,
) {
  return apiResponseError(response, `${fallback} (HTTP ${response.status}).`)
    .message;
}

function fileLabel(file: IncomingFileResponse) {
  return file.locations?.[0]?.relative_path || file.variant_id;
}

function trackLabel(track: NonNullable<LocalProviderItem["tracks"]>[number]) {
  return `${track.position}. ${track.title_override || track.recording_title}`;
}

export interface LocalReleaseEditorProps {
  group: IncomingGroupResponse;
  files: IncomingFileResponse[];
  authorityRevision: number | undefined;
  onConfirmed?: (receipt: MatchingLocalPreview) => void | Promise<void>;
}

export function LocalReleaseEditor({
  group,
  files,
  authorityRevision,
  onConfirmed,
}: LocalReleaseEditorProps) {
  const [states, setStates] = useState<Record<string, EditorState>>({});
  const [catalogs, setCatalogs] = useState<Record<string, CatalogState>>({});
  const sequence = useRef(0);
  const catalogRequests = useRef<Record<string, number>>({});
  const detailRequest = useRef(0);
  const previousScopes = useRef<Record<string, string>>({});
  const selectedReleaseRef = useRef("");
  const currentScope = `${group.id}:${group.revision}:${authorityRevision === undefined ? "unknown" : authorityRevision}`;
  const currentScopeRef = useRef(currentScope);
  currentScopeRef.current = currentScope;
  const state = states[group.id] ?? emptyEditorState();
  selectedReleaseRef.current = state.selectedReleaseID;
  const scopedFiles = files.filter((file) =>
    group.members?.includes(file.variant_id),
  );
  const tracks = state.release?.tracks ?? [];
  const confirmationInFlight = state.busy === "confirm";

  function update(updateState: (current: EditorState) => EditorState) {
    setStates((current) => ({
      ...current,
      [group.id]: updateState(current[group.id] ?? emptyEditorState()),
    }));
  }

  useEffect(() => {
    const previous = previousScopes.current[group.id];
    previousScopes.current[group.id] = currentScope;
    if (previous === undefined || previous === currentScope) return;
    sequence.current += 1;
    setStates((current) => {
      const existing = current[group.id] ?? emptyEditorState();
      return {
        ...current,
        [group.id]: {
          ...existing,
          preview: undefined,
          stale: true,
          notice: existing.receipt
            ? "Локальное сопоставление подтверждено; данные группы обновлены."
            : "Снимок группы или полномочий изменился. Выберите релиз и создайте новый предпросмотр.",
        },
      };
    });
  }, [currentScope, group.id]);

  async function loadCatalog() {
    const request = (catalogRequests.current[group.id] ?? 0) + 1;
    catalogRequests.current[group.id] = request;
    setCatalogs((current) => ({
      ...current,
      [group.id]: { ...current[group.id], busy: true, error: undefined },
    }));
    try {
      const items: LocalProviderItem[] = [];
      let offset = 0;
      let total = Number.POSITIVE_INFINITY;
      while (offset < total) {
        const response = await listLocalProviderReleases(
          { provider: "local", limit: 100, offset },
          { cache: "no-store" },
        );
        if (request !== catalogRequests.current[group.id]) return;
        if (response.status !== 200) {
          setCatalogs((current) => ({
            ...current,
            [group.id]: {
              ...current[group.id],
              error: responseMessage(response, "Не удалось загрузить релизы"),
            },
          }));
          return;
        }
        const page = response.data.items ?? [];
        items.push(...page);
        total = response.data.total;
        offset += page.length;
        if (page.length === 0) break;
      }
      if (request !== catalogRequests.current[group.id]) return;
      setCatalogs((current) => ({
        ...current,
        [group.id]: { items },
      }));
    } catch (reason) {
      if (request === catalogRequests.current[group.id]) {
        setCatalogs((current) => ({
          ...current,
          [group.id]: {
            ...current[group.id],
            error: message(reason, "Не удалось загрузить релизы."),
          },
        }));
      }
    } finally {
      if (request === catalogRequests.current[group.id]) {
        setCatalogs((current) => ({
          ...current,
          [group.id]: { ...current[group.id], busy: false },
        }));
      }
    }
  }

  async function selectRelease(releaseID: string) {
    if (state.pending) return;
    const request = ++detailRequest.current;
    selectedReleaseRef.current = releaseID;
    sequence.current += 1;
    update((current) => ({
      ...current,
      selectedReleaseID: releaseID,
      release: undefined,
      assignments: {},
      preview: undefined,
      receipt: undefined,
      error: undefined,
      notice: undefined,
      stale: false,
    }));
    if (!releaseID) return;
    try {
      const response = await readLocalProviderRelease(releaseID, {
        cache: "no-store",
      });
      if (
        request !== detailRequest.current ||
        selectedReleaseRef.current !== releaseID
      )
        return;
      if (response.status !== 200) {
        update((current) => ({
          ...current,
          error: responseMessage(response, "Не удалось прочитать релиз"),
        }));
        return;
      }
      update((current) => ({ ...current, release: response.data }));
    } catch (reason) {
      if (
        request === detailRequest.current &&
        selectedReleaseRef.current === releaseID
      ) {
        update((current) => ({
          ...current,
          error: message(reason, "Не удалось прочитать релиз."),
        }));
      }
    }
  }

  function makePreviewBody(): PreviewBody | undefined {
    if (authorityRevision === undefined) {
      update((current) => ({
        ...current,
        error:
          "Полномочия ещё не загружены. Обновите экран и повторите предпросмотр.",
      }));
      return undefined;
    }
    if (!state.release || !state.selectedReleaseID) {
      update((current) => ({
        ...current,
        error: "Выберите локальный релиз и дождитесь загрузки его дорожек.",
      }));
      return undefined;
    }
    const allowedMembers = new Set(scopedFiles.map((file) => file.variant_id));
    const allowedTracks = new Set(tracks.map((track) => track.id));
    const assignments = Object.entries(state.assignments)
      .filter(
        ([fileID, trackID]) =>
          allowedMembers.has(fileID) && allowedTracks.has(trackID),
      )
      .map(([file_member_id, provider_track_id]) => ({
        file_member_id,
        provider_track_id,
      }));
    if (assignments.length === 0) {
      update((current) => ({
        ...current,
        error: "Назначьте хотя бы один файл дорожке выбранного релиза.",
      }));
      return undefined;
    }
    return {
      provider_release_id: state.selectedReleaseID,
      expected_group_revision: group.revision,
      expected_authority_revision: authorityRevision,
      assignments,
    };
  }

  async function preview() {
    if (state.pending) return;
    const body = makePreviewBody();
    if (!body) return;
    const request = ++sequence.current;
    const requestedScope = currentScope;
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
      if (
        request !== sequence.current ||
        currentScopeRef.current !== requestedScope
      )
        return;
      if (response.status !== 200) {
        update((current) => ({
          ...current,
          busy: undefined,
          busyRequest: undefined,
          preview: undefined,
          pending: response.status === 409 ? undefined : current.pending,
          stale: response.status === 409,
          error:
            response.status === 409
              ? "Снимок устарел. Обновите данные группы и создайте новый предпросмотр."
              : responseMessage(response, "Предпросмотр не выполнен"),
        }));
        return;
      }
      update((current) => ({
        ...current,
        busy: undefined,
        busyRequest: undefined,
        preview: { scope: requestedScope, body, response: response.data },
        pending: undefined,
        receipt: undefined,
      }));
    } catch (reason) {
      if (request === sequence.current) {
        update((current) => ({
          ...current,
          busy: undefined,
          busyRequest: undefined,
          error: message(reason, "Предпросмотр не выполнен."),
        }));
      }
    } finally {
      update((current) =>
        current.busyRequest === request
          ? { ...current, busy: undefined, busyRequest: undefined }
          : current,
      );
    }
  }

  async function confirm() {
    const pending =
      state.pending ??
      (state.preview?.scope === currentScope
        ? {
            scope: currentScope,
            body: {
              ...state.preview.body,
              preview_digest: state.preview.response.preview_digest,
              request_id: createUUID(),
            },
          }
        : undefined);
    if (!pending || state.busy) return;
    if (!state.pending) update((current) => ({ ...current, pending }));
    update((current) => ({ ...current, busy: "confirm", error: undefined }));
    try {
      const response = await confirmIncomingGroupLocalMatching(
        group.id,
        pending.body,
      );
      if (response.status !== 200) {
        update((current) => ({
          ...current,
          busy: undefined,
          stale: response.status === 409,
          preview: response.status === 409 ? undefined : current.preview,
          pending: response.status === 409 ? undefined : current.pending,
          error:
            response.status === 409
              ? "Снимок устарел. Получите новый предпросмотр перед подтверждением."
              : responseMessage(response, "Подтверждение не выполнено"),
        }));
        return;
      }
      update((current) => ({
        ...current,
        busy: undefined,
        receipt: response.data,
        pending: undefined,
        preview: undefined,
        notice: "Локальное сопоставление подтверждено.",
      }));
      try {
        await onConfirmed?.(response.data);
      } catch (reason) {
        update((current) => ({
          ...current,
          notice: `Сопоставление подтверждено, но обновить экран не удалось: ${message(reason, "ошибка обновления")}`,
        }));
      }
    } catch (reason) {
      update((current) => ({
        ...current,
        busy: undefined,
        error: `${message(reason, "Подтверждение не выполнено.")} Повторите запрос, чтобы безопасно проверить тот же результат.`,
      }));
    }
  }

  const previewCurrent =
    state.preview?.scope === currentScope ? state.preview : undefined;
  const canPreview =
    authorityRevision !== undefined &&
    state.busy === undefined &&
    !confirmationInFlight &&
    state.pending === undefined;

  return (
    <section
      aria-labelledby={`local-release-${group.id}`}
      className="space-y-4 rounded border border-stone-300 p-4"
    >
      <h2 id={`local-release-${group.id}`} className="text-lg font-semibold">
        Сопоставить с локальным релизом
      </h2>
      <p>
        Выберите существующий локальный релиз и явно назначьте файлы его
        дорожкам. Создание и редактирование каталога выполняется в{" "}
        {state.pending ? (
          "собственном каталоге"
        ) : (
          <a className="underline" href="#/own-catalog">
            собственном каталоге
          </a>
        )}
        .
      </p>

      <fieldset
        disabled={state.pending !== undefined || confirmationInFlight}
        className="space-y-3 rounded border border-stone-300 p-3"
      >
        <legend className="font-medium">Локальный релиз</legend>
        <AppButton
          type="button"
          isDisabled={catalogs[group.id]?.busy}
          onPress={loadCatalog}
        >
          {catalogs[group.id]?.busy
            ? "Загрузка локальных релизов…"
            : "Загрузить локальные релизы"}
        </AppButton>
        {catalogs[group.id]?.error && (
          <p role="alert">{catalogs[group.id].error}</p>
        )}
        {catalogs[group.id]?.items && (
          <label className="block">
            Локальный релиз
            <select
              className="block w-full rounded border p-2"
              value={state.selectedReleaseID}
              onChange={(event) => void selectRelease(event.target.value)}
            >
              <option value="">Выберите локальный релиз</option>
              {catalogs[group.id]?.items?.map((release) => (
                <option key={release.id} value={release.id}>
                  {release.title || release.id}
                </option>
              ))}
            </select>
          </label>
        )}
        {state.selectedReleaseID && !state.release && !state.error && (
          <p role="status">Загрузка дорожек локального релиза…</p>
        )}
        {state.release && (
          <p role="status">
            {state.release.title} · {tracks.length} дорожек
          </p>
        )}
      </fieldset>

      {state.release && (
        <fieldset
          disabled={state.pending !== undefined || confirmationInFlight}
          className="space-y-3 rounded border border-stone-300 p-3"
        >
          <legend className="font-medium">Назначение файлов дорожкам</legend>
          {scopedFiles.map((file) => (
            <label className="block" key={file.variant_id}>
              {fileLabel(file)}
              <select
                className="block w-full rounded border p-2"
                aria-label={`Дорожка для файла ${fileLabel(file)}`}
                value={state.assignments[file.variant_id] ?? ""}
                onChange={(event) => {
                  if (state.pending) return;
                  sequence.current += 1;
                  const trackID = event.target.value;
                  update((current) => {
                    if (current.pending) return current;
                    const assignments = trackID
                      ? {
                          ...current.assignments,
                          [file.variant_id]: trackID,
                        }
                      : Object.fromEntries(
                          Object.entries(current.assignments).filter(
                            ([memberID]) => memberID !== file.variant_id,
                          ),
                        );
                    return {
                      ...current,
                      assignments,
                      preview: undefined,
                      receipt: undefined,
                      error: undefined,
                      notice: undefined,
                      stale: false,
                    };
                  });
                }}
              >
                <option value="">Не назначен</option>
                {tracks.map((track) => (
                  <option key={track.id} value={track.id}>
                    {trackLabel(track)}
                  </option>
                ))}
              </select>
            </label>
          ))}
        </fieldset>
      )}

      {state.error && <p role="alert">{state.error}</p>}
      {state.notice && <p role="status">{state.notice}</p>}
      {state.stale && (
        <p role="status">
          Снимок изменился. После обновления данных создайте новый предпросмотр.
        </p>
      )}
      <div className="flex flex-wrap gap-2">
        <AppButton type="button" isDisabled={!canPreview} onPress={preview}>
          {state.busy === "preview"
            ? "Предпросмотр…"
            : "Предпросмотр назначения"}
        </AppButton>
        {previewCurrent && (
          <AppButton
            type="button"
            isDisabled={state.busy !== undefined}
            onPress={confirm}
          >
            {state.pending
              ? "Повторить тот же запрос подтверждения"
              : "Подтвердить сопоставление"}
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
          aria-label="Предпросмотр локального сопоставления"
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
        </section>
      )}
      {state.receipt?.local_release_id && (
        <p role="status">
          Сопоставление подтверждено для релиза {state.receipt.local_release_id}
          .
        </p>
      )}
    </section>
  );
}
