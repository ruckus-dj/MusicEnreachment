import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useRef, useState } from "react";
import { useOperationFollower } from "../../api/client/useOperationFollower";
import {
  type AggregateDisposition,
  type AggregateInspectorPresentation,
  type AggregatePositionDiagnostic,
  type AggregatePositionStatus,
  type AggregatePositionView,
  aggregateDispositions,
} from "../../ui/features/library/AggregatePublicationPreviewView";
import { firstPublicationPreviewMessages } from "../../ui/features/library/firstPublicationPreviewMessages";
import type { PublicationPreviewDetailsState } from "../../ui/features/library/PublicationPreviewDetailsView";
import {
  MatchingReceiptPublicationFrame,
  matchingReceiptFileChoiceLabels,
} from "../../ui/features/matching/MatchingReceiptPublicationFrame";
import {
  type MatchingReceiptPublicationIssue,
  MatchingReceiptPublicationOutcomeView,
} from "../../ui/features/matching/MatchingReceiptPublicationOutcomeView";
import {
  FirstPublicationPreview,
  type PublicationMatchingReceipt,
  type PublicationPreviewController,
  type PublicationPreviewIdentity,
  type PublicationPreviewState,
  type SubmitMatchingReceiptPublication,
} from "../library/FirstPublicationPreview";
import { operationStateLabel } from "../operations/operationPresentation";
import {
  type ReceiptPublicationAdmission,
  receiptAdmissionKey,
  receiptPublicationAdmission,
  recordReceiptPublicationReadFailure,
  recordReceiptPublicationSnapshot,
  refreshReceiptPublication,
  submitReceiptPublication,
  useReceiptPublicationAdmissions,
} from "./receiptPublicationAdmissions";

/** One exact linked file of a saved matching position. */
export type IncomingReceiptPublicationSource = {
  /** Track-source identity; the same value as the confirmation receipt. */
  id: string;
  label: string;
  receipt: PublicationMatchingReceipt;
};

/** One saved matching position with the exact files linked to it. */
export type IncomingReceiptPublicationPosition = {
  id: string;
  label: string;
  sources: readonly IncomingReceiptPublicationSource[];
};

export type IncomingReceiptPublicationProps = {
  locale: "en" | "ru";
  releaseTitle: string;
  releaseId: string;
  positions: readonly IncomingReceiptPublicationPosition[];
  onBack: () => void;
};

/**
 * An unresolved multi-file position shows the aggregate inspector before its
 * receipt-scoped preview exists. The portable details view owns the empty
 * state; the row and the file choice describe what is missing.
 */
const emptyPreviewDetails: PublicationPreviewDetailsState = {
  error: "",
  loading: false,
  admittedOperationID: "",
  admissionUncertain: false,
  inspection: {
    workflow: "matching_receipt",
    sourceCandidates: [],
    selectedChoice: null,
    needsExplicitChoice: false,
    sourceChoiceDisabled: false,
  },
};

/**
 * A sole linked file is selected implicitly. A position with several files
 * stays unresolved until the operator chooses one, so the server never falls
 * back to a saved assignment or a quality-derived pick.
 */
function selectedSource(
  position: IncomingReceiptPublicationPosition,
  chosen: Readonly<Record<string, string | null>>,
) {
  const requested = chosen[position.id];
  if (requested !== undefined) {
    if (requested === null) return undefined;
    const found = position.sources.find((source) => source.id === requested);
    if (found) return found;
  }
  return position.sources.length === 1 ? position.sources[0] : undefined;
}

function matchingReceiptKey(receipt: PublicationMatchingReceipt | undefined) {
  return receipt
    ? `${receipt.kind}:${receipt.requestId}:${receipt.trackSourceId}:${receipt.mediaVariantId}`
    : "";
}

function sameReceipt(
  left: PublicationMatchingReceipt | undefined,
  right: PublicationMatchingReceipt | undefined,
) {
  return (
    matchingReceiptKey(left) !== "" &&
    matchingReceiptKey(left) === matchingReceiptKey(right)
  );
}

/**
 * A state can belong to the previous source choice while the next prepare is
 * in flight. Only a receipt recorded by the controller for the selected file
 * may contribute rows or a confirmation identity.
 */
function stateIsReceipt(
  state: PublicationPreviewState | undefined,
  receipt: PublicationMatchingReceipt | undefined,
) {
  return Boolean(state && receipt && sameReceipt(state.receipt, receipt));
}

