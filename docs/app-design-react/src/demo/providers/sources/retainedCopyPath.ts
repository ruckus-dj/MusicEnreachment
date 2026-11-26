import type { SourceFiles } from "../library/catalogData";
import type {
  RetainedSourceCopy,
  SourceProcessingState,
} from "./sourceProcessingModel";

export interface RetainedCopyRoot {
  id: string;
  path: string;
}

/** Strips the owning source root from a location path when it is actually below it. */
export function sourceRelativePath(
  rootPath: string,
  filePath: string,
): string | null {
  if (!rootPath) return null;
  const prefix = rootPath.endsWith("/") ? rootPath : `${rootPath}/`;
  if (!filePath.startsWith(prefix)) return null;
  return filePath.slice(prefix.length) || null;
}

/**
 * Presents a retained work copy by its source-relative location instead of its
 * internal copy/file identity. Falls back to the actual location path, then to
 * `null` when no location is available for the copy.
 */
export function retainedCopyLabel(
  copy: RetainedSourceCopy,
  state: SourceProcessingState,
  roots: RetainedCopyRoot[],
  files: SourceFiles,
): string | null {
  const root = roots.find((item) => item.id === copy.rootId);
  const rootPath = root?.path ?? "";
  const locate = (path: string | undefined) => {
    if (!path) return null;
    return sourceRelativePath(rootPath, path) ?? path;
  };
  const ownerPath = state.files.find(
    (file) => file.id === copy.ownerFileId,
  )?.path;
  const ownerLabel = locate(ownerPath);
  if (ownerLabel) return ownerLabel;
  const locationPath = files[copy.fileId]?.locations?.find(
    (location) => location.rootId === copy.rootId,
  )?.path;
  return locate(locationPath);
}
