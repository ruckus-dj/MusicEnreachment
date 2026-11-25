import { type QueryClient, useQuery } from "@tanstack/react-query";
import { isOperationSnapshotNewer } from "../../api/client/useOperationFollower";
import {
  getListCurrentOperationsQueryKey,
  getOperation,
  startLibraryFirstPublication,
} from "../../api/generated/client";
import type {
  LibraryFirstPublicationAdmissionInputBody,
  OperationResponse,
} from "../../api/generated/client.schemas";
import type { PublicationMatchingReceipt } from "../library/FirstPublicationPreview";

export type ReceiptPublicationAdmission =
  | { kind: "pending" }
  | { kind: "uncertain" }
  | {
      kind: "admitted";
      operationId: string;
      snapshot: OperationResponse;
      readFailed?: boolean;
    };

const cacheKey = ["incoming-receipt-publication-admissions"] as const;
const emptyAdmissions: Readonly<Record<string, ReceiptPublicationAdmission>> =
  {};
const refreshReads = new WeakMap<QueryClient, Map<string, symbol>>();

export function receiptAdmissionKey(
  releaseId: string,
  positionId: string,
  receipt: PublicationMatchingReceipt,
) {
  return `${receipt.kind}:${receipt.requestId}:${releaseId}:${positionId}`;
}

export function useReceiptPublicationAdmissions() {
  return useQuery({
    queryKey: cacheKey,
    queryFn: () => emptyAdmissions,
    initialData: emptyAdmissions,
    enabled: false,
    gcTime: Number.POSITIVE_INFINITY,
  }).data;
}

export function receiptPublicationAdmission(
  queryClient: QueryClient,
  key: string,
) {
  return queryClient.getQueryData<typeof emptyAdmissions>(cacheKey)?.[key];
}

export function recordReceiptPublicationSnapshot(
  queryClient: QueryClient,
  key: string,
  snapshot: OperationResponse,
) {
  queryClient.setQueryData<typeof emptyAdmissions>(cacheKey, (current = {}) => {
    const admission = current[key];
    if (admission?.kind !== "admitted" || admission.operationId !== snapshot.id)
      return current;
    if (isOperationSnapshotNewer(admission.snapshot, snapshot))
      return {
        ...current,
        [key]: {
          kind: "admitted",
          operationId: admission.operationId,
          snapshot,
        },
      };
    // An equivalent read is still a successful read: clear a prior read
    // failure without replacing the durable snapshot. Older reads are ignored.
    if (
      admission.readFailed &&
      !isOperationSnapshotNewer(snapshot, admission.snapshot)
    ) {
      const { readFailed: _cleared, ...admissionWithoutFailure } = admission;
      return { ...current, [key]: admissionWithoutFailure };
    }
    return current;
  });
}

export function recordReceiptPublicationReadFailure(
  queryClient: QueryClient,
  key: string,
  operationId: string,
) {
  queryClient.setQueryData<typeof emptyAdmissions>(cacheKey, (current = {}) => {
    const admission = current[key];
    if (
      admission?.kind !== "admitted" ||
      admission.operationId !== operationId ||
      admission.readFailed
    )
      return current;
    return { ...current, [key]: { ...admission, readFailed: true } };
  });
}

export async function refreshReceiptPublication(
  queryClient: QueryClient,
  key: string,
  operationId: string,
) {
  const admission = receiptPublicationAdmission(queryClient, key);
  if (admission?.kind !== "admitted" || admission.operationId !== operationId)
    return;

  let reads = refreshReads.get(queryClient);
  if (!reads) {
    reads = new Map();
    refreshReads.set(queryClient, reads);
  }
  const read = Symbol();
  reads.set(key, read);
  try {
    const response = await getOperation(operationId, { cache: "no-store" });
    if (reads.get(key) !== read) return;
    if (response.status === 200 && response.data.id === operationId) {
      recordReceiptPublicationSnapshot(queryClient, key, response.data);
    } else recordReceiptPublicationReadFailure(queryClient, key, operationId);
  } catch {
    if (reads.get(key) === read)
      recordReceiptPublicationReadFailure(queryClient, key, operationId);
  } finally {
    if (reads.get(key) === read) reads.delete(key);
  }
}

export async function submitReceiptPublication(
  queryClient: QueryClient,
  releaseId: string,
  positionId: string,
  receipt: PublicationMatchingReceipt,
  body: LibraryFirstPublicationAdmissionInputBody,
) {
  const key = receiptAdmissionKey(releaseId, positionId, receipt);
  const current = queryClient.getQueryData<typeof emptyAdmissions>(cacheKey);
  if (current?.[key]) return undefined;
  queryClient.setQueryData(cacheKey, {
    ...current,
    [key]: { kind: "pending" },
  });

  function settle(admission?: ReceiptPublicationAdmission) {
    queryClient.setQueryData<typeof emptyAdmissions>(
      cacheKey,
      (current = {}) => {
        if (admission) return { ...current, [key]: admission };
        const { [key]: _finished, ...remaining } = current;
        return remaining;
      },
    );
    if (admission)
      void queryClient.invalidateQueries({
        queryKey: getListCurrentOperationsQueryKey(),
      });
  }

  try {
    const response = await startLibraryFirstPublication(
      releaseId,
      positionId,
      body,
    );
    if (response.status === 202 && response.data.id) {
      settle({
        kind: "admitted",
        operationId: response.data.id,
        snapshot: response.data,
      });
    } else if (response.status === 409) {
      settle();
    } else {
      settle({ kind: "uncertain" });
    }
    return response;
  } catch (reason) {
    settle({ kind: "uncertain" });
    throw reason;
  }
}
