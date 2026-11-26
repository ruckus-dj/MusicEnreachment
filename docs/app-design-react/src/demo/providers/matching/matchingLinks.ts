import {
  findProviderTrack,
  type IncomingFiles,
  type IncomingGroup,
  type IncomingPlan,
  type IncomingReleases,
  type IncomingState,
  type MatchingLinkReceipt,
  type MatchingLinkReceiptPosition,
  selectedSourceId,
  sourceIds,
  unassignFile,
  usableFile,
} from "../incoming/incomingModel";

export type SaveMatchingLinksResult = {
  receipt: MatchingLinkReceipt;
  positions: number;
  links: number;
  remaining: number;
};

function localTrackFor(
  release: IncomingReleases[number],
  position: number,
): IncomingReleases[number]["tracks"][number] | undefined {
  return release.tracks.find((track) => track.position === position);
}

function localTrackForBinding(
  release: IncomingReleases[number],
  bindingId: string,
): IncomingReleases[number]["tracks"][number] | undefined {
  return release.tracks.find(
    (track) => track.localProviderTrackId === bindingId,
  );
}

function materializeLocalRelease(
  target: IncomingReleases[number],
  providerId: string,
): IncomingReleases[number] {
  const id = `local-${providerId}`;
  return {
    ...structuredClone(target),
    id,
    provider: "Local",
    providerId,
    tracks: target.tracks.map((track) => ({
      ...structuredClone(track),
      id: `${id}-${track.id}`,
      localProviderTrackId: track.id,
      providerTrackId: null,
      sourceId: null,
      sourceIds: [],
      published: null,
      metadataBaseline: undefined,
      metadataBaselineExtras: undefined,
    })),
  };
}

/**
 * Saves accepted source links without publishing anything. Existing project
 * links and the current publication stay untouched; only newly assigned usable
 * files are added to the position association list.
 */
