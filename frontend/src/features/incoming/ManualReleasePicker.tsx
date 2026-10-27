import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import {
  getGetIncomingGroupMatchingQueryKey,
  getGetIncomingGroupsQueryKey,
  startIncomingGroupManualReleaseSelection,
} from "../../api/generated/client";
import type { OperationResponse } from "../../api/generated/client.schemas";
import { AppButton } from "../../components/AppButton";
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
            error: result.safe_error || "Не удалось подготовить издание.",
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
            error:
              response.data.detail ||
              `Не удалось подготовить издание (HTTP ${response.status}).`,
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
          error:
            reason instanceof Error
              ? reason.message
              : "Не удалось подготовить издание.",
        },
      }));
    }
  }

  return (
    <section
      className="rounded border border-stone-300 bg-white p-4"
      aria-labelledby="manual-release-title"
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
      <h2 id="manual-release-title" className="font-semibold">
        Выбрать издание по MBID или ссылке
      </h2>
      <p className="mt-1 text-sm">
        Будет выполнен только запрос выбранного MusicBrainz release. Поиск по
        артисту не запускается.
      </p>
      <div className="mt-3 flex flex-wrap gap-2">
        <input
          aria-label="MBID или официальная ссылка на издание"
          className="min-w-64 flex-1 rounded border border-stone-400 p-2"
          value={state.value}
          disabled={operationPending || disabled}
          onChange={(event) => {
            setStates((current) => ({
              ...current,
              [groupId]: {
                ...current[groupId],
                value: event.target.value,
                body: undefined,
                error: undefined,
                finished: false,
              },
            }));
          }}
        />
        <AppButton
          onPress={() => void submit()}
          isDisabled={
            !state.value.trim() ||
            operationPending ||
            disabled ||
            operation?.state === "failed"
          }
        >
          Подготовить издание
        </AppButton>
      </div>
      {state.operationId &&
      (operation?.state === "queued" ||
        operation?.state === "running" ||
        !operation) ? (
        <p role="status" className="mt-2">
          Подготовка издания…
        </p>
      ) : null}
      {state.error ? (
        <p role="alert" className="mt-2">
          {state.error}
        </p>
      ) : null}
      {state.finished ? (
        <p role="status" className="mt-2">
          Издание подготовлено.
        </p>
      ) : null}
      {state.operationId && operationErrors[state.operationId] ? (
        <div role="alert" className="mt-2 space-y-2">
          <p>{operationErrors[state.operationId].message}</p>
          <AppButton
            onPress={() =>
              void refreshers.current[state.operationId as string]?.()
            }
          >
            Обновить состояние операции
          </AppButton>
        </div>
      ) : null}
    </section>
  );
}
