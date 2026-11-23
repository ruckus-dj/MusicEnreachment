import { type QueryClient, useQuery } from "@tanstack/react-query";
import {
  getListCurrentOperationsQueryKey,
  getOperation,
  startLibraryFirstPublication,
} from "../../api/generated/client";
import type { LibraryFirstPublicationAdmissionInputBody } from "../../api/generated/client.schemas";
import type { PublicationMatchingReceipt } from "../library/FirstPublicationPreview";

export type ReceiptPublicationAdmission =
  | { kind: "pending" | "uncertain" }
  | { kind: "admitted"; operationId: string; state?: string; error?: string };

const cacheKey = ["incoming-receipt-publication-admissions"] as const;
const emptyAdmissions: Readonly<Record<string, ReceiptPublicationAdmission>> =
  {};

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

export async function refreshReceiptPublication(
  queryClient: QueryClient,
  key: string,
  operationId: string,
  fallback: string,
) {
  let result: { state?: string; error?: string };
  try {
    const response = await getOperation(operationId, { cache: "no-store" });
    result =
      response.status === 200 && response.data.id === operationId
        ? { state: response.data.state, error: response.data.safe_error }
        : { error: fallback };
  } catch {
    result = { error: fallback };
  }
  queryClient.setQueryData<typeof emptyAdmissions>(cacheKey, (current = {}) => {
    const admission = current[key];
    if (admission?.kind !== "admitted" || admission.operationId !== operationId)
      return current;
    return { ...current, [key]: { ...admission, ...result } };
  });
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
      settle({ kind: "admitted", operationId: response.data.id });
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
