import fixtureData from "../../fixtures/original-fixtures.json";
import {
  type CatalogArtist,
  type SourceFile as CatalogSourceFile,
  desiredTags,
  effectiveReleaseTitle,
  effectiveReleaseYear,
  type OriginalRelease,
  type OriginalTrack,
  originalArtistName,
  originalArtists,
  originalFiles,
  originalProviders,
  originalReleases,
  publicationMetadataIssues,
  sourceOnlyAudioTagKeys,
} from "../library/catalogData";
import { demoReleasePrimaryArtistId } from "../library/libraryPrimaryArtistDemo";
import { replaceAudioMeasurements } from "../library/metadata/audioMeasurements";
import { retainUnspecifiedContributorTags } from "../library/metadata/scopedContributors";
import { sameTagValues } from "../library/tagEquality";
import type { UiNotice } from "../notices/uiNotice";
import { compareDemoAudioUpgrade } from "../publication/audioUpgradeQuality";

export type TagMap = Record<string, string[]>;
export type SourceFile = CatalogSourceFile & {
  confidence?: string | null;
  fingerprint?: string;
  providerTrackId?: string | null;
};
export type IncomingFiles = Record<string, SourceFile>;
export type IncomingReleases = OriginalRelease[];
export type IncomingReason = {
  code: "incomplete" | "confident" | "compilation";
  params?: Record<string, string | number>;
};
export type IncomingNotice = {
  code: "automaticPublished" | "nothingToPublish";
  params?: Record<string, string | number>;
};
export type AutomaticMatchingResult = {
  /** Publication operations applied by this run, including audio upgrades. */
  publications: number;
  /** Newly added source associations; existing links are preserved. */
  links: number;
  /** Files removed from the active incoming group. */
  retired: number;
};
export type Assignment = {
  sourceIds: string[];
  selectedSourceId: string | null;
  sourceMeta: Record<
    string,
    {
      method: string;
      confidence: string | null;
      /** Accepted evidence for this exact source/position pair, not a label-derived score. */
      score?: { value: number; sourceSHA256: string };
    }
  >;
  method: string;
  confidence: string | null;
  providerTrackId: string | null;
  included: boolean;
  fileId?: string | null;
};
function automaticPairScore(
  assignment: Assignment | undefined,
  file: SourceFile | undefined,
) {
  const score = file?.sha256
    ? assignment?.sourceMeta[file.id]?.score
    : undefined;
  return score &&
    score.sourceSHA256 === file?.sha256 &&
    score.value >= 0 &&
    score.value <= 1
    ? score.value
    : undefined;
}
export type IncomingGroup = {
  id: string;
  title: string;
  artistId: string;
  releaseId: string;
  fileIds: string[];
  candidateId: string | null;
  mode: "local" | "provider";
  autoEligible: boolean;
  processed: boolean;
  reason: IncomingReason | string;
  drafts: Record<string, Record<string, Assignment>>;
};
/** One exact position included in a saved matching result. */
export type MatchingLinkReceiptPosition = {
  trackId: string;
  title: string;
  position: number;
  displayedNumber?: string;
  sourceIds: string[];
  selectedSourceId: string | null;
  included: boolean;
};
/**
 * Saved result of an explicit link save. The receipt is domain state owned by
 * the data provider; presentation code only maps it to a typed view.
 */
export type MatchingLinkReceipt = {
  id: string;
  groupId: string;
  groupTitle: string;
  releaseId: string;
  releaseTitle: string;
  providerReleaseId: string | null;
  positions: MatchingLinkReceiptPosition[];
  remainingFileIds: string[];
  remainingFileNames: string[];
};
export type IncomingState = {
  groups: IncomingGroup[];
  groupId: string;
  selectedFile: string | null;
  selectedTarget: string | null;
  providerQuery: string;
  providerResults: IncomingReleases | null;
  notice: string;
  noticeDescriptor: IncomingNotice | null;
  uiNotice?: UiNotice | null;
  inventoryVersion: number;
  plan: IncomingPlan | null;
  confirmDowngrade: boolean;
  receipts: MatchingLinkReceipt[];
};

const fixtures = fixtureData as unknown as {
  artists: { id: string; name: string }[];
  incomingGroups: IncomingGroup[];
};

export const incomingFixtures = {
  ...fixtures,
  artists: originalArtists,
  files: originalFiles as IncomingFiles,
  releases: originalReleases,
  providerReleases: originalProviders,
};

function reasonForGroup(group: IncomingGroup): IncomingReason {
  if (group.mode === "local") return { code: "compilation" };
  if (group.autoEligible) {
    const count = group.fileIds.length;
    return { code: "confident", params: { count, total: count } };
  }
  return { code: "incomplete" };
}

