import type {
  IncomingFileResponse,
  IncomingGroupResponse,
  IncomingLocationResponse,
} from "../../api/generated/client.schemas";

export type IncomingOverviewOrder = {
  groups: IncomingGroupResponse[];
  folderPaths: ReadonlyMap<string, readonly string[]>;
};

type FolderEvidence = {
  source: string;
  folder: string;
  members: number;
};

type GroupEvidence = {
  representative?: FolderEvidence;
  folderPaths: readonly string[];
};

// Group order must not depend on the interface language.
const natural = new Intl.Collator("en", { numeric: true });

function naturalCompare(a: string, b: string) {
  return natural.compare(a, b) || (a < b ? -1 : a > b ? 1 : 0);
}

function naturalPathCompare(a: string, b: string) {
  const left = a.split("/").filter(Boolean);
  const right = b.split("/").filter(Boolean);
  const depth = Math.min(left.length, right.length);
  for (let index = 0; index < depth; index++) {
    const compared = naturalCompare(left[index], right[index]);
    if (compared) return compared;
  }
  return left.length - right.length;
}

function parentOf(path: string) {
  return path.slice(0, Math.max(0, path.lastIndexOf("/")));
}

function folderOf(path: string) {
  const index = path.lastIndexOf("/");
  if (index > 0) return path.slice(0, index);
  return index === 0 ? "/" : "";
}

function normalizeRoot(path: string) {
  const normalized = path.replace(/\\/g, "/");
  return normalized === "/" ? normalized : normalized.replace(/\/+$/, "");
}

function joinPath(root: string, relative: string) {
  return root === "/" ? `/${relative}` : `${root}/${relative}`;
}

/*
 * The production API exposes locations, not a canonical per-file path. The
 * configured root is the source identity shown to the operator; the inventory
 * path is only a fallback for a snapshot without it. A location without a root
 * path cannot be placed in a source folder and is treated as missing evidence.
 */
function locationRoot(location: IncomingLocationResponse) {
  return normalizeRoot(location.configured_path || location.inventory_path);
}

function groupEvidence(
  group: IncomingGroupResponse,
  filesById: ReadonlyMap<string, IncomingFileResponse>,
): GroupEvidence {
  const folders = new Map<string, FolderEvidence>();
  const folderPaths = new Set<string>();
  const members = new Set([
    ...(group.members ?? []),
    ...(group.unready_members ?? []),
  ]);
  for (const id of members) {
    const seen = new Set<string>();
    for (const location of filesById.get(id)?.locations ?? []) {
      const relative = location.relative_path;
      const root = locationRoot(location);
      if (!relative || !root) continue;
      const folder = parentOf(relative);
      const key = JSON.stringify([root, folder]);
      if (!seen.has(key)) {
        seen.add(key);
        const existing = folders.get(key);
        if (existing) existing.members += 1;
        else folders.set(key, { source: root, folder, members: 1 });
      }
      const path = folderOf(joinPath(root, relative));
      if (path) folderPaths.add(path);
    }
  }
  /*
   * A group is represented by the exact folder holding the most distinct
   * members. The API has no primary/canonical file path, so equal counts keep
   * the first folder seen instead of inventing a precedence.
   */
  let representative: FolderEvidence | undefined;
  for (const folder of folders.values()) {
    if (!representative || folder.members > representative.members) {
      representative = folder;
    }
  }
  return { representative, folderPaths: [...folderPaths] };
}

function compareFolders(a?: FolderEvidence, b?: FolderEvidence) {
  // Groups without source paths follow every located group.
  if (!a || !b) return a ? -1 : b ? 1 : 0;
  return (
    naturalCompare(a.source, b.source) ||
    naturalPathCompare(a.folder, b.folder) ||
    naturalCompare(a.folder, b.folder)
  );
}

/**
 * Orders incoming groups by exact source and folder for the overview, keeping
 * groups from one folder adjacent without changing their composition. Returns
 * the full folder paths of every member location for destination search.
 */
export function incomingOverviewOrder(
  groups: readonly IncomingGroupResponse[],
  filesById: ReadonlyMap<string, IncomingFileResponse>,
): IncomingOverviewOrder {
  const evidence = groups.map((group) => groupEvidence(group, filesById));
  const order = groups.map((_, index) => index);
  order.sort(
    (a, b) =>
      compareFolders(evidence[a].representative, evidence[b].representative) ||
      a - b,
  );
  const folderPaths = new Map<string, readonly string[]>();
  groups.forEach((group, index) => {
    folderPaths.set(group.id, evidence[index].folderPaths);
  });
  return { groups: order.map((index) => groups[index]), folderPaths };
}
