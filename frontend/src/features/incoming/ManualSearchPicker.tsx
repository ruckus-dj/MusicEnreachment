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
import { useI18n } from "../../i18n/I18nProvider";
import { ManualSearchView } from "../matchingWorkspace/ManualSearchView";
import {
  type ManualSearchMessageKey,
  type ManualSearchViewError,
  manualSearchMessage,
} from "../matchingWorkspace/manualSearchContract";
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
  artistCredit?: string;
  disambiguation?: string;
  date?: string;
  country?: string;
  trackCount?: number;
  formats: string[];
  mediaCount: number;
  labels: { name?: string; catalogNumber?: string }[];
  score?: number;
};
type GroupState = {
  query: string;
  limit: string;
  offset: number;
  body?: ManualPickerSearchBody;
  operationId?: string;
  admitting?: boolean;
  error?: string;
  errorKey?: ManualSearchMessageKey;
  errorStatus?: number;
};

class ManualPickerMessageError extends Error {
  constructor(readonly messageKey: ManualSearchMessageKey) {
    super(messageKey);
  }
}

const canonicalMBID =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;

function nonEmptyString(value: unknown) {
  return typeof value === "string" && value.trim() ? value : undefined;
}

function nonNegativeInteger(value: unknown) {
  return Number.isSafeInteger(value) && (value as number) >= 0
    ? (value as number)
    : undefined;
}

function parseArtistCredit(value: unknown) {
  if (!Array.isArray(value)) return undefined;
  let credit = "";
  for (const entry of value) {
    if (!entry || typeof entry !== "object" || Array.isArray(entry))
      return undefined;
    const record = entry as Record<string, unknown>;
    const artist = record.artist;
    const name =
      nonEmptyString(record.name) ??
      (artist && typeof artist === "object" && !Array.isArray(artist)
        ? nonEmptyString((artist as Record<string, unknown>).name)
        : undefined);
    if (!name) return undefined;
    credit += name;
    if (typeof record.joinphrase === "string") credit += record.joinphrase;
  }
  return credit.trim() || undefined;
}

function parseMedia(value: unknown) {
  if (!Array.isArray(value)) return { formats: [], mediaCount: 0 };
  const formats: string[] = [];
  let mediaCount = 0;
  for (const entry of value) {
    if (!entry || typeof entry !== "object" || Array.isArray(entry)) continue;
    mediaCount += 1;
    const format = nonEmptyString((entry as Record<string, unknown>).format);
    if (format && !formats.includes(format)) formats.push(format);
  }
  return { formats, mediaCount };
}

function sumMediaTracks(value: unknown) {
  if (!Array.isArray(value) || !value.length) return undefined;
  let total = 0;
  for (const entry of value) {
    if (!entry || typeof entry !== "object" || Array.isArray(entry))
      return undefined;
    const count = nonNegativeInteger(
      (entry as Record<string, unknown>)["track-count"],
    );
    if (count === undefined) return undefined;
    total += count;
  }
  return Number.isSafeInteger(total) ? total : undefined;
}

function parseLabels(value: unknown) {
  if (!Array.isArray(value)) return [];
  const labels: { name?: string; catalogNumber?: string }[] = [];
  for (const entry of value) {
    if (!entry || typeof entry !== "object" || Array.isArray(entry)) continue;
    const record = entry as Record<string, unknown>;
    const label = record.label;
    const name =
      label && typeof label === "object" && !Array.isArray(label)
        ? nonEmptyString((label as Record<string, unknown>).name)
        : undefined;
    const catalogNumber = nonEmptyString(record["catalog-number"]);
    if (name || catalogNumber)
      labels.push({
        ...(name ? { name } : {}),
        ...(catalogNumber ? { catalogNumber } : {}),
      });
  }
  return labels;
}

