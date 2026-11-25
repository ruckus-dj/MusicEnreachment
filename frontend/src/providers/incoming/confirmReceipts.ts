import { type QueryClient, useQuery } from "@tanstack/react-query";
import type {
  MatchingLocalAssignment,
  MatchingLocalPreview,
  MatchingManualPreview,
} from "../../api/generated/client.schemas";

type ManualConfirmReceipt = {
  kind: "manual";
  groupId: string;
  /** Provider release identity the confirmation was validated against. */
  releaseId: string;
  requestId: string;
  order: number;
  response: MatchingManualPreview;
  omittedFileLabels: Record<string, string>;
};

type LocalConfirmReceipt = {
  kind: "local";
  groupId: string;
  /** Local provider release identity the confirmation was validated against. */
  releaseId: string;
  requestId: string;
  order: number;
  response: MatchingLocalPreview;
  assignments?: readonly MatchingLocalAssignment[];
};

export type IncomingConfirmReceipt = ManualConfirmReceipt | LocalConfirmReceipt;
type IncomingConfirmReceipts = Readonly<Record<string, IncomingConfirmReceipt>>;

const emptyReceipts: IncomingConfirmReceipts = {};

const confirmReceiptsQueryKey = ["incoming-confirm-receipts"] as const;

function receiptKey(
  kind: IncomingConfirmReceipt["kind"],
  groupId: string,
  releaseId: string,
) {
  return `${kind}:${groupId}:${releaseId}`;
}

let submissionOrder = 0;

// Allocate on first submission, never on response or exact-body retry.
export function nextIncomingConfirmOrder() {
  return ++submissionOrder;
}

/**
 * Confirm receipts live in the application QueryClient for the current page
 * load. They are not persisted, so a reload starts without stale receipts, and
 * each test's own QueryClient starts empty.
 */
export function useIncomingConfirmReceipts(): IncomingConfirmReceipts {
  // The cache is written imperatively after a successful confirmation. This
  // disabled observer only reads the shared entry and re-renders when a late
  // response saves a receipt for the route that is currently open.
  return useQuery({
    queryKey: confirmReceiptsQueryKey,
    queryFn: () => emptyReceipts,
    enabled: false,
    gcTime: Number.POSITIVE_INFINITY,
    initialData: emptyReceipts,
  }).data;
}

export function saveIncomingConfirmReceipt(
  queryClient: QueryClient,
  receipt: IncomingConfirmReceipt,
) {
  const key = receiptKey(receipt.kind, receipt.groupId, receipt.releaseId);
  queryClient.setQueryData<IncomingConfirmReceipts>(
    confirmReceiptsQueryKey,
    (current = emptyReceipts) => {
      if ((current[key]?.order ?? 0) >= receipt.order) return current;
      return { ...current, [key]: receipt };
    },
  );
}

/**
 * Resolves the receipt for the exact origin release. A missing release keeps
 * the group's most recent receipt, which is the only identity available when a
 * confirmed group has already left the active Incoming list.
 */
export function findIncomingConfirmReceipt<
  K extends IncomingConfirmReceipt["kind"],
>(
  receipts: IncomingConfirmReceipts,
  kind: K,
  groupId: string,
  releaseId?: string,
): Extract<IncomingConfirmReceipt, { kind: K }> | undefined {
  if (!groupId) return undefined;
  if (releaseId) {
    return receipts[receiptKey(kind, groupId, releaseId)] as
      | Extract<IncomingConfirmReceipt, { kind: K }>
      | undefined;
  }
  let latest: IncomingConfirmReceipt | undefined;
  for (const receipt of Object.values(receipts)) {
    if (receipt.kind !== kind || receipt.groupId !== groupId) continue;
    if (!latest || receipt.order > latest.order) latest = receipt;
  }
  return latest as Extract<IncomingConfirmReceipt, { kind: K }> | undefined;
}
