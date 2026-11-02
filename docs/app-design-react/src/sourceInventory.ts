import type { OriginalRelease, SourceFiles } from "./catalogData";

export function removeRootFromInventory(
	files: SourceFiles,
	releases: OriginalRelease[],
	rootId: string,
) {
	const nextFiles = structuredClone(files);
	const nextReleases = structuredClone(releases);
	const orphaned = new Set<string>();
	for (const file of Object.values(nextFiles)) {
		if (!file.locations) continue;
		file.locations = file.locations.filter(
			(location) => location.rootId !== rootId,
		);
		if (file.locations.length) continue;
		file.available = false;
		orphaned.add(file.id);
		for (const release of nextReleases)
			for (const track of release.tracks) {
				const sources = [
					...new Set([
						...(track.sourceIds || []),
						...(track.sourceId ? [track.sourceId] : []),
					]),
				];
				if (!sources.includes(file.id)) continue;
				track.sourceIds = sources.filter((id) => id !== file.id);
				if (track.sourceId === file.id) track.sourceId = null;
			}
		if (!file.sha256) delete nextFiles[file.id];
	}
	return { files: nextFiles, releases: nextReleases, orphaned };
}

export const rootLocations = (files: SourceFiles, rootId: string) =>
	Object.values(files)
		.flatMap((file) => file.locations || [])
		.filter((location) => location.rootId === rootId);
