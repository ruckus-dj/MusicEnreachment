import { getGetOperationUrl, getOperation } from "../generated/client";
import type { OperationResponse } from "../generated/client.schemas";

export function subscribeToOperation(
  operationId: string,
  onSnapshot: (snapshot: OperationResponse) => void,
  onError: (error: Error) => void,
) {
  const events = new EventSource(`${getGetOperationUrl(operationId)}/events`);
  let request: AbortController | undefined;

  async function refetch() {
    request?.abort();
    const current = new AbortController();
    request = current;
    try {
      const response = await getOperation(operationId, {
        signal: current.signal,
        cache: "no-store",
      });
      if (current.signal.aborted) return;
      if (response.status !== 200) {
        onError(
          new Error(`Operation snapshot request failed (${response.status}).`),
        );
        return;
      }
      onSnapshot(response.data);
    } catch (reason) {
      if (!current.signal.aborted) {
        onError(
          reason instanceof Error
            ? reason
            : new Error("Operation snapshot request failed."),
        );
      }
    }
  }

  function disconnected() {
    request?.abort();
    onError(new Error("Operation event stream disconnected."));
  }

  // EventSource emits open again after its native reconnect. Events are only
  // wake-ups: their payload is never an operation snapshot or a replay log.
  events.addEventListener("open", refetch);
  events.addEventListener("operation-changed", refetch);
  events.addEventListener("error", disconnected);

  return () => {
    events.removeEventListener("open", refetch);
    events.removeEventListener("operation-changed", refetch);
    events.removeEventListener("error", disconnected);
    events.close();
    request?.abort();
  };
}
