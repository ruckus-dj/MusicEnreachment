import { api } from "./request";

export type AlbumRemapSelector = {
  readonly release_mbid: string | null;
  readonly artist_name: string | null;
  readonly album_name: string | null;
  readonly artist_missing: boolean;
  readonly album_missing: boolean;
};

export type AlbumRemapFile = {
  readonly source_id: string;
  readonly record_id: string;
  readonly path: string;
  readonly sha256: string;
  readonly duration_seconds: number | null;
  readonly source_metadata_revision: number;
  readonly current_recording_mbid: string | null;
  readonly current_release_mbid: string | null;
  readonly tags: Readonly<Record<string, string>>;
};

export type AlbumRemapContext = {
  readonly album_snapshot_token: string;
  readonly files: readonly AlbumRemapFile[];
};

export type AlbumRemapRelease = {
  readonly release_mbid: string;
  readonly title: string;
  readonly artist_credit: string;
  readonly disambiguation?: string | null;
  readonly date: string | null;
  readonly country: string | null;
  readonly status?: string | null;
  readonly medium_count?: number | null;
  readonly track_count?: number | null;
};

export type AlbumRemapReleaseSearch = {
  readonly items: readonly AlbumRemapRelease[];
};

export type AlbumRemapTrack = {
  readonly track_mbid: string;
  readonly recording_mbid: string;
  readonly medium_position: number;
  readonly track_position: number;
  readonly title: string;
  readonly recording_title: string;
  readonly artist_credit: string;
  readonly duration_seconds: number | null;
  readonly data_track: boolean;
  readonly assignable: boolean;
};

export type AlbumRemapSuggestion = {
  readonly source_id: string;
  readonly track_mbid: string;
};

export type AlbumRemapPreview = {
  readonly album_snapshot_token: string;
  readonly release_snapshot_token: string;
  readonly release: AlbumRemapRelease;
  readonly tracks: readonly AlbumRemapTrack[];
  readonly suggestions: readonly AlbumRemapSuggestion[];
  readonly unmatched_source_ids: readonly string[];
};

export type AlbumRemapAssignment = {
  readonly source_id: string;
  readonly track_mbid: string;
};

export type AlbumRemapPreviewRequest = {
  readonly selector: AlbumRemapSelector;
  readonly album_snapshot_token: string;
  readonly release_mbid: string;
};

export type AlbumRemapApplyRequest = AlbumRemapPreviewRequest & {
  readonly release_snapshot_token: string;
  readonly assignments: readonly AlbumRemapAssignment[];
  readonly unmatched_source_ids: readonly string[];
};

export type AlbumRemapApplyResult = {
  readonly release_mbid: string;
  readonly applied: boolean;
  readonly untouched_source_ids: readonly string[];
  readonly affected_record_ids: readonly string[];
  readonly publication_refresh_queued: boolean;
};

type AlbumRemapSourceResponse = {
  readonly source_id: string;
  readonly record_id: string;
  readonly path: string;
  readonly sha256: string;
  readonly duration_seconds: number | null;
  readonly source_metadata_revision: number;
  readonly recording_mbid: string | null;
  readonly release_mbid: string | null;
  readonly canonical_tags: Readonly<Record<string, string>>;
};

type AlbumRemapContextResponse = {
  readonly album_snapshot_token: string;
  readonly sources: readonly AlbumRemapSourceResponse[];
};

type AlbumRemapReleaseSearchResponse = {
  readonly releases: readonly AlbumRemapRelease[];
};

type AlbumRemapTrackSlotResponse = {
  readonly track_mbid: string;
  readonly recording_mbid: string;
  readonly medium_position: number;
  readonly track_position: number;
  readonly title: string;
  readonly artist_credit: string;
  readonly duration_seconds: number | null;
  readonly assignable: boolean;
  readonly suggested_source_id: string | null;
};

type AlbumRemapPreviewResponse = {
  readonly release_snapshot_token: string;
  readonly release: AlbumRemapRelease;
  readonly track_slots: readonly AlbumRemapTrackSlotResponse[];
};

type AlbumRemapApplyResponse = {
  readonly assigned_source_ids: readonly string[];
  readonly unmatched_source_ids: readonly string[];
  readonly publication_refresh_queued: boolean;
  readonly queued_release_artwork: boolean;
};

export async function loadAlbumRemapContext(
  selector: AlbumRemapSelector,
): Promise<AlbumRemapContext> {
  const response = await api<AlbumRemapContextResponse>("/api/library/album-remaps/context", {
    method: "POST",
    body: JSON.stringify({ selector }),
  });
  return {
    album_snapshot_token: response.album_snapshot_token,
    files: response.sources.map((source) => ({
      source_id: source.source_id,
      record_id: source.record_id,
      path: source.path,
      sha256: source.sha256,
      duration_seconds: source.duration_seconds,
      source_metadata_revision: source.source_metadata_revision,
      current_recording_mbid: source.recording_mbid,
      current_release_mbid: source.release_mbid,
      tags: source.canonical_tags,
    })),
  };
}

export async function searchAlbumRemapReleases(query: string): Promise<AlbumRemapReleaseSearch> {
  const response = await api<AlbumRemapReleaseSearchResponse>(
    "/api/library/album-remaps/releases/search",
    {
      method: "POST",
      body: JSON.stringify({ query }),
    },
  );
  return { items: response.releases };
}

export async function previewAlbumRemap(
  request: AlbumRemapPreviewRequest,
): Promise<AlbumRemapPreview> {
  const response = await api<AlbumRemapPreviewResponse>("/api/library/album-remaps/preview", {
    method: "POST",
    body: JSON.stringify(request),
  });
  return {
    album_snapshot_token: request.album_snapshot_token,
    release_snapshot_token: response.release_snapshot_token,
    release: response.release,
    tracks: response.track_slots.map((slot) => ({
      track_mbid: slot.track_mbid,
      recording_mbid: slot.recording_mbid,
      medium_position: slot.medium_position,
      track_position: slot.track_position,
      title: slot.title,
      recording_title: slot.title,
      artist_credit: slot.artist_credit,
      duration_seconds: slot.duration_seconds,
      data_track: !slot.assignable,
      assignable: slot.assignable,
    })),
    suggestions: response.track_slots.flatMap((slot) =>
      slot.suggested_source_id === null
        ? []
        : [{ source_id: slot.suggested_source_id, track_mbid: slot.track_mbid }],
    ),
    unmatched_source_ids: [],
  };
}

export async function applyAlbumRemap(
  request: AlbumRemapApplyRequest,
): Promise<AlbumRemapApplyResult> {
  const response = await api<AlbumRemapApplyResponse>("/api/library/album-remaps/apply", {
    method: "POST",
    body: JSON.stringify(request),
  });
  return {
    release_mbid: request.release_mbid,
    applied: true,
    untouched_source_ids: response.unmatched_source_ids,
    affected_record_ids: [],
    publication_refresh_queued: response.publication_refresh_queued,
  };
}
