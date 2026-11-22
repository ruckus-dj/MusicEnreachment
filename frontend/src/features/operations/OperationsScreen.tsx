import { useQueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useRef, useState } from "react";
import { apiResponseError, errorMessage } from "../../api/client/errors";
import {
  abandonMatchingRecheckPublications,
  dismissOperation,
  getListCurrentOperationsQueryKey,
  type listCurrentOperationsResponse,
  previewMatchingRecheckAbandonment,
  retryOperation,
  useListCurrentOperations,
} from "../../api/generated/client";
import type {
  ListCurrentOperationsParams,
  MatchingRecheckAbandonmentPosition,
  OperationResponse,
  OperationTargetRef,
} from "../../api/generated/client.schemas";
import { useI18n } from "../../i18n/I18nProvider";
import type { Locale } from "../../i18n/locale";
import { useHashLocation } from "../../navigation/hashLocation";
import { incomingGroupMatchingHref } from "../incoming/incomingGroupsRoute";
import { MatchingRecheckAbandonDialog } from "./MatchingRecheckAbandonDialog";
import type {
  OperationsListAction,
  OperationsListRow,
  OperationsListStatus,
} from "./OperationsList";
import { OperationsScreenView } from "./OperationsScreenView";
import {
  operationKindLabel,
  operationStageLabel,
  operationStateLabel,
  operationTargetLabel,
} from "./operationPresentation";
import { operationsScreenCopy } from "./operationsScreenCopy";

const PAGE_SIZE = 50;
const MAX_PAGE_OFFSET = 1_000_000;
const MAX_QUERY_LENGTH = 200;
const OPERATIONS_POLL_INTERVAL_MS = 8_000;
const operationsListQueryKey = getListCurrentOperationsQueryKey();

const statusFilters = [
  "attention",
  "all",
  "failed",
  "running",
  "queued",
] as const;
type OperationStatusFilter = (typeof statusFilters)[number];

const kindFilters = ["publication", "analysis", "removal", "matching"] as const;
type OperationKindFilter = (typeof kindFilters)[number];
type OperationKindSelection = OperationKindFilter | "all";

const sortColumns = ["target", "operation", "state", "created"] as const;
type OperationSort = (typeof sortColumns)[number];
const sortDirections = ["asc", "desc"] as const;
type OperationSortDirection = (typeof sortDirections)[number];

const DISMISSIBLE_TOOL_KINDS = new Set(["install", "move_tools_root"]);
const GENERIC_RETRY_KINDS = new Set([
  "install",
  "move_tools_root",
  "matching_evaluation_v1",
  "matching_apply_v1",
  "publish_track_v1",
  "provider_release_lookup",
  "provider_recording_lookup",
  "provider_release_search",
  "provider_recording_search",
  "provider_artist_search",
  "provider_artist_release_browse",
  "provider_acoustid_lookup",
  "scan_source",
]);

const messages = {
  en: {
    ...operationsScreenCopy.en,
    retryHttpError: (status: number) =>
      `Could not retry operation (HTTP ${status}).`,
    dismissHttpError: (status: number) =>
      `Could not dismiss operation (HTTP ${status}).`,
    abandonHttpError: (status: number) =>
      `Could not abandon unfinished republications (HTTP ${status}).`,
    loadHttpError: (status: number) =>
      `Could not load operations (HTTP ${status}).`,
    abandon: "Abandon…",
    abandoning: "Loading…",
    abandoned: "Unfinished republications abandoned.",
    abandonError: "Could not abandon unfinished republications.",
  },
  ru: {
    ...operationsScreenCopy.ru,
    retryHttpError: (status: number) =>
      `Не удалось повторить операцию (HTTP ${status}).`,
    dismissHttpError: (status: number) =>
      `Не удалось скрыть операцию (HTTP ${status}).`,
    abandonHttpError: (status: number) =>
      `Не удалось прервать незавершённые перепубликации (HTTP ${status}).`,
    loadHttpError: (status: number) =>
      `Не удалось загрузить операции (HTTP ${status}).`,
    abandon: "Прервать…",
    abandoning: "Загрузка…",
    abandoned: "Незавершённые перепубликации прерваны.",
    abandonError: "Не удалось прервать незавершённые перепубликации.",
  },
} as const;

