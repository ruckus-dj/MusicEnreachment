import type { OriginalRelease, SourceFiles } from "../library/catalogData";

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

type InventoryRoot = {
	id: string;
	path: string;
	inventoryPath?: string;
	fixturePath?: string;
	lastSuccessfulScanAt?: string;
};

function scopedLocations(
	files: SourceFiles,
	root: InventoryRoot,
	path: string,
) {
	const locations = rootLocations(files, root.id);
	// Keep the deliberately aliased fixture locations at their original root.
	// Once the operator chooses another folder, do not reattribute those rows.
	if (path === (root.fixturePath ?? root.path)) return locations;
	const prefix = `${path.replace(/\/+$/, "")}/`;
	return locations.filter((location) => location.path.startsWith(prefix));
}

/** The retained inventory belongs to the previous folder until the next scan succeeds. */
export const inventoryPathStale = (root: InventoryRoot) =>
	Boolean(
		root.lastSuccessfulScanAt &&
			root.inventoryPath &&
			root.inventoryPath !== root.path,
	);

/**
 * Locations captured by the last successful scan.
 */
export function retainedInventoryLocations(
	files: SourceFiles,
	root: InventoryRoot,
) {
	if (!root.lastSuccessfulScanAt) return [];
	return scopedLocations(files, root, root.inventoryPath ?? root.path);
}

/**
 * Locations a scan of the current folder admits. Once the folder changed, a
 * scan cannot reuse paths from the retained inventory: it is scoped to the new
 * folder and may legitimately find nothing.
 */
export function scanScopeLocations(files: SourceFiles, root: InventoryRoot) {
	return scopedLocations(files, root, root.path);
}
