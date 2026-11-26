import {
  type IncomingFiles,
  type IncomingReleases,
  type MatchingLinkReceipt,
  type MatchingLinkReceiptPosition,
  usableFile,
} from "../incoming/incomingModel";
import {
  type CatalogArtist,
  desiredTags,
  effectiveReleaseTitle,
  effectiveReleaseYear,
  type OriginalRelease,
  type OriginalTrack,
  originalArtistName,
  originalArtists,
  type SourceFile,
} from "../library/catalogData";
import { compareDemoAudioUpgrade } from "../publication/audioUpgradeQuality";
import { retainedCurrentUnavailable } from "../publication/contextualPublicationJob";
import { publicationPathOwned } from "../publication/libraryPublicationDemo";
import {
  choosePublicationOutput,
  demoOutputCodec,
  fixturePublicationDestination,
  type PublicationDestination,
} from "../publication/publicationOutput";
import { demoPathInside } from "../sources/demoPathValidation";

const sourceEncoderTags = new Set(["ENCODEDBY", "ENCODING", "ENCODERSETTINGS"]);

export type MatchingReceiptPublicationSelections =
  | Readonly<Record<string, string | null | undefined>>
  | ReadonlyMap<string, string | null | undefined>;

export type MatchingReceiptPublicationOptions = {
  /** Scope preparation and application to one exact saved track ID. */
  positionId?: string;
  /** Saved receipt list; when supplied, apply requires exact membership. */
  savedReceipts?: readonly MatchingLinkReceipt[];
};

export type MatchingReceiptPublicationCurrent = NonNullable<
  OriginalTrack["published"]
>;

export type MatchingReceiptPublicationAction =
  | "create"
  | "replace"
  | "unchanged"
  | "blocked";

export type MatchingReceiptPublicationNotice =
  | "matchingReceipt.releaseMissing"
  | "matchingReceipt.receiptEmpty"
  | "matchingReceipt.requestedPositionMissing"
  | "matchingReceipt.duplicatePosition"
  | "matchingReceipt.positionMissing"
  | "matchingReceipt.positionNotIncluded"
  | "matchingReceipt.sourceNotChosen"
  | "matchingReceipt.sourceOutsideReceipt"
  | "matchingReceipt.sourceUnlinked"
  | "matchingReceipt.sourceMissing"
  | "matchingReceipt.sourceUnavailable"
  | "matchingReceipt.formatUnsupported"
  | "matchingReceipt.currentPathMissing"
  | "matchingReceipt.outputPathMissing"
  | "matchingReceipt.willCreate"
  | "matchingReceipt.willReplace"
  | "matchingReceipt.alreadyCurrent"
  | "matchingReceipt.sameSourceDigest"
  | "matchingReceipt.blocked"
  | "matchingReceipt.qualityNotBetter"
  | "matchingReceipt.qualityUnavailable"
  | "matchingReceipt.transcodeNotProven"
  | "matchingReceipt.apply.previewMismatch"
  | "matchingReceipt.apply.receiptMissing"
  | "matchingReceipt.apply.receiptChanged"
  | "matchingReceipt.apply.stateChanged"
  | "matchingReceipt.apply.blocked";

const receiptBlockerCodes = new Set<MatchingReceiptPublicationNotice>([
  "matchingReceipt.releaseMissing",
  "matchingReceipt.receiptEmpty",
  "matchingReceipt.requestedPositionMissing",
]);

export type MatchingReceiptPublicationTarget = {
  sourceId: string;
  tags: Record<string, string[]>;
  codec: string;
  container: string;
  path: string;
  coverHash: string | null;
  lyrics: string | null;
};

export type MatchingReceiptPublicationRow = {
  trackId: string;
  position: number;
  displayedNumber?: string;
  title: string;
  sourceId: string | null;
  sourceName?: string;
  current: MatchingReceiptPublicationCurrent | null;
  target: MatchingReceiptPublicationTarget | null;
  action: MatchingReceiptPublicationAction;
  reason: MatchingReceiptPublicationNotice;
  blockers: readonly MatchingReceiptPublicationNotice[];
};

export type MatchingReceiptPublicationPreview = {
  receiptId: string;
  releaseId: string;
  releaseTitle: string;
  destination: PublicationDestination;
  rows: MatchingReceiptPublicationRow[];
  /** Every receipt-level and position-level diagnostic for this plan. */
  blockers: MatchingReceiptPublicationNotice[];
  create: number;
  replace: number;
  unchanged: number;
  changed: number;
  /** At least one eligible row can be written; sibling blockers do not veto it. */
  canApply: boolean;
  /** Stable prototype state token; it is not a security digest. */
  digest: string;
};

