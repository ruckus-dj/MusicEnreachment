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
  OperationResponse,
  OperationTargetRef,
} from "../../api/generated/client.schemas";
import { useI18n } from "../../app/i18n/host";
import { useHashLocation } from "../../app/navigation/hashLocation";
import {
  type OperationsLocation,
  operationsLocationHref,
  parseOperationsLocation,
} from "../../app/navigation/operationsLocation";
import {
  ApplicationOperationsBinder,
  type ApplicationOperationsModel,
  type ApplicationRouteProviderProps,
} from "../../ui/app/ApplicationRouteOutlet";
import {
  libraryPositionHref,
  libraryReleaseHref,
} from "../../ui/features/library/libraryDetailRoute";
import type { MatchingRecheckAbandonPosition } from "../../ui/features/operations/MatchingRecheckAbandonDialog";
import type {
  OperationsListAction,
  OperationsListRow,
  OperationsListStatus,
} from "../../ui/features/operations/OperationsList";
import { operationsControllerCopy as messages } from "../../ui/features/operations/operationsScreenCopy";
import { useOperationsHeadingFocus } from "../../ui/features/operations/useOperationsHeadingFocus";
import type { Locale } from "../../ui/i18n/locale";
import { incomingGroupMatchingHref } from "../incoming/incomingGroupsRoute";
import { libraryArtistRouteHref } from "../library/libraryRoute";
import {
  operationKindLabel,
  operationStageLabel,
  operationStateLabel,
  operationTargetLabel,
} from "./operationPresentation";

const PAGE_SIZE = 50;
const MAX_QUERY_LENGTH = 200;
const OPERATIONS_POLL_INTERVAL_MS = 8_000;
const operationsListQueryKey = getListCurrentOperationsQueryKey();

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

type Copy = (typeof messages)[Locale];

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
    return libraryPositionHref(releaseId, positionId);
  if (ref.kind === "release" && releaseId) return libraryReleaseHref(releaseId);
  if (ref.kind === "artist" && artistId)
    return libraryArtistRouteHref(artistId);
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

export function ProductionOperationsBinder({
  children,
}: ApplicationRouteProviderProps) {
  const model = useOperationsModel();
  return (
    <ApplicationOperationsBinder model={model}>
      {children}
    </ApplicationOperationsBinder>
  );
}

function useOperationsModel(): ApplicationOperationsModel {
  const { locale } = useI18n();
  const copy = messages[locale];
  const queryClient = useQueryClient();
  const hash = useHashLocation();
  const route = parseOperationsLocation(hash);
  const { headingRef, focusHeading, restoreHeadingFocus } =
    useOperationsHeadingFocus();
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
    positions: MatchingRecheckAbandonPosition[];
  }>();
  const [abandonBusy, setAbandonBusy] = useState(false);
  const [abandonError, setAbandonError] = useState<string>();
  const [manualRefreshing, setManualRefreshing] = useState(false);

  const updateRoute = useCallback(
    (next: Partial<OperationsLocation>, replace = false) => {
      const href = operationsLocationHref({
        status: route.status,
        kind: route.kind,
        sort: route.sort,
        direction: route.direction,
        offset: route.offset,
        ...next,
      });
      if (replace) window.location.replace(href);
      else window.location.hash = href;
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
    if (route.offset > lastOffset) updateRoute({ offset: lastOffset }, true);
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
        positions: (response.data.positions ?? []).map((position) => ({
          operationId: position.operation_id,
          relativePath: position.relative_path,
          committed: position.committed,
        })),
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
      focusHeading();
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

  return {
    locale,
    rows,
    status: listStatus,
    error: loadError,
    notice: notice ? copy[notice] : undefined,
    actionError: actionErrorText(actionError, copy),
    headingRef,
    sortColumn: route.sort,
    sortDirection: route.direction,
    onSortChange: (sort, direction) =>
      updateRoute({ sort, direction, offset: 0 }),
    onRefresh: () => {
      void refresh();
    },
    isRefreshing: manualRefreshing,
    filters: {
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
    },
    pagination,
    matchingRecheckAbandon: abandonPreview
      ? {
          positions: abandonPreview.positions,
          busy: abandonBusy,
          error: abandonError,
          onConfirm: () => {
            void confirmAbandon();
          },
          onCancel: () => {
            if (abandonBusy) return;
            setAbandonPreview(undefined);
            setAbandonError(undefined);
          },
        }
      : undefined,
  };
}
