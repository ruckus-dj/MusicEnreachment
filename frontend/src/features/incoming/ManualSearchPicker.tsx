import { useQueries } from "@tanstack/react-query";
import { useRef, useState } from "react";
import {
  getGetIncomingGroupManualPickerSearchQueryKey,
  getIncomingGroupManualPickerSearch,
  listLocalProviderReleases,
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
  type ManualSearchSelection,
  type ManualSearchViewError,
  type ManualSearchViewItem,
  type ManualSearchViewResults,
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
  selectedReleaseId?: string;
  selectedLocalReleaseId?: string;
  onSelectRelease: (releaseId: string) => void;
  onSelectLocalRelease?: (releaseId: string) => void;
};

type PickerItem = ManualSearchViewItem;
type LocalPickerPage = {
  total: number;
  items: PickerItem[];
};
type LocalSearchError = {
  detail?: string;
  messageKey: ManualSearchMessageKey;
  status?: number;
};
type SearchRequest = {
  query: string;
  limit: number;
  offset: number;
};
type GroupState = {
  query: string;
  limit: string;
  offset: number;
  body?: ManualPickerSearchBody;
  request?: SearchRequest;
  local?: LocalPickerPage;
  localError?: LocalSearchError;
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
const canonicalUUID = /^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$/i;

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
    const releaseGroup = value["release-group"];
    const releaseGroupTitle =
      releaseGroup &&
      typeof releaseGroup === "object" &&
      !Array.isArray(releaseGroup)
        ? nonEmptyString((releaseGroup as Record<string, unknown>).title)
        : undefined;
    const date = nonEmptyString(value.date);
    const country = nonEmptyString(value.country);
    const trackCount =
      nonNegativeInteger(value["track-count"]) ?? sumMediaTracks(value.media);
    const media = parseMedia(value.media);
    return {
      id,
      title,
      provider: "MusicBrainz",
      ...(artistCredit ? { artistCredit } : {}),
      ...(releaseGroupTitle && releaseGroupTitle !== title
        ? { releaseGroupTitle }
        : {}),
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

function parseLocalPage(
  payload: unknown,
  offset: number,
  limit: number,
): LocalPickerPage {
  if (!payload || typeof payload !== "object" || Array.isArray(payload))
    throw new ManualPickerMessageError("invalidLocalResults");
  const record = payload as Record<string, unknown>;
  const total = record.total;
  const resultOffset = record.offset;
  const rawItems = record.items;
  if (
    !Number.isSafeInteger(total) ||
    (total as number) < 0 ||
    !Number.isInteger(resultOffset) ||
    resultOffset !== offset ||
    !Array.isArray(rawItems) ||
    rawItems.length > limit ||
    rawItems.length > Math.max(0, (total as number) - offset)
  )
    throw new ManualPickerMessageError("invalidLocalResults");
  const seen = new Set<string>();
  const items = rawItems.map((item): PickerItem => {
    if (!item || typeof item !== "object" || Array.isArray(item))
      throw new ManualPickerMessageError("invalidLocalResults");
    const value = item as Record<string, unknown>;
    const id = value.id;
    const title = value.title;
    if (
      typeof id !== "string" ||
      !canonicalUUID.test(id) ||
      seen.has(id) ||
      typeof title !== "string" ||
      !title.trim()
    )
      throw new ManualPickerMessageError("invalidLocalResults");
    seen.add(id);
    const releaseGroupTitle = nonEmptyString(value.release_group_title);
    const edition =
      value.edition &&
      typeof value.edition === "object" &&
      !Array.isArray(value.edition)
        ? (value.edition as Record<string, unknown>)
        : {};
    const artistCredit = nonEmptyString(edition.artist_credit);
    const trackCount = nonNegativeInteger(edition.track_count);
    const date = nonEmptyString(edition.release_date);
    const country = nonEmptyString(edition.country);
    const format = nonEmptyString(edition.format);
    const label = nonEmptyString(edition.label);
    const catalogNumber = nonEmptyString(edition.catalog_number);
    const disambiguation = nonEmptyString(edition.disambiguation);
    return {
      id,
      title,
      provider: "Local",
      ...(artistCredit ? { artistCredit } : {}),
      ...(trackCount !== undefined ? { trackCount } : {}),
      ...(date ? { date } : {}),
      ...(country ? { country } : {}),
      ...(disambiguation ? { disambiguation } : {}),
      ...(releaseGroupTitle && releaseGroupTitle !== title
        ? { releaseGroupTitle }
        : {}),
      formats: format ? [format] : [],
      mediaCount: 0,
      labels: label || catalogNumber ? [{ name: label, catalogNumber }] : [],
    };
  });
  return { total: total as number, items };
}

export function ManualSearchPicker({
  groupId,
  groupRevision,
  disabled = false,
  selectedReleaseId,
  selectedLocalReleaseId,
  onSelectRelease,
  onSelectLocalRelease,
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
  const submitRuns = useRef<Record<string, number>>({});
  const localAborts = useRef<Record<string, AbortController>>({});
  const currentGroup = useRef(groupId);
  currentGroup.current = groupId;
  const localEnabled = Boolean(onSelectLocalRelease);
  const selectedItem: ManualSearchSelection | undefined = selectedLocalReleaseId
    ? { provider: "Local", id: selectedLocalReleaseId }
    : selectedReleaseId
      ? { provider: "MusicBrainz", id: selectedReleaseId }
      : undefined;
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
  const request = state.request;
  const requestBody = state.body;
  const requestIsCurrent = Boolean(
    request &&
      request.query === state.query &&
      request.limit === pageLimit &&
      request.offset === state.offset,
  );
  const bodyIsCurrent = Boolean(
    requestIsCurrent &&
      requestBody &&
      requestBody.entity === "release" &&
      requestBody.expected_group_revision === groupRevision &&
      requestBody.query === request?.query,
  );
  const parsed =
    resultQuery?.data && bodyIsCurrent ? resultQuery.data : undefined;
  const localPage = localEnabled && requestIsCurrent ? state.local : undefined;
  const localError =
    localEnabled && requestIsCurrent ? state.localError : undefined;
  const startFailed = Boolean(state.error || state.errorKey);
  const operationFailed = operation?.state === "failed";
  const resultsFailed = Boolean(resultQuery?.isError);
  const musicBrainzSettled =
    Boolean(parsed) || startFailed || operationFailed || resultsFailed;
  const localSettled =
    !localEnabled || localPage !== undefined || localError !== undefined;
  const settled = localSettled && musicBrainzSettled;
  const localItems = localPage?.items ?? [];
  let results: ManualSearchViewResults | undefined;
  if (settled && (localPage || parsed)) {
    const remaining = Math.max(0, pageLimit - localItems.length);
    results = {
      count: (localPage?.total ?? 0) + (parsed?.count ?? 0),
      items: [
        ...localItems,
        ...(parsed
          ? parsed.items
              .slice(0, remaining)
              .map((item) =>
                localEnabled ? { ...item, score: undefined } : item,
              )
          : []),
      ],
    };
  }

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
  if (localError) {
    const retry = pending ? undefined : () => void submit(state.offset);
    errors.push({
      id: "local",
      message:
        localError.detail ||
        manualSearchMessage(
          locale,
          localError.messageKey,
          localError.status === undefined ? {} : { status: localError.status },
        ),
      ...(retry ? { onRetry: retry } : {}),
    });
  }

  function changeQuery(query: string) {
    setStates((current) => ({
      ...current,
      [groupId]: {
        ...state,
        query,
        offset: 0,
        local: undefined,
        localError: undefined,
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
        local: undefined,
        localError: undefined,
        error: undefined,
        errorKey: undefined,
        errorStatus: undefined,
      },
    }));
  }

  async function submit(offset = state.offset) {
    const submittedQuery = state.query;
    if (
      !submittedQuery.trim() ||
      !Number.isInteger(pageLimit) ||
      pageLimit < 1 ||
      pageLimit > 100 ||
      pending ||
      disabled
    )
      return;
    const requestGroup = groupId;
    const run = (submitRuns.current[requestGroup] ?? 0) + 1;
    submitRuns.current[requestGroup] = run;
    localAborts.current[requestGroup]?.abort();
    const controller = new AbortController();
    localAborts.current[requestGroup] = controller;
    const request: SearchRequest = {
      query: submittedQuery,
      limit: pageLimit,
      offset,
    };
    setStates((current) => ({
      ...current,
      [requestGroup]: {
        ...(current[requestGroup] ?? state),
        offset,
        request,
        body: undefined,
        operationId: undefined,
        local: undefined,
        localError: undefined,
        admitting: true,
        error: undefined,
        errorKey: undefined,
        errorStatus: undefined,
      },
    }));

    let localPage: LocalPickerPage | undefined;
    let localError: LocalSearchError | undefined;
    if (localEnabled) {
      const timeout = window.setTimeout(() => controller.abort(), 15_000);
      try {
        const response = await listLocalProviderReleases(
          {
            provider: "local",
            q: submittedQuery,
            limit: pageLimit,
            offset,
          },
          { cache: "no-store", signal: controller.signal },
        );
        if (submitRuns.current[requestGroup] !== run) return;
        if (response.status !== 200) {
          localError = response.data.detail
            ? { detail: response.data.detail, messageKey: "localResultsFailed" }
            : { messageKey: "localNotAccepted", status: response.status };
        } else {
          localPage = parseLocalPage(response.data, offset, pageLimit);
        }
      } catch (reason) {
        if (submitRuns.current[requestGroup] !== run) return;
        localError = controller.signal.aborted
          ? { messageKey: "localResultsFailed" }
          : reason instanceof ManualPickerMessageError
            ? { messageKey: reason.messageKey }
            : {
                detail: reason instanceof Error ? reason.message : undefined,
                messageKey: "localResultsFailed",
              };
      } finally {
        window.clearTimeout(timeout);
      }
    }
    if (submitRuns.current[requestGroup] !== run) return;
    if (currentGroup.current !== requestGroup) {
      setStates((current) => ({
        ...current,
        [requestGroup]: {
          ...(current[requestGroup] ?? state),
          admitting: false,
          local: localPage,
          localError,
        },
      }));
      return;
    }

    const localCount = localPage?.items.length ?? 0;
    const mbOffset = localPage ? Math.max(0, offset - localPage.total) : offset;
    const mbLimit = localPage ? Math.max(1, pageLimit - localCount) : pageLimit;
    const previous = state.body;
    const requestId =
      previous &&
      previous.entity === "release" &&
      previous.query === submittedQuery &&
      previous.expected_group_revision === groupRevision &&
      previous.offset === mbOffset &&
      previous.limit === mbLimit
        ? previous.request_id
        : crypto.randomUUID();
    const body: ManualPickerSearchBody = {
      entity: "release",
      query: submittedQuery,
      expected_group_revision: groupRevision,
      offset: mbOffset,
      limit: mbLimit,
      request_id: requestId,
    };
    setStates((current) => {
      if (submitRuns.current[requestGroup] !== run) return current;
      return {
        ...current,
        [requestGroup]: {
          ...(current[requestGroup] ?? state),
          offset,
          body,
          request,
          admitting: true,
          local: localPage,
          localError,
          error: undefined,
          errorKey: undefined,
          errorStatus: undefined,
        },
      };
    });
    try {
      const response = await startIncomingGroupManualPickerSearch(
        requestGroup,
        body,
      );
      if (submitRuns.current[requestGroup] !== run) return;
      if (response.status !== 202) {
        setStates((current) => ({
          ...current,
          [requestGroup]: {
            ...current[requestGroup],
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
        [requestGroup]: {
          ...current[requestGroup],
          admitting: false,
          operationId: response.data.operation_id,
        },
      }));
    } catch (reason) {
      setStates((current) => ({
        ...current,
        [requestGroup]: {
          ...current[requestGroup],
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
        variant={localEnabled ? "editions" : "musicbrainz"}
        query={state.query}
        limit={state.limit}
        offset={state.offset}
        disabled={disabled}
        pending={pending}
        results={results}
        errors={errors}
        selectedItem={selectedItem}
        onQueryChange={changeQuery}
        onLimitChange={changeLimit}
        onSearch={() => void submit(0)}
        onSelectItem={(item) => {
          if (item.provider === "Local") {
            onSelectLocalRelease?.(item.id);
            return;
          }
          onSelectRelease(item.id);
        }}
        onPrevious={() => void submit(Math.max(0, state.offset - pageLimit))}
        onNext={() => void submit(state.offset + pageLimit)}
      />
    </>
  );
}
