export interface WorkerPoolSettings {
  readonly filesystem_scan: number;
  readonly acoustid_analysis: number;
  readonly musicbrainz_analysis: number;
  readonly candidate_selection: number;
  readonly folder_release_selection: number;
  readonly final_publish: number;
  readonly selection_refresh: number;
  readonly lrclib_fetch: number;
  readonly artwork_enrichment: number;
  readonly reconciliation_scan: number;
}

export type Tags = Record<string, string>;
export type ProviderName = "acoustid" | "musicbrainz";
export type Revision = {
  readonly id?: number;
  readonly source_id: string;
  readonly layer: string;
  readonly revision: number;
  readonly tags: Tags;
  readonly actor?: string;
  readonly created_at?: string;
};
export type Candidate = {
  readonly candidate_key: string;
  readonly evidence: {
    readonly provider: ProviderName;
    readonly entity?: "recording" | "recording_release";
    readonly recording_mbid?: string;
    readonly release_mbid?: string;
    readonly compatible_ids?: readonly string[];
    readonly artist: string;
    readonly release: string;
    readonly disambiguation?: string | null;
    readonly title?: string;
    readonly album?: string;
    readonly score: number | null;
    readonly duration_seconds?: number | null;
    readonly acoustid_score?: number | null;
    readonly musicbrainz_score?: number | null;
    readonly score_components?: {
      readonly artist: number;
      readonly release: number;
      readonly duration: number;
      readonly title: number;
      readonly track: number;
      readonly track_number?: number | null;
      readonly track_total?: number | null;
      readonly disc_number?: number | null;
      readonly disc_total?: number | null;
      readonly musicbrainz?: number | null;
      readonly acoustid?: number | null;
      readonly artist_match?: number | null;
      readonly release_match?: number | null;
      readonly duration_match?: number | null;
      readonly title_match?: number | null;
      readonly track_number_match?: number | null;
      readonly track_total_match?: number | null;
      readonly disc_number_match?: number | null;
      readonly disc_total_match?: number | null;
      readonly musicbrainz_match?: number | null;
      readonly acoustid_match?: number | null;
      readonly recording_artist?: number | null;
      readonly release_artist?: number | null;
      readonly recording_artist_match?: number | null;
      readonly release_artist_match?: number | null;
    } | null;
    readonly tags: Tags;
  };
};
export type Source = {
  readonly source_id: string;
  readonly path: string;
  readonly format?: string;
  readonly sha256: string;
  readonly state: string;
  readonly disappeared_at?: string | null;
  readonly origin?: string;
  readonly size_bytes?: number;
  readonly duration_seconds?: number | null;
  readonly tag_observations?: readonly {
    readonly name: string;
    readonly value: string;
    readonly format: string;
  }[];
  readonly fingerprints?: readonly {
    readonly state: string;
    readonly fingerprint: string | null;
    readonly duration_seconds: number | null;
    readonly tool_version: string | null;
  }[];
  readonly provider_attempts?: readonly {
    readonly provider: string;
    readonly outcome: string;
    readonly snapshot_sha256: string;
    readonly created_at?: string;
  }[];
  readonly candidates?: readonly Candidate[];
  readonly review_decisions?: readonly {
    readonly state: string;
    readonly rationale: string;
  }[];
};
export type Publication = {
  readonly publication_id: string;
  readonly path: string;
  readonly source_id: string;
  readonly format?: string;
  readonly sha256?: string;
  readonly metadata_revision_id?: number | null;
  readonly state: string;
  readonly created_at?: string;
};
export type DestinationConflict = {
  readonly path: string;
  readonly ownership: "managed" | "unmanaged";
  readonly reason: string;
};
export type Summary = {
  readonly record_id: string;
  readonly musicbrainz_recording_id?: string | null;
  readonly musicbrainz_release_id?: string | null;
  readonly musicbrainz_artist_id?: string | null;
  readonly source_state: string;
  readonly processing_state: string;
  readonly match_state: string;
  readonly publication_state: string;
  readonly metadata_state: string;
  readonly artwork?: {
    readonly url: string;
    readonly state: string;
  } | null;
  readonly sources: readonly Source[];
  readonly publications: readonly Publication[];
  readonly metadata_revisions?: readonly Revision[];
};
export type Event = {
  readonly kind: string;
  readonly state: string;
  readonly reason: string | null;
  readonly details: Record<string, unknown>;
  readonly source_id?: string | null;
  readonly created_at: string;
};
export type LyricsStatus =
  | "none"
  | "pending"
  | "synced"
  | "no_candidate"
  | "validation_rejected"
  | "error";