export type MatchingReceiptPublicationRowResult = {
  trackId: string;
  action: MatchingReceiptPublicationAction;
  applied: boolean;
  notice?: MatchingReceiptPublicationNotice;
};

export type MatchingReceiptPublicationApplyResult = {
  applied: boolean;
  changed: number;
  created: number;
  replaced: number;
  /** Set only when the whole scoped plan is rejected. */
  reason?: MatchingReceiptPublicationNotice;
  preview: MatchingReceiptPublicationPreview;
  results: MatchingReceiptPublicationRowResult[];
};

function isSelectionMap(
  selections: MatchingReceiptPublicationSelections,
): selections is ReadonlyMap<string, string | null | undefined> {
  return selections instanceof Map;
}

function selectedFileId(
  selections: MatchingReceiptPublicationSelections,
  position: MatchingLinkReceiptPosition,
): string | null {
  let explicit: string | null | undefined;
  if (isSelectionMap(selections))
    explicit = selections.has(position.trackId)
      ? (selections.get(position.trackId) ?? null)
      : undefined;
  else
    explicit = Object.hasOwn(selections, position.trackId)
      ? (selections[position.trackId] ?? null)
      : undefined;
  if (explicit !== undefined) return explicit;
  if (
    position.selectedSourceId &&
    position.sourceIds.includes(position.selectedSourceId)
  )
    return position.selectedSourceId;
  return position.sourceIds.length === 1
    ? (position.sourceIds[0] ?? null)
    : null;
}

function trackSourceIds(track: Pick<OriginalTrack, "sourceId" | "sourceIds">) {
  return new Set(
    [
      ...(track.sourceIds ?? []),
      ...(track.sourceId ? [track.sourceId] : []),
    ].map(String),
  );
}

/**
 * The retained CURRENT container, not the global destination, determines the
 * upgrade transform. A format change belongs to a new publication, not to an
 * in-place CURRENT replacement. An unrecognized container stays unprovable
 * rather than falling back to a guessed copy or transcode.
 */
function retainedPublicationFormat(
  container: string,
): PublicationDestination["format"] | null {
  switch (container.trim().toUpperCase()) {
    case "M4A":
    case "MP4":
      return "m4a";
    case "MKA":
    case "MATROSKA":
      return "mka";
    default:
      return null;
  }
}

function encoderTagName(key: string) {
  return key.trim().toUpperCase();
}

function isSourceEncoderTag(key: string) {
  return sourceEncoderTags.has(encoderTagName(key));
}

/**
 * Retains CURRENT tags. Encoder descriptions always belong to the audio stream
 * being published, so old values are removed; source values are projected only
 * when publication copies that exact source stream.
 */
function projectSourceEncoderTags(
  currentTags: Record<string, string[]>,
  sourceTags: Record<string, string[]>,
  copySourceStream: boolean,
): Record<string, string[]> {
  const result: Record<string, string[]> = {};
  for (const [key, values] of Object.entries(currentTags))
    if (!isSourceEncoderTag(key)) result[key] = [...values];
  if (!copySourceStream) return result;
  for (const [key, values] of Object.entries(sourceTags))
    if (isSourceEncoderTag(key)) result[encoderTagName(key)] = [...values];
  return result;
}

function copiesSourceStream(
  file: SourceFile,
  format: PublicationDestination["format"],
) {
  const output = demoOutputCodec(file.codec, format);
  return (
    Boolean(output) && output.toUpperCase() === file.codec.trim().toUpperCase()
  );
}

function upgradeTarget(
  current: MatchingReceiptPublicationCurrent,
  file: SourceFile,
  format: PublicationDestination["format"],
): MatchingReceiptPublicationTarget {
  return {
    sourceId: file.id,
    tags: projectSourceEncoderTags(
      current.tags,
      file.tags ?? {},
      copiesSourceStream(file, format),
    ),
    codec: demoOutputCodec(file.codec, format),
    container: current.container,
    path: current.path,
    coverHash: current.coverHash,
    lyrics: current.lyrics,
  };
}