export const artistName = (
  artistId: string,
  artists: CatalogArtist[] = fixtures.artists,
) => artists.find((artist) => artist.id === artistId)?.name ?? "";
export const providerReleases: IncomingReleases = originalProviders;
export const normalized = (value: unknown) =>
  String(value ?? "")
    .normalize("NFKC")
    .toLocaleLowerCase()
    .replace(/\s+/g, " ")
    .trim();
export const sourceIds = (assignment?: Partial<Assignment> | null) =>
  assignment?.sourceIds ?? (assignment?.fileId ? [assignment.fileId] : []);
export const selectedSourceId = (assignment?: Partial<Assignment> | null) =>
  assignment && Object.hasOwn(assignment, "selectedSourceId")
    ? (assignment.selectedSourceId ?? null)
    : (assignment?.fileId ?? null);
export const usableFile = (file?: SourceFile) =>
  Boolean(file?.available && file.locations?.length);

export function incomingMatchingCount(groups: readonly IncomingGroup[]) {
  return groups.filter((group) => !group.processed).length;
}

export function createIncomingState(): IncomingState {
  return {
    groups: structuredClone(fixtures.incomingGroups).map((group) => ({
      ...group,
      reason: reasonForGroup(group),
    })),
    groupId: "north",
    selectedFile: null,
    selectedTarget: null,
    providerQuery: "Northern Lines",
    providerResults: null,
    notice: "",
    noticeDescriptor: null,
    uiNotice: null,
    inventoryVersion: 0,
    plan: null,
    confirmDowngrade: false,
    receipts: [],
  };
}

export function findTarget(
  group: IncomingGroup,
  releases: IncomingReleases,
  providers: IncomingReleases = providerReleases,
): OriginalRelease | null {
  if (group.mode === "local")
    return (
      providers.find(
        (release) =>
          release.provider === "Local" && release.id === group.releaseId,
      ) ??
      releases.find((release) => release.id === group.releaseId) ??
      null
    );
  return providers.find((release) => release.id === group.candidateId) ?? null;
}

/** Stable per-edition draft identity without re-resolving a target. */
export function matchingDraftKey(
  mode: IncomingGroup["mode"],
  targetId: string | null | undefined,
) {
  const id = targetId == null ? "" : String(targetId);
  return mode === "local" ? `local:${id}` : id;
}

export function getDraft(
  group: IncomingGroup,
  files: IncomingFiles,
  providers: IncomingReleases = providerReleases,
  releases: IncomingReleases = originalReleases,
  resolvedTarget?: OriginalRelease | null,
) {
  const target = resolvedTarget ?? findTarget(group, releases, providers);
  if (!target) return {};
  const targetTracks = target.tracks;
  const key = matchingDraftKey(group.mode, target.id);
  if (!Object.hasOwn(group.drafts, key)) {
    const assignments: Record<string, Assignment> = {};
    const localRelease =
      group.mode === "local" && target.providerId
        ? releases.find(
            (release) =>
              release.provider === "Local" &&
              release.providerId === target.providerId,
          )
        : undefined;
    for (const track of targetTracks) {
      const linkedTrack = localRelease?.tracks.find(
        (candidate) => candidate.localProviderTrackId === track.id,
      );
      const source = group.fileIds
        .map((id) => files[id])
        .find(
          (file) =>
            file &&
            (group.mode === "local"
              ? file.id === (linkedTrack?.sourceId ?? track.sourceId)
              : file.providerTrackId === track.id &&
                file.confidence === "high"),
        );
      if (!source) continue;
      const method =
        group.mode === "local"
          ? "Из исходной группы"
          : "Авто · тестовый вердикт";
      assignments[String(track.id)] = {
        sourceIds: [source.id],
        selectedSourceId: source.id,
        sourceMeta: {
          [source.id]: { method, confidence: source.confidence ?? null },
        },
        method,
        confidence: source.confidence ?? null,
        providerTrackId:
          group.mode === "local"
            ? (linkedTrack?.providerTrackId ?? track.providerTrackId ?? null)
            : String(track.id),
        included: true,
      };
    }
    group.drafts[key] = assignments;
  }
  return group.drafts[key];
}

