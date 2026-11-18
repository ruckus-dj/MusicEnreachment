import { useQueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { apiResponseError, errorMessage } from "../../api/client/errors";
import {
  dismissOperation,
  getListCurrentOperationsQueryKey,
  type listCurrentOperationsResponse,
  retryOperation,
  useListCurrentOperations,
} from "../../api/generated/client";
import type {
  ListCurrentOperationsParams,
  OperationResponse,
  OperationTargetRef,
} from "../../api/generated/client.schemas";
import { useI18n } from "../../i18n/I18nProvider";
import type { Locale } from "../../i18n/locale";
import { useHashLocation } from "../../navigation/hashLocation";
import { incomingGroupMatchingHref } from "../incoming/incomingGroupsRoute";
import {
  OperationsList,
  type OperationsListAction,
  type OperationsListRow,
  type OperationsListStatus,
} from "./OperationsList";
import {
  operationKindLabel,
  operationProgressLabel,
  operationStageLabel,
  operationStateLabel,
  operationTargetLabel,
} from "./operationPresentation";

const PAGE_SIZE = 50;
const MAX_PAGE_OFFSET = 1_000_000;
const MAX_QUERY_LENGTH = 200;
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
    title: "Current operations",
    description:
      "Work in progress and errors that need attention. History is not retained.",
    refresh: "Refresh",
    refreshing: "Refreshing…",
    loading: "Loading operations…",
    loadError: "Could not load operations.",
    retryLoad: "Retry loading",
    emptyTitle: "No current operations",
    emptyDescription:
      "Work in progress and failed operations will appear here.",
    target: "Target",
    operation: "Operation",
    stateStage: "Status / stage",
    progress: "Progress",
    created: "Created",
    error: "Error",
    actions: "Actions",
    footnote:
      "Queued, running, and failed operations are shown. Completed operations are hidden.",
    search: "Search operations",
    statusFilter: "Status",
    kindFilter: "Kind",
    details: "Details",
    close: "Close",
    previous: "Previous",
    next: "Next",
    allStatuses: "All",
    attention: "Needs attention",
    failed: "Failed",
    running: "Running",
    queued: "Queued",
    allKinds: "All kinds",
    kindPublication: "Publication",
    kindAnalysis: "Analysis",
    kindRemoval: "Removal",
    kindMatching: "Matching",
    paginationRange: "{from}–{to} of {total}",
    retrying: "Retrying…",
    retry: "Retry",
    dismissing: "Dismissing…",
    dismiss: "Dismiss",
    dismissed: "Operation dismissed.",
    retryError: "Could not retry operation.",
    retryHttpError: (status: number) =>
      `Could not retry operation (HTTP ${status}).`,
    dismissError: "Could not dismiss operation.",
    dismissHttpError: (status: number) =>
      `Could not dismiss operation (HTTP ${status}).`,
    loadHttpError: (status: number) =>
      `Could not load operations (HTTP ${status}).`,
  },
  ru: {
    title: "Текущие операции",
    description:
      "Работа в процессе и ошибки, требующие внимания. История не сохраняется.",
    refresh: "Обновить",
    refreshing: "Обновление…",
    loading: "Загрузка операций…",
    loadError: "Не удалось загрузить операции.",
    retryLoad: "Повторить загрузку",
    emptyTitle: "Нет текущих операций",
    emptyDescription:
      "Здесь появятся операции в процессе и неудачные операции.",
    target: "Объект",
    operation: "Операция",
    stateStage: "Состояние / этап",
    progress: "Ход выполнения",
    created: "Создано",
    error: "Ошибка",
    actions: "Действия",
    footnote:
      "Показаны операции в очереди, выполняемые и неудачные. Завершённые операции скрыты.",
    search: "Поиск операций",
    statusFilter: "Статус",
    kindFilter: "Тип",
    details: "Подробности",
    close: "Закрыть",
    previous: "Назад",
    next: "Вперёд",
    allStatuses: "Все",
    attention: "Требует внимания",
    failed: "Ошибка",
    running: "Выполняется",
    queued: "В очереди",
    allKinds: "Все типы",
    kindPublication: "Публикация",
    kindAnalysis: "Анализ",
    kindRemoval: "Удаление",
    kindMatching: "Сопоставление",
    paginationRange: "{from}–{to} из {total}",
    retrying: "Повтор…",
    retry: "Повторить",
    dismissing: "Скрытие…",
    dismiss: "Скрыть",
    dismissed: "Операция скрыта.",
    retryError: "Не удалось повторить операцию.",
    retryHttpError: (status: number) =>
      `Не удалось повторить операцию (HTTP ${status}).`,
    dismissError: "Не удалось скрыть операцию.",
    dismissHttpError: (status: number) =>
      `Не удалось скрыть операцию (HTTP ${status}).`,
    loadHttpError: (status: number) =>
      `Не удалось загрузить операции (HTTP ${status}).`,
  },
} as const;

type Copy = (typeof messages)[Locale];

interface OperationsRoute {
  status: OperationStatusFilter;
  kind: OperationKindSelection;
  offset: number;
}

const defaultRoute: OperationsRoute = {
  status: "attention",
  kind: "all",
  offset: 0,
};