function createTarget(
  release: OriginalRelease,
  track: OriginalTrack,
  file: SourceFile,
  destination: PublicationDestination,
  artists: CatalogArtist[],
): MatchingReceiptPublicationTarget {
  const output = choosePublicationOutput(file, {
    ...destination,
    artist: originalArtistName(release.artistId, artists),
    year: effectiveReleaseYear(release),
    release: effectiveReleaseTitle(release),
    position: track.position,
    title: track.title,
  });
  return {
    sourceId: file.id,
    tags: desiredTags(release, track, artists, file.id, output.container),
    codec: demoOutputCodec(file.codec, destination.format),
    container: output.container,
    path: output.path,
    coverHash: track.coverHash ?? null,
    lyrics: track.lyrics ?? null,
  };
}

function stableStringify(value: unknown): string {
  if (Array.isArray(value)) return `[${value.map(stableStringify).join(",")}]`;
  if (value !== null && typeof value === "object") {
    const record = value as Record<string, unknown>;
    return `{${Object.keys(record)
      .filter((key) => record[key] !== undefined)
      .sort()
      .map((key) => `${JSON.stringify(key)}:${stableStringify(record[key])}`)
      .join(",")}}`;
  }
  return JSON.stringify(value) ?? "null";
}

function publicationDigest(
  receipt: MatchingLinkReceipt,
  positions: readonly MatchingLinkReceiptPosition[],
  destination: PublicationDestination,
  rows: readonly MatchingReceiptPublicationRow[],
  files: IncomingFiles,
): string {
  return stableStringify({
    version: "melo:matching-receipt-publication:prototype:v2",
    receipt: {
      id: receipt.id,
      releaseId: receipt.releaseId,
      positions: positions
        .map((position) => ({
          trackId: String(position.trackId),
          position: position.position,
          displayedNumber: position.displayedNumber ?? null,
          sourceIds: [...position.sourceIds].sort(),
          selectedSourceId: position.selectedSourceId,
          included: position.included,
        }))
        .sort((left, right) => left.trackId.localeCompare(right.trackId)),
    },
    destination,
    rows: rows.map((row) => ({
      trackId: row.trackId,
      position: row.position,
      displayedNumber: row.displayedNumber ?? null,
      title: row.title,
      sourceId: row.sourceId,
      sourceName: row.sourceName ?? null,
      source: row.sourceId ? files[row.sourceId] : undefined,
      current: row.current,
      target: row.target,
      action: row.action,
      reason: row.reason,
      blockers: [...row.blockers],
    })),
  });
}

/**
 * Builds an ordinary publication preview for one saved matching result.
 *
 * The saved receipt is authoritative for its exact track IDs and each
 * position's saved source IDs. A selected file is valid only while it remains
 * both in that saved membership and in the live position association. A
 * position with no CURRENT publication is created. A position with CURRENT is
 * left unchanged unless both its measured source and its retained-container
 * output are provably better; no unknown fact is treated as an improvement.
 */
