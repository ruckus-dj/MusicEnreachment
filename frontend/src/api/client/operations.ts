import { getGetOperationUrl, getOperation } from "../generated/client";
import type { OperationResponse } from "../generated/client.schemas";

export function subscribeToOperation(
  operationId: string,
  onSnapshot: (snapshot: OperationResponse) => void,
  onError: (error: Error) => void,
) {
  const events = new EventSource(`${getGetOperationUrl(operationId)}/events`);
  let request: AbortController | undefined;
  let dirty = false;
  let subscribed = true;

  async function refetch() {
    if (!subscribed) return;
    if (request) {
      dirty = true;
      return;
    }

    const current = new AbortController();
    request = current;
    try {
      const response = await getOperation(operationId, {
        signal: current.signal,
        cache: "no-store",
      });
      if (!subscribed || current.signal.aborted) return;
      if (response.status !== 200) {
        onError(
          new Error(`Operation snapshot request failed (${response.status}).`),
        );
        return;
      }
      onSnapshot(response.data);
    } catch (reason) {
      if (subscribed && !current.signal.aborted) {
        onError(
          reason instanceof Error
            ? reason
            : new Error("Operation snapshot request failed."),
        );
      }
    } finally {
      if (request === current) request = undefined;
      if (subscribed && dirty) {
        dirty = false;
        void refetch();
      }
    }
  }

  function disconnected() {
    if (!subscribed) return;
    dirty = false;
    request?.abort();
    onError(new Error("Operation event stream disconnected."));
  }

  // EventSource emits open again after its native reconnect. Events are only
  // wake-ups: their payload is never an operation snapshot or a replay log.
  events.addEventListener("open", refetch);
  events.addEventListener("operation-changed", refetch);
  events.addEventListener("error", disconnected);

  return () => {
    subscribed = false;
    dirty = false;
    events.removeEventListener("open", refetch);
    events.removeEventListener("operation-changed", refetch);
    events.removeEventListener("error", disconnected);
    events.close();
    request?.abort();
  };
}
