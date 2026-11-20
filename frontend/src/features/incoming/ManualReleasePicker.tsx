import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import {
  getGetIncomingGroupMatchingQueryKey,
  getGetIncomingGroupsQueryKey,
  startIncomingGroupManualReleaseSelection,
} from "../../api/generated/client";
import type { OperationResponse } from "../../api/generated/client.schemas";
import { useI18n } from "../../i18n/I18nProvider";
import { ManualReleasePreparationView } from "../matchingWorkspace/ManualReleasePreparationView";
import {
  type ManualReleasePreparationError,
  type ManualReleasePreparationMessageKey,
  manualReleasePreparationMessage,
} from "../matchingWorkspace/manualReleasePreparationContract";
import {
  errorAfterSnapshotRead,
  type IncomingOperationError,
  IncomingOperationFollower,
} from "./IncomingOperationFollower";

type Props = {
  groupId: string;
  groupRevision: string;
  evaluationRevision?: string;
  disabled?: boolean;
  searchRelease?: string;
  /**
   * Explicit release choice made in the unified search. Every distinct sequence
   * admits at most one preparation; it is never triggered by `searchRelease`,
   * which only prefills the manual form.
   */
  requestedRelease?: { id: string; sequence: number };
  /** Reports this picker's own admitting/following state to the parent. */
  onPendingChange?: (pending: boolean) => void;
  onPrepared: (releaseMBID: string) => void;
};

type RevisionIntent = {
  groupId: string;
  groupRevision: string;
  evaluationRevision?: string;
};

type RequestBody = {
  expected_group_revision: string;
  expected_evaluation_revision?: string;
  selected_release: string;
  request_id: string;
};
type GroupState = {
  value: string;
  searchRelease?: string;
  body?: RequestBody;
  admitting?: boolean;
  operationId?: string;
  error?: string;
  errorKey?: ManualReleasePreparationMessageKey;
  errorStatus?: number;
  finished?: boolean;
};

const releaseIDPattern =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;

function canonicalReleaseID(value: string | undefined): string | undefined {
  const trimmed = value?.trim();
  if (!trimmed) return undefined;
  const releaseID = trimmed.slice(trimmed.lastIndexOf("/") + 1);
  return releaseIDPattern.test(releaseID) ? releaseID.toLowerCase() : undefined;
}

function normalizedSelection(value: string | undefined): string {
  const trimmed = value?.trim() ?? "";
  return canonicalReleaseID(trimmed) ?? trimmed;
}

// A retained request body is only reusable while it still describes the
// current selection and revision intent; otherwise the input is a new intent.
function matchesRequestBody(
  body: RequestBody | undefined,
  selection: string,
  revisions: { groupRevision: string; evaluationRevision?: string },
): body is RequestBody {
  return (
    body !== undefined &&
    normalizedSelection(body.selected_release) ===
      normalizedSelection(selection) &&
    body.expected_group_revision === revisions.groupRevision &&
    body.expected_evaluation_revision === revisions.evaluationRevision
  );
}

function operationIsPending(
  groupState: GroupState,
  snapshots: Record<string, OperationResponse>,
): boolean {
  const operationId = groupState.operationId;
  if (groupState.admitting) return true;
  if (!operationId) return false;
  const snapshot = snapshots[operationId];
  return (
    !snapshot || snapshot.state === "queued" || snapshot.state === "running"
  );
}