export function assignFile(
  group: IncomingGroup,
  targetId: string,
  fileId: string,
  files: IncomingFiles,
  releases: IncomingReleases = originalReleases,
  providers: IncomingReleases = providerReleases,
  resolvedTarget?: OriginalRelease | null,
) {
  if (!group.fileIds.includes(fileId) || !usableFile(files[fileId]))
    return false;
  const target = resolvedTarget ?? findTarget(group, releases, providers);
  if (!target?.tracks.some((track) => track.id === targetId)) return false;
  const assignments = getDraft(group, files, providers, releases, target);
  const old = assignments[targetId];
  if (sourceIds(old).includes(fileId)) return false;
  const assignment = old ?? {
    sourceIds: [],
    selectedSourceId: null,
    sourceMeta: {},
    method: "Вручную · перетаскивание или выбор",
    confidence: null,
    providerTrackId: group.mode === "local" ? null : targetId,
    included: false,
  };
  for (const id of assignment.sourceIds)
    assignment.sourceMeta[id] ??= {
      method: assignment.method,
      confidence: files[id]?.confidence ?? null,
    };
  assignment.sourceMeta[fileId] = {
    method: "Вручную · добавлен вариант",
    confidence: files[fileId].confidence ?? null,
  };
  assignment.sourceIds = [...assignment.sourceIds, fileId];
  assignments[targetId] = assignment;
  return true;
}

export function unassignFile(
  assignmentMap: Record<string, Assignment>,
  targetId: string,
  fileId: string,
) {
  const assignment = assignmentMap[targetId];
  if (!assignment) return;
  assignment.sourceIds = assignment.sourceIds.filter((id) => id !== fileId);
  if (assignment.selectedSourceId === fileId)
    assignment.selectedSourceId = null;
  delete assignment.sourceMeta[fileId];
  if (!assignment.sourceIds.length) delete assignmentMap[targetId];
}

export function providerTags(
  release: OriginalRelease,
  track: OriginalTrack,
  artists?: CatalogArtist[],
  audioSourceId: string | null = track.sourceId,
  outputContainer?: string,
): TagMap {
  return desiredTags(release, track, artists, audioSourceId, outputContainer);
}

export function findProviderTrack(
  id: string | null,
  providers: IncomingReleases = providerReleases,
) {
  if (!id) return null;
  for (const release of providers) {
    const track = release.tracks.find((item) => item.id === id);
    if (track) return { release, track };
  }
  return null;
}

export type PlanRow = {
  targetTrack: OriginalTrack;
  assignment?: Assignment;
  track?: OriginalTrack;
  file?: SourceFile;
  outputCodec?: string;
  provider: ReturnType<typeof findProviderTrack>;
  nextTags: TagMap;
  previous?: NonNullable<OriginalTrack["published"]>;
  action: "Пропустить" | "Без изменений" | "Заменить" | "Создать";
  downgrade: boolean;
};

/**
 * Complete single-position row. `ordinaryUnchanged` additionally proves the
 * recorded CURRENT path, container, assets and lyrics, so metadata/cover edits
 * are not hidden by the generic source/codec/tag gate.
 */
export type PositionPlanRow = PlanRow & {
  /** Path rendered from current settings, independent of retained target. */
  renderedPath?: string;
  renderedContainer?: string;
  /** Path retained from CURRENT; absent outside retained workflow. */
  retainedPath?: string;
  ordinaryUnchanged: boolean;
};
export type IncomingPlan = {
  mode: IncomingGroup["mode"];
  groupId: string;
  target: OriginalRelease;
  localReleaseId: string | null;
  assignments: Record<string, Assignment>;
  inventoryVersion: number;
  /** Exact single-position context; a one-row group is still a group plan. */
  positionId?: string;
  /** Rebuild the selected source even when the current snapshot is unchanged. */
  force?: boolean;
  /** Retained-position workflow replaces the output without changing its path. */
  retainCurrentPath?: boolean;
};

export function makePlan(
  state: IncomingState,
  files: IncomingFiles,
  releases: IncomingReleases,
  providers: IncomingReleases = providerReleases,
): IncomingPlan | null {
  const group = state.groups.find((item) => item.id === state.groupId);
  return group ? makeGroupPlan(state, group, files, releases, providers) : null;
}

export function makeGroupPlan(
  state: IncomingState,
  group: IncomingGroup,
  files: IncomingFiles,
  releases: IncomingReleases,
  providers: IncomingReleases = providerReleases,
): IncomingPlan | null {
  const target = findTarget(group, releases, providers);
  if (!target) return null;
  const local =
    group.mode === "local"
      ? target
      : releases.find(
          (release) => release.providerId === (target.providerId ?? target.id),
        );
  return {
    mode: group.mode,
    groupId: group.id,
    target: structuredClone(target),
    localReleaseId: local ? String(local.id) : null,
    assignments: structuredClone(getDraft(group, files, providers, releases)),
    inventoryVersion: state.inventoryVersion,
  };
}