type Copy = (typeof messages)[Locale];

interface OperationsRoute {
  status: OperationStatusFilter;
  kind: OperationKindSelection;
  sort: OperationSort;
  direction: OperationSortDirection;
  offset: number;
}

const defaultRoute: OperationsRoute = {
  status: "attention",
  kind: "all",
  sort: "created",
  direction: "desc",
  offset: 0,
};

function isStatusFilter(value: string): value is OperationStatusFilter {
  return statusFilters.some((candidate) => candidate === value);
}

function isKindFilter(value: string): value is OperationKindFilter {
  return kindFilters.some((candidate) => candidate === value);
}

function isSortColumn(value: string): value is OperationSort {
  return sortColumns.some((candidate) => candidate === value);
}

function isSortDirection(value: string): value is OperationSortDirection {
  return sortDirections.some((candidate) => candidate === value);
}

function parseOperationsRoute(hash: string): OperationsRoute {
  const path = hash.startsWith("#") ? hash.slice(1) : hash;
  const [pathname, search = ""] = path.split("?", 2);
  if (pathname !== "/operations") return defaultRoute;
  const params = new URLSearchParams(search);
  const requestedStatus = params.get("status") ?? "";
  const requestedKind = params.get("kind") ?? "";
  const requestedSort = params.get("sort") ?? "";
  const requestedDirection = params.get("direction") ?? "";
  const requestedOffset = Number(params.get("offset") ?? 0);
  return {
    status: isStatusFilter(requestedStatus)
      ? requestedStatus
      : defaultRoute.status,
    kind:
      requestedKind === "all" || isKindFilter(requestedKind)
        ? requestedKind
        : defaultRoute.kind,
    sort: isSortColumn(requestedSort) ? requestedSort : defaultRoute.sort,
    direction: isSortDirection(requestedDirection)
      ? requestedDirection
      : defaultRoute.direction,
    offset:
      Number.isSafeInteger(requestedOffset) && requestedOffset > 0
        ? Math.min(requestedOffset, MAX_PAGE_OFFSET)
        : 0,
  };
}

function operationsRouteHref(route: OperationsRoute) {
  const params = new URLSearchParams();
  if (route.status !== defaultRoute.status) params.set("status", route.status);
  if (route.kind !== defaultRoute.kind) params.set("kind", route.kind);
  if (route.sort !== defaultRoute.sort) params.set("sort", route.sort);
  if (route.direction !== defaultRoute.direction)
    params.set("direction", route.direction);
  if (route.offset > 0)
    params.set("offset", String(Math.min(route.offset, MAX_PAGE_OFFSET)));
  const search = params.toString();
  return search ? `#/operations?${search}` : "#/operations";
}

function pageShowsState(
  state: string,
  status: ListCurrentOperationsParams["status"],
) {
  switch (status) {
    case "all":
      return true;
    case "failed":
    case "running":
    case "queued":
      return state === status;
    default:
      return state === "failed" || state === "running";
  }
}

function reconcilePage(
  current: listCurrentOperationsResponse | undefined,
  id: string,
  next: OperationResponse | null,
  page: ListCurrentOperationsParams,
): listCurrentOperationsResponse | undefined {
  if (current?.status !== 200) return current;
  const operations = current.data.operations ?? [];
  const previous = operations.find((operation) => operation.id === id);
  if (!previous) return current;
  const staysInResult =
    next?.id === id && pageShowsState(next.state, page.status);
  if (
    staysInResult &&
    (page.sort !== "state" || previous.state === next.state)
  ) {
    return {
      ...current,
      data: {
        ...current.data,
        operations: operations.map((operation) =>
          operation.id === id
            ? {
                ...next,
                target_label: next.target_label ?? operation.target_label,
                target_ref: next.target_ref ?? operation.target_ref,
              }
            : operation,
        ),
      },
    };
  }
  return {
    ...current,
    data: {
      ...current.data,
      operations: operations.filter((operation) => operation.id !== id),
      // The row may still count, but only the server can place it on a page.
      total: staysInResult
        ? current.data.total
        : Math.max(0, current.data.total - 1),
    },
  };
}

