import type { IncomingFileResponse } from "../../api/generated/client.schemas";

type ProposalFile = {
  raw_tags?: Record<string, string[] | null>;
};

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
    const tags = proposal.get(memberId)?.raw_tags ?? file?.tags;
    const title = tags?.TITLE?.find(Boolean);
    const artist = tags?.ARTIST?.find(Boolean);
    const detail = [artist, title].filter(Boolean).join(" · ");
    return {
      id: memberId,
      label:
        path ||
        [title, artist].filter(Boolean).join(" · ") ||
        `${fallbackLabel} ${Math.max(0, index) + 1}`,
      detail: path && detail ? detail : undefined,
    };
  }
  return {
    label: (memberId: string, index: number) =>
      getSource(memberId, index).label,
    sources: memberIds.map(getSource),
  };
}