export function ManualReleasePicker({
  groupId,
  groupRevision,
  evaluationRevision,
  disabled = false,
  searchRelease,
  requestedRelease,
  onPendingChange,
  onPrepared,
}: Props) {
  const { locale } = useI18n();
  const message = (
    key: ManualReleasePreparationMessageKey,
    values?: Record<string, string | number>,
  ) => manualReleasePreparationMessage(locale, key, values);
  const queryClient = useQueryClient();
  const [states, setStates] = useState<Record<string, GroupState>>({});
  const [snapshots, setSnapshots] = useState<Record<string, OperationResponse>>(
    {},
  );
  const [operationErrors, setOperationErrors] = useState<
    Record<string, IncomingOperationError>
  >({});
  const statesRef = useRef(states);
  const snapshotsRef = useRef(snapshots);
  const currentGroupRef = useRef(groupId);
  const refreshers = useRef<Record<string, () => Promise<void>>>({});
  const handledRequestedSequence = useRef<number | undefined>(undefined);
  const pendingReporter = useRef(onPendingChange);
  const reportedPending = useRef<boolean | undefined>(undefined);
  const admitRef = useRef<
    | ((
        selection: string,
        options?: {
          allowDisabled?: boolean;
          intent?: RevisionIntent;
        },
      ) => Promise<void>)
    | undefined
  >(undefined);
  statesRef.current = states;
  snapshotsRef.current = snapshots;
  currentGroupRef.current = groupId;
  pendingReporter.current = onPendingChange;
  const currentRevisionsRef = useRef({ groupRevision, evaluationRevision });
  currentRevisionsRef.current = { groupRevision, evaluationRevision };
  const state = states[groupId] ?? { value: "" };
  useEffect(() => {
    if (!searchRelease) return;
    setStates((current) => ({
      ...current,
      [groupId]: {
        ...current[groupId],
        value: searchRelease,
        searchRelease,
        error: undefined,
        errorKey: undefined,
        errorStatus: undefined,
        finished: false,
      },
    }));
  }, [groupId, searchRelease]);
  const operations = Object.entries(states).filter(
    ([, item]) => item.operationId,
  );
  const operation = snapshots[state.operationId ?? ""];
  const operationPending = operationIsPending(state, snapshots);
  const errors: ManualReleasePreparationError[] = [];
  if (state.error || state.errorKey) {
    errors.push({
      id: "prepare",
      message:
        state.error ||
        message(state.errorKey ?? "releaseNotPrepared", {
          status: state.errorStatus ?? "",
        }),
    });
  }
  if (state.operationId && operationErrors[state.operationId]) {
    const operationId = state.operationId;
    errors.push({
      id: "operation",
      message: operationErrors[operationId].message,
      onRetry: () => void refreshers.current[operationId]?.(),
    });
  }

  useEffect(() => {
    if (reportedPending.current === operationPending) return;
    reportedPending.current = operationPending;
    pendingReporter.current?.(operationPending);
  }, [operationPending]);
  useEffect(
    () => () => {
      if (!reportedPending.current) return;
      reportedPending.current = false;
      pendingReporter.current?.(false);
    },
    [],
  );

  useEffect(() => {
    for (const [submittedGroupId, submitted] of operations) {
      const result = snapshots[submitted.operationId ?? ""];
      if (
        !result ||
        result.state === "queued" ||
        result.state === "running" ||
        submitted.finished
      )
        continue;
      if (result.state === "succeeded") {
        void queryClient.invalidateQueries({
          queryKey: getGetIncomingGroupsQueryKey(),
        });
        void queryClient.invalidateQueries({
          queryKey: getGetIncomingGroupMatchingQueryKey(submittedGroupId),
        });
        const releaseID = canonicalReleaseID(submitted.body?.selected_release);
        const sameIntent = matchesRequestBody(
          submitted.body,
          submitted.value,
          currentRevisionsRef.current,
        );
        if (
          sameIntent &&
          releaseID &&
          currentGroupRef.current === submittedGroupId
        )
          onPrepared(releaseID);
        setStates((current) => ({
          ...current,
          [submittedGroupId]: sameIntent
            ? {
                ...current[submittedGroupId],
                finished: true,
                operationId: undefined,
                body: undefined,
                error: undefined,
              }
            : {
                ...current[submittedGroupId],
                operationId: undefined,
                body: undefined,
              },
        }));
      } else {
        setStates((current) =>
          matchesRequestBody(
            submitted.body,
            submitted.value,
            currentRevisionsRef.current,
          )
            ? {
                ...current,
                [submittedGroupId]: {
                  ...current[submittedGroupId],
                  operationId: undefined,
                  error: result.safe_error,
                  errorKey: result.safe_error
                    ? undefined
                    : "releaseNotPrepared",
                  errorStatus: undefined,
                },
              }
            : {
                ...current,
                [submittedGroupId]: {
                  ...current[submittedGroupId],
                  operationId: undefined,
                  body: undefined,
                },
              },
        );
      }
    }
  }, [onPrepared, operations, queryClient, snapshots]);

  async function admit(
    selection: string,
    options: {
      allowDisabled?: boolean;
      intent?: RevisionIntent;
    } = {},
  ) {
    const requestedGroupId = groupId;
    const intent = options.intent ?? {
      groupId: requestedGroupId,
      ...currentRevisionsRef.current,
    };
    const latest = statesRef.current[requestedGroupId] ?? { value: "" };
    if (!selection.trim()) return;
    if (!options.allowDisabled && disabled) return;
    if (operationIsPending(latest, snapshotsRef.current)) return;
    // An action observed for a previous group or revision must not admit a
    // request against the group the picker happens to show now.
    if (
      intent.groupId !== requestedGroupId ||
      intent.groupRevision !== currentRevisionsRef.current.groupRevision ||
      intent.evaluationRevision !==
        currentRevisionsRef.current.evaluationRevision
    )
      return;
    const body = matchesRequestBody(latest.body, selection, intent)
      ? latest.body
      : {
          expected_group_revision: intent.groupRevision,
          ...(intent.evaluationRevision
            ? { expected_evaluation_revision: intent.evaluationRevision }
            : {}),
          selected_release: selection,
          request_id: crypto.randomUUID(),
        };
    setStates((current) => ({
      ...current,
      [requestedGroupId]: {
        ...(current[requestedGroupId] ?? { value: selection }),
        value: selection,
        body,
        admitting: true,
        error: undefined,
        errorKey: undefined,
        errorStatus: undefined,
        finished: false,
      },
    }));
    try {
      const response = await startIncomingGroupManualReleaseSelection(
        requestedGroupId,
        body,
      );
      if (response.status !== 202) {
        setStates((current) => ({
          ...current,
          [requestedGroupId]: {
            ...current[requestedGroupId],
            admitting: false,
            ...(response.status >= 400 && response.status < 500
              ? { body: undefined }
              : {}),
            error: response.data.detail,
            errorKey: response.data.detail
              ? undefined
              : "prepareReleaseNotAccepted",
            errorStatus: response.data.detail ? undefined : response.status,
          },
        }));
        return;
      }
      setStates((current) => ({
        ...current,
        [requestedGroupId]: {
          ...current[requestedGroupId],
          admitting: false,
          operationId: response.data.operation_id,
        },
      }));
    } catch (reason) {
      setStates((current) => ({
        ...current,
        [requestedGroupId]: {
          ...current[requestedGroupId],
          admitting: false,
          error: reason instanceof Error ? reason.message : undefined,
          errorKey: reason instanceof Error ? undefined : "releaseNotPrepared",
          errorStatus: undefined,
        },
      }));
    }
  }

  admitRef.current = admit;

  useEffect(() => {
    if (!requestedRelease) return;
    if (handledRequestedSequence.current === requestedRelease.sequence) return;
    handledRequestedSequence.current = requestedRelease.sequence;
    const intent = { groupId, groupRevision, evaluationRevision };
    if (
      currentGroupRef.current !== intent.groupId ||
      currentRevisionsRef.current.groupRevision !== intent.groupRevision ||
      currentRevisionsRef.current.evaluationRevision !==
        intent.evaluationRevision
    )
      return;
    setStates((current) => ({
      ...current,
      [groupId]: {
        ...current[groupId],
        value: requestedRelease.id,
        error: undefined,
        errorKey: undefined,
        errorStatus: undefined,
        finished: false,
      },
    }));
    // The explicit search action must still admit when the surrounding
    // controls are disabled for unrelated in-flight work.
    void admitRef.current?.(requestedRelease.id, {
      allowDisabled: true,
      intent,
    });
  }, [evaluationRevision, groupId, groupRevision, requestedRelease]);

  function submit() {
    void admit(state.value);
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
      <ManualReleasePreparationView
        locale={locale}
        value={state.value}
        disabled={disabled}
        pending={operationPending}
        prepared={Boolean(state.finished)}
        prepareDisabled={operation?.state === "failed"}
        errors={errors}
        onValueChange={(value) =>
          setStates((current) => ({
            ...current,
            [groupId]: {
              ...current[groupId],
              value,
              body: undefined,
              error: undefined,
              errorKey: undefined,
              errorStatus: undefined,
              finished: false,
            },
          }))
        }
        onPrepare={() => void submit()}
      />
    </>
  );
}