type OperationAction = "retry" | "dismiss" | "abandon";

type ActionFailure =
  | {
      action: OperationAction;
      response: { status: number; data?: unknown };
    }
  | { action: OperationAction; reason: unknown };

function formatDate(value: string | undefined, locale: string) {
  if (!value) return "—";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString(locale);
}

/**
 * Measured progress only. Operations without an observable count stay hidden
 * rather than showing a fabricated or zero percentage.
 */
function measuredProgressLabel(
  operation: OperationResponse,
  locale: Locale,
): string | undefined {
  if (operation.kind === "move_tools_root") {
    const total = operation.bytes_total;
    if (operation.stage !== "copy" || total == null || total <= 0)
      return undefined;
    const completed = Math.max(0, Math.min(operation.bytes_completed, total));
    const percent = Math.floor((completed / total) * 100);
    return new Intl.NumberFormat(locale, {
      style: "percent",
      maximumFractionDigits: 0,
    }).format(percent / 100);
  }
  if (operation.kind === "install") {
    if (operation.bytes_completed <= 0) return undefined;
    const completed = operation.bytes_completed.toLocaleString(locale);
    const total = operation.bytes_total;
    if (total == null || total <= 0) {
      return locale === "ru" ? `${completed} байт` : `${completed} bytes`;
    }
    return locale === "ru"
      ? `${completed} / ${total.toLocaleString(locale)} байт`
      : `${completed} / ${total.toLocaleString(locale)} bytes`;
  }
  return undefined;
}

function targetHref(ref: OperationTargetRef | undefined) {
  if (!ref) return undefined;
  const {
    group_id: groupId,
    release_id: releaseId,
    position_id: positionId,
    artist_id: artistId,
    root_id: rootId,
    location_id: locationId,
  } = ref;
  if (ref.kind === "group" && groupId)
    return incomingGroupMatchingHref(groupId);
  if (ref.kind === "position" && releaseId && positionId)
    return `#/library/releases/${encodeURIComponent(releaseId)}/positions/${encodeURIComponent(positionId)}`;
  if (ref.kind === "release" && releaseId)
    return `#/library/releases/${encodeURIComponent(releaseId)}`;
  if (ref.kind === "artist" && artistId)
    return `#/library/artists/${encodeURIComponent(artistId)}`;
  if (ref.kind === "source_location" && rootId && locationId)
    return `#/sources/${encodeURIComponent(rootId)}/locations/${encodeURIComponent(locationId)}`;
  if (ref.kind === "source_root" && rootId)
    return `#/sources/${encodeURIComponent(rootId)}`;
  return undefined;
}

function actionErrorText(failure: ActionFailure | null, copy: Copy) {
  if (!failure) return undefined;
  if ("response" in failure) {
    const { status } = failure.response;
    return apiResponseError(
      failure.response,
      failure.action === "retry"
        ? copy.retryHttpError(status)
        : failure.action === "dismiss"
          ? copy.dismissHttpError(status)
          : copy.abandonHttpError(status),
    ).message;
  }
  return errorMessage(
    failure.reason,
    failure.action === "retry"
      ? copy.retryError
      : failure.action === "dismiss"
        ? copy.dismissError
        : copy.abandonError,
  );
}

interface PendingAction {
  id: string;
  action: OperationAction;
}

