import {
  type ReactNode,
  useCallback,
  useEffect,
  useRef,
  useState,
} from "react";
import { listSourceLocations } from "../../api/generated/client";
import type {
  SourceLocationResponse,
  SourceRootResponse,
} from "../../api/generated/client.schemas";
import { useI18n } from "../../app/i18n/host";
import {
  SourceInventoryDataBinder,
  type SourceInventoryModel,
} from "../../ui/features/sources/SourceInventoryOutlet";
import type { SourceInventoryStatus } from "../../ui/features/sources/SourceInventoryView";
import { sourceMessage } from "../../ui/features/sources/sourceMessages";
import {
  inventoryAvailabilityNote,
  inventoryScopeNote,
  message,
  renderSourceError,
  sourceFailure,
} from "./sourcesApi";

// The server validates ?limit between 1 and 200, so a prop that drifts out of
// that range still produces a query the API accepts.
const MAX_PAGE_LIMIT = 200;
const DEFAULT_PAGE_LIMIT = 50;

export type SourceLocationsProps = {
  sourceId: string;
  root: SourceRootResponse;
  refreshToken?: string | number;
  limit?: number;
};

function pageLimit(limit?: number): number {
  if (limit === undefined) return DEFAULT_PAGE_LIMIT;
  return Math.min(MAX_PAGE_LIMIT, Math.max(1, Math.trunc(limit)));
}

function inventoryStatus(
  status: SourceLocationResponse["probe_status"],
): SourceInventoryStatus {
  switch (status) {
    case "audio":
    case "no_audio":
    case "not_analyzed":
    case "probe_error":
      return status;
    default:
      return "unknown";
  }
}

export function SourceLocations({
  sourceId,
  root,
  refreshToken,
  limit,
  children,
}: SourceLocationsProps & { children: ReactNode }) {
  const { locale } = useI18n();
  const size = pageLimit(limit);
  const generation = root.scan_generation;
  // Another source, generation, page size or refresh token starts page 1 again.
  const inventoryKey = `${sourceId}\u0000${generation}\u0000${size}\u0000${String(
    refreshToken ?? "",
  )}`;
  const loadedKey = useRef<string | undefined>(undefined);
  const requestEpoch = useRef(0);
  const [locations, setLocations] = useState<SourceLocationResponse[]>([]);
  const [nextCursor, setNextCursor] = useState<string | undefined>(undefined);
  const [loaded, setLoaded] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<ReturnType<typeof message> | null>(null);

  const loadFirstPage = useCallback(async () => {
    const epoch = ++requestEpoch.current;
    setLocations([]);
    setNextCursor(undefined);
    setLoaded(false);
    setError(null);
    setBusy(true);
    try {
      const response = await listSourceLocations(
        sourceId,
        { limit: size },
        { cache: "no-store" },
      );
      if (requestEpoch.current !== epoch) return;
      if (response.status !== 200) throw sourceFailure("read", response);
      setLocations(response.data.locations || []);
      setNextCursor(response.data.next_cursor);
      setLoaded(true);
    } catch (reason) {
      if (requestEpoch.current !== epoch) return;
      setError(message(reason));
      setLoaded(true);
    } finally {
      if (requestEpoch.current === epoch) setBusy(false);
    }
  }, [sourceId, size]);

  useEffect(() => {
    if (loadedKey.current === inventoryKey) return;
    loadedKey.current = inventoryKey;
    if (generation === 0) {
      // Generation 0 holds no published inventory: only applied scans are listed.
      requestEpoch.current += 1;
      setLocations([]);
      setNextCursor(undefined);
      setLoaded(false);
      setError(null);
      setBusy(false);
      return;
    }
    void loadFirstPage();
  }, [inventoryKey, generation, loadFirstPage]);

  async function loadMore() {
    const cursor = nextCursor;
    if (!cursor) return;
    const epoch = requestEpoch.current;
    setBusy(true);
    setError(null);
    try {
      const response = await listSourceLocations(
        sourceId,
        { limit: size, cursor },
        { cache: "no-store" },
      );
      if (requestEpoch.current !== epoch) return;
      if (response.status !== 200) throw sourceFailure("read", response);
      setLocations((current) => [
        ...current,
        ...(response.data.locations || []),
      ]);
      setNextCursor(response.data.next_cursor);
    } catch (reason) {
      if (requestEpoch.current !== epoch) return;
      setError(message(reason));
    } finally {
      if (requestEpoch.current === epoch) setBusy(false);
    }
  }

  // A failed later page keeps its loaded rows and continues from its cursor.
  function retry() {
    if (locations.length === 0) void loadFirstPage();
    else void loadMore();
  }

  const notes = [
    inventoryAvailabilityNote(root, locale),
    inventoryScopeNote(root, locale),
  ].filter(Boolean);

  const model: SourceInventoryModel = {
    path: root.inventory_path ?? root.configured_path,
    unpublished: generation === 0,
    notes,
    loading: !loaded,
    error: error ? renderSourceError(error, locale) : undefined,
    onRetry: retry,
    rows: locations.map((location) => ({
      id: location.id,
      path: location.relative_path,
      href: `#/sources/${encodeURIComponent(sourceId)}/locations/${encodeURIComponent(location.id)}`,
      sizeBytes: location.size_bytes,
      modifiedAt: location.mtime,
      status: inventoryStatus(location.probe_status),
      notes:
        location.probe_status === "no_audio"
          ? [sourceMessage(locale, "noAudioReason")]
          : location.probe_status === "probe_error"
            ? [
                ...(location.safe_error ? [location.safe_error] : []),
                sourceMessage(locale, "probeRetryNote"),
              ]
            : undefined,
    })),
    loadingMore: busy,
    onLoadMore: nextCursor ? () => void loadMore() : undefined,
  };

  return (
    <SourceInventoryDataBinder model={model}>
      {children}
    </SourceInventoryDataBinder>
  );
}