function stateReady(state: PublicationPreviewState) {
  return (
    state.canConfirm &&
    Boolean(state.identity) &&
    !state.loading &&
    !state.confirming &&
    !state.admittedOperationID &&
    !state.admissionUncertain &&
    !state.inspection.sourceChoiceDisabled
  );
}

type ReceiptAdmission = {
  controller: PublicationPreviewController;
  identity: PublicationPreviewIdentity;
  position: IncomingReceiptPublicationPosition;
  receipt: PublicationMatchingReceipt;
};

/**
 * Capture only included, receipt-current CREATE/UPDATE positions. Unchanged
 * positions are never confirmed, and a stale state whose receipt differs from
 * the chosen file is never sent.
 */
function readyAdmissions(
  positions: readonly IncomingReceiptPublicationPosition[],
  included: ReadonlySet<string>,
  controllers: ReadonlyMap<string, PublicationPreviewController>,
  states: Readonly<Record<string, PublicationPreviewState>>,
  chosen: Readonly<Record<string, string | null>>,
): ReceiptAdmission[] {
  const admissions: ReceiptAdmission[] = [];
  for (const position of positions) {
    if (!included.has(position.id)) continue;
    const controller = controllers.get(position.id);
    const source = selectedSource(position, chosen);
    const state = states[position.id];
    if (!controller || !source || !state) continue;
    if (!stateIsReceipt(state, source.receipt) || !stateReady(state)) continue;
    if (state.receiptAction !== "create" && state.receiptAction !== "update")
      continue;
    const identity = state.identity;
    if (
      !identity ||
      identity.workflow !== "matching_receipt" ||
      identity.mode !== "ordinary" ||
      !sameReceipt(identity.receipt, source.receipt)
    )
      continue;
    admissions.push({
      controller,
      identity,
      position,
      receipt: source.receipt,
    });
  }
  return admissions;
}

function rowDiagnostic(
  included: boolean,
  controller: PublicationPreviewController | undefined,
  source: IncomingReceiptPublicationSource | undefined,
  state: PublicationPreviewState | undefined,
  current: boolean,
  noSource: string,
): AggregatePositionDiagnostic | undefined {
  if (!included) return undefined;
  if (!controller) return { kind: "no-controller" };
  if (!source) return { kind: "text", text: noSource };
  if (!current) return undefined;
  const text = state?.error || state?.blockers[0];
  return text ? { kind: "text", text } : undefined;
}

function rowStatus(
  included: boolean,
  controller: PublicationPreviewController | undefined,
  source: IncomingReceiptPublicationSource | undefined,
  state: PublicationPreviewState | undefined,
  current: boolean,
): { status: AggregatePositionStatus; counted: AggregateDisposition } {
  if (!included)
    return {
      status: { kind: "disposition", disposition: "skipped" },
      counted: "skipped",
    };
  if (!controller || !source)
    return {
      status: { kind: "disposition", disposition: "blocked" },
      counted: "blocked",
    };
  if (current && state?.loading)
    return { status: { kind: "loading" }, counted: "blocked" };
  if (current && state?.admittedOperationID)
    return { status: { kind: "queued" }, counted: "blocked" };
  if (
    current &&
    state?.blockers.some((blocker) =>
      blocker.startsWith("desired tags are incomplete:"),
    )
  )
    return {
      status: { kind: "disposition", disposition: "unprocessable" },
      counted: "unprocessable",
    };
  if (current && (state?.error || state?.admissionUncertain))
    return {
      status: { kind: "disposition", disposition: "blocked" },
      counted: "blocked",
    };
  if (!current || !state?.inspection.preview)
    return { status: { kind: "not-prepared" }, counted: "blocked" };
  if (state.receiptAction === "unchanged")
    return {
      status: { kind: "disposition", disposition: "unchanged" },
      counted: "unchanged",
    };
  if (stateReady(state) && state.receiptAction === "create")
    return {
      status: { kind: "disposition", disposition: "create" },
      counted: "create",
    };
  if (stateReady(state) && state.receiptAction === "update")
    return {
      status: { kind: "disposition", disposition: "update" },
      counted: "update",
    };
  return {
    status: { kind: "disposition", disposition: "blocked" },
    counted: "blocked",
  };
}

/**
 * One receipt-scoped publication controller. It stays mounted while the file
 * choice changes, so an admitted or uncertain request is never discarded by a
 * choice-driven remount.
 */