function isStatusFilter(value: string): value is OperationStatusFilter {
  return statusFilters.some((candidate) => candidate === value);
}

function isKindFilter(value: string): value is OperationKindFilter {
  return kindFilters.some((candidate) => candidate === value);
}

function parseOperationsRoute(hash: string): OperationsRoute {
  const path = hash.startsWith("#") ? hash.slice(1) : hash;
  const [pathname, search = ""] = path.split("?", 2);
  if (pathname !== "/operations") return defaultRoute;
  const params = new URLSearchParams(search);
  const requestedStatus = params.get("status") ?? "";
  const requestedKind = params.get("kind") ?? "";
  const requestedOffset = Number(params.get("offset") ?? 0);
  return {
    status: isStatusFilter(requestedStatus)
      ? requestedStatus
      : defaultRoute.status,
    kind:
      requestedKind === "all" || isKindFilter(requestedKind)
        ? requestedKind
        : defaultRoute.kind,
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
  status: ListCurrentOperationsParams["status"],
): listCurrentOperationsResponse | undefined {
  if (current?.status !== 200) return current;
  const operations = current.data.operations ?? [];
  if (!operations.some((operation) => operation.id === id)) return current;
  if (next?.id === id && pageShowsState(next.state, status)) {
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
      total: Math.max(0, current.data.total - 1),
    },
  };
}

type ActionFailure =
  | {
      action: "retry" | "dismiss";
      response: { status: number; data?: unknown };
    }
  | { action: "retry" | "dismiss"; reason: unknown };

function formatDate(value: string | undefined, locale: string) {
  if (!value) return "—";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString(locale);
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
    return apiResponseError(
      failure.response,
      failure.action === "retry"
        ? copy.retryHttpError(failure.response.status)
        : copy.dismissHttpError(failure.response.status),
    ).message;
  }
  return errorMessage(
    failure.reason,
    failure.action === "retry" ? copy.retryError : copy.dismissError,
  );
}

interface PendingAction {
  id: string;
  action: "retry" | "dismiss";
}

function operationRow(
  operation: OperationResponse,
  locale: Locale,
  pending: PendingAction | undefined,
  perform: (id: string, action: "retry" | "dismiss", trigger: Element) => void,
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
    progress: operationProgressLabel(operation, locale),
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
  const [notice, setNotice] = useState<"dismissed">();
  const [actionError, setActionError] = useState<ActionFailure | null>(null);

  const updateRoute = useCallback(
    (next: Partial<OperationsRoute>) => {
      window.location.hash = operationsRouteHref({
        status: route.status,
        kind: route.kind,
        offset: route.offset,
        ...next,
      });
    },
    [route.kind, route.offset, route.status],
  );

  const search = searchText.trim().slice(0, MAX_QUERY_LENGTH);
  const params = {
    status: route.status,
    kind: route.kind === "all" ? undefined : route.kind,
    q: search || undefined,
    offset: route.offset,
  };
  const paramsKey = JSON.stringify(params);

  const operationsQuery = useListCurrentOperations(params);

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
        (current) => reconcilePage(current, id, next, page.status),
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

  const statusOptions = statusFilters.map((value) => ({
    value,
    label:
      value === "attention"
        ? copy.attention
        : value === "all"
          ? copy.allStatuses
          : copy[value],
  }));
  const kindOptions = [
    { value: "all", label: copy.allKinds },
    ...kindFilters.map((value) => ({
      value,
      label:
        value === "publication"
          ? copy.kindPublication
          : value === "analysis"
            ? copy.kindAnalysis
            : value === "removal"
              ? copy.kindRemoval
              : copy.kindMatching,
    })),
  ];
  const numberFormat = useMemo(() => new Intl.NumberFormat(locale), [locale]);
  const rows = operations.map((operation) =>
    operationRow(
      operation,
      locale,
      pending,
      (id, action, trigger) => {
        void perform(id, action, trigger);
      },
      copy,
      !loadError,
    ),
  );
  const pagination =
    rows.length > 0 && total > 0
      ? {
          label: copy.paginationRange
            .replace(
              "{from}",
              numberFormat.format(Math.min(route.offset + 1, total)),
            )
            .replace(
              "{to}",
              numberFormat.format(Math.min(route.offset + PAGE_SIZE, total)),
            )
            .replace("{total}", numberFormat.format(total)),
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

  return (
    <OperationsList
      copy={copy}
      rows={rows}
      status={listStatus}
      error={loadError}
      notice={notice ? copy[notice] : undefined}
      actionError={actionErrorText(actionError, copy)}
      headingRef={heading}
      onRefresh={() => {
        void operationsQuery.refetch();
      }}
      isRefreshing={operationsQuery.isFetching}
      filters={{
        query: searchText,
        onQueryChange: (value) => {
          setSearchText(value);
          if (route.offset !== 0) updateRoute({ offset: 0 });
        },
        status: route.status,
        statusOptions,
        onStatusChange: (value) => {
          if (!isStatusFilter(value)) return;
          updateRoute({ status: value, offset: 0 });
        },
        kind: route.kind,
        kindOptions,
        onKindChange: (value) => {
          if (value !== "all" && !isKindFilter(value)) return;
          updateRoute({ kind: value, offset: 0 });
        },
      }}
      pagination={pagination}
    />
  );
}
