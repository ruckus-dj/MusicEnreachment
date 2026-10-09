import { useCallback, useEffect, useRef, useState } from "react";
import { subscribeToOperation } from "../../api/client/operations";
import {
  cleanupSourceAnalysisArtifacts,
  getOperation,
  listSourceAnalysisArtifactCleanupCandidates,
} from "../../api/generated/client";
import type {
  OperationResponse,
  SourceAnalysisArtifactCleanupCandidate,
} from "../../api/generated/client.schemas";
import { message } from "./sourcesApi";

const pending = (operation: OperationResponse) =>
  operation.state === "queued" || operation.state === "running";

// Monotonic acceptance rule shared by every snapshot source (the SSE wake-up
// re-read and the immediate REST read after an accepted POST). A snapshot only
// replaces the current one when it belongs to a new operation, or when it is
// strictly fresher for the same operation. Equal timestamps accept a pending ->
// terminal transition (a terminal can share its timestamp with the last pending
// update) but never let a terminal regress back to a non-terminal snapshot.
export function isOperationSnapshotNewer(
  current: OperationResponse | undefined,
  incoming: OperationResponse,
): boolean {
  if (!current || current.id !== incoming.id) return true;
  if (incoming.updated_at > current.updated_at) return true;
  return (
    incoming.updated_at === current.updated_at &&
    pending(current) &&
    !pending(incoming)
  );
}

function requestFailure(response: {
  status: number;
  data?: { detail?: string };
}) {
  return new Error(
    response.data?.detail || `Сервер вернул ошибку ${response.status}.`,
  );
}

export function useSourceArtifactCleanup() {
  const [candidates, setCandidates] = useState<
    SourceAnalysisArtifactCleanupCandidate[]
  >([]);
  const [batchCount, setBatchCount] = useState<number>();
  const [selected, setSelected] = useState<string[]>([]);
  const [operation, setOperation] = useState<OperationResponse>();
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [streamError, setStreamError] = useState("");
  const operationSnapshot = useRef<OperationResponse | undefined>(undefined);
  const lifetime = useRef(new AbortController());
  const candidateRequest = useRef<AbortController | undefined>(undefined);
  const actionRequest = useRef<AbortController | undefined>(undefined);
  const operationRequest = useRef<AbortController | undefined>(undefined);
  const generation = useRef(0);

  const load = useCallback(async (preserveError = false) => {
    candidateRequest.current?.abort();
    const request = new AbortController();
    candidateRequest.current = request;
    setLoading(true);
    if (!preserveError) setError("");
    try {
      const response = await listSourceAnalysisArtifactCleanupCandidates({
        signal: request.signal,
        cache: "no-store",
      });
      if (request.signal.aborted || lifetime.current.signal.aborted) return;
      if (response.status !== 200) throw requestFailure(response);
      setCandidates(response.data.candidates ?? []);
      setBatchCount(response.data.count);
    } catch (reason) {
      if (!request.signal.aborted && !lifetime.current.signal.aborted)
        setError(message(reason));
    } finally {
      if (!request.signal.aborted && !lifetime.current.signal.aborted)
        setLoading(false);
    }
  }, []);

  // The single funnel for every snapshot source. Rejecting a stale snapshot
  // here keeps both the stream callback and the immediate REST read from
  // restoring a pending view over an already accepted terminal state.
  const applySnapshot = useCallback((snapshot: OperationResponse) => {
    if (!isOperationSnapshotNewer(operationSnapshot.current, snapshot)) return;
    operationSnapshot.current = snapshot;
    setOperation(snapshot);
  }, []);

  useEffect(() => {
    lifetime.current = new AbortController();
    void load();
    return () => {
      generation.current += 1;
      lifetime.current.abort();
      candidateRequest.current?.abort();
      actionRequest.current?.abort();
      operationRequest.current?.abort();
    };
  }, [load]);

  const operationId = operation?.id;
  const isPending = operation ? pending(operation) : false;
  useEffect(() => {
    if (!operationId || !isPending) return;
    const currentGeneration = ++generation.current;
    let disposed = false;
    const current = () =>
      !disposed &&
      !lifetime.current.signal.aborted &&
      generation.current === currentGeneration;
    const close = subscribeToOperation(
      operationId,
      (snapshot) => {
        if (!current()) return;
        setStreamError("");
        applySnapshot(snapshot);
      },
      (reason) => {
        if (current()) setStreamError(reason.message);
      },
    );
    return () => {
      disposed = true;
      close();
    };
  }, [operationId, isPending, applySnapshot]);

  const results = operation?.cleanup_results;
  const wasPending = useRef(false);
  useEffect(() => {
    if (isPending) {
      wasPending.current = true;
      return;
    }
    if (operation && wasPending.current) {
      wasPending.current = false;
      setSelected([]);
      void load();
    }
  }, [isPending, operation, load]);

  function toggle(id: string) {
    setSelected((current) =>
      current.includes(id)
        ? current.filter((selectedId) => selectedId !== id)
        : [...current, id],
    );
  }

  async function start(ids: readonly string[]) {
    if (
      !ids.length ||
      ids.length > 1000 ||
      actionRequest.current ||
      (operationSnapshot.current && pending(operationSnapshot.current)) ||
      lifetime.current.signal.aborted
    )
      return;
    const request = new AbortController();
    // Claim the action synchronously: React state updates do not prevent two
    // same-tick callers from issuing duplicate destructive POSTs.
    actionRequest.current = request;
    setBusy(true);
    setError("");
    try {
      const response = await cleanupSourceAnalysisArtifacts(
        { artifact_ids: [...ids] },
        { signal: request.signal },
      );
      if (request.signal.aborted || lifetime.current.signal.aborted) return;
      if (response.status !== 202) {
        if (response.status === 409) void load(true);
        throw requestFailure(response);
      }
      wasPending.current = true;
      operationSnapshot.current = response.data;
      setOperation(response.data);
      void readOperationSnapshot(response.data.id);
    } catch (reason) {
      if (!request.signal.aborted && !lifetime.current.signal.aborted)
        setError(message(reason));
    } finally {
      if (
        actionRequest.current === request &&
        !request.signal.aborted &&
        !lifetime.current.signal.aborted
      ) {
        actionRequest.current = undefined;
        setBusy(false);
      }
    }
  }

  async function readOperationSnapshot(operationId: string) {
    operationRequest.current?.abort();
    const request = new AbortController();
    operationRequest.current = request;
    try {
      const response = await getOperation(operationId, {
        signal: request.signal,
        cache: "no-store",
      });
      if (
        request.signal.aborted ||
        lifetime.current.signal.aborted ||
        response.status !== 200
      )
        return;
      applySnapshot(response.data);
    } catch {
      // The event stream and its wake-up-triggered REST reads remain active.
      // An initial snapshot failure must not turn an accepted action into an
      // apparent POST failure.
    } finally {
      if (operationRequest.current === request)
        operationRequest.current = undefined;
    }
  }

  return {
    candidates,
    batchCount,
    selected,
    operation,
    results,
    loading,
    busy,
    error,
    streamError,
    isPending,
    load,
    toggle,
    start,
  };
}