function ReceiptPositionPreview({
  releaseId,
  position,
  receipt,
  onController,
  onStateChange,
  onSubmitReceipt,
  onRefresh,
}: {
  releaseId: string;
  position: IncomingReceiptPublicationPosition;
  receipt: PublicationMatchingReceipt | undefined;
  onController: (controller: PublicationPreviewController | null) => void;
  onStateChange: (state: PublicationPreviewState) => void;
  onSubmitReceipt: SubmitMatchingReceiptPublication;
  onRefresh: () => void;
}) {
  const [controller, setController] =
    useState<PublicationPreviewController | null>(null);
  const receiptRef = useRef(receipt);
  receiptRef.current = receipt;
  const handleController = useCallback(
    (next: PublicationPreviewController | null) => {
      setController(next);
      onController(next);
    },
    [onController],
  );
  const receiptKey = matchingReceiptKey(receipt);

  // Preview the exact selected file as soon as this position's controller is
  // available and whenever the choice or release changes.
  useEffect(() => {
    const nextReceipt = receiptRef.current;
    if (!releaseId || !controller || !nextReceipt) return;
    // Ignore an effect left over from an older render than the current choice.
    if (matchingReceiptKey(nextReceipt) !== receiptKey) return;
    controller.prepare({
      workflow: "matching_receipt",
      mode: "ordinary",
      receipt: nextReceipt,
    });
  }, [controller, receiptKey, releaseId]);

  return (
    <FirstPublicationPreview
      releaseId={releaseId}
      positionId={position.id}
      aggregateManaged
      onAdmitted={ignoreAdmission}
      onSubmitReceipt={onSubmitReceipt}
      onRefresh={onRefresh}
      onController={handleController}
      onStateChange={onStateChange}
    />
  );
}

const ignoreAdmission = () => {};

type ReceiptPublicationBatch = {
  sequence: number;
  rows: readonly {
    key: string;
    positionId: string;
    label: string;
    receipt: PublicationMatchingReceipt;
    declined?: { detail?: string };
  }[];
};

function completedBatch(
  batch: ReceiptPublicationBatch | null,
  attempts: Readonly<Record<string, ReceiptPublicationAdmission>>,
) {
  if (!batch) return undefined;
  let published = 0;
  const failures: { id: string; label: string; detail?: string }[] = [];
  const issues: MatchingReceiptPublicationIssue[] = [];
  for (const row of batch.rows) {
    const label = { id: row.positionId, label: row.label };
    if (row.declined) {
      issues.push({ ...label, kind: "stale", detail: row.declined.detail });
      continue;
    }
    const attempt = attempts[row.key];
    if (attempt?.kind === "uncertain") {
      issues.push({ ...label, kind: "uncertain" });
      continue;
    }
    if (attempt?.kind !== "admitted") return undefined;
    const snapshot = attempt.snapshot;
    if (snapshot.state === "succeeded") published += 1;
    else if (snapshot.state === "failed")
      failures.push({
        ...label,
        detail: snapshot.safe_error,
      });
    else return undefined;
  }
  return { attempted: batch.rows.length, published, failures, issues };
}

function operationPending(state: string) {
  return state === "queued" || state === "running";
}

type ReceiptRefresh = () => Promise<void>;

function ReceiptPublicationFollower({
  admissionKey,
  attempt,
  registerRefresh,
}: {
  admissionKey: string;
  attempt: Extract<ReceiptPublicationAdmission, { kind: "admitted" }>;
  registerRefresh: (key: string, refresh: ReceiptRefresh) => () => void;
}) {
  const queryClient = useQueryClient();
  const enabled = operationPending(attempt.snapshot.state);
  const { refresh } = useOperationFollower(attempt.operationId, {
    enabled,
    initialSnapshot: attempt.snapshot,
    onSnapshot: (snapshot) =>
      recordReceiptPublicationSnapshot(queryClient, admissionKey, snapshot),
    onSnapshotRead: (snapshot, _source, accepted) => {
      if (!accepted && attempt.readFailed)
        recordReceiptPublicationSnapshot(queryClient, admissionKey, snapshot);
    },
    onError: () =>
      recordReceiptPublicationReadFailure(
        queryClient,
        admissionKey,
        attempt.operationId,
      ),
    refreshOnStreamError: true,
  });
  useEffect(() => {
    if (enabled) return registerRefresh(admissionKey, refresh);
  }, [admissionKey, enabled, refresh, registerRefresh]);
  return null;
}

