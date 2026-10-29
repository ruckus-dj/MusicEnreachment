import { getOperation } from "../generated/client";
import type { OperationResponse } from "../generated/client.schemas";

type OperationSubscriber = {
  refetch: () => void;
  disconnected: () => void;
};

const subscribers = new Set<OperationSubscriber>();
let events: EventSource | undefined;
let eventsOpen = false;

function refetchAll() {
  for (const subscriber of subscribers) subscriber.refetch();
}

function disconnectedAll() {
  for (const subscriber of subscribers) subscriber.disconnected();
}

function startEvents() {
  if (events) return;
  events = new EventSource("/api/operations/events");
  eventsOpen = false;
  events.addEventListener("open", () => {
    eventsOpen = true;
    refetchAll();
  });
  events.addEventListener("operation-changed", refetchAll);
  events.addEventListener("error", () => {
    eventsOpen = false;
    disconnectedAll();
  });
}

function stopEvents() {
  if (!events) return;
  events.removeEventListener("open", refetchAll);
  events.removeEventListener("operation-changed", refetchAll);
  events.removeEventListener("error", disconnectedAll);
  events.close();
  events = undefined;
  eventsOpen = false;
}

export function subscribeToOperation(
  operationId: string,
  onSnapshot: (snapshot: OperationResponse) => void,
  onError: (error: Error) => void,
  onMissing?: (error: Error) => void,
) {
  let request: AbortController | undefined;
  let dirty = false;
  let subscribed = true;

  function refetch() {
    if (!subscribed) return;
    if (request) {
      dirty = true;
      return;
    }

    const current = new AbortController();
    request = current;
    void (async () => {
      try {
        const response = await getOperation(operationId, {
          signal: current.signal,
          cache: "no-store",
        });
        if (!subscribed || current.signal.aborted) return;
        if (response.status !== 200) {
          const error = new Error(
            `Operation snapshot request failed (${response.status}).`,
          );
          if (response.status === 404 && onMissing) {
            onMissing(error);
            stop();
          } else {
            onError(error);
          }
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
          refetch();
        }
      }
    })();
  }

  function disconnected() {
    if (!subscribed) return;
    dirty = false;
    request?.abort();
    onError(new Error("Operation event stream disconnected."));
  }

  function stop() {
    if (!subscribed) return;
    subscribed = false;
    dirty = false;
    subscribers.delete(subscription);
    request?.abort();
    if (subscribers.size === 0) stopEvents();
  }

  const subscription = { refetch, disconnected };
  subscribers.add(subscription);
  startEvents();
  // Initial followers get their snapshot on stream open. A late joiner cannot
  // rely on another open event, so read immediately only once transport is open.
  if (eventsOpen) refetch();

  return stop;
}