function operationRow(
  operation: OperationResponse,
  locale: Locale,
  pending: PendingAction | undefined,
  perform: (id: string, action: OperationAction, trigger: Element) => void,
  copy: Copy,
  actionsAvailable: boolean,
): OperationsListRow {
  const targetLabel =
    operation.target_label || operationTargetLabel(operation, locale);
  const stage = operationStageLabel(operation.stage, locale);
  const failed = operation.state === "failed";
  const actions: OperationsListAction[] = [];

  if (actionsAvailable && failed && GENERIC_RETRY_KINDS.has(operation.kind)) {
    const isPending =
      pending?.id === operation.id && pending.action === "retry";
    actions.push({
      id: "retry",
      label: isPending ? copy.retrying : copy.retry,
      onPress: (trigger) => perform(operation.id, "retry", trigger),
      isPending,
      isDisabled: Boolean(pending) && !isPending,
    });
  }
  if (actionsAvailable && failed && operation.matching_recheck_member) {
    const isPending =
      pending?.id === operation.id && pending.action === "abandon";
    actions.push({
      id: "abandon",
      label: isPending ? copy.abandoning : copy.abandon,
      onPress: (trigger) => perform(operation.id, "abandon", trigger),
      isPending,
      isDisabled: Boolean(pending) && !isPending,
    });
  }
  if (
    actionsAvailable &&
    failed &&
    DISMISSIBLE_TOOL_KINDS.has(operation.kind)
  ) {
    const isPending =
      pending?.id === operation.id && pending.action === "dismiss";
    actions.push({
      id: "dismiss",
      label: isPending ? copy.dismissing : copy.dismiss,
      onPress: (trigger) => perform(operation.id, "dismiss", trigger),
      isPending,
      isDisabled: Boolean(pending) && !isPending,
    });
  }

  return {
    id: operation.id,
    target: targetLabel,
    targetHref: targetHref(operation.target_ref),
    operation: operationKindLabel(operation.kind, locale),
    state: operationStateLabel(operation.state, locale),
    stateTone: failed
      ? "error"
      : operation.state === "running" || operation.state === "queued"
        ? "info"
        : "neutral",
    stage: stage || undefined,
    progress: measuredProgressLabel(operation, locale),
    created: formatDate(operation.created_at, locale),
    error: failed ? operation.safe_error || undefined : undefined,
    actions: actions.length > 0 ? actions : undefined,
  };
}

