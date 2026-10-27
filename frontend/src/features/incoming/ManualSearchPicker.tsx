import { useQueries } from "@tanstack/react-query";
import { useRef, useState } from "react";
import {
  getGetIncomingGroupManualPickerSearchQueryKey,
  getIncomingGroupManualPickerSearch,
  startIncomingGroupManualPickerSearch,
} from "../../api/generated/client";
import type {
  ManualPickerSearchBody,
  OperationResponse,
} from "../../api/generated/client.schemas";
import { AppButton } from "../../components/AppButton";
import {
  errorAfterSnapshotRead,
  type IncomingOperationError,
  IncomingOperationFollower,
} from "./IncomingOperationFollower";

type Props = {
  groupId: string;
  groupRevision: string;
  disabled?: boolean;
  onSelectRelease: (releaseId: string) => void;
};

type PickerItem = {
  id: string;
  title: string;
  disambiguation?: string;
  score?: number;
};
type GroupState = {
  entity: "artist" | "release";
  query: string;
  limit: string;
  offset: number;
  artistId?: string;
  artistName?: string;
  body?: ManualPickerSearchBody;
  operationId?: string;
  admitting?: boolean;
  error?: string;
};

const canonicalMBID =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;

function parseItems(
  payload: unknown,
  entity: "artist" | "release",
  browsingArtist: boolean,
  offset: number,
  limit: number,
) {
  if (!payload || typeof payload !== "object" || Array.isArray(payload))
    throw new Error("MusicBrainz вернул некорректные результаты поиска.");
  const record = payload as Record<string, unknown>;
  const count = record[browsingArtist ? "release-count" : "count"];
  const resultOffset = record[browsingArtist ? "release-offset" : "offset"];
  const rawItems = record[entity === "artist" ? "artists" : "releases"];
  if (
    !Array.isArray(rawItems) ||
    !Number.isInteger(count) ||
    (count as number) < 0 ||
    !Number.isInteger(resultOffset) ||
    resultOffset !== offset ||
    rawItems.length > limit ||
    (count as number) < offset + rawItems.length
  )
    throw new Error("MusicBrainz вернул некорректные результаты поиска.");
  const seen = new Set<string>();
  const items = rawItems.map((item): PickerItem => {
    if (!item || typeof item !== "object" || Array.isArray(item))
      throw new Error("MusicBrainz вернул некорректные результаты поиска.");
    const value = item as Record<string, unknown>;
    const id = value.id;
    const title = entity === "artist" ? value.name : value.title;
    if (
      typeof id !== "string" ||
      !canonicalMBID.test(id) ||
      seen.has(id) ||
      typeof title !== "string" ||
      !title.trim() ||
      (value.score !== undefined &&
        (typeof value.score !== "number" || !Number.isFinite(value.score)))
    )
      throw new Error("MusicBrainz вернул некорректные результаты поиска.");
    seen.add(id);
    return {
      id,
      title,
      ...(typeof value.disambiguation === "string"
        ? { disambiguation: value.disambiguation }
        : {}),
      ...(typeof value.score === "number" ? { score: value.score } : {}),
    };
  });
  return { count: count as number, items };
}

