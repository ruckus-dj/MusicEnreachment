import type { MatchingBoardProps } from "../../ui/features/matching/MatchingBoard";
import { readSourceTrackOrder } from "../../ui/features/matching/sourceFileOrder";
import {
  type Assignment,
  type IncomingFiles,
  sourceIds,
  usableFile,
} from "../providers/incoming/incomingModel";
import {
  type OriginalRelease,
  trackNumberLabel,
} from "../providers/library/catalogData";

export function fixtureMatchingBoard(
  groupId: string,
  memberIds: readonly string[],
  target: OriginalRelease,
  files: IncomingFiles,
  assignments: Record<string, Assignment>,
) {
  const positions = target.tracks.map((track) => ({
    track_id: track.id,
    medium: (track.medium ?? 1) - 1,
    position: (track.mediumPosition ?? track.position) - 1,
    displayed_number: trackNumberLabel(target, track),
    title: track.title,
  }));
  const media = new Map<number, number[]>();
  for (const { medium, position } of positions) {
    const numbers = media.get(medium);
    if (numbers) numbers.push(position);
    else media.set(medium, [position]);
  }
  return {
    groupId,
    release: {
      release_id: target.id,
      provider_release_id: target.providerId ?? target.id,
      title: target.title,
      tracklist_complete:
        positions.length > 0 &&
        [...media.values()].every((numbers) =>
          numbers
            .sort((a, b) => a - b)
            .every((position, index) => position === index),
        ),
      positions,
    },
    members: memberIds,
    sources: memberIds.flatMap((id) => {
      const file = files[id];
      return file
        ? [
            {
              id,
              label: file.name,
              path: file.locations?.[0]?.path || file.path,
              fileName: file.name,
              ...readSourceTrackOrder(file.tags),
              detail: [file.tags.ARTIST?.[0], file.tags.TITLE?.[0]]
                .filter(Boolean)
                .join(" · "),
              available: usableFile(file),
            },
          ]
        : [];
    }),
    associations: Object.fromEntries(
      Object.entries(assignments).map(([trackId, assignment]) => [
        trackId,
        sourceIds(assignment),
      ]),
    ),
  } satisfies Pick<
    MatchingBoardProps,
    "groupId" | "release" | "members" | "sources" | "associations"
  >;
}
