import { useCallback, useEffect, useRef, useState } from "react";
import {
  isOperationSnapshotNewer,
  useOperationFollower,
} from "../../api/client/useOperationFollower";
import {
  cleanupSourceAnalysisArtifacts,
  listSourceAnalysisArtifactCleanupCandidates,
} from "../../api/generated/client";
import type {
  OperationResponse,
  SourceAnalysisArtifactCleanupCandidate,
} from "../../api/generated/client.schemas";
import { message } from "./sourcesApi";

export { isOperationSnapshotNewer } from "../../api/client/useOperationFollower";

const pending = (operation: OperationResponse) =>
  operation.state === "queued" || operation.state === "running";

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
      lifetime.current.abort();
      candidateRequest.current?.abort();
      actionRequest.current?.abort();
    };
  }, [load]);

  const operationId = operation?.id;
  const isPending = operation ? pending(operation) : false;
  useOperationFollower(operationId, {
    enabled: Boolean(operationId && isPending),
    initialSnapshot: operation,
    onSnapshot: (snapshot) => {
      applySnapshot(snapshot);
    },
    onSnapshotRead: (_snapshot, source) => {
      if (source === "stream") setStreamError("");
    },
    onHttpError: requestFailure,
    onStreamError: (reason) => setStreamError(reason.message),
  });

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
