import { useQueries, useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import {
  getOperation,
  startIncomingGroupManualReleaseSelection,
} from "../../api/generated/client";
import { AppButton } from "../../components/AppButton";

type Props = {
  groupId: string;
  groupRevision: string;
  evaluationRevision?: string;
  disabled?: boolean;
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
  onPrepared,
}: Props) {
  const queryClient = useQueryClient();
  const [states, setStates] = useState<Record<string, GroupState>>({});
  const currentGroupRef = useRef(groupId);
  currentGroupRef.current = groupId;
  const currentRevisionsRef = useRef({ groupRevision, evaluationRevision });
  currentRevisionsRef.current = { groupRevision, evaluationRevision };
  const state = states[groupId] ?? { value: "" };
  const operationQueries = useQueries({
    queries: Object.entries(states)
      .flatMap(([, item]) =>
        item.operationId ? [{ operationId: item.operationId }] : [],
      )
      .map(({ operationId }) => ({
        queryKey: ["manual-release-operation", operationId],
        queryFn: async ({ signal }: { signal: AbortSignal }) => {
          const response = await getOperation(operationId, {
            signal,
            cache: "no-store",
          });
          if (response.status !== 200)
            throw new Error(
              response.data.detail ||
                "Не удалось прочитать состояние операции.",
            );
          return response.data;
        },
        refetchInterval: (query: { state: { data?: { state: string } } }) =>
          query.state.data?.state === "queued" ||
          query.state.data?.state === "running"
            ? 1000
            : false,
      })),
  });
  const operations = Object.entries(states).filter(
    ([, item]) => item.operationId,
  );
  const operationQuery =
    operationQueries[operations.findIndex(([id]) => id === groupId)];
  const operation = operationQuery?.data;
  const operationPending =
    Boolean(state.admitting) ||
    (Boolean(state.operationId) &&
      (!operation ||
        operation.state === "queued" ||
        operation.state === "running"));

  useEffect(() => {
    for (const [index, [submittedGroupId, submitted]] of operations.entries()) {
      const result = operationQueries[index]?.data;
      if (
        !result ||
        result.state === "queued" ||
        result.state === "running" ||
        submitted.finished
      )
        continue;
      if (result.state === "succeeded") {
        void queryClient.invalidateQueries({
          queryKey: ["/api/incoming-groups"],
        });
        void queryClient.invalidateQueries({
          queryKey: [`/api/incoming-groups/${submittedGroupId}/matching`],
        });
        const selected = submitted.body?.selected_release;
        const releaseID = selected?.includes("/")
          ? selected.slice(selected.lastIndexOf("/") + 1)
          : selected;
        if (
          releaseID &&
          currentGroupRef.current === submittedGroupId &&
          currentRevisionsRef.current.groupRevision ===
            submitted.body?.expected_group_revision &&
          currentRevisionsRef.current.evaluationRevision ===
            submitted.body?.expected_evaluation_revision
        )
          onPrepared(releaseID.toLowerCase());
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
  }, [onPrepared, operationQueries, operations, queryClient]);

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
                value: event.target.value,
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
        operationQuery?.isLoading) ? (
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
      {state.operationId && operationQuery?.isError ? (
        <p role="alert">{operationQuery.error.message}</p>
      ) : null}
      {state.operationId && operationQuery?.isError ? (
        <AppButton onPress={() => void operationQuery.refetch()}>
          Обновить состояние операции
        </AppButton>
      ) : null}
    </section>
  );
}
