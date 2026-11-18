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
import { useI18n } from "../../i18n/I18nProvider";
import {
  errorAfterSnapshotRead,
  type IncomingOperationError,
  IncomingOperationFollower,
} from "./IncomingOperationFollower";
import {
  type ManualPickerMessageKey,
  manualPickerMessage,
} from "./manualPickerMessages";

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
  errorKey?: ManualPickerMessageKey;
  errorStatus?: number;
};

class ManualPickerMessageError extends Error {
  constructor(readonly messageKey: ManualPickerMessageKey) {
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

function labelText(label: { name?: string; catalogNumber?: string }) {
  if (label.name && label.catalogNumber)
    return `${label.name} (${label.catalogNumber})`;
  return label.name ?? label.catalogNumber ?? "";
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
  const message = (
    key: ManualPickerMessageKey,
    values?: Record<string, string | number>,
  ) => manualPickerMessage(locale, key, values);
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

  function editionDetails(item: PickerItem) {
    const formats = item.formats.join(" / ");
    const media =
      item.mediaCount > 1
        ? [formats, message("discCount", { count: item.mediaCount })]
            .filter(Boolean)
            .join(" · ")
        : formats;
    return [
      item.date ? message("releaseDate", { date: item.date }) : undefined,
      item.country
        ? message("releaseCountry", { country: item.country })
        : undefined,
      item.trackCount !== undefined
        ? message("releaseTracks", { count: item.trackCount })
        : undefined,
      media ? message("releaseFormats", { formats: media }) : undefined,
      item.labels.length
        ? message("releaseLabels", {
            labels: item.labels.map(labelText).join(", "),
          })
        : undefined,
      item.score !== undefined
        ? message("score", { score: item.score })
        : undefined,
    ].filter((detail): detail is string => Boolean(detail));
  }

  function chooseLabel(item: PickerItem) {
    const details = [item.artistCredit, ...editionDetails(item)]
      .filter(Boolean)
      .join(", ");
    return details
      ? message("selectReleaseDescribed", { title: item.title, details })
      : message("selectReleaseTitled", { title: item.title });
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
    <section
      className="rounded border border-stone-300 bg-white p-4"
      aria-labelledby="manual-search-title"
    >
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
      <h2 id="manual-search-title" className="font-semibold">
        {message("searchTitle")}
      </h2>
      <p className="mt-1 text-sm">{message("searchDescription")}</p>
      <div className="mt-3 flex flex-wrap gap-2">
        <label className="sr-only" htmlFor="manual-search-query">
          {message("searchQuery")}
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
                offset: 0,
                error: undefined,
                errorKey: undefined,
                errorStatus: undefined,
              },
            }))
          }
        />
        <label className="sr-only" htmlFor="manual-search-limit">
          {message("resultLimit")}
        </label>
        <input
          id="manual-search-limit"
          type="number"
          min="1"
          max="100"
          aria-label={message("resultLimit")}
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
                errorKey: undefined,
                errorStatus: undefined,
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
          {message("search")}
        </AppButton>
      </div>
      {pending ? (
        <p role="status" className="mt-2">
          {message("searching")}
        </p>
      ) : null}
      {state.error || state.errorKey || operation?.state === "failed" ? (
        <p role="alert" className="mt-2">
          {state.error ||
            operation?.safe_error ||
            message(state.errorKey ?? "searchFailed", {
              status: state.errorStatus ?? "",
            })}
        </p>
      ) : null}
      {currentOperationId && operationErrors[currentOperationId] ? (
        <div role="alert" className="mt-2 space-y-2">
          <p>{operationErrors[currentOperationId].message}</p>
          <AppButton
            onPress={() => void refreshers.current[currentOperationId]?.()}
          >
            {message("retryReading")}
          </AppButton>
        </div>
      ) : null}
      {resultQuery?.isError ? (
        <p role="alert">
          {resultQuery.error instanceof ManualPickerMessageError
            ? message(resultQuery.error.messageKey)
            : resultQuery.error.message}
        </p>
      ) : null}
      {parsed ? (
        <>
          {parsed.items.length === 0 ? (
            <p role="status" className="mt-3">
              {message("noMatches")}
            </p>
          ) : (
            <ul className="mt-3 divide-y divide-stone-200 rounded border border-stone-200">
              {parsed.items.map((item) => {
                const details = editionDetails(item);
                return (
                  <li
                    key={item.id}
                    className="flex flex-wrap items-start justify-between gap-3 p-3"
                  >
                    <div className="min-w-0 flex-1">
                      <p className="font-medium">
                        <span>{item.title}</span>
                        {item.disambiguation ? (
                          <span className="ml-1 text-sm font-normal text-stone-600">
                            ({item.disambiguation})
                          </span>
                        ) : null}
                      </p>
                      {item.artistCredit ? (
                        <p className="text-sm">{item.artistCredit}</p>
                      ) : null}
                      {details.length ? (
                        <p className="mt-1 text-xs text-[var(--muted)] wrap-anywhere">
                          {details.join(" · ")}
                        </p>
                      ) : null}
                    </div>
                    <AppButton
                      aria-label={chooseLabel(item)}
                      onPress={() => onSelectRelease(item.id)}
                    >
                      {message("selectRelease")}
                    </AppButton>
                  </li>
                );
              })}
            </ul>
          )}
          <div className="mt-3 flex items-center gap-2">
            <span>
              {message("resultRange", {
                start: state.offset + 1,
                end: Math.min(state.offset + pageLimit, parsed.count),
                count: parsed.count,
              })}
            </span>
            <AppButton
              onPress={() => void submit(Math.max(0, state.offset - pageLimit))}
              isDisabled={state.offset === 0 || pending}
            >
              {message("previous")}
            </AppButton>
            <AppButton
              onPress={() => void submit(state.offset + pageLimit)}
              isDisabled={state.offset + pageLimit >= parsed.count || pending}
            >
              {message("next")}
            </AppButton>
          </div>
        </>
      ) : null}
    </section>
  );
}
