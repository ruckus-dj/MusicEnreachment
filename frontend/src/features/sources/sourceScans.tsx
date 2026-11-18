import { useQueryClient } from "@tanstack/react-query";
import {
  createContext,
  type ReactNode,
  useCallback,
  useContext,
  useEffect,
  useRef,
  useState,
} from "react";
import {
  isOperationSnapshotNewer,
  useOperationFollower,
} from "../../api/client/useOperationFollower";
import {
  listOperations,
  retryOperation,
  startSourceScan,
} from "../../api/generated/client";
import type { OperationResponse } from "../../api/generated/client.schemas";
import { incomingGroupCountQueryKey } from "../incoming/incomingGroupsQueryKeys";
import {
  message,
  type SourceError,
  sourceError,
  sourceMessageFailure,
  sourceResponseError,
} from "./sourcesApi";

const pending = (scan: OperationResponse | undefined) =>
  scan?.state === "queued" || scan?.state === "running";

type AcceptAction = (scan: OperationResponse) => boolean;
type Scans = {
  checking: boolean;
  discoveryError: SourceError | null;
  revision: number;
  rootRevision: (root: string) => number;
  operation: (root: string) => OperationResponse | undefined;
  busy: (root: string) => boolean;
  scanning: (root: string) => boolean;
  error: (root: string) => SourceError | null;
  streamError: (root: string) => boolean;
  start: (root: string) => Promise<void>;
  retry: (root: string) => Promise<void>;
  discover: () => Promise<void>;
};

const ScansContext = createContext<Scans | null>(null);

export function useSourceScans(): Scans {
  const scans = useContext(ScansContext);
  if (!scans) throw new Error("Source scans must have a provider");
  return scans;
}

function scanFailure(response: { status: number; data?: { detail?: string } }) {
  if (
    response.status === 409 &&
    response.data?.detail === "source root has an active scan"
  )
    return sourceMessageFailure("scanAlreadyRunning");
  if (response.status === 404) return sourceMessageFailure("sourceMissing");
  if (response.status === 400) return sourceMessageFailure("scanRejected");
  return sourceResponseError(
    response,
    sourceError("serverError", { status: response.status }),
  );
}

function followsRoot(scan: OperationResponse, root: string) {
  // Terminal delivery releases the active root selector. For a followed ID
  // or a root-scoped POST, the caller already knows the owning root.
  return (
    scan.kind === "scan_source" &&
    (scan.target_source_root_id === root ||
      (scan.target_source_root_id === undefined && !pending(scan)))
  );
}

function ScanFollower({
  root,
  scan,
  onSnapshot,
  onConnection,
  register,
}: {
  root: string;
  scan: OperationResponse;
  onSnapshot: (root: string, scan: OperationResponse) => void;
  onConnection: (root: string, id: string, failed: boolean) => void;
  register: (id: string, accept: AcceptAction) => () => void;
}) {
  const { acceptSnapshot } = useOperationFollower(scan.id, {
    enabled: pending(scan),
    initialSnapshot: scan,
    onSnapshot: (next) => onSnapshot(root, next),
    onSnapshotRead: (_next, source) => {
      if (source === "stream") onConnection(root, scan.id, false);
    },
    onError: () => onConnection(root, scan.id, true),
    onStreamError: () => onConnection(root, scan.id, true),
    refreshOnStreamError: true,
  });
  useEffect(
    () => register(scan.id, (next) => acceptSnapshot(next, "action", true)),
    [scan.id, acceptSnapshot, register],
  );
  return null;
}

