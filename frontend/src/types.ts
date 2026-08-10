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
    readonly recording_mbid?: string;
    readonly artist: string;
    readonly release: string;
    readonly title?: string;
    readonly album?: string;
    readonly score: number | null;
    readonly tags: Tags;
  };
};
export type Source = {
  readonly source_id: string;
  readonly path: string;
  readonly format?: string;
  readonly sha256: string;
  readonly state: string;
  readonly origin?: string;
  readonly size_bytes?: number;
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
  readonly source_state: string;
  readonly processing_state: string;
  readonly match_state: string;
  readonly publication_state: string;
  readonly metadata_state: string;
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
export type Detail = Summary & {
  readonly states: {
    readonly source: string;
    readonly processing: string;
    readonly match: string;
    readonly publication: string;
    readonly metadata: string;
  };
  readonly events: readonly Event[];
  readonly destination_conflict?: DestinationConflict | null;
};
export type Screen = "artists" | "albums" | "tracks" | "track" | "settings";
export type Layer = "original" | "analyzed" | "final";
export type Route = {
  readonly screen: Screen;
  readonly artist?: string;
  readonly album?: string;
  readonly recordId?: string;
  readonly sourceId?: string;
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
  readonly musicbrainz_enabled: boolean;
  readonly musicbrainz_user_agent: string;
  readonly acoustid_enabled: boolean;
  readonly acoustid_client_key_configured: boolean;
  readonly artwork_enabled: boolean;
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
export type RuntimeSettingsDraft = {
  readonly confidence_threshold: number;
  readonly timeout_seconds: number;
  readonly retry_delay_seconds: number;
  readonly max_attempts: number;
  readonly musicbrainz_enabled: boolean;
  readonly musicbrainz_user_agent: string;
  readonly acoustid_enabled: boolean;
  readonly acoustid_client_key: string;
  readonly artwork_enabled: boolean;
};
export type WatchedRecord = {
  readonly recordId: string;
  readonly sourceId: string;
  readonly startedAt: number;
  readonly sawPending: boolean;
};