export function makeReleasePlan(
  state: IncomingState,
  release: OriginalRelease,
  files: IncomingFiles,
  trackId: string | null = null,
): IncomingPlan {
  const assignments: Record<string, Assignment> = {};
  for (const track of release.tracks) {
    const selectedSource = track.sourceId;
    const availableSources = [
      ...new Set([
        ...(track.sourceIds ?? []),
        ...(selectedSource ? [selectedSource] : []),
      ]),
    ].filter((id) => usableFile(files[id]));
    if (!availableSources.length) continue;
    assignments[String(track.id)] = {
      sourceIds: availableSources,
      selectedSourceId:
        selectedSource && availableSources.includes(selectedSource)
          ? selectedSource
          : availableSources.length === 1
            ? availableSources[0]
            : null,
      sourceMeta: {},
      method: "Текущий выбранный источник",
      confidence: null,
      providerTrackId: track.providerTrackId ?? null,
      included: !trackId || track.id === trackId,
    };
  }
  return {
    mode: "local",
    groupId: "",
    target: structuredClone(release),
    localReleaseId: release.id,
    assignments,
    inventoryVersion: state.inventoryVersion,
    ...(trackId ? { positionId: trackId } : {}),
  };
}

export function planRows(
  plan: IncomingPlan,
  files: IncomingFiles,
  releases: IncomingReleases,
  providers: IncomingReleases = providerReleases,
  artists?: CatalogArtist[],
  destination: PublicationDestination = fixturePublicationDestination,
  exactProviderTracks = false,
): PlanRow[] {
  const existing = releases.find(
    (release) => release.id === plan.localReleaseId,
  );
  const targetTracks = plan.target.tracks;
  return targetTracks.map((targetTrack) => {
    const assignment = plan.assignments[String(targetTrack.id)];
    const track = existing?.tracks.find((item) =>
      exactProviderTracks
        ? item.providerTrackId === targetTrack.id
        : plan.positionId && plan.mode === "local"
          ? item.id === targetTrack.id
          : item.position === targetTrack.position,
    );
    const candidate = assignment && files[selectedSourceId(assignment) ?? ""];
    const file = usableFile(candidate) ? candidate : undefined;
    const provider = findProviderTrack(
      assignment?.providerTrackId ?? null,
      providers,
    );
    const releaseForTags =
      plan.mode === "local" ? (existing ?? plan.target) : plan.target;
    const projected =
      plan.mode === "local"
        ? {
            ...(track ?? targetTrack),
            recordingId:
              provider?.track.recordingId || track?.recordingId || null,
          }
        : { ...targetTrack, sourceId: file?.id ?? null };
    const nextTags = providerTags(
      releaseForTags,
      projected,
      artists,
      selectedSourceId(assignment),
      file
        ? destination.format === "original"
          ? demoSourceContainer(file)
          : destination.format
        : undefined,
    );
    const previous = track?.published ?? undefined;
    if (plan.mode === "local" && plan.positionId && !plan.groupId && previous)
      retainUnspecifiedContributorTags(
        nextTags,
        previous.tags,
        track?.positionContributors,
        track?.recordingContributors,
      );
    const outputCodec = file
      ? demoOutputCodec(file.codec, destination.format)
      : undefined;
    const unchanged =
      previous &&
      file &&
      previous.sourceId === file.id &&
      previous.codec === outputCodec &&
      JSON.stringify(previous.tags) === JSON.stringify(nextTags);
    return {
      targetTrack,
      assignment,
      track,
      file,
      outputCodec,
      provider,
      nextTags,
      previous,
      action:
        !assignment?.included || !file
          ? "Пропустить"
          : unchanged && !plan.force
            ? "Без изменений"
            : previous
              ? "Заменить"
              : "Создать",
      downgrade: Boolean(
        demoIsLosslessCodec(previous?.codec) &&
          file &&
          !demoIsLosslessCodec(file.codec) &&
          assignment?.included,
      ),
    };
  });
}

import {
  choosePublicationOutput,
  demoIsLosslessCodec,
  demoOutputCodec,
  demoSourceContainer,
  fixturePublicationDestination,
  type PublicationDestination,
  retainedOutputIssue,
} from "../publication/publicationOutput";

/**
 * Targeted single-position projection. The generic plan gate proves only
 * source, codec and tag equality; a contextual position must also prove the
 * recorded CURRENT path, container, assets and lyrics before calling the row
 * unchanged. Retaining the destination does not make the rendered path equal
 * to CURRENT.
 */