export type Detail = Summary & {
  readonly states: {
    readonly source: string;
    readonly processing: string;
    readonly match: string;
    readonly publication: string;
    readonly metadata: string;
  };
  readonly lyrics_status: LyricsStatus;
  readonly lyrics_synced: boolean;
  readonly events: readonly Event[];
  readonly destination_conflict?: DestinationConflict | null;
};
export type ManualSourceSelection = {
  readonly source_id: string;
};
export type EffectiveSourceSelection = {
  readonly source_id: string | null;
  readonly baseline_source_id: string | null;
  readonly policy_version: string;
};
export type MusicBrainzCandidateLookup = {
  readonly recording_mbid: string;
  readonly release_mbid?: string;
};
export type MusicBrainzCandidateLookupResult = {
  readonly recording_mbid: string;
  readonly release_mbid: string | null;
  readonly status: "review_required";
  readonly candidate_count: number;
};
export type RecordingCorrection = {
  readonly recording_mbid: string;
  readonly release_mbid?: string;
};
export type RecordingCorrectionResult = {
  readonly recording_mbid: string;
  readonly record_id: string;
};
export type WorkerQueueJob = {
  readonly job_id: string;
  readonly kind: string;
  readonly state: "queued" | "running";
  readonly queue_state: "ready" | "retry_wait";
  readonly source_id: string | null;
  readonly library_record_id: string | null;
  readonly release_mbid: string | null;
  readonly created_at: string;
  readonly next_attempt_at: string | null;
  readonly attempt_count: number;
  readonly target: {
    readonly record_id: string;
    readonly source_id: string;
    readonly title: string;
    readonly artist: string;
    readonly album: string;
    readonly path: string;
  } | null;
};
export type WorkerQueue = {
  readonly observed_at: string;
  readonly worker: {
    readonly configured_concurrency: number;
    readonly liveness: "available" | "unavailable";
    readonly slots: readonly {
      readonly slot: number;
      readonly state: "disabled" | "error" | "idle" | "processing";
      readonly observed_at: string;
      readonly error: string | null;
      readonly job_id: string | null;
      readonly job_kind: string | null;
    }[];
  };
  readonly summary: {
    readonly running: number;
    readonly ready: number;
    readonly retry_wait: number;
  };
  readonly total_jobs: number;
  readonly jobs: readonly WorkerQueueJob[];
};
export type Screen =
  | "artists"
  | "albums"
  | "tracks"
  | "track"
  | "manual-actions"
  | "workers"
  | "settings";