export function saveMatchingLinks(
  plan: IncomingPlan,
  group: IncomingGroup,
  state: IncomingState,
  files: IncomingFiles,
  releases: IncomingReleases,
  providers: IncomingReleases,
): SaveMatchingLinksResult | null {
  if (
    plan.groupId !== group.id ||
    plan.inventoryVersion !== state.inventoryVersion
  )
    return null;
  const accepted = new Map<
    string,
    { sourceIds: string[]; selectedSourceId: string | null; included: boolean }
  >();
  const activeFiles = new Set(group.fileIds);
  for (const track of plan.target.tracks) {
    const assignment = plan.assignments[String(track.id)];
    const ids = [...new Set(sourceIds(assignment))].filter(
      (id) => activeFiles.has(id) && usableFile(files[id]),
    );
    if (!ids.length) continue;
    const selected = selectedSourceId(assignment);
    accepted.set(String(track.id), {
      sourceIds: ids,
      selectedSourceId: selected && ids.includes(selected) ? selected : null,
      included: true,
    });
  }
  if (!accepted.size) return null;

  const localProviderId = plan.mode === "local" ? plan.target.providerId : null;
  if (plan.mode === "local") {
    if (!localProviderId) return null;
    const current = providers.find(
      (release) =>
        release.provider === "Local" &&
        release.providerId === localProviderId &&
        release.id === plan.target.id,
    );
    if (!current) return null;
  }
  let release =
    plan.mode === "local"
      ? releases.find(
          (item) =>
            item.provider === "Local" && item.providerId === localProviderId,
        )
      : releases.find((item) => item.id === plan.localReleaseId);
  if (!release) {
    if (plan.mode === "local") {
      release = materializeLocalRelease(plan.target, localProviderId ?? "");
      releases.push(release);
    } else {
      release = {
        ...structuredClone(plan.target),
        id: `import-${plan.target.id}`,
        providerId: plan.target.providerId ?? plan.target.id,
        tracks: plan.target.tracks.map((track) => ({
          ...structuredClone(track),
          id: `import-${plan.target.id}-t${track.position}`,
          sourceId: null,
          sourceIds: [],
          providerTrackId: null,
          published: null,
          metadataBaseline: undefined,
          metadataBaselineExtras: undefined,
        })),
      };
      releases.push(release);
    }
    plan.localReleaseId = String(release.id);
  }
  if (plan.mode === "local" && release.tracks.length > 0) {
    const targetTrackIds = new Set(
      plan.target.tracks.map((track) => String(track.id)),
    );
    const boundTracks = release.tracks.filter(
      (track) => track.localProviderTrackId,
    );
    const boundTrackIds = boundTracks.map((track) =>
      String(track.localProviderTrackId),
    );
    if (
      !boundTracks.length ||
      new Set(boundTrackIds).size !== boundTrackIds.length ||
      boundTracks.some(
        (track) => !targetTrackIds.has(String(track.localProviderTrackId)),
      )
    )
      return null;
  }

  const positions: MatchingLinkReceiptPosition[] = [];
  const savedFileIds = new Set<string>();
  let links = 0;
  for (const targetTrack of plan.target.tracks) {
    const assignment = accepted.get(String(targetTrack.id));
    if (!assignment) continue;
    let track =
      plan.mode === "local"
        ? localTrackForBinding(release, String(targetTrack.id))
        : localTrackFor(release, targetTrack.position);
    if (!track) {
      track = {
        ...structuredClone(targetTrack),
        id:
          plan.mode === "local"
            ? `${release.id}-${targetTrack.id}`
            : `${release.id}-t${targetTrack.position}`,
        localProviderTrackId:
          plan.mode === "local" ? String(targetTrack.id) : null,
        sourceId: null,
        sourceIds: [],
        providerTrackId: null,
        published: null,
        metadataBaseline: undefined,
        metadataBaselineExtras: undefined,
      };
      release.tracks.push(track);
    }
    if (plan.mode === "local")
      track.localProviderTrackId = String(targetTrack.id);
    const existing = new Set([
      ...(track.sourceIds ?? []),
      ...(track.sourceId ? [track.sourceId] : []),
    ]);
    for (const id of assignment.sourceIds) {
      savedFileIds.add(id);
      if (existing.has(id)) continue;
      existing.add(id);
      links++;
    }
    track.sourceIds = [...existing];
    const draft = plan.assignments[String(targetTrack.id)];
    const provider = findProviderTrack(
      draft?.providerTrackId ?? null,
      providers,
    );
    if (plan.mode === "provider") {
      if (draft?.providerTrackId) track.providerTrackId = draft.providerTrackId;
      if (provider) track.recordingId = provider.track.recordingId;
    } else if (provider) {
      track.recordingId = provider.track.recordingId;
    }
    positions.push({
      trackId: String(track.id),
      title: String(targetTrack.title),
      position: targetTrack.position,
      displayedNumber: targetTrack.displayedNumber,
      sourceIds: [...assignment.sourceIds],
      selectedSourceId: assignment.selectedSourceId,
      included: assignment.included,
    });
  }
  if (!positions.length) return null;

  group.fileIds = group.fileIds.filter((id) => !savedFileIds.has(id));
  for (const assignments of Object.values(group.drafts))
    for (const targetId of Object.keys(assignments))
      for (const fileId of savedFileIds)
        unassignFile(assignments, targetId, fileId);
  group.processed = group.fileIds.length === 0;

  const receipt: MatchingLinkReceipt = {
    id: `link-${group.id}-${state.receipts.filter((item) => item.groupId === group.id).length + 1}`,
    groupId: group.id,
    groupTitle: group.title,
    releaseId: String(release.id),
    releaseTitle: release.title,
    providerReleaseId: release.providerId ?? null,
    positions,
    remainingFileIds: [...group.fileIds],
    remainingFileNames: group.fileIds.map((id) => files[id]?.name ?? id),
  };
  state.receipts.push(receipt);
  state.plan = null;
  state.confirmDowngrade = false;

  return {
    receipt,
    positions: positions.length,
    links,
    remaining: group.fileIds.length,
  };
}