function parseItems(payload: unknown, offset: number, limit: number) {
  if (!payload || typeof payload !== "object" || Array.isArray(payload))
    throw new ManualPickerMessageError("invalidResults");
  const record = payload as Record<string, unknown>;
  const count = record.count;
  const resultOffset = record.offset;
  const rawItems = record.releases;
  if (
    !Array.isArray(rawItems) ||
    !Number.isInteger(count) ||
    (count as number) < 0 ||
    !Number.isInteger(resultOffset) ||
    resultOffset !== offset ||
    rawItems.length > limit ||
    (count as number) < offset + rawItems.length
  )
    throw new ManualPickerMessageError("invalidResults");
  const seen = new Set<string>();
  const items = rawItems.map((item): PickerItem => {
    if (!item || typeof item !== "object" || Array.isArray(item))
      throw new ManualPickerMessageError("invalidResults");
    const value = item as Record<string, unknown>;
    const id = value.id;
    const title = value.title;
    if (
      typeof id !== "string" ||
      !canonicalMBID.test(id) ||
      seen.has(id) ||
      typeof title !== "string" ||
      !title.trim() ||
      (value.score !== undefined &&
        (typeof value.score !== "number" || !Number.isFinite(value.score)))
    )
      throw new ManualPickerMessageError("invalidResults");
    seen.add(id);
    const artistCredit = parseArtistCredit(value["artist-credit"]);
    const disambiguation = nonEmptyString(value.disambiguation);
    const date = nonEmptyString(value.date);
    const country = nonEmptyString(value.country);
    const trackCount =
      nonNegativeInteger(value["track-count"]) ?? sumMediaTracks(value.media);
    const media = parseMedia(value.media);
    return {
      id,
      title,
      ...(artistCredit ? { artistCredit } : {}),
      ...(disambiguation ? { disambiguation } : {}),
      ...(date ? { date } : {}),
      ...(country ? { country } : {}),
      ...(trackCount !== undefined ? { trackCount } : {}),
      formats: media.formats,
      mediaCount: media.mediaCount,
      labels: parseLabels(value["label-info"]),
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
  const { locale } = useI18n();
  const [states, setStates] = useState<Record<string, GroupState>>({});
  const [snapshots, setSnapshots] = useState<Record<string, OperationResponse>>(
    {},
  );
  const [operationErrors, setOperationErrors] = useState<
    Record<string, IncomingOperationError>
  >({});
  const refreshers = useRef<Record<string, () => Promise<void>>>({});
  const state = states[groupId] ?? {
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
        if (response.status !== 200) {
          if (response.data.detail) throw new Error(response.data.detail);
          throw new ManualPickerMessageError("readResultsFailed");
        }
        const body = item.body;
        if (
          !body ||
          typeof body.offset !== "number" ||
          typeof body.limit !== "number"
        )
          throw new ManualPickerMessageError("searchParametersFailed");
        return parseItems(response.data.payload, body.offset, body.limit);
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
  const resultIsCurrent = Boolean(
    requestBody &&
      requestBody.expected_group_revision === groupRevision &&
      requestBody.limit === pageLimit &&
      requestBody.offset === state.offset &&
      requestBody.entity === "release" &&
      requestBody.query === state.query,
  );
  const parsed =
    resultQuery?.data && resultIsCurrent ? resultQuery.data : undefined;

  const errors: ManualSearchViewError[] = [];
  if (state.error || state.errorKey || operation?.state === "failed") {
    errors.push({
      id: "search",
      message:
        state.error ||
        operation?.safe_error ||
        manualSearchMessage(locale, state.errorKey ?? "searchFailed", {
          status: state.errorStatus ?? "",
        }),
    });
  }
  if (currentOperationId && operationErrors[currentOperationId]) {
    errors.push({
      id: "operation",
      message: operationErrors[currentOperationId].message,
      onRetry: () => void refreshers.current[currentOperationId]?.(),
    });
  }
  if (resultQuery?.isError) {
    errors.push({
      id: "results",
      message:
        resultQuery.error instanceof ManualPickerMessageError
          ? manualSearchMessage(locale, resultQuery.error.messageKey)
          : resultQuery.error.message,
    });
  }

  function changeQuery(query: string) {
    setStates((current) => ({
      ...current,
      [groupId]: {
        ...state,
        query,
        offset: 0,
        error: undefined,
        errorKey: undefined,
        errorStatus: undefined,
      },
    }));
  }

  function changeLimit(limit: string) {
    setStates((current) => ({
      ...current,
      [groupId]: {
        ...state,
        limit,
        offset: 0,
        error: undefined,
        errorKey: undefined,
        errorStatus: undefined,
      },
    }));
  }

  async function submit(offset = state.offset) {
    if (
      !state.query.trim() ||
      !Number.isInteger(pageLimit) ||
      pageLimit < 1 ||
      pageLimit > 100 ||
      pending ||
      disabled
    )
      return;
    let body = state.body;
    if (
      !body ||
      body.expected_group_revision !== groupRevision ||
      body.limit !== pageLimit ||
      body.offset !== offset ||
      body.entity !== "release" ||
      body.query !== state.query
    ) {
      body = {
        entity: "release",
        query: state.query,
        expected_group_revision: groupRevision,
        offset,
        limit: pageLimit,
        request_id: crypto.randomUUID(),
      };
    }
    setStates((current) => ({
      ...current,
      [groupId]: {
        ...state,
        offset,
        body,
        admitting: true,
        error: undefined,
        errorKey: undefined,
        errorStatus: undefined,
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
            error: response.data.detail,
            errorKey: response.data.detail ? undefined : "searchNotAccepted",
            errorStatus: response.data.detail ? undefined : response.status,
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
          error: reason instanceof Error ? reason.message : undefined,
          errorKey: reason instanceof Error ? undefined : "searchFailed",
          errorStatus: undefined,
        },
      }));
    }
  }

  return (
    <>
      {operations.map(([, item]) => (
        <IncomingOperationFollower
          key={item.operationId}
          operationId={item.operationId as string}
          enabled={
            snapshots[item.operationId as string]?.state !== "succeeded" &&
            snapshots[item.operationId as string]?.state !== "failed"
          }
          refreshers={refreshers}
          onSnapshot={(snapshot) => {
            setSnapshots((current) => ({
              ...current,
              [snapshot.id]: snapshot,
            }));
            if (snapshot.state === "succeeded" || snapshot.state === "failed")
              setOperationErrors((current) => {
                if (current[snapshot.id]?.kind !== "stream") return current;
                const next = { ...current };
                delete next[snapshot.id];
                return next;
              });
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
      <ManualSearchView
        locale={locale}
        query={state.query}
        limit={state.limit}
        offset={state.offset}
        disabled={disabled}
        pending={pending}
        results={
          parsed ? { count: parsed.count, items: parsed.items } : undefined
        }
        errors={errors}
        onQueryChange={changeQuery}
        onLimitChange={changeLimit}
        onSearch={() => void submit(0)}
        onSelectRelease={onSelectRelease}
        onPrevious={() => void submit(Math.max(0, state.offset - pageLimit))}
        onNext={() => void submit(state.offset + pageLimit)}
      />
    </>
  );
}