export function planPositionRow(
  plan: IncomingPlan,
  files: IncomingFiles,
  releases: IncomingReleases,
  providers: IncomingReleases = providerReleases,
  artists?: CatalogArtist[],
  destination: PublicationDestination = fixturePublicationDestination,
  positionId: string | undefined = plan.positionId,
): PositionPlanRow | null {
  const target = plan.target.tracks.find((track) => track.id === positionId);
  if (!target || plan.mode !== "local" || plan.groupId) return null;
  const row = planRows(
    { ...plan, positionId, target: { ...plan.target, tracks: [target] } },
    files,
    releases,
    providers,
    artists,
    destination,
  )[0];
  if (!row) return null;
  const release =
    releases.find((item) => item.id === plan.localReleaseId) ?? plan.target;
  const output = row.file
    ? choosePublicationOutput(row.file, {
        ...destination,
        artist: originalArtistName(
          demoReleasePrimaryArtistId(release, artists ?? originalArtists) ??
            release.artistId,
          artists,
        ),
        year: effectiveReleaseYear(release),
        release: effectiveReleaseTitle(release),
        position: row.targetTrack.position,
        title: row.targetTrack.title,
      })
    : undefined;
  const plannedPath =
    plan.retainCurrentPath && row.previous ? row.previous.path : output?.path;
  const plannedContainer =
    plan.retainCurrentPath && row.previous
      ? row.previous.container
      : output?.container;
  const renderedPath = output?.path;
  const renderedContainer = output?.container;
  const retainedPath = plan.retainCurrentPath ? row.previous?.path : undefined;
  const copyCompatibleSource = Boolean(
    row.file &&
      row.outputCodec &&
      renderedContainer &&
      row.file.codec.toUpperCase() === row.outputCodec.toUpperCase() &&
      demoSourceContainer(row.file) === renderedContainer,
  );
  const tagsUnchanged = sameTagValues(row.previous?.tags, row.nextTags);
  const assetsUnchanged =
    (row.previous?.coverHash ?? null) === (row.track?.coverHash ?? null) &&
    (row.previous?.lyrics ?? null) === (row.track?.lyrics ?? null);
  const ordinaryUnchanged = Boolean(
    row.previous &&
      row.file &&
      row.previous.sourceId === row.file.id &&
      row.previous.path === plannedPath &&
      row.previous.path === renderedPath &&
      row.previous.codec === row.outputCodec &&
      row.previous.container === plannedContainer &&
      row.previous.container === renderedContainer &&
      copyCompatibleSource &&
      tagsUnchanged &&
      assetsUnchanged,
  );
  return {
    ...row,
    renderedPath,
    renderedContainer,
    retainedPath,
    ordinaryUnchanged,
    downgrade: Boolean(
      row.previous &&
        row.outputCodec &&
        demoIsLosslessCodec(row.previous.codec) &&
        !demoIsLosslessCodec(row.outputCodec) &&
        row.assignment?.included,
    ),
    action:
      !row.assignment?.included || !row.file
        ? "Пропустить"
        : ordinaryUnchanged && !plan.force
          ? "Без изменений"
          : row.previous
            ? "Заменить"
            : "Создать",
  };
}

