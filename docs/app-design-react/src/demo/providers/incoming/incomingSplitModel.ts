import {
	type IncomingFiles,
	type IncomingGroup,
	type IncomingState,
	unassignFile,
	usableFile,
} from "./incomingModel";

/** Files that can be detached without making the source group unreadable. */
export function splittableFileIds(
	group: IncomingGroup,
	files: IncomingFiles,
): string[] {
	return [...new Set(group.fileIds)].filter((id) => usableFile(files[id]));
}

/** Splitting keeps the source group: it needs a second eligible file. */
export function canSplitGroup(group: IncomingGroup, files: IncomingFiles) {
	return splittableFileIds(group, files).length > 1;
}

/**
 * Detach one exact file into a manual group without touching source files,
 * releases, publication state or tag projections.
 *
 * The manual group reuses the source target identity (provider candidate or
 * local release) so the existing Matching route resolves a real target instead
 * of a dangling release reference. It is never auto-eligible or processed, and
 * the source group keeps its remaining assignments: only the detached file is
 * removed from its drafts. Other groups are copied unchanged.
 */
export function splitFileIntoNewGroup(
	state: IncomingState,
	files: IncomingFiles,
	sourceGroupId: string,
	fileId: string,
): IncomingState | null {
	const sourceIndex = state.groups.findIndex(
		(group) => group.id === sourceGroupId,
	);
	if (sourceIndex < 0) return null;
	const source = state.groups[sourceIndex];
	if (!source.fileIds.includes(fileId)) return null;
	if (!usableFile(files[fileId])) return null;
	if (!canSplitGroup(source, files)) return null;

	const next = structuredClone(state);
	const nextSource = next.groups[sourceIndex];
	nextSource.fileIds = nextSource.fileIds.filter((id) => id !== fileId);
	for (const assignments of Object.values(nextSource.drafts))
		for (const targetId of Object.keys(assignments))
			unassignFile(assignments, targetId, fileId);
	nextSource.autoEligible = false;
	nextSource.processed = false;
	nextSource.reason = { code: "incomplete" };
	next.groups.splice(sourceIndex + 1, 0, {
		id: `split-${crypto.randomUUID()}`,
		title: source.title,
		artistId: source.artistId,
		releaseId: source.releaseId,
		fileIds: [fileId],
		candidateId: source.candidateId,
		mode: source.mode,
		autoEligible: false,
		processed: false,
		reason: { code: "incomplete" },
		drafts: {
			[source.mode === "local" ? "local" : String(source.candidateId)]: {},
		},
	});
	if (next.plan?.groupId === sourceGroupId) {
		next.plan = null;
		next.confirmDowngrade = false;
	}
	if (next.groupId === sourceGroupId && next.selectedFile === fileId)
		next.selectedFile = null;
	return next;
}
