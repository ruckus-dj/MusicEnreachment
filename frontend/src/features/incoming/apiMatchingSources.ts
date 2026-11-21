import type { IncomingFileResponse } from "../../api/generated/client.schemas";
import { readSourceTrackOrder } from "../matchingWorkspace/sourceFileOrder";

type ProposalFile = {
  raw_tags?: Record<string, string[] | null>;
};

type TagValues = Record<string, readonly string[] | null> | undefined;

export function readIncomingSourceTrackOrder(tags: TagValues) {
  const result: Record<string, readonly string[]> = {};
  for (const [key, values] of Object.entries(tags ?? {})) {
    if (values != null) result[key] = values;
  }
  return readSourceTrackOrder(result);
}

/**
 * Last segment of an incoming relative path. Directory-only values and
 * separators are ignored so sorting never falls back to a whole path.
 */
export function incomingSourceFileName(path: string | undefined) {
  const segments =
    path
      ?.trim()
      .split(/[\\/]+/)
      .filter(Boolean) ?? [];
  const name = segments.at(-1);
  return name && name !== "." && name !== ".." ? name : undefined;
}

export function apiMatchingSources(
  memberIds: readonly string[],
  files: readonly IncomingFileResponse[],
  proposal: ReadonlyMap<string, ProposalFile>,
  fallbackLabel: string,
) {
  const filesById = new Map(files.map((file) => [file.variant_id, file]));
  function getSource(memberId: string, index: number) {
    const file = filesById.get(memberId);
    const path = file?.locations?.[0]?.relative_path;
    const savedTags = proposal.get(memberId)?.raw_tags;
    const visibleTags = file?.display_tags ?? savedTags ?? file?.tags;
    const title = visibleTags?.TITLE?.find(Boolean);
    const artist = visibleTags?.ARTIST?.find(Boolean);
    const detail = [artist, title].filter(Boolean).join(" · ");
    /*
     * The saved capture is authoritative for both positions; a field it does
     * not contain falls back to the current incoming tags.
     */
    const savedOrder = readIncomingSourceTrackOrder(savedTags);
    const incomingOrder = readIncomingSourceTrackOrder(file?.tags);
    return {
      id: memberId,
      label:
        path ||
        [title, artist].filter(Boolean).join(" · ") ||
        `${fallbackLabel} ${Math.max(0, index) + 1}`,
      detail: path && detail ? detail : undefined,
      path,
      fileName: incomingSourceFileName(path),
      discNumber: savedOrder.discNumber ?? incomingOrder.discNumber,
      trackNumber: savedOrder.trackNumber ?? incomingOrder.trackNumber,
    };
  }
  return {
    label: (memberId: string, index: number) =>
      getSource(memberId, index).label,
    sources: memberIds.map(getSource),
  };
}