export function applyPlan(
  plan: IncomingPlan,
  files: IncomingFiles,
  releases: IncomingReleases,
  providers: IncomingReleases = providerReleases,
  automatic: false | "audio" | "first" = false,
  artists?: CatalogArtist[],
  destination: PublicationDestination = fixturePublicationDestination,
  positionId?: string,
) {
  let release = releases.find((item) => item.id === plan.localReleaseId);
  if (plan.mode === "local" && !release) return 0;
  if (automatic === "audio" && !release) return 0;
  const rows = positionId
    ? [
        planPositionRow(
          plan,
          files,
          releases,
          providers,
          artists,
          destination,
          positionId,
        ),
      ].filter((row): row is PositionPlanRow => row !== null)
    : planRows(
        plan,
        files,
        releases,
        providers,
        artists,
        destination,
        Boolean(automatic) && plan.mode === "provider",
      );
  const hasApplicableRow = rows.some((row) => {
    if (
      !row.assignment?.included ||
      !row.file ||
      (row.action === "Без изменений" && !plan.force)
    )
      return false;
    const metadataRelease =
      plan.mode === "local" ? (release ?? plan.target) : plan.target;
    return !publicationMetadataIssues(
      metadataRelease,
      row.track ?? row.targetTrack,
      selectedSourceId(row.assignment),
    ).length;
  });
  if (!release && !hasApplicableRow) return 0;
  if (!release) {
    release = {
      ...structuredClone(plan.target),
      id: `import-${plan.target.id}`,
      providerId:
        plan.mode === "provider"
          ? (plan.target.providerId ?? plan.target.id)
          : null,
      tracks: plan.target.tracks.map((track) => ({
        ...track,
        id: `import-${plan.target.id}-t${track.position}`,
        sourceId: null,
        providerTrackId: null,
        published: null,
      })),
    };
    releases.push(release);
    plan.localReleaseId = String(release.id);
  }
  let changed = 0;
  for (const row of rows) {
    const track =
      automatic && plan.mode === "provider"
        ? row.track
        : release.tracks.find((item) =>
            positionId
              ? item.id === positionId
              : item.position === row.targetTrack.position,
          );
    if (!track) continue;
    if (automatic === "first" && row.previous) continue;
    const currentSource = row.previous?.sourceId
      ? files[row.previous.sourceId]
      : undefined;
    if (
      automatic === "audio" &&
      (!row.previous ||
        !row.file ||
        row.previous.sourceId === row.file.id ||
        !currentSource?.sha256 ||
        !row.file.sha256 ||
        currentSource.sha256 === row.file.sha256 ||
        retainedOutputIssue(row.previous, row.file, destination) ||
        !compareDemoAudioUpgrade(
          row.file,
          row.previous.codec,
          currentSource,
          destination.format,
        ).better)
    )
      continue;
    if (positionId && row.action === "Без изменений") continue;
    if (
      plan.retainCurrentPath &&
      (!track.published ||
        !row.file ||
        retainedOutputIssue(track.published, row.file, destination))
    )
      continue;
    if (
      automatic !== "audio" &&
      row.assignment?.included &&
      row.file &&
      publicationMetadataIssues(
        plan.mode === "local" ? release : plan.target,
        row.track ?? row.targetTrack,
        selectedSourceId(row.assignment),
      ).length
    )
      continue;
    const assigned = automatic ? [] : sourceIds(row.assignment);
    const usableAssignments = assigned.filter((id) => usableFile(files[id]));
    const existingSources = [
      ...new Set([
        ...(track.sourceIds ?? []),
        ...(track.sourceId ? [track.sourceId] : []),
      ]),
    ].filter((id) => usableFile(files[id]));
    if (usableAssignments.length)
      track.sourceIds = [
        ...new Set([...existingSources, ...usableAssignments]),
      ];
    if (
      !row.assignment?.included ||
      !row.file ||
      (row.action === "Без изменений" && !plan.force)
    )
      continue;
    if (!automatic) {
      track.sourceId = selectedSourceId(row.assignment);
      track.providerTrackId = row.assignment.providerTrackId;
      if (row.provider) track.recordingId = row.provider.track.recordingId;
    }
    const published = track.published;
    if (automatic === "audio" && row.previous) {
      const replacementTags = replaceAudioMeasurements(
        row.previous.tags,
        desiredTags(release, track, artists, row.file.id),
      );
      const outputCodec = demoOutputCodec(row.file.codec, destination.format);
      for (const key of sourceOnlyAudioTagKeys) {
        delete replacementTags[key];
        if (outputCodec === row.file.codec && Object.hasOwn(row.file.tags, key))
          replacementTags[key] = [...row.file.tags[key]];
      }
      track.published = {
        ...row.previous,
        tags: replacementTags,
        codec: outputCodec,
        sourceId: row.file.id,
        readAt: "Только что · демо",
        matchingConfidence: automaticPairScore(row.assignment, row.file),
      };
      track.metadataBaseline = structuredClone(replacementTags);
      changed++;
      continue;
    }
    const output = choosePublicationOutput(row.file, {
      ...destination,
      artist: originalArtistName(
        demoReleasePrimaryArtistId(release, artists ?? originalArtists) ??
          release.artistId,
        artists,
      ),
      year: effectiveReleaseYear(release),
      release: effectiveReleaseTitle(release),
      position: row.targetTrack.position,
      title: row.targetTrack.title,
    });
    const rebuildSource =
      !published ||
      plan.force ||
      published.sourceId !== row.file.id ||
      published.codec !== demoOutputCodec(row.file.codec, destination.format);
    track.published = {
      tags: structuredClone(row.nextTags),
      coverHash: track.coverHash ?? null,
      lyrics: track.lyrics ?? null,
      codec: demoOutputCodec(row.file.codec, destination.format),
      container:
        rebuildSource || !published ? output.container : published.container,
      sourceId: row.file.id,
      readAt: "Только что · демо",
      path:
        published && plan.retainCurrentPath
          ? published.path
          : rebuildSource || !published
            ? output.path
            : published.path,
      ...(automatic === "first"
        ? { matchingConfidence: automaticPairScore(row.assignment, row.file) }
        : {}),
    };
    track.metadataBaseline = structuredClone(row.nextTags);
    track.metadataBaselineExtras = {
      coverHash: track.coverHash ?? null,
      lyrics: track.lyrics ?? null,
    };
    changed++;
  }
  return changed;
}