export function ManualSearchPicker({
  groupId,
  groupRevision,
  disabled = false,
  onSelectRelease,
}: Props) {
  const [states, setStates] = useState<Record<string, GroupState>>({});
  const [snapshots, setSnapshots] = useState<Record<string, OperationResponse>>(
    {},
  );
  const [operationErrors, setOperationErrors] = useState<
    Record<string, IncomingOperationError>
  >({});
  const refreshers = useRef<Record<string, () => Promise<void>>>({});
  const state = states[groupId] ?? {
    entity: "artist",
    query: "",
    limit: "10",
    offset: 0,
  };
  const operations = Object.entries(states).filter(
    ([, item]) => item.operationId,
  );
  const resultQueries = useQueries({
    queries: operations.map(([key, item]) => ({
      queryKey: [
        ...getGetIncomingGroupManualPickerSearchQueryKey(
          key,
          item.operationId as string,
        ),
        item.body?.request_id,
      ],
      enabled: snapshots[item.operationId as string]?.state === "succeeded",
      queryFn: async ({ signal }: { signal: AbortSignal }) => {
        const response = await getIncomingGroupManualPickerSearch(
          key,
          item.operationId as string,
          { signal, cache: "no-store" },
        );
        if (response.status !== 200)
          throw new Error(
            response.data.detail || "Не удалось прочитать результаты поиска.",
          );
        const body = item.body;
        if (
          !body ||
          typeof body.offset !== "number" ||
          typeof body.limit !== "number"
        )
          throw new Error("Не удалось прочитать параметры поиска.");
        return parseItems(
          response.data.payload,
          body.artist_id ? "release" : body.entity,
          Boolean(body.artist_id),
          body.offset,
          body.limit,
        );
      },
    })),
  });
  const currentIndex = operations.findIndex(([key]) => key === groupId);
  const currentOperationId = operations[currentIndex]?.[1].operationId;
  const operation = snapshots[currentOperationId ?? ""];
  const resultQuery = resultQueries[currentIndex];
  const pageLimit = Number(state.limit);
  const pending = Boolean(
    state.admitting ||
      (state.operationId &&
        (!operation ||
          operation.state === "queued" ||
          operation.state === "running")),
  );
  const requestBody = state.body;
  const requestEntity = requestBody?.artist_id
    ? "release"
    : requestBody?.entity;
  const resultIsCurrent = Boolean(
    requestBody &&
      requestBody.expected_group_revision === groupRevision &&
      requestBody.limit === pageLimit &&
      requestBody.entity === (state.artistId ? "artist" : state.entity) &&
      requestBody.query === state.query &&
      requestBody.artist_id === state.artistId,
  );
  const parsed =
    resultQuery?.data && resultIsCurrent ? resultQuery.data : undefined;

  async function submit(
    offset = state.offset,
    artistId = state.artistId,
    override?: {
      entity: "artist" | "release";
      query: string;
      artistName?: string;
    },
  ) {
    if (
      !state.query.trim() ||
      !Number.isInteger(pageLimit) ||
      pageLimit < 1 ||
      pageLimit > 100 ||
      pending ||
      disabled
    )
      return;
    const entity = override?.entity ?? state.entity;
    const query = override?.query ?? state.query;
    let body = state.body;
    if (
      !body ||
      body.expected_group_revision !== groupRevision ||
      body.limit !== pageLimit ||
      body.offset !== offset ||
      body.query !== query ||
      body.entity !== (artistId ? "artist" : entity) ||
      body.artist_id !== artistId
    ) {
      body = {
        entity: artistId ? "artist" : entity,
        query,
        expected_group_revision: groupRevision,
        offset,
        limit: pageLimit,
        request_id: crypto.randomUUID(),
        ...(artistId ? { artist_id: artistId } : {}),
      };
    }
    setStates((current) => ({
      ...current,
      [groupId]: {
        ...state,
        ...(override ? { entity, query, artistName: override.artistName } : {}),
        offset,
        artistId,
        body,
        admitting: true,
        error: undefined,
      },
    }));
    try {
      const response = await startIncomingGroupManualPickerSearch(
        groupId,
        body,
      );
      if (response.status !== 202) {
        setStates((current) => ({
          ...current,
          [groupId]: {
            ...current[groupId],
            admitting: false,
            body:
              response.status >= 400 && response.status < 500
                ? undefined
                : body,
            error:
              response.data.detail ||
              `Поиск не принят (HTTP ${response.status}).`,
          },
        }));
        return;
      }
      setStates((current) => ({
        ...current,
        [groupId]: {
          ...current[groupId],
          admitting: false,
          operationId: response.data.operation_id,
        },
      }));
    } catch (reason) {
      setStates((current) => ({
        ...current,
        [groupId]: {
          ...current[groupId],
          admitting: false,
          body,
          error:
            reason instanceof Error
              ? reason.message
              : "Не удалось выполнить поиск.",
        },
      }));
    }
  }

  return (
    <section
      className="rounded border border-stone-300 bg-white p-4"
      aria-labelledby="manual-search-title"
    >
      {operations.map(([, item]) => (
        <IncomingOperationFollower
          key={item.operationId}
          operationId={item.operationId as string}
          refreshers={refreshers}
          onSnapshot={(snapshot) => {
            setSnapshots((current) => ({
              ...current,
              [snapshot.id]: snapshot,
            }));
          }}
          onSnapshotRead={(_snapshot, source) =>
            setOperationErrors((current) => {
              const error = errorAfterSnapshotRead(
                current[item.operationId as string],
                source,
              );
              if (error === current[item.operationId as string]) return current;
              const next = { ...current };
              if (error) next[item.operationId as string] = error;
              else delete next[item.operationId as string];
              return next;
            })
          }
          onError={(error) =>
            setOperationErrors((current) =>
              current[item.operationId as string]?.kind === "stream"
                ? current
                : {
                    ...current,
                    [item.operationId as string]: {
                      message: error.message,
                      kind: "rest",
                    },
                  },
            )
          }
          onStreamError={(error) =>
            setOperationErrors((current) => ({
              ...current,
              [item.operationId as string]: {
                message: error.message,
                kind: "stream",
              },
            }))
          }
        />
      ))}
      <h2 id="manual-search-title" className="font-semibold">
        Поиск MusicBrainz
      </h2>
      <p className="mt-1 text-sm">
        Поиск выполняется только после отправки запроса; результаты не запускают
        подготовку автоматически.
      </p>
      <div className="mt-3 flex flex-wrap gap-2">
        <label className="sr-only" htmlFor="manual-search-entity">
          Тип поиска
        </label>
        <select
          id="manual-search-entity"
          className="rounded border border-stone-400 p-2"
          value={state.entity}
          disabled={pending || disabled}
          onChange={(event) =>
            setStates((current) => ({
              ...current,
              [groupId]: {
                ...state,
                entity: event.target.value as "artist" | "release",
                artistId: undefined,
                artistName: undefined,
                offset: 0,
                error: undefined,
              },
            }))
          }
        >
          <option value="artist">Артисты</option>
          <option value="release">Издания</option>
        </select>
        <label className="sr-only" htmlFor="manual-search-query">
          Поисковый запрос
        </label>
        <input
          id="manual-search-query"
          className="min-w-64 flex-1 rounded border border-stone-400 p-2"
          value={state.query}
          disabled={pending || disabled}
          onChange={(event) =>
            setStates((current) => ({
              ...current,
              [groupId]: {
                ...state,
                query: event.target.value,
                artistId: undefined,
                artistName: undefined,
                offset: 0,
                error: undefined,
              },
            }))
          }
        />
        <label className="sr-only" htmlFor="manual-search-limit">
          Количество результатов
        </label>
        <input
          id="manual-search-limit"
          type="number"
          min="1"
          max="100"
          aria-label="Количество результатов"
          className="w-28 rounded border border-stone-400 p-2"
          value={state.limit}
          disabled={pending || disabled}
          onChange={(event) =>
            setStates((current) => ({
              ...current,
              [groupId]: {
                ...state,
                limit: event.target.value,
                offset: 0,
                error: undefined,
              },
            }))
          }
        />
        <AppButton
          onPress={() => void submit(0)}
          isDisabled={
            !state.query.trim() ||
            !Number.isInteger(pageLimit) ||
            pageLimit < 1 ||
            pageLimit > 100 ||
            pending ||
            disabled
          }
        >
          Искать
        </AppButton>
      </div>
      {state.artistId ? (
        <p className="mt-2 text-sm">Издания артиста: {state.artistName}</p>
      ) : null}
      {pending ? (
        <p role="status" className="mt-2">
          Поиск MusicBrainz…
        </p>
      ) : null}
      {state.error || operation?.state === "failed" ? (
        <p role="alert" className="mt-2">
          {state.error ||
            operation?.safe_error ||
            "Не удалось выполнить поиск."}
        </p>
      ) : null}
      {currentOperationId && operationErrors[currentOperationId] ? (
        <div role="alert" className="mt-2 space-y-2">
          <p>{operationErrors[currentOperationId].message}</p>
          <AppButton
            onPress={() => void refreshers.current[currentOperationId]?.()}
          >
            Повторить чтение
          </AppButton>
        </div>
      ) : null}
      {resultQuery?.isError ? (
        <p role="alert">{resultQuery.error.message}</p>
      ) : null}
      {parsed ? (
        <>
          {parsed.items.length === 0 ? (
            <p role="status" className="mt-3">
              Совпадений нет.
            </p>
          ) : (
            <ul className="mt-3 space-y-2">
              {parsed.items.map((item) => (
                <li
                  key={item.id}
                  className="rounded border border-stone-200 p-3"
                >
                  <p className="font-medium">{item.title}</p>
                  {item.disambiguation ? (
                    <p className="text-sm">{item.disambiguation}</p>
                  ) : null}
                  {item.score !== undefined ? (
                    <p className="text-sm">Оценка MusicBrainz: {item.score}</p>
                  ) : null}
                  {requestEntity === "artist" ? (
                    <AppButton
                      onPress={() => {
                        setStates((current) => ({
                          ...current,
                          [groupId]: {
                            ...state,
                            entity: "release",
                            query: item.title,
                            artistId: item.id,
                            artistName: item.title,
                            offset: 0,
                            body: undefined,
                            error: undefined,
                          },
                        }));
                        void submit(0, item.id, {
                          entity: "release",
                          query: item.title,
                          artistName: item.title,
                        });
                      }}
                    >
                      Просмотреть издания артиста
                    </AppButton>
                  ) : (
                    <AppButton onPress={() => onSelectRelease(item.id)}>
                      Выбрать это издание
                    </AppButton>
                  )}
                </li>
              ))}
            </ul>
          )}
          <div className="mt-3 flex items-center gap-2">
            <span>
              Результаты {state.offset + 1}–
              {Math.min(state.offset + pageLimit, parsed.count)} из{" "}
              {parsed.count}
            </span>
            <AppButton
              onPress={() => void submit(Math.max(0, state.offset - pageLimit))}
              isDisabled={state.offset === 0 || pending}
            >
              Предыдущая
            </AppButton>
            <AppButton
              onPress={() => void submit(state.offset + pageLimit)}
              isDisabled={state.offset + pageLimit >= parsed.count || pending}
            >
              Следующая
            </AppButton>
          </div>
        </>
      ) : null}
    </section>
  );
}