export function OperationsScreen() {
  const { locale } = useI18n();
  const copy = messages[locale];
  const queryClient = useQueryClient();
  const hash = useHashLocation();
  const route = parseOperationsRoute(hash);
  const heading = useRef<HTMLHeadingElement>(null);
  const pendingGuard = useRef(false);
  const manualRefreshGuard = useRef(false);
  const lastGood = useRef<
    | {
        key: string;
        operations: OperationResponse[];
        total: number;
      }
    | undefined
  >(undefined);
  const [searchText, setSearchText] = useState("");
  const [pending, setPending] = useState<PendingAction>();
  const [notice, setNotice] = useState<"dismissed" | "abandoned">();
  const [actionError, setActionError] = useState<ActionFailure | null>(null);
  const [abandonPreview, setAbandonPreview] = useState<{
    id: string;
    revision: string;
    positions: MatchingRecheckAbandonmentPosition[];
  }>();
  const [abandonBusy, setAbandonBusy] = useState(false);
  const [abandonError, setAbandonError] = useState<string>();
  const [manualRefreshing, setManualRefreshing] = useState(false);

  const updateRoute = useCallback(
    (next: Partial<OperationsRoute>) => {
      window.location.hash = operationsRouteHref({
        status: route.status,
        kind: route.kind,
        sort: route.sort,
        direction: route.direction,
        offset: route.offset,
        ...next,
      });
    },
    [route.direction, route.kind, route.offset, route.sort, route.status],
  );

  const search = searchText.trim().slice(0, MAX_QUERY_LENGTH);
  const localeSorting = route.sort === "target" || route.sort === "operation";
  const params = {
    status: route.status,
    kind: route.kind === "all" ? undefined : route.kind,
    q: search || undefined,
    sort: route.sort,
    direction: route.direction,
    ...(localeSorting ? { locale } : {}),
    offset: route.offset,
  };
  const paramsKey = JSON.stringify(params);

  const operationsQuery = useListCurrentOperations(params, {
    fetch: { cache: "no-store" },
    query: {
      // Foreground-only: background tabs must not keep polling. The list stays
      // bounded, so an empty or failed filter keeps polling too.
      refetchInterval:
        pending || abandonPreview ? false : OPERATIONS_POLL_INTERVAL_MS,
      refetchIntervalInBackground: false,
    },
  });

  const currentData =
    operationsQuery.data?.status === 200
      ? operationsQuery.data.data
      : undefined;

  useEffect(() => {
    if (!currentData) return;
    lastGood.current = {
      key: paramsKey,
      operations: currentData.operations ?? [],
      total: currentData.total,
    };
  }, [currentData, paramsKey]);

  const cached =
    lastGood.current?.key === paramsKey ? lastGood.current : undefined;
  const body = currentData ?? cached;
  const operations = body?.operations ?? [];
  const total = body?.total ?? 0;

  const responseError =
    operationsQuery.data && operationsQuery.data.status !== 200
      ? operationsQuery.data
      : undefined;
  const loadError = operationsQuery.isError
    ? errorMessage(operationsQuery.error, copy.loadError)
    : responseError
      ? apiResponseError(
          responseError,
          copy.loadHttpError(responseError.status),
        ).message
      : undefined;
  const listStatus: OperationsListStatus = loadError
    ? "error"
    : operationsQuery.isPending && operations.length === 0
      ? "loading"
      : "ready";

  const lastOffset =
    currentData && currentData.total > 0
      ? Math.floor((currentData.total - 1) / PAGE_SIZE) * PAGE_SIZE
      : 0;
  useEffect(() => {
    if (!currentData || loadError || pending || operationsQuery.isFetching)
      return;
    if (route.offset > lastOffset) updateRoute({ offset: lastOffset });
  }, [
    currentData,
    lastOffset,
    loadError,
    operationsQuery.isFetching,
    pending,
    route.offset,
    updateRoute,
  ]);

  async function reconcileCachedPages(
    id: string,
    next: OperationResponse | null,
  ) {
    await queryClient.cancelQueries({ queryKey: operationsListQueryKey });
    for (const query of queryClient.getQueryCache().findAll({
      queryKey: operationsListQueryKey,
    })) {
      const page = (query.queryKey[1] ?? {}) as ListCurrentOperationsParams;
      const pageKey = JSON.stringify(page);
      const updated = queryClient.setQueryData<listCurrentOperationsResponse>(
        query.queryKey,
        (current) => reconcilePage(current, id, next, page),
      );
      if (updated?.status === 200 && lastGood.current?.key === pageKey) {
        lastGood.current = {
          key: pageKey,
          operations: updated.data.operations ?? [],
          total: updated.data.total,
        };
      }
    }
  }

  function restoreHeadingFocus(trigger: Element) {
    const owner = trigger.closest("button") ?? trigger;
    const active = document.activeElement;
    if (owner.isConnected && active !== owner && !owner.contains(active))
      return;
    heading.current?.focus({ preventScroll: true });
  }

  async function perform(
    id: string,
    action: "retry" | "dismiss",
    trigger: Element,
  ) {
    if (pendingGuard.current) return;
    pendingGuard.current = true;
    setPending({ id, action });
    setActionError(null);
    setNotice(undefined);
    try {
      if (action === "retry") {
        const response = await retryOperation(id);
        if (response.status !== 200) {
          setActionError({ action, response });
          return;
        }
        await reconcileCachedPages(id, response.data);
      } else {
        const response = await dismissOperation(id);
        if (response.status !== 204) {
          setActionError({ action, response });
          return;
        }
        await reconcileCachedPages(id, null);
        setNotice("dismissed");
        restoreHeadingFocus(trigger);
      }
      await queryClient.invalidateQueries({ queryKey: operationsListQueryKey });
    } catch (reason) {
      setActionError({ action, reason });
    } finally {
      pendingGuard.current = false;
      setPending(undefined);
    }
  }

  async function openAbandon(id: string) {
    if (pendingGuard.current) return;
    pendingGuard.current = true;
    setPending({ id, action: "abandon" });
    setActionError(null);
    setNotice(undefined);
    try {
      const response = await previewMatchingRecheckAbandonment(id, {
        cache: "no-store",
      });
      if (response.status !== 200) {
        setActionError({ action: "abandon", response });
        return;
      }
      setAbandonError(undefined);
      setAbandonPreview({
        id,
        revision: response.data.revision,
        positions: response.data.positions ?? [],
      });
    } catch (reason) {
      setActionError({ action: "abandon", reason });
    } finally {
      pendingGuard.current = false;
      setPending(undefined);
    }
  }

  async function confirmAbandon() {
    if (!abandonPreview || abandonBusy) return;
    setAbandonBusy(true);
    setAbandonError(undefined);
    try {
      const response = await abandonMatchingRecheckPublications(
        abandonPreview.id,
        { revision: abandonPreview.revision },
      );
      if (response.status !== 204) {
        if (response.status === 409) {
          setAbandonPreview(undefined);
          setActionError({ action: "abandon", response });
          await queryClient.invalidateQueries({
            queryKey: operationsListQueryKey,
          });
          return;
        }
        setAbandonError(
          apiResponseError(response, copy.abandonHttpError(response.status))
            .message,
        );
        return;
      }
      const { id } = abandonPreview;
      setAbandonPreview(undefined);
      await reconcileCachedPages(id, null);
      setNotice("abandoned");
      heading.current?.focus({ preventScroll: true });
      await queryClient.invalidateQueries({ queryKey: operationsListQueryKey });
    } catch (reason) {
      setAbandonError(errorMessage(reason, copy.abandonError));
    } finally {
      setAbandonBusy(false);
    }
  }

  const rows = operations.map((operation) =>
    operationRow(
      operation,
      locale,
      pending,
      (id, action, trigger) => {
        if (action === "abandon") {
          void openAbandon(id);
          return;
        }
        void perform(id, action, trigger);
      },
      copy,
      !loadError,
    ),
  );
  const pagination =
    rows.length > 0 && total > 0
      ? {
          from: Math.min(route.offset + 1, total),
          to: Math.min(route.offset + PAGE_SIZE, total),
          total,
          onPrevious:
            route.offset > 0
              ? () =>
                  updateRoute({
                    offset: Math.max(0, route.offset - PAGE_SIZE),
                  })
              : undefined,
          onNext:
            route.offset + PAGE_SIZE < total
              ? () => updateRoute({ offset: route.offset + PAGE_SIZE })
              : undefined,
        }
      : undefined;

  async function refresh() {
    if (manualRefreshGuard.current) return;
    manualRefreshGuard.current = true;
    setManualRefreshing(true);
    try {
      await operationsQuery.refetch({ cancelRefetch: false });
    } finally {
      manualRefreshGuard.current = false;
      setManualRefreshing(false);
    }
  }

  return (
    <>
      <OperationsScreenView
        copy={copy}
        locale={locale}
        rows={rows}
        status={listStatus}
        error={loadError}
        notice={notice ? copy[notice] : undefined}
        actionError={actionErrorText(actionError, copy)}
        headingRef={heading}
        sortColumn={route.sort}
        sortDirection={route.direction}
        onSortChange={(sort, direction) =>
          updateRoute({ sort, direction, offset: 0 })
        }
        onRefresh={() => {
          void refresh();
        }}
        isRefreshing={manualRefreshing}
        filters={{
          query: searchText,
          onQueryChange: (value) => {
            setSearchText(value);
            if (route.offset !== 0) updateRoute({ offset: 0 });
          },
          status: route.status,
          onStatusChange: (value) => {
            updateRoute({ status: value, offset: 0 });
          },
          kind: route.kind,
          onKindChange: (value) => {
            updateRoute({ kind: value, offset: 0 });
          },
        }}
        pagination={pagination}
      />
      {abandonPreview && (
        <MatchingRecheckAbandonDialog
          locale={locale}
          positions={abandonPreview.positions}
          busy={abandonBusy}
          error={abandonError}
          onConfirm={() => {
            void confirmAbandon();
          }}
          onCancel={() => {
            if (abandonBusy) return;
            setAbandonPreview(undefined);
            setAbandonError(undefined);
          }}
        />
      )}
    </>
  );
}