type AutomaticPosition = {
  targetTrack: OriginalTrack;
  assignment: Assignment;
  file: SourceFile;
};

/**
 * Proves a complete automatic layout: one exact analyzed folder, every active
 * file covered by a distinct usable pair, and explicit numeric/hash evidence for
 * every pair. Edition positions may stay unassigned; a partial or ambiguous
 * layout rejects the whole group instead of applying its matched part.
 */
function automaticLayout(
  group: IncomingGroup,
  target: OriginalRelease,
  assignments: Record<string, Assignment>,
  files: IncomingFiles,
  threshold: number,
): AutomaticPosition[] | null {
  if (!Number.isFinite(threshold) || threshold < 0 || threshold > 1)
    return null;
  if (target.tracks.length < group.fileIds.length) return null;
  const assigned = new Set<string>();
  const targetIds = new Set<string>();
  const positions: AutomaticPosition[] = [];
  for (const targetTrack of target.tracks) {
    const assignment = assignments[String(targetTrack.id)];
    if (!assignment?.included) continue;
    const fileId = selectedSourceId(assignment);
    if (!fileId || !group.fileIds.includes(fileId)) continue;
    if (assigned.has(fileId) || targetIds.has(String(targetTrack.id)))
      return null;
    const file = files[fileId];
    if (!usableFile(file)) continue;
    const method =
      assignment.sourceMeta[fileId]?.method || assignment.method || "";
    if (
      assignment.providerTrackId !== targetTrack.id ||
      !method.startsWith("Авто") ||
      !sourceIds(assignment).includes(fileId)
    )
      continue;
    const score = automaticPairScore(assignment, file);
    if (score === undefined || score < threshold) continue;
    assigned.add(fileId);
    targetIds.add(String(targetTrack.id));
    positions.push({ targetTrack, assignment, file });
  }
  if (positions.length !== group.fileIds.length) return null;
  if (!group.fileIds.every((id) => assigned.has(id))) return null;
  return positions;
}

/**
 * Adds accepted automatic associations without replacing saved links or the
 * selected publication source, then retires the accepted members like an
 * explicit link save. No manual receipt or preview state is produced.
 */
function saveAutomaticLinks(
  state: IncomingState,
  group: IncomingGroup,
  plan: IncomingPlan,
  positions: readonly AutomaticPosition[],
  releases: IncomingReleases,
): { links: number; retired: number } | null {
  if (
    !positions.length ||
    plan.groupId !== group.id ||
    plan.inventoryVersion !== state.inventoryVersion ||
    plan.mode !== "provider"
  )
    return null;
  const target = plan.target;
  let release = releases.find((item) => item.id === plan.localReleaseId);
  const imported = !release;
  if (!release) {
    release = {
      ...structuredClone(target),
      id: `import-${target.id}`,
      providerId: target.providerId ?? target.id,
      tracks: target.tracks.map((track) => ({
        ...structuredClone(track),
        id: `import-${target.id}-${track.id}`,
        localProviderTrackId: null,
        sourceId: null,
        sourceIds: [],
        providerTrackId: track.id,
        published: null,
        metadataBaseline: undefined,
        metadataBaselineExtras: undefined,
      })),
    };
  }
  const resolved = positions.map(({ targetTrack, file }) => ({
    file,
    tracks: release.tracks.filter(
      (track) => track.providerTrackId === targetTrack.id,
    ),
  }));
  // Do not treat a matching ordinal, another edition or an unrelated saved
  // source association as proof that this exact provider position owns the file.
  for (const { file, tracks } of resolved) {
    const track = tracks[0];
    if (
      tracks.length !== 1 ||
      releases.some((entry) =>
        entry.tracks.some(
          (other) =>
            other !== track &&
            (other.sourceId === file.id || other.sourceIds?.includes(file.id)),
        ),
      )
    )
      return null;
  }
  if (imported) {
    releases.push(release);
    plan.localReleaseId = String(release.id);
  }
  let links = 0;
  const savedFileIds = new Set<string>();
  for (const {
    file,
    tracks: [track],
  } of resolved) {
    const existing = new Set([
      ...(track.sourceIds ?? []),
      ...(track.sourceId ? [track.sourceId] : []),
    ]);
    if (!existing.has(file.id)) {
      existing.add(file.id);
      links++;
    }
    track.sourceIds = [...existing];
    savedFileIds.add(file.id);
  }
  if (!savedFileIds.size) return null;

  group.fileIds = group.fileIds.filter((id) => !savedFileIds.has(id));
  for (const assignments of Object.values(group.drafts))
    for (const targetId of Object.keys(assignments))
      for (const fileId of savedFileIds)
        unassignFile(assignments, targetId, fileId);
  group.processed = group.fileIds.length === 0;
  return { links, retired: savedFileIds.size };
}

