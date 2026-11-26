import {
	type BrowseDirectory,
	DirectoryBrowseError,
} from "../../ui/components/DirectoryPickerView";

const directoryTree: Record<string, readonly string[]> = {
	"/": ["mnt", "srv", "var"],
	"/mnt": [
		"archive",
		"audio",
		"backup",
		"inbox",
		"music",
		"projects",
		"samples",
		"shared",
		"staging",
	],
	"/mnt/archive": [],
	"/mnt/inbox": ["Northern Lines", "Road Notes", "Small Hours"],
	"/mnt/audio": ["incoming", "masters"],
	"/mnt/music": ["Albums", "Compilations"],
	"/mnt/music/Albums": ["Artist A", "Artist B"],
	"/mnt/music/Compilations": ["Road Notes"],
	"/srv": ["media", "music"],
	"/srv/media": ["library"],
	"/srv/media/library": [],
	"/srv/music": ["Albums", "Compilations"],
	"/srv/music/Albums": ["Northern Lines", "Small Hours"],
	"/srv/music/Compilations": ["Road Notes"],
	"/var": ["lib"],
	"/var/lib": ["melotrove"],
	"/var/lib/melotrove": ["cache", "tools"],
	"/var/lib/melotrove/tools": [],
};

export function demoDirectoryStart(path: string) {
	return Object.hasOwn(directoryTree, path) ? path : "/";
}

export const browseDemoDirectory: BrowseDirectory = async (
	path,
	offset,
	signal,
) => {
	signal.throwIfAborted();
	const currentPath = path.trim().replace(/\/+$/, "") || "/";
	if (!Object.hasOwn(directoryTree, currentPath)) {
		throw new DirectoryBrowseError("notFound");
	}
	if (currentPath === "/mnt/archive") {
		throw new DirectoryBrowseError("unreadable");
	}
	const entries = directoryTree[currentPath];
	const nextOffset = offset + 200;
	return {
		currentPath,
		parentPath: currentPath.slice(0, currentPath.lastIndexOf("/")) || "/",
		directories: entries
			.slice(offset, nextOffset)
			.map((entry) => `${currentPath === "/" ? "" : currentPath}/${entry}`),
		offset,
		nextOffset: nextOffset < entries.length ? nextOffset : undefined,
	};
};
