import { useCallback, useEffect, useRef, useState } from "react";
import { useOperationFollower } from "../../api/client/useOperationFollower";
import { startSetupGenreCatalogRefresh } from "../../api/generated/client";
import type {
  OperationResponse,
  SetupStateBody,
} from "../../api/generated/client.schemas";
import type { SetupGenreCatalogState } from "../../ui/features/setup/SetupGenreCatalogView";
import { setupHttpError } from "../../ui/features/setup/setupMessages";

const pollIntervalMs = 2000;

function operationPending(operation: OperationResponse) {
  return operation.state === "queued" || operation.state === "running";
}

function settledCatalogState(state: SetupStateBody): SetupGenreCatalogState {
  if (state.genre_catalog.ready) return "ready";
  // A ready label without the matching proof must not unlock the wizard.
  return state.genre_catalog.status === "ready"
    ? "failed"
    : state.genre_catalog.status;
}

/**
 * Owns the setup-only genre catalog proof. The catalog is loaded explicitly
 * for the verified MusicBrainz configuration identity; while a local operation
 * is followed, or when a reload observes the server already loading, the
 * snapshot is refreshed until it settles.
 */
export function useSetupGenreCatalogProvider({
  state,
  enabled,
  ensureProvider,
  refresh,
  onError,
}: {
  state: SetupStateBody;
  enabled: boolean;
  ensureProvider: () => Promise<string>;
  refresh: () => Promise<SetupStateBody>;
  onError: (reason: unknown) => void;
}) {
  const [operationId, setOperationId] = useState<string>();
  const [starting, setStarting] = useState(false);
  const startingRef = useRef(false);
  const reading = useRef(false);
  const handlers = useRef({ ensureProvider, refresh, onError });
  handlers.current = { ensureProvider, refresh, onError };

  const catalog = state.genre_catalog;

  const sync = useCallback(async () => {
    if (reading.current) return;
    reading.current = true;
    try {
      const fresh = await handlers.current.refresh();
      if (fresh.genre_catalog.status !== "loading") setOperationId(undefined);
    } catch (reason) {
      handlers.current.onError(reason);
    } finally {
      reading.current = false;
    }
  }, []);

  const onSnapshot = useCallback(
    (snapshot: OperationResponse) => {
      if (!operationPending(snapshot)) void sync();
    },
    [sync],
  );

  useOperationFollower(operationId, {
    enabled,
    onSnapshot,
    onError: (reason) => handlers.current.onError(reason),
  });

  const load = useCallback(() => {
    if (
      !enabled ||
      startingRef.current ||
      operationId ||
      catalog.status === "loading"
    )
      return;
    startingRef.current = true;
    setStarting(true);
    void (async () => {
      try {
        const identity = await handlers.current.ensureProvider();
        const response = await startSetupGenreCatalogRefresh({
          configuration_identity: identity,
        });
        if (response.status !== 202) throw setupHttpError(response);
        setOperationId(response.data.id);
      } catch (reason) {
        handlers.current.onError(reason);
        // A transport error can arrive after the server queued the refresh.
        await sync();
      } finally {
        startingRef.current = false;
        setStarting(false);
      }
    })();
  }, [catalog.status, enabled, operationId, sync]);

  // Recover an in-progress download after a reload or a lost operation wake-up.
  const polling =
    enabled && (operationId !== undefined || catalog.status === "loading");
  useEffect(() => {
    if (!polling) return;
    const timer = setInterval(() => void sync(), pollIntervalMs);
    return () => clearInterval(timer);
  }, [polling, sync]);

  const loading =
    starting || operationId !== undefined || catalog.status === "loading";
  return {
    state: loading ? "loading" : settledCatalogState(state),
    count: catalog.ready ? catalog.count : undefined,
    load,
  };
}