/**
 * Retires a fully evidenced automatic layout independently of publication.
 * First publication is opt-in and requires the complete edition; audio upgrades
 * are applied regardless of that setting when strictly better. A manual source
 * selection is never replaced by an automatic first publication.
 */
export function runAutomaticMatching(
  state: IncomingState,
  files: IncomingFiles,
  releases: IncomingReleases,
  readyFolders: readonly ReadonlySet<string>[],
  matchingPairThreshold: number,
  autoPublishCompleteAlbums: boolean,
  providers: IncomingReleases = providerReleases,
  artists?: CatalogArtist[],
  destination: PublicationDestination = fixturePublicationDestination,
): AutomaticMatchingResult {
  const result: AutomaticMatchingResult = {
    publications: 0,
    links: 0,
    retired: 0,
  };
  const retiredGroups = new Set<string>();
  for (const group of state.groups) {
    if (
      group.processed ||
      !group.autoEligible ||
      group.mode !== "provider" ||
      !group.fileIds.length
    )
      continue;
    const folder = readyFolders.find(
      (candidate) =>
        candidate.size === group.fileIds.length &&
        group.fileIds.every((id) => candidate.has(id)),
    );
    if (!folder) continue;
    const assignments = getDraft(group, files, providers, releases);
    const target = findTarget(group, releases, providers);
    if (!target) continue;
    const positions = automaticLayout(
      group,
      target,
      assignments,
      files,
      matchingPairThreshold,
    );
    if (!positions) continue;
    const plan = makeGroupPlan(state, group, files, releases, providers);
    if (!plan) continue;
    plan.assignments = Object.fromEntries(
      positions.map(
        ({ targetTrack, assignment, file }) =>
          [
            String(targetTrack.id),
            {
              ...structuredClone(assignment),
              sourceIds: [file.id],
              selectedSourceId: file.id,
              included: true,
            },
          ] as const,
      ),
    );

    const saved = saveAutomaticLinks(state, group, plan, positions, releases);
    if (!saved) continue;
    if (group.fileIds.length === 0) retiredGroups.add(group.id);
    result.links += saved.links;
    result.retired += saved.retired;
    result.publications += applyPlan(
      plan,
      files,
      releases,
      providers,
      "audio",
      artists,
      destination,
    );

    if (
      autoPublishCompleteAlbums &&
      positions.length === plan.target.tracks.length
    ) {
      const localRelease = releases.find(
        (item) => item.id === plan.localReleaseId,
      );
      plan.assignments = Object.fromEntries(
        positions
          .filter(({ targetTrack, file }) => {
            const track = localRelease?.tracks.find(
              (item) => item.providerTrackId === targetTrack.id,
            );
            // Never publish over a saved manual selection or existing CURRENT.
            return (
              !track?.published &&
              (!track?.sourceId || track.sourceId === file.id)
            );
          })
          .flatMap(({ targetTrack }) => {
            const assignment = plan.assignments[String(targetTrack.id)];
            return assignment
              ? [[String(targetTrack.id), assignment] as const]
              : [];
          }),
      );
      if (Object.keys(plan.assignments).length)
        result.publications += applyPlan(
          plan,
          files,
          releases,
          providers,
          "first",
          artists,
          destination,
        );
    }
  }
  if (retiredGroups.size > 0) {
    state.groups = state.groups.filter((group) => !retiredGroups.has(group.id));
  }
  return result;
}

export function removeRootFromIncomingInventory(
  rootId: string,
  state: IncomingState,
  files: IncomingFiles,
  releases: IncomingReleases,
) {
  for (const file of Object.values(files)) {
    file.locations = (file.locations ?? []).filter(
      (location) => location.rootId !== rootId,
    );
    if (!file.locations.length) {
      file.available = false;
      for (const group of state.groups) {
        group.fileIds = group.fileIds.filter((id) => id !== file.id);
        for (const assignments of Object.values(group.drafts))
          for (const assignment of Object.values(assignments)) {
            assignment.sourceIds = assignment.sourceIds.filter(
              (id) => id !== file.id,
            );
            if (assignment.selectedSourceId === file.id)
              assignment.selectedSourceId = null;
            delete assignment.sourceMeta[file.id];
          }
      }
      for (const release of releases)
        for (const track of release.tracks) {
          track.sourceIds = (track.sourceIds ?? []).filter(
            (id) => id !== file.id,
          );
          if (track.sourceId === file.id) track.sourceId = null;
        }
    }
  }
  state.inventoryVersion++;
}
