import type { Locale } from "./i18n/types";
import type { IncomingFiles, IncomingGroup } from "./incomingModel";
import { incomingMessage, type IncomingMessage } from "./incomingMessages";

export type IncomingOverviewPath = {
	key: string;
	label: string;
};

export type IncomingOverviewRow = {
	group: IncomingGroup;
	name: string;
	memberCount: number;
	pathCount: number;
	readyCount: number;
	paths: IncomingOverviewPath[];
};

type GroupEvidence = {
	album?: string;
	folder?: string;
	path?: string;
	qualifiedPath?: string;
	otherFiles: number;
};

type GroupName = Pick<GroupEvidence, "album" | "path" | "otherFiles">;

type SourceLocation = {
	relative: string;
	root?: string;
	qualified: string;
};

const folded = (value: string) => value.normalize("NFC").toLowerCase();

function normalizeRoot(path: string) {
	return path.replace(/\\/g, "/").replace(/\/+$/, "");
}

function commonDirectory(paths: string[]) {
	const parts = paths.map((path) =>
		path.replace(/\\/g, "/").split("/").filter(Boolean),
	);
	if (!parts.length) return "";
	const directory: string[] = [];
	const depth = Math.min(...parts.map((item) => Math.max(0, item.length - 1)));
	for (let index = 0; index < depth; index++) {
		const segment = parts[0][index];
		if (!parts.every((item) => item[index] === segment)) break;
		directory.push(segment);
	}
	return directory.length ? `/${directory.join("/")}` : "";
}

function sourceLocation(path: string, roots: string[]): SourceLocation {
	const normalized = path.replace(/\\/g, "/");
	const root = roots.find(
		(item) => normalized === item || normalized.startsWith(`${item}/`),
	);
	return root
		? {
				relative: normalized.slice(root.length + 1),
				root,
				qualified: normalized,
			}
		: { relative: normalized, qualified: normalized };
}

function groupEvidence(
	group: IncomingGroup,
	files: IncomingFiles,
	roots: string[],
): GroupEvidence {
	const memberIds = [...new Set(group.fileIds)];
	const albums = new Map<string, string>();
	const folders = new Map<string, { label: string; members: number }>();
	const paths: SourceLocation[] = [];
	for (const id of memberIds) {
		const file = files[id];
		for (const [tag, values] of Object.entries(file?.tags ?? {})) {
			if (tag.trim().toUpperCase() !== "ALBUM") continue;
			for (const raw of values ?? []) {
				const title = raw.trim();
				const key = folded(title);
				if (key && !albums.has(key)) albums.set(key, title);
			}
		}
		const seen = new Set<string>();
		for (const location of file?.locations ?? []) {
			const source = sourceLocation(location.path, roots);
			const parent = source.relative.slice(
				0,
				Math.max(0, source.relative.lastIndexOf("/")),
			);
			const key = `${location.rootId}:${parent}`;
			if (!seen.has(key)) {
				seen.add(key);
				folders.set(key, {
					label: parent || source.root || "",
					members: (folders.get(key)?.members ?? 0) + 1,
				});
			}
			paths.push(source);
		}
	}
	paths.sort(
		(a, b) =>
			a.relative.localeCompare(b.relative, "en") ||
			a.qualified.localeCompare(b.qualified, "en"),
	);
	const rootedPaths = paths.filter((item) => item.root);
	const first = rootedPaths[0] ?? paths[0];
	const commonFolders = [...folders.values()].filter(
		(folder) => folder.members === memberIds.length,
	);
	const commonFolder =
		memberIds.length > 1 && commonFolders.length === 1
			? commonFolders[0].label
			: undefined;
	return {
		album: albums.size === 1 ? albums.values().next().value : undefined,
		folder: commonFolder || undefined,
		path: first?.relative,
		qualifiedPath: first?.qualified,
		otherFiles: Math.max(0, memberIds.length - 1),
	};
}

export function incomingGroupNameText(name: GroupName, locale: Locale) {
	if (name.album) {
		return name.path ? `${name.album} · ${name.path}` : name.album;
	}
	if (!name.path) return incomingMessage(locale, "sourcePathUnavailable");
	if (!name.otherFiles) return name.path;
	const category = new Intl.PluralRules(locale).select(name.otherFiles);
	const key: IncomingMessage =
		category === "one"
			? "remainingFilesOne"
			: category === "few"
				? "remainingFilesFew"
				: category === "many"
					? "remainingFilesMany"
					: "remainingFilesOther";
	return `${name.path} ${incomingMessage(locale, key, {
		count: name.otherFiles,
	})}`;
}

export function incomingOverviewRows(
	groups: IncomingGroup[],
	files: IncomingFiles,
	locale: Locale,
	sourceRoots: readonly string[] = [],
): IncomingOverviewRow[] {
	const configuredRoots = [...new Set(sourceRoots.map(normalizeRoot))]
		.filter(Boolean)
		.sort((a, b) => b.length - a.length);
	// The standalone prototype has no API-provided relative paths. Prefer the
	// configured source roots; fall back to one derived from the demo queue
	// paths when the caller has no root metadata.
	const fallbackRoot = commonDirectory(
		groups.flatMap((group) =>
			[...new Set(group.fileIds)].flatMap((id) => {
				const path = files[id]?.path;
				return path ? [path] : [];
			}),
		),
	);
	const roots =
		configuredRoots.length > 0
			? configuredRoots
			: fallbackRoot
				? [fallbackRoot]
				: [];
	const evidence = groups.map((group) => groupEvidence(group, files, roots));
	const counts = new Map<string, number>();
	for (const item of evidence) {
		const text = item.album ?? item.folder ?? item.path;
		if (text) counts.set(folded(text), (counts.get(folded(text)) ?? 0) + 1);
	}
	return groups.map((group, index) => {
		const item = evidence[index];
		const text = item.album ?? item.folder ?? item.path;
		const duplicate = Boolean(text && (counts.get(folded(text)) ?? 0) > 1);
		const name = incomingGroupNameText(
			{
				album: item.album,
				path: duplicate
					? item.qualifiedPath
					: item.album
						? undefined
						: (item.folder ?? item.path),
				otherFiles:
					!item.album && (!item.folder || duplicate) ? item.otherFiles : 0,
			},
			locale,
		);
		const memberIds = [...new Set(group.fileIds)];
		const paths = new Map<string, string>();
		for (const id of memberIds) {
			for (const location of files[id]?.locations ?? []) {
				const key = `${location.rootId}:${location.path}`;
				if (paths.has(key)) continue;
				const source = sourceLocation(location.path, roots);
				paths.set(
					key,
					source.root ? `${source.root} / ${source.relative}` : source.relative,
				);
			}
		}
		return {
			group,
			name,
			memberCount: memberIds.length,
			pathCount: paths.size,
			readyCount: memberIds.filter(
				(id) =>
					Boolean(files[id]?.available) &&
					Boolean(files[id]?.locations?.length),
			).length,
			paths: [...paths].map(([key, label]) => ({ key, label })),
		};
	});
}