export function prepareMatchingReceiptPublication(
  receipt: MatchingLinkReceipt,
  releases: IncomingReleases,
  files: IncomingFiles,
  selections: MatchingReceiptPublicationSelections = {},
  destination: PublicationDestination = fixturePublicationDestination,
  artists: CatalogArtist[] = originalArtists,
  options: MatchingReceiptPublicationOptions = {},
): MatchingReceiptPublicationPreview {
  const blockers: MatchingReceiptPublicationNotice[] = [];
  const release = releases.find((item) => item.id === receipt.releaseId);
  if (!release) blockers.push("matchingReceipt.releaseMissing");
  if (!receipt.positions.length) blockers.push("matchingReceipt.receiptEmpty");
  const positionId = options.positionId;
  const positions = positionId
    ? receipt.positions.filter(
        (position) => String(position.trackId) === positionId,
      )
    : receipt.positions;
  if (positionId && receipt.positions.length && !positions.length)
    blockers.push("matchingReceipt.requestedPositionMissing");

  const rows: MatchingReceiptPublicationRow[] = [];
  const tracksById = new Map<string, OriginalTrack>(
    (release?.tracks ?? []).map((track) => [String(track.id), track] as const),
  );
  const seenTrackIds = new Set<string>();

  for (const position of positions) {
    const trackId = String(position.trackId);
    const track = tracksById.get(trackId);
    const rowBlockers: MatchingReceiptPublicationNotice[] = [];
    if (seenTrackIds.has(trackId))
      rowBlockers.push("matchingReceipt.duplicatePosition");
    seenTrackIds.add(trackId);
    if (!track) rowBlockers.push("matchingReceipt.positionMissing");
    if (!position.included)
      rowBlockers.push("matchingReceipt.positionNotIncluded");

    const sourceId = selectedFileId(selections, position);
    const file = sourceId ? files[sourceId] : undefined;
    if (!sourceId) {
      rowBlockers.push("matchingReceipt.sourceNotChosen");
    } else {
      if (!position.sourceIds.includes(sourceId))
        rowBlockers.push("matchingReceipt.sourceOutsideReceipt");
      if (track && !trackSourceIds(track).has(sourceId))
        rowBlockers.push("matchingReceipt.sourceUnlinked");
      if (!file) rowBlockers.push("matchingReceipt.sourceMissing");
      else if (!usableFile(file))
        rowBlockers.push("matchingReceipt.sourceUnavailable");
    }

    const current = track?.published ? structuredClone(track.published) : null;
    let action: MatchingReceiptPublicationAction = "blocked";
    let reason: MatchingReceiptPublicationNotice = "matchingReceipt.blocked";
    let target: MatchingReceiptPublicationTarget | null = null;

    if (!rowBlockers.length && release && track && file && sourceId) {
      if (!current) {
        if (!demoOutputCodec(file.codec, destination.format)) {
          rowBlockers.push("matchingReceipt.formatUnsupported");
        } else {
          action = "create";
          reason = "matchingReceipt.willCreate";
          target = createTarget(release, track, file, destination, artists);
        }
      } else if (
        !current.path ||
        retainedCurrentUnavailable(track.observedFile)
      ) {
        rowBlockers.push("matchingReceipt.currentPathMissing");
      } else if (current.sourceId === sourceId) {
        action = "unchanged";
        reason = "matchingReceipt.alreadyCurrent";
      } else if (
        current.sourceId &&
        file.sha256 &&
        files[current.sourceId]?.sha256 &&
        file.sha256 === files[current.sourceId]?.sha256
      ) {
        action = "unchanged";
        reason = "matchingReceipt.sameSourceDigest";
      } else {
        const format = retainedPublicationFormat(current.container);
        if (!format) {
          action = "unchanged";
          reason = "matchingReceipt.qualityUnavailable";
        } else if (!demoOutputCodec(file.codec, format)) {
          rowBlockers.push("matchingReceipt.formatUnsupported");
        } else {
          const comparison = compareDemoAudioUpgrade(
            file,
            current.codec,
            current.sourceId ? files[current.sourceId] : undefined,
            format,
          );
          if (comparison.better) {
            action = "replace";
            reason = "matchingReceipt.willReplace";
            target = upgradeTarget(current, file, format);
          } else {
            action = "unchanged";
            reason = comparison.notice;
          }
        }
      }
      if (
        target &&
        (!demoPathInside(destination.outputDirectory, target.path) ||
          publicationPathOwned(
            { releases },
            { release_id: receipt.releaseId, position_id: trackId },
            target.path,
          ))
      )
        rowBlockers.push("matchingReceipt.outputPathMissing");
    }

    if (rowBlockers.length) {
      action = "blocked";
      target = null;
      reason = "matchingReceipt.blocked";
    }

    rows.push({
      trackId,
      position: track?.position ?? position.position,
      displayedNumber: track?.displayedNumber ?? position.displayedNumber,
      title: track?.title ?? position.title,
      sourceId,
      sourceName: file?.name,
      current,
      target,
      action,
      reason,
      blockers: rowBlockers,
    });
  }

  const create = rows.filter((row) => row.action === "create").length;
  const replace = rows.filter((row) => row.action === "replace").length;
  const unchanged = rows.filter((row) => row.action === "unchanged").length;
  const changed = create + replace;
  const allBlockers = [...blockers, ...rows.flatMap((row) => row.blockers)];

  return {
    receiptId: receipt.id,
    releaseId: receipt.releaseId,
    releaseTitle: release?.title ?? receipt.releaseTitle,
    destination,
    rows,
    blockers: allBlockers,
    create,
    replace,
    unchanged,
    changed,
    canApply:
      changed > 0 && !blockers.some((code) => receiptBlockerCodes.has(code)),
    digest: publicationDigest(receipt, positions, destination, rows, files),
  };
}

