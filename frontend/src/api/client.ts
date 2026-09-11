import { errorMessages } from "../errorMessages";
import type {
  EffectiveSourceSelection,
  LyricsStatus,
  ManualSourceSelection,
  RecordingCorrection,
  RecordingCorrectionResult,
  SourceRoot,
  SourceRootCandidateList,
  SourceRootCreate,
  SourceRootList,
  StorageBrowser,
  StorageConfig,
  StorageOutputPreview,
  Summary,
  WorkerQueue,
} from "../types";

export type LibraryArtist = { readonly name: string | null; readonly track_count: number };
export type LibraryAlbum = {
  readonly album_id: string | null;
  readonly album_name: string | null;
  readonly track_count: number;
  readonly artwork_url?: string | null;
};
export type LibraryTrack = {
  readonly record_id: string;
  readonly source_id: string;
  readonly source_path: string;
  readonly artist_name: string | null;
  readonly album_name: string | null;
  readonly album_id: string | null;
  readonly title: string;
  readonly track_number: string | null;
  readonly source_state: string;
  readonly processing_state: string;
  readonly match_state: string;
  readonly publication_state: string;
  readonly lyrics_status: LyricsStatus;
  readonly lyrics_synced: boolean;
};

export class ApiError extends Error {
  readonly status: number;

  constructor(status: number, message: string) {
    super(message);
    this.name = "ApiError";
    this.status = status;
  }
}

export async function api<T>(path: string, options?: RequestInit): Promise<T> {
  const response = await fetch(path, {
    headers: { "content-type": "application/json" },
    ...options,
  });
  const payload: unknown = await response.json().catch(() => ({}));
  if (!response.ok) {
    const detail =
      typeof payload === "object" && payload !== null && "detail" in payload
        ? payload.detail
        : null;
    throw new ApiError(
      response.status,
      typeof detail === "string" ? detail : errorMessages.requestFailed,
    );
  }
  return payload as T;
}

export function listSourceRoots(): Promise<SourceRootList> {
  return api<SourceRootList>("/api/settings/source-roots");
}

export function createSourceRoot(request: SourceRootCreate): Promise<SourceRoot> {
  return api<SourceRoot>("/api/settings/source-roots", {
    method: "POST",
    body: JSON.stringify(request),
  });
}

export function listSourceRootCandidates(): Promise<SourceRootCandidateList> {
  return api<SourceRootCandidateList>("/api/settings/source-roots/candidates");
}

export async function removeSourceRoot(rootId: string): Promise<void> {
  await api<unknown>(`/api/settings/source-roots/${encodeURIComponent(rootId)}`, {
    method: "DELETE",
  });
}

export function browseStorage(path?: string): Promise<StorageBrowser> {
  const query = path ? `?path=${encodeURIComponent(path)}` : "";
  return api<StorageBrowser>(`/api/settings/storage/browser${query}`);
}

export function listLibraryRecords(): Promise<{ items: Summary[] }> {
  return api<{ items: Summary[] }>("/api/library/records");
}

export type ManualActionCounts = {
  readonly analysis_error: number;
  readonly needs_review: number;
};

export function listManualActions(
  action: "analysis-error" | "needs-review",
): Promise<{ items: Summary[]; counts: ManualActionCounts }> {
  return api<{ items: Summary[]; counts: ManualActionCounts }>(
    `/api/library/manual-actions?action=${encodeURIComponent(action)}`,
  );
}

export function listLibraryArtists(
  published?: boolean,
): Promise<{ items: LibraryArtist[]; total_track_count: number }> {
  const suffix = published === undefined ? "" : `?published=${String(published)}`;
  return api<{ items: LibraryArtist[]; total_track_count: number }>(
    `/api/library/artists${suffix}`,
  );
}

export function listLibraryAlbums(
  artistName: string | null,
  published?: boolean,
  artistMissing = false,
): Promise<{ items: LibraryAlbum[] }> {
  const params = new URLSearchParams();
  if (artistName !== null) params.set("artist", artistName);
  if (artistMissing) params.set("artist_missing", "true");
  if (published !== undefined) params.set("published", String(published));
  return api<{ items: LibraryAlbum[] }>(`/api/library/albums?${params.toString()}`);
}

export function listLibraryTracks(
  artistName: string | null,
  albumId?: string,
  albumName?: string,
  published?: boolean,
  artistMissing = false,
  albumMissing = false,
): Promise<{ items: LibraryTrack[] }> {
  const params = new URLSearchParams();
  if (artistName !== null) params.set("artist", artistName);
  if (artistMissing) params.set("artist_missing", "true");
  if (albumMissing) params.set("album_missing", "true");
  if (albumId) params.set("album_id", albumId);
  if (albumName) params.set("album_name", albumName);
  if (published !== undefined) params.set("published", String(published));
  return api<{ items: LibraryTrack[] }>(`/api/library/tracks?${params.toString()}`);
}

export function getStorageConfig(): Promise<StorageConfig> {
  return api<StorageConfig>("/api/settings/storage");
}

export function getWorkerQueue(): Promise<WorkerQueue> {
  return api<WorkerQueue>("/api/workers/queue");
}

export function previewStorageOutput(path: string): Promise<StorageOutputPreview> {
  return api<StorageOutputPreview>("/api/settings/storage/output/preview", {
    method: "POST",
    body: JSON.stringify({ path }),
  });
}

export function moveStorageOutput(path: string): Promise<StorageConfig> {
  return api<StorageConfig>("/api/settings/storage/output", {
    method: "PUT",
    body: JSON.stringify({ path }),
  });
}

export function selectEffectiveSource(
  recordId: string,
  request: ManualSourceSelection,
): Promise<EffectiveSourceSelection> {
  return api<EffectiveSourceSelection>(
    `/api/library/records/${encodeURIComponent(recordId)}/effective-source`,
    { method: "POST", body: JSON.stringify(request) },
  );
}

export function submitRecordingCorrection(
  recordId: string,
  sourceId: string,
  request: RecordingCorrection,
): Promise<RecordingCorrectionResult> {
  return api<RecordingCorrectionResult>(
    `/api/library/records/${encodeURIComponent(recordId)}/sources/${encodeURIComponent(sourceId)}/musicbrainz/override`,
    { method: "POST", body: JSON.stringify(request) },
  );
}
