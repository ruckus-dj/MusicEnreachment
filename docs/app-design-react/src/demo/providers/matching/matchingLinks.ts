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

	let release = releases.find((item) => item.id === plan.localReleaseId);
	if (!release) {
		release = {
			...structuredClone(plan.target),
			id: `import-${plan.target.id}`,
			providerId:
				plan.mode === "provider"
					? (plan.target.providerId ?? plan.target.id)
					: null,
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
		plan.localReleaseId = String(release.id);
	}

	const positions: MatchingLinkReceiptPosition[] = [];
	const savedFileIds = new Set<string>();
	let links = 0;
	for (const targetTrack of plan.target.tracks) {
		const assignment = accepted.get(String(targetTrack.id));
		if (!assignment) continue;
		let track = localTrackFor(release, targetTrack.position);
		if (!track) {
			track = {
				...structuredClone(targetTrack),
				id: `${release.id}-t${targetTrack.position}`,
				sourceId: null,
				sourceIds: [],
				providerTrackId: null,
				published: null,
				metadataBaseline: undefined,
				metadataBaselineExtras: undefined,
			};
			release.tracks.push(track);
		}
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
	group.drafts = {};
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