export function SourceScansProvider({ children }: { children: ReactNode }) {
  const queryClient = useQueryClient();
  const [operations, setOperations] = useState<Map<string, OperationResponse>>(
    () => new Map(),
  );
  const tracked = useRef(operations);
  const [actions, setActions] = useState<ReadonlySet<string>>(() => new Set());
  const inFlight = useRef(new Set<string>());
  const [errors, setErrors] = useState<Record<string, SourceError | null>>({});
  const [connectionErrors, setConnectionErrors] = useState<
    Record<string, boolean>
  >({});
  const [revisions, setRevisions] = useState<Record<string, number>>({});
  const [revision, setRevision] = useState(0);
  const [checking, setChecking] = useState(true);
  const [discoveryError, setDiscoveryError] = useState<SourceError | null>(
    null,
  );
  const discovery = useRef<AbortController | null>(null);
  const discoveryReady = useRef(false);
  const reconcileRoots = useRef(new Set<string>());
  const accepts = useRef(new Map<string, AcceptAction>());
  const mounted = useRef(false);

  const publish = useCallback((next: Map<string, OperationResponse>) => {
    tracked.current = next;
    setOperations(next);
  }, []);

  const refreshRoot = useCallback(
    (root: string) => {
      setRevisions((value) => ({ ...value, [root]: (value[root] ?? 0) + 1 }));
      setRevision((value) => value + 1);
      void queryClient.invalidateQueries({
        queryKey: incomingGroupCountQueryKey(),
        exact: true,
      });
    },
    [queryClient],
  );

  const discover = useCallback(async () => {
    discovery.current?.abort();
    const request = new AbortController();
    discovery.current = request;
    discoveryReady.current = false;
    const before = tracked.current;
    setChecking(true);
    setDiscoveryError(null);
    try {
      // One discovery for the whole Sources screen, not a query per directory.
      const response = await listOperations(
        { active_source_scans: true },
        { cache: "no-store", signal: request.signal },
      );
      if (request.signal.aborted) return;
      if (response.status !== 200) {
        setDiscoveryError(
          sourceError("discoverScanError", { status: response.status }),
        );
        return;
      }
      for (const scan of response.data.operations ?? []) {
        const root = scan.target_source_root_id;
        if (
          !root ||
          !followsRoot(scan, root) ||
          !pending(scan) ||
          inFlight.current.has(root)
        )
          continue;
        const previous = tracked.current.get(root);
        // The follower owns active snapshots. Ignore roots that changed while
        // discovery was in flight, but adopt work admitted elsewhere since a
        // locally tracked scan finished (including a retry of the same ID).
        if (before.get(root) !== previous || pending(previous)) continue;
        if (!isOperationSnapshotNewer(previous, scan)) continue;
        if (previous?.id !== scan.id || !accepts.current.get(scan.id)?.(scan)) {
          publish(new Map(tracked.current).set(root, scan));
        }
        setConnectionErrors((current) => ({ ...current, [root]: false }));
        setErrors((current) => ({ ...current, [root]: null }));
      }
      discoveryReady.current = true;
    } catch (reason) {
      if (!request.signal.aborted) setDiscoveryError(message(reason));
    } finally {
      if (!request.signal.aborted) {
        for (const root of reconcileRoots.current) {
          if (!pending(tracked.current.get(root))) refreshRoot(root);
        }
        reconcileRoots.current.clear();
        setChecking(false);
      }
    }
  }, [publish, refreshRoot]);

  useEffect(() => {
    mounted.current = true;
    void discover();
    return () => {
      mounted.current = false;
      discoveryReady.current = false;
      discovery.current?.abort();
    };
  }, [discover]);

  const onSnapshot = useCallback(
    (root: string, scan: OperationResponse) => {
      const previous = tracked.current.get(root);
      if (previous?.id !== scan.id || !followsRoot(scan, root)) return;
      publish(new Map(tracked.current).set(root, scan));
      if (pending(previous) && !pending(scan)) refreshRoot(root);
    },
    [publish, refreshRoot],
  );

  const onConnection = useCallback(
    (root: string, id: string, failed: boolean) => {
      const scan = tracked.current.get(root);
      if (scan?.id !== id || (failed && !pending(scan))) return;
      setConnectionErrors((current) =>
        current[root] === failed ? current : { ...current, [root]: failed },
      );
    },
    [],
  );

  const register = useCallback((id: string, accept: AcceptAction) => {
    accepts.current.set(id, accept);
    return () => {
      accepts.current.delete(id);
    };
  }, []);

  async function act(root: string, retry: boolean) {
    const previous = tracked.current.get(root);
    if (
      !discoveryReady.current ||
      pending(previous) ||
      inFlight.current.has(root)
    )
      return;
    if (retry && previous?.state !== "failed") return;
    inFlight.current.add(root);
    setActions(new Set(inFlight.current));
    setErrors((current) => ({ ...current, [root]: null }));
    try {
      const response =
        retry && previous
          ? await retryOperation(previous.id)
          : await startSourceScan(root);
      if (!mounted.current) return;
      if (response.status !== 200) throw scanFailure(response);
      const scan = response.data;
      if (!followsRoot(scan, root) || (retry && scan.id !== previous?.id)) {
        throw sourceMessageFailure("scanRejected");
      }
      if (previous?.id !== scan.id || !accepts.current.get(scan.id)?.(scan)) {
        publish(new Map(tracked.current).set(root, scan));
      }
      if (!pending(scan)) refreshRoot(root);
      setConnectionErrors((current) => ({ ...current, [root]: false }));
    } catch (reason) {
      if (!mounted.current) return;
      setErrors((current) => ({ ...current, [root]: message(reason) }));
      // Follow an admitted scan if it is still active. Otherwise reread the
      // root after discovery so a fast scan with a lost reply isn't missed.
      reconcileRoots.current.add(root);
      inFlight.current.delete(root);
      void discover();
    } finally {
      inFlight.current.delete(root);
      if (mounted.current) setActions(new Set(inFlight.current));
    }
  }

  return (
    <ScansContext.Provider
      value={{
        checking,
        discoveryError,
        revision,
        rootRevision: (root) => revisions[root] ?? 0,
        operation: (root) => operations.get(root),
        busy: (root) => actions.has(root),
        scanning: (root) => actions.has(root) || pending(operations.get(root)),
        error: (root) => errors[root] ?? null,
        streamError: (root) => connectionErrors[root] ?? false,
        start: (root) => act(root, false),
        retry: (root) => act(root, true),
        discover,
      }}
    >
      {children}
      {[...operations].map(([root, scan]) => (
        <ScanFollower
          key={scan.id}
          root={root}
          scan={scan}
          onSnapshot={onSnapshot}
          onConnection={onConnection}
          register={register}
        />
      ))}
    </ScansContext.Provider>
  );
}