export function IncomingReceiptPublication({
  locale,
  releaseTitle,
  releaseId,
  positions,
  onBack,
}: IncomingReceiptPublicationProps) {
  const queryClient = useQueryClient();
  const attemptStates = useReceiptPublicationAdmissions();
  const receipt = positions.find((position) => position.sources.length > 0)
    ?.sources[0]?.receipt;
  const batchKey = [
    "incoming-receipt-publication-batch",
    receipt?.kind,
    receipt?.requestId,
    releaseId,
  ] as const;
  const batch = useQuery<ReceiptPublicationBatch | null>({
    queryKey: batchKey,
    queryFn: () => null,
    initialData: null,
    enabled: false,
    gcTime: Number.POSITIVE_INFINITY,
  }).data;
  const [controllers, setControllers] = useState<
    ReadonlyMap<string, PublicationPreviewController>
  >(() => new Map());
  const [states, setStates] = useState<
    Readonly<Record<string, PublicationPreviewState>>
  >({});
  const [chosen, setChosen] = useState<Readonly<Record<string, string | null>>>(
    {},
  );
  const [excluded, setExcluded] = useState<ReadonlySet<string>>(
    () => new Set(),
  );
  const [selectedPositionID, setSelectedPositionID] = useState("");
  const operationRefreshes = useRef(new Map<string, ReceiptRefresh>());
  const registerRefresh = useCallback(
    (key: string, refresh: ReceiptRefresh) => {
      operationRefreshes.current.set(key, refresh);
      return () => {
        if (operationRefreshes.current.get(key) === refresh)
          operationRefreshes.current.delete(key);
      };
    },
    [],
  );
  const callbackCache = useRef(
    new Map<
      string,
      {
        onController: (controller: PublicationPreviewController | null) => void;
        onStateChange: (state: PublicationPreviewState) => void;
      }
    >(),
  );

  const included = new Set(
    positions
      .filter((position) => !excluded.has(position.id))
      .map((position) => position.id),
  );
  const selectedPosition =
    positions.find((position) => position.id === selectedPositionID) ??
    positions[0];
  const copy = firstPublicationPreviewMessages[locale];
  const fileChoiceLabels = matchingReceiptFileChoiceLabels(locale);
  const keys = new Map(
    positions.flatMap((position) => {
      const receipt = position.sources[0]?.receipt;
      return receipt
        ? [
            [
              position.id,
              receiptAdmissionKey(releaseId, position.id, receipt),
            ] as const,
          ]
        : [];
    }),
  );
  const admissionFor = (id: string) => attemptStates[keys.get(id) ?? ""];

  function callbacksFor(positionID: string) {
    let callbacks = callbackCache.current.get(positionID);
    if (!callbacks) {
      callbacks = {
        onController: (controller) => {
          setControllers((current) => {
            const existing = current.get(positionID);
            if (controller ? existing === controller : !existing)
              return current;
            const next = new Map(current);
            if (controller) next.set(positionID, controller);
            else next.delete(positionID);
            return next;
          });
        },
        onStateChange: (state) => {
          setStates((current) =>
            current[positionID] === state
              ? current
              : { ...current, [positionID]: state },
          );
        },
      };
      callbackCache.current.set(positionID, callbacks);
    }
    return callbacks;
  }

  function preparePosition(position: IncomingReceiptPublicationPosition) {
    const controller = controllers.get(position.id);
    const source = selectedSource(position, chosen);
    if (!controller || !source || admissionFor(position.id)) return;
    controller.prepare({
      workflow: "matching_receipt",
      mode: "ordinary",
      receipt: source.receipt,
    });
  }

  function prepareScope() {
    for (const position of positions)
      if (included.has(position.id)) preparePosition(position);
  }

  function toggleInclude(positionID: string) {
    const state = states[positionID];
    if (
      admissionFor(positionID) ||
      state?.confirming ||
      state?.admittedOperationID ||
      state?.admissionUncertain
    )
      return;
    setExcluded((current) => {
      const next = new Set(current);
      if (next.has(positionID)) next.delete(positionID);
      else next.add(positionID);
      return next;
    });
  }

  const counts = aggregateDispositions.reduce(
    (result, disposition) => {
      result[disposition] = 0;
      return result;
    },
    {} as Record<AggregateDisposition, number>,
  );
  const positionViews: AggregatePositionView[] = positions.map((position) => {
    const isIncluded = included.has(position.id);
    const source = selectedSource(position, chosen);
    const controller = controllers.get(position.id);
    const state = states[position.id];
    const current = stateIsReceipt(state, source?.receipt);
    const attempt = admissionFor(position.id);
    if (attempt && isIncluded) {
      const operationState =
        attempt.kind === "admitted" ? attempt.snapshot.state : undefined;
      const status: AggregatePositionStatus =
        attempt.kind === "admitted" &&
        (!operationState || operationState === "queued")
          ? { kind: "queued" }
          : {
              kind: "operation",
              label: operationState
                ? operationStateLabel(operationState, locale)
                : attempt.kind === "pending"
                  ? copy.admissionPending
                  : copy.receiptAdmissionUncertain,
            };
      return { id: position.id, label: position.label, status };
    }
    const { status, counted } = rowStatus(
      isIncluded,
      controller,
      source,
      state,
      current,
    );
    counts[counted] += 1;
    return {
      id: position.id,
      label: position.label,
      status,
      diagnostic: rowDiagnostic(
        isIncluded,
        controller,
        source,
        state,
        current,
        copy.noSource,
      ),
    };
  });
  const preparing = positions.some((position) => {
    if (!included.has(position.id)) return false;
    const source = selectedSource(position, chosen);
    const state = states[position.id];
    return Boolean(
      source && stateIsReceipt(state, source.receipt) && state?.loading,
    );
  });
  const canPrepare =
    !preparing &&
    positions.some(
      (position) =>
        included.has(position.id) &&
        controllers.has(position.id) &&
        !admissionFor(position.id) &&
        !states[position.id]?.inspection.sourceChoiceDisabled &&
        Boolean(selectedSource(position, chosen)),
    );
  // Capture the exact identities rendered as ready; a click must not re-derive
  // them after the controller has already started or lost a preview.
  const admissions = readyAdmissions(
    positions,
    new Set([...included].filter((id) => !admissionFor(id))),
    controllers,
    states,
    chosen,
  );

  const selectedState = selectedPosition
    ? states[selectedPosition.id]
    : undefined;
  const selectedController = selectedPosition
    ? controllers.get(selectedPosition.id)
    : undefined;
  const chosenSource = selectedPosition
    ? selectedSource(selectedPosition, chosen)
    : undefined;
  const selectedCurrent = stateIsReceipt(selectedState, chosenSource?.receipt);
  const selectedAdmission =
    selectedPosition && admissionFor(selectedPosition.id);
  const fileChoice =
    selectedPosition &&
    selectedController &&
    selectedPosition.sources.length > 1
      ? {
          ...fileChoiceLabels,
          options: selectedPosition.sources.map((source) => ({
            id: source.id,
            label: source.label,
          })),
          selectedId: chosenSource?.id ?? null,
          disabled: Boolean(
            selectedAdmission || selectedState?.inspection.sourceChoiceDisabled,
          ),
          onSelect: (sourceID: string | null) => {
            setChosen((current) => ({
              ...current,
              [selectedPosition.id]: sourceID,
            }));
          },
        }
      : undefined;

  let inspector: AggregateInspectorPresentation;
  if (!selectedPosition) {
    inspector = { kind: "empty" };
  } else if (!selectedController) {
    inspector = { kind: "not-prepared" };
  } else {
    const details =
      selectedCurrent && selectedState ? selectedState : emptyPreviewDetails;
    const fileLabel =
      chosenSource && !fileChoice
        ? { caption: fileChoiceLabels.legend, value: chosenSource.label }
        : undefined;
    inspector = {
      kind: "publication",
      details: selectedAdmission
        ? {
            ...details,
            error:
              selectedAdmission.kind !== "admitted"
                ? ""
                : selectedAdmission.readFailed
                  ? copy.refreshFailed
                  : (selectedAdmission.snapshot.safe_error ?? ""),
            admissionUncertain: selectedAdmission.kind === "uncertain",
            admissionUncertainStatus: copy.receiptAdmissionUncertain,
            admittedOperationID:
              selectedAdmission.kind === "admitted"
                ? selectedAdmission.operationId
                : "",
            admittedOperationStatus:
              selectedAdmission.kind === "admitted" &&
              selectedAdmission.snapshot.state &&
              selectedAdmission.snapshot.state !== "queued"
                ? operationStateLabel(selectedAdmission.snapshot.state, locale)
                : undefined,
            loading: false,
            inspection: { ...details.inspection, sourceChoiceDisabled: true },
          }
        : details,
      fileChoice,
      fileLabel,
      onSourceChoice: (choice) => {
        if (selectedCurrent && !selectedAdmission)
          selectedController.selectSource(choice);
      },
      onRefresh:
        selectedAdmission?.kind === "admitted"
          ? () => {
              const key = keys.get(selectedPosition.id);
              if (!key) return;
              // Use the follower's sequenced reader while it is attached.
              if (operationPending(selectedAdmission.snapshot.state)) {
                void operationRefreshes.current.get(key)?.();
              } else {
                void refreshReceiptPublication(
                  queryClient,
                  key,
                  selectedAdmission.operationId,
                );
              }
            }
          : selectedAdmission
            ? undefined
            : () => preparePosition(selectedPosition),
    };
  }

  function confirm() {
    const upcoming = admissions.filter(
      ({ position, receipt }) =>
        !receiptPublicationAdmission(
          queryClient,
          receiptAdmissionKey(releaseId, position.id, receipt),
        ),
    );
    if (!upcoming.length) return;
    // Freeze the requested positions before the controllers begin any writes.
    queryClient.setQueryData<ReceiptPublicationBatch | null>(
      batchKey,
      (previous) => ({
        sequence: (previous?.sequence ?? 0) + 1,
        rows: upcoming.map(({ position, receipt }) => ({
          key: receiptAdmissionKey(releaseId, position.id, receipt),
          positionId: position.id,
          label: position.label,
          receipt,
        })),
      }),
    );
    for (const { controller, identity } of upcoming)
      controller.confirm(identity);
  }

  function submit(positionId: string): SubmitMatchingReceiptPublication {
    return async (receipt, body) => {
      const previous = queryClient.getQueryData<ReceiptPublicationBatch | null>(
        batchKey,
      );
      const response = await submitReceiptPublication(
        queryClient,
        releaseId,
        positionId,
        receipt,
        body,
      );
      // A rejected stale plan never entered the queue. Its lock is released
      // by the admission provider; only mark the exact initiating batch here.
      if (response?.status === 409 && previous) {
        queryClient.setQueryData<ReceiptPublicationBatch | null>(
          batchKey,
          (active) =>
            active?.sequence === previous.sequence
              ? {
                  ...active,
                  rows: active.rows.map((row) =>
                    row.positionId === positionId &&
                    sameReceipt(row.receipt, receipt)
                      ? { ...row, declined: { detail: response.data.detail } }
                      : row,
                  ),
                }
              : active,
        );
      }
      return response;
    };
  }

  const outcome = completedBatch(batch ?? null, attemptStates);
  return (
    <MatchingReceiptPublicationFrame
      locale={locale}
      releaseTitle={releaseTitle}
      onBack={onBack}
      counts={counts}
      positions={positionViews}
      included={included}
      selectedPositionId={selectedPosition?.id ?? ""}
      canPrepare={canPrepare}
      canConfirm={admissions.length > 0}
      preparing={preparing}
      inspector={inspector}
      onToggleInclude={toggleInclude}
      onSelectPosition={(positionID) => setSelectedPositionID(positionID)}
      onPrepare={prepareScope}
      onConfirm={confirm}
    >
      {positions.map((position) => {
        const callbacks = callbacksFor(position.id);
        return (
          <ReceiptPositionPreview
            key={position.id}
            releaseId={releaseId}
            position={position}
            receipt={
              admissionFor(position.id)
                ? undefined
                : selectedSource(position, chosen)?.receipt
            }
            onController={callbacks.onController}
            onStateChange={callbacks.onStateChange}
            onSubmitReceipt={submit(position.id)}
            onRefresh={() => preparePosition(position)}
          />
        );
      })}
      {[...keys.values()].map((key) => {
        const attempt = attemptStates[key];
        return attempt?.kind === "admitted" ? (
          <ReceiptPublicationFollower
            key={`${key}:${attempt.operationId}`}
            admissionKey={key}
            attempt={attempt}
            registerRefresh={registerRefresh}
          />
        ) : null;
      })}
      {outcome && (
        <MatchingReceiptPublicationOutcomeView locale={locale} {...outcome} />
      )}
    </MatchingReceiptPublicationFrame>
  );
}
