import type {
  EffectiveSourceSelection,
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
} from "../types";

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
      typeof detail === "string" ? detail : "Не удалось выполнить запрос",
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

export function getStorageConfig(): Promise<StorageConfig> {
  return api<StorageConfig>("/api/settings/storage");
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
