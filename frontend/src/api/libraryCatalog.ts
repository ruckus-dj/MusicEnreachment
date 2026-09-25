import type { LyricsStatus, PublicationReconciliationJob, Summary } from "../types";
import { api } from "./request";

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
export type MetadataRefreshResult = { readonly queued: number };
export type LibraryStatus = {
  readonly total_track_count: number;
  readonly has_analysis: boolean;
};
export type ManualActionCounts = {
  readonly analysis_error: number;
  readonly needs_review: number;
};

export function getLibraryStatus(): Promise<LibraryStatus> {
  return api<LibraryStatus>("/api/library/status");
}

export function refreshLibraryMetadata(): Promise<MetadataRefreshResult> {
  return api<MetadataRefreshResult>("/api/library/metadata/refresh", { method: "POST" });
}

export function startPublicationReconciliation(): Promise<PublicationReconciliationJob> {
  return api<PublicationReconciliationJob>("/api/reconciliation/publications", { method: "POST" });
}

export function getPublicationReconciliation(jobId: string): Promise<PublicationReconciliationJob> {
  return api<PublicationReconciliationJob>(
    `/api/reconciliation/publications/${encodeURIComponent(jobId)}`,
  );
}

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
  const suffix = params.size > 0 ? `?${params.toString()}` : "";
  return api<{ items: LibraryAlbum[] }>(`/api/library/albums${suffix}`);
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
  const suffix = params.size > 0 ? `?${params.toString()}` : "";
  return api<{ items: LibraryTrack[] }>(`/api/library/tracks${suffix}`);
}