/**
 * Applies only the exact preview the operator confirmed. A scoped position
 * applies independently of every other selected row: a blocked or unchanged
 * sibling does not prevent an eligible row from being written. A stale preview
 * or a receipt-level blocker rejects the whole scoped plan before any write.
 */
export function applyMatchingReceiptPublication(
  preview: MatchingReceiptPublicationPreview,
  receipt: MatchingLinkReceipt,
  releases: IncomingReleases,
  files: IncomingFiles,
  selections: MatchingReceiptPublicationSelections = {},
  destination: PublicationDestination = fixturePublicationDestination,
  artists: CatalogArtist[] = originalArtists,
  options: MatchingReceiptPublicationOptions = {},
): MatchingReceiptPublicationApplyResult {
  const fresh = prepareMatchingReceiptPublication(
    receipt,
    releases,
    files,
    selections,
    destination,
    artists,
    options,
  );
  const reject = (
    reason: MatchingReceiptPublicationNotice,
  ): MatchingReceiptPublicationApplyResult => ({
    applied: false,
    changed: 0,
    created: 0,
    replaced: 0,
    reason,
    preview: fresh,
    results: [],
  });

  if (
    preview.receiptId !== receipt.id ||
    preview.releaseId !== receipt.releaseId
  )
    return reject("matchingReceipt.apply.previewMismatch");

  const savedReceipts = options.savedReceipts;
  if (savedReceipts) {
    const saved = savedReceipts.find((item) => item.id === receipt.id);
    if (!saved) return reject("matchingReceipt.apply.receiptMissing");
    if (
      saved.groupId !== receipt.groupId ||
      saved.releaseId !== receipt.releaseId ||
      stableStringify(saved.positions) !== stableStringify(receipt.positions)
    )
      return reject("matchingReceipt.apply.receiptChanged");
  }

  if (preview.digest !== fresh.digest)
    return reject("matchingReceipt.apply.stateChanged");

  const receiptBlocker = fresh.blockers.find((code) =>
    receiptBlockerCodes.has(code),
  );
  if (receiptBlocker) return reject(receiptBlocker);

  const release = releases.find((item) => item.id === receipt.releaseId);
  if (!release) return reject("matchingReceipt.releaseMissing");

  const results: MatchingReceiptPublicationRowResult[] = [];
  let created = 0;
  let replaced = 0;

  for (const row of fresh.rows) {
    if (row.action === "blocked") {
      results.push({
        trackId: row.trackId,
        action: row.action,
        applied: false,
        notice: row.blockers[0] ?? "matchingReceipt.blocked",
      });
      continue;
    }
    if (row.action === "unchanged") {
      results.push({
        trackId: row.trackId,
        action: row.action,
        applied: false,
        notice: row.reason,
      });
      continue;
    }

    const target = row.target;
    const sourceId = row.sourceId;
    const track = release.tracks.find(
      (item) => String(item.id) === row.trackId,
    );
    const file = sourceId ? files[sourceId] : undefined;
    if (!target || !sourceId || !track || !file || !usableFile(file)) {
      results.push({
        trackId: row.trackId,
        action: row.action,
        applied: false,
        notice: "matchingReceipt.apply.blocked",
      });
      continue;
    }
    if (!trackSourceIds(track).has(sourceId)) {
      results.push({
        trackId: row.trackId,
        action: row.action,
        applied: false,
        notice: "matchingReceipt.sourceUnlinked",
      });
      continue;
    }

    track.sourceId = sourceId;
    track.published = row.current
      ? {
          ...row.current,
          tags: structuredClone(target.tags),
          coverHash: target.coverHash,
          lyrics: target.lyrics,
          codec: target.codec,
          container: target.container,
          sourceId,
          path: target.path,
          readAt: "Только что · демо",
        }
      : {
          tags: structuredClone(target.tags),
          coverHash: target.coverHash,
          lyrics: target.lyrics,
          codec: target.codec,
          container: target.container,
          sourceId,
          path: target.path,
          readAt: "Только что · демо",
        };
    track.metadataBaseline = structuredClone(target.tags);
    track.metadataBaselineExtras = {
      coverHash: target.coverHash,
      lyrics: target.lyrics,
    };
    results.push({
      trackId: row.trackId,
      action: row.action,
      applied: true,
    });
    if (row.action === "create") created += 1;
    else replaced += 1;
  }

  return {
    applied: created + replaced > 0,
    changed: created + replaced,
    created,
    replaced,
    preview: fresh,
    results,
  };
}
