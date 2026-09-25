import type {
  EncodingApplied,
  EncodingDetail,
  EncodingPreview,
  EncodingRequest,
} from "../domain/sourceEncoding";
import type {
  CurrentStateCleanup,
  EffectiveSourceSelection,
  ManualSourceSelection,
  MusicBrainzCandidateLookup,
  MusicBrainzCandidateLookupResult,
  SourceRoot,
  SourceRootCandidateList,
  SourceRootCreate,
  SourceRootList,
  StorageBrowser,
  StorageConfig,
  StorageOutputPreview,
  WorkerQueue,
} from "../types";
import { api } from "./request";

export type {
  AlbumRemapApplyRequest,
  AlbumRemapApplyResult,
  AlbumRemapAssignment,
  AlbumRemapContext,
  AlbumRemapFile,
  AlbumRemapPreview,
  AlbumRemapPreviewRequest,
  AlbumRemapRelease,
  AlbumRemapReleaseSearch,
  AlbumRemapSelector,
  AlbumRemapSuggestion,
  AlbumRemapTrack,
} from "./albumRemap";
export {
  applyAlbumRemap,
  loadAlbumRemapContext,
  previewAlbumRemap,
  searchAlbumRemapReleases,
} from "./albumRemap";
export type {
  LibraryAlbum,
  LibraryArtist,
  LibraryStatus,
  LibraryTrack,
  ManualActionCounts,
  MetadataRefreshResult,
} from "./libraryCatalog";
export {
  getLibraryStatus,
  getPublicationReconciliation,
  listLibraryAlbums,
  listLibraryArtists,
  listLibraryTracks,
  listManualActions,
  refreshLibraryMetadata,
  removePublication,
  startPublicationReconciliation,
} from "./libraryCatalog";
export { ApiError, api } from "./request";

export function getSourceEncoding(sourceId: string, signal?: AbortSignal): Promise<EncodingDetail> {
  return api(`/api/sources/${encodeURIComponent(sourceId)}/encoding`, { signal });
}

export function previewSourceEncoding(
  sourceId: string,
  request: EncodingRequest,
  signal?: AbortSignal,
): Promise<EncodingPreview> {
  return api(`/api/sources/${encodeURIComponent(sourceId)}/encoding/preview`, {
    method: "POST",
    body: JSON.stringify(request),
    signal,
  });
}

export function applySourceEncoding(
  sourceId: string,
  request: EncodingRequest,
): Promise<EncodingApplied> {
  return api(`/api/sources/${encodeURIComponent(sourceId)}/encoding/apply`, {
    method: "POST",
    body: JSON.stringify(request),
  });
}

export function listSourceRoots(): Promise<SourceRootList> {
  return api<SourceRootList>("/api/settings/source-roots");
}

export function previewCurrentStateCleanup(): Promise<CurrentStateCleanup> {
  return api<CurrentStateCleanup>("/api/settings/maintenance/current-state/preview", {
    method: "POST",
  });
}

export function applyCurrentStateCleanup(): Promise<CurrentStateCleanup> {
  return api<CurrentStateCleanup>("/api/settings/maintenance/current-state/apply", {
    method: "POST",
  });
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

export function loadMusicBrainzCandidates(
  recordId: string,
  sourceId: string,
  request: MusicBrainzCandidateLookup,
): Promise<MusicBrainzCandidateLookupResult> {
  return api<MusicBrainzCandidateLookupResult>(
    `/api/library/records/${encodeURIComponent(recordId)}/sources/${encodeURIComponent(sourceId)}/musicbrainz/release-candidates`,
    { method: "POST", body: JSON.stringify(request) },
  );
}