export type Layer = "original" | "analyzed" | "final";
export type ManualActionFilter = "analysis-error" | "needs-review";
export type Route = {
  readonly screen: Screen;
  readonly artist?: string;
  readonly artistMissing?: boolean;
  readonly album?: string;
  readonly albumMissing?: boolean;
  readonly recordId?: string;
  readonly sourceId?: string;
  readonly publicationFilter?: "all" | "published" | "unpublished";
  readonly manualActionFilter?: ManualActionFilter;
};
export type WorkflowStatus = {
  readonly tone: "ready" | "pending" | "error";
  readonly label: string;
  readonly detail: string;
};
export type RuntimeSettings = {
  readonly confidence_threshold: number;
  readonly timeout_seconds: number;
  readonly retry_delay_seconds: number;
  readonly max_attempts: number;
  readonly worker_pools: WorkerPoolSettings;
  readonly musicbrainz_enabled: boolean;
  readonly musicbrainz_user_agent: string;
  readonly musicbrainz_host: string;
  readonly musicbrainz_request_delay_seconds: number;
  readonly acoustid_enabled: boolean;
  readonly acoustid_request_delay_seconds: number;
  readonly acoustid_client_key_configured: boolean;
  readonly artwork_enabled: boolean;
  readonly lrclib_enabled: boolean;
  readonly lrclib_host: string;
  readonly lrclib_user_agent: string;
  readonly lrclib_timeout_seconds: number;
  readonly lrclib_max_attempts: number;
  readonly lrclib_request_delay_seconds: number;
  readonly lrclib_max_response_bytes: number;
  readonly lrclib_match_confidence_threshold: number;
};
export type GenreCatalogItem = {
  readonly musicbrainz_id: string;
  readonly source_name: string;
  readonly display_name: string;
};
export type GenreCatalog = {
  readonly items: readonly GenreCatalogItem[];
  readonly last_synced_at: string | null;
};
export type SourceRoot = {
  readonly id: string;
  readonly display_name: string;
  readonly canonical_path: string;
  readonly enabled: boolean;
  readonly scan_state: string;
};
export type SourceRootList = {
  readonly items: readonly SourceRoot[];
};
export type SourceRootCandidate = {
  readonly name: string;
  readonly canonical_path: string;
};
export type SourceRootCandidateList = {
  readonly items: readonly SourceRootCandidate[];
};
export type SourceRootCreate = {
  readonly path: string;
  readonly display_name: string;
};
export type StorageBrowserItem = {
  readonly name: string;
  readonly path: string;
};
export type StorageBrowser = {
  readonly path: string;
  readonly parent_path: string | null;
  readonly items: readonly StorageBrowserItem[];
};
export type StorageConfig = {
  readonly output_root: string;
  readonly state: string;
  readonly generation: number;
};
export type StorageOutputPreview = {
  readonly output_root: string;
  readonly same_filesystem: boolean;
  readonly file_count: number;
};
export type RuntimeSettingsDraft = {
  readonly confidence_threshold: number;
  readonly timeout_seconds: number;
  readonly retry_delay_seconds: number;
  readonly max_attempts: number;
  readonly worker_pools: WorkerPoolSettings;
  readonly musicbrainz_enabled: boolean;
  readonly musicbrainz_user_agent: string;
  readonly musicbrainz_host: string;
  readonly musicbrainz_request_delay_seconds: number;
  readonly acoustid_enabled: boolean;
  readonly acoustid_request_delay_seconds: number;
  readonly acoustid_client_key: string;
  readonly artwork_enabled: boolean;
  readonly lrclib_enabled: boolean;
  readonly lrclib_host: string;
  readonly lrclib_user_agent: string;
  readonly lrclib_timeout_seconds: number;
  readonly lrclib_max_attempts: number;
  readonly lrclib_request_delay_seconds: number;
  readonly lrclib_max_response_bytes: number;
  readonly lrclib_match_confidence_threshold: number;
};
/**
 * Body of `PUT /api/settings`. The endpoint rejects partial bodies with 422, so the payload is
 * built field by field from a complete draft; every required field is listed here explicitly.
 */
export type RuntimeSettingsPayload = Omit<RuntimeSettingsDraft, "acoustid_client_key"> & {
  readonly acoustid_client_key: string | null;
};
export type WatchedRecord = {
  readonly recordId: string;
  readonly sourceId: string;
  readonly startedAt: number;
  readonly sawPending: boolean;
};
export type ScanResult = {
  readonly added: number;
  readonly changed: number;
  readonly moved: number;
  readonly removed: number;
  readonly unchanged: number;
  readonly queued_jobs: number;
};
export type ScanJob = {
  readonly job_id: string;
  readonly state: string;
  readonly result: ScanResult | null;
};
