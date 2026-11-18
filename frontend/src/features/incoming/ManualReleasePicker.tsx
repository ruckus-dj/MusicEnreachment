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
  onPrepared: (releaseMBID: string) => void;
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

export function ManualReleasePicker({
  groupId,
  groupRevision,
  evaluationRevision,
  disabled = false,
  searchRelease,
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
  const currentGroupRef = useRef(groupId);
  const refreshers = useRef<Record<string, () => Promise<void>>>({});
  currentGroupRef.current = groupId;
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
  const operationPending =
    Boolean(state.admitting) ||
    (Boolean(state.operationId) &&
      (!operation ||
        operation.state === "queued" ||
        operation.state === "running"));
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
        const selected = submitted.body?.selected_release;
        const releaseID = selected?.includes("/")
          ? selected.slice(selected.lastIndexOf("/") + 1)
          : selected;
        const canonicalReleaseID =
          releaseID &&
          /^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i.test(
            releaseID,
          )
            ? releaseID.toLowerCase()
            : undefined;
        if (
          canonicalReleaseID &&
          currentGroupRef.current === submittedGroupId &&
          currentRevisionsRef.current.groupRevision ===
            submitted.body?.expected_group_revision &&
          currentRevisionsRef.current.evaluationRevision ===
            submitted.body?.expected_evaluation_revision
        )
          onPrepared(canonicalReleaseID);
        setStates((current) => ({
          ...current,
          [submittedGroupId]: {
            ...current[submittedGroupId],
            finished: true,
            operationId: undefined,
            body: undefined,
            error: undefined,
          },
        }));
      } else {
        setStates((current) => ({
          ...current,
          [submittedGroupId]: {
            ...current[submittedGroupId],
            operationId: undefined,
            error: result.safe_error,
            errorKey: result.safe_error ? undefined : "releaseNotPrepared",
            errorStatus: undefined,
          },
        }));
      }
    }
  }, [onPrepared, operations, queryClient, snapshots]);

  async function submit() {
    if (!state.value.trim() || operationPending) return;
    const requestedGroupId = groupId;
    let body = state.body;
    if (!body) {
      body = {
        expected_group_revision: groupRevision,
        ...(evaluationRevision
          ? { expected_evaluation_revision: evaluationRevision }
          : {}),
        selected_release: state.value,
        request_id: crypto.randomUUID(),
      };
    }
    setStates((current) => ({
      ...current,
      [requestedGroupId]: {
        ...state,
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
