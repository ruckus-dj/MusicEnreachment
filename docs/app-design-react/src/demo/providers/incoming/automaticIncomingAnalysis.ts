import type { SourceFiles } from "../library/catalogData";
import {
  inventoryPathStale,
  scanScopeLocations,
} from "../sources/sourceInventory";
import type {
  SourceProcessingFile,
  SourceProcessingState,
} from "../sources/sourceProcessingModel";

/** Root fields that prove its inventory belongs to the folder it still points at. */
export interface AutomaticIncomingRoot {
  id: string;
  path: string;
  available: boolean | null;
  inventoryPath?: string;
  fixturePath?: string;
  lastSuccessfulScanAt?: string;
}

/** One folder whose admitted files all finished the current analysis work. */
export interface ReadyAutomaticIncomingFolder {
  /** Stable root + parent-folder identity; work revisions do not change it. */
  key: string;
  /** Committed-work identity: work revisions plus the admitted file set. */
  signature: string;
  fileIds: ReadonlySet<string>;
}

type AdmittedMember = {
  fileId: string;
  locationId: string;
  path: string;
  observedSize: number | string;
  observedMtime?: string;
};

const workKey = (rootId: string, locationId: string) =>
  `${rootId}:${locationId}`;

const collator = new Intl.Collator("en", { numeric: true });

const parentOf = (path: string) => {
  const normalized = path.replace(/\\/g, "/");
  return normalized.slice(0, Math.max(0, normalized.lastIndexOf("/")));
};

/** A failed check or a changed folder leaves the previous inventory uncommitted. */
const currentInventory = (root: AutomaticIncomingRoot) =>
  root.available === true &&
  Boolean(root.lastSuccessfulScanAt) &&
  !inventoryPathStale(root);

/**
 * Completed work is only reusable when it describes this exact location at the
 * observed size and mtime; otherwise it belongs to an older inventory pass.
 */
function completedRevision(
  work: SourceProcessingFile | undefined,
  root: AutomaticIncomingRoot,
  member: AdmittedMember,
): number | null {
  if (
    !work ||
    work.id !== workKey(root.id, member.locationId) ||
    work.rootId !== root.id ||
    work.fileId !== member.fileId ||
    work.path !== member.path ||
    work.status !== "completed" ||
    work.blockedReason !== undefined ||
    work.workRevision === undefined ||
    !work.steps.some((step) => step.id === "audio_inspection") ||
    !work.steps.some((step) => step.id === "fingerprint") ||
    !work.steps.every((step) => step.status === "succeeded") ||
    work.observedMtime !== member.observedMtime ||
    work.observedSize !== member.observedSize
  )
    return null;
  return work.workRevision;
}

/**
 * Folder batches ready for automatic incoming analysis. A batch is committed
 * only after a successful scan of the current folder, and only when every
 * admitted direct sibling of that folder carries completed work for the same
 * observation. Tags are deliberately not consulted: missing audio metadata is
 * not evidence that a scanned file is not audio.
 */
export function readyAutomaticIncomingFolders({
  roots,
  files,
  processing,
}: {
  roots: readonly AutomaticIncomingRoot[];
  files: SourceFiles;
  processing: SourceProcessingState;
}): ReadyAutomaticIncomingFolder[] {
  const workById = new Map(
    processing.files.map((work) => [work.id, work] as const),
  );
  const ready: ReadyAutomaticIncomingFolder[] = [];

  for (const root of roots) {
    if (!currentInventory(root)) continue;

    const admitted = new Set(
      scanScopeLocations(files, root).map((location) =>
        workKey(location.rootId, location.id),
      ),
    );
    const folders = new Map<string, AdmittedMember[]>();
    for (const file of Object.values(files))
      for (const location of file.locations ?? []) {
        if (location.rootId !== root.id) continue;
        if (!admitted.has(workKey(location.rootId, location.id))) continue;
        const key = `${root.id}:${parentOf(location.path)}`;
        const members = folders.get(key) ?? [];
        members.push({
          fileId: file.id,
          locationId: location.id,
          path: location.path,
          observedSize: location.size ?? file.size,
          observedMtime: location.mtime,
        });
        folders.set(key, members);
      }

    for (const [key, members] of folders) {
      let complete = true;
      const committed: (AdmittedMember & { revision: number })[] = [];
      for (const member of members) {
        const revision = completedRevision(
          workById.get(workKey(root.id, member.locationId)),
          root,
          member,
        );
        if (revision === null) {
          complete = false;
          break;
        }
        committed.push({ ...member, revision });
      }
      if (!complete) continue;

      committed.sort(
        (a, b) =>
          collator.compare(a.path, b.path) ||
          collator.compare(a.locationId, b.locationId),
      );
      ready.push({
        key,
        signature: JSON.stringify([
          root.path,
          committed.map((member) => [
            member.locationId,
            member.fileId,
            member.revision,
            member.observedMtime,
            member.observedSize,
          ]),
        ]),
        fileIds: new Set(committed.map((member) => member.fileId)),
      });
    }
  }

  return ready.sort((a, b) => collator.compare(a.key, b.key));
}
