export const settingsDraftStorageKey = "melotrove.settings-text-draft.v1";

export const settingsDraftFields = [
  "outputDirectory",
  "workDirectory",
  "publicationPathTemplate",
  "sourceFileConcurrency",
  "toolsDirectoryCandidate",
  "musicBrainzMode",
  "musicBrainzURL",
  "musicBrainzDelay",
] as const;

export type SettingsDraftField = (typeof settingsDraftFields)[number];
export type SettingsDraftValue = {
  value: string;
  baseline: string;
  revision: number;
};
export type SettingsDraftSnapshot = Partial<
  Record<SettingsDraftField, SettingsDraftValue>
>;

const discardedDraftMarker = JSON.stringify({ version: 1, discarded: true });
let memoryDraft: SettingsDraftSnapshot | null | undefined;

function cloneSnapshot(snapshot: SettingsDraftSnapshot): SettingsDraftSnapshot {
  return Object.fromEntries(
    Object.entries(snapshot).map(([field, value]) => [field, { ...value }]),
  ) as SettingsDraftSnapshot;
}

export function resetSettingsDraftMemory() {
  memoryDraft = undefined;
}

export function getSettingsDraftStorage(): Storage | undefined {
  try {
    return typeof window === "undefined" ? undefined : window.sessionStorage;
  } catch {
    return undefined;
  }
}

export function readSettingsDraft(
  storage: Storage | undefined,
): SettingsDraftSnapshot | null {
  if (memoryDraft !== undefined)
    return memoryDraft === null ? null : cloneSnapshot(memoryDraft);
  try {
    const raw = storage?.getItem(settingsDraftStorageKey);
    if (!raw || raw === discardedDraftMarker) {
      if (raw === discardedDraftMarker) memoryDraft = null;
      return null;
    }
    const parsed: unknown = JSON.parse(raw);
    if (!parsed || typeof parsed !== "object") return null;
    const envelope = parsed as { version?: unknown; fields?: unknown };
    if (envelope.version !== 1) return null;
    const fields = envelope.fields;
    if (!fields || typeof fields !== "object") return null;
    const snapshot: SettingsDraftSnapshot = {};
    for (const field of settingsDraftFields) {
      const entry = (fields as Record<string, unknown>)[field];
      if (entry === undefined) continue;
      if (
        !entry ||
        typeof entry !== "object" ||
        typeof (entry as SettingsDraftValue).value !== "string" ||
        typeof (entry as SettingsDraftValue).baseline !== "string" ||
        !Number.isSafeInteger((entry as SettingsDraftValue).revision) ||
        (entry as SettingsDraftValue).revision < 0
      )
        return null;
      if (
        field === "musicBrainzMode" &&
        (!["", "public", "self-hosted"].includes(
          (entry as SettingsDraftValue).value,
        ) ||
          !["", "public", "self-hosted"].includes(
            (entry as SettingsDraftValue).baseline,
          ))
      )
        return null;
      snapshot[field] = { ...(entry as SettingsDraftValue) };
    }
    return Object.keys(snapshot).length ? snapshot : null;
  } catch {
    return null;
  }
}

export function writeSettingsDraft(
  storage: Storage | undefined,
  snapshot: SettingsDraftSnapshot | null,
): boolean {
  memoryDraft = snapshot === null ? null : cloneSnapshot(snapshot);
  try {
    if (!storage) return false;
    if (snapshot) {
      storage.setItem(
        settingsDraftStorageKey,
        JSON.stringify({ version: 1, fields: snapshot }),
      );
      return true;
    }
    try {
      storage.removeItem(settingsDraftStorageKey);
      return true;
    } catch {
      storage.setItem(settingsDraftStorageKey, discardedDraftMarker);
      return true;
    }
  } catch {
    return false;
  }
}

export function rebaseSettingsDraft(
  snapshot: SettingsDraftSnapshot,
  current: Partial<Record<SettingsDraftField, string>>,
): SettingsDraftSnapshot {
  let changed = false;
  const next = cloneSnapshot(snapshot);
  for (const field of settingsDraftFields) {
    const draft = next[field];
    const serverValue = current[field];
    if (!draft || serverValue === undefined || draft.value !== draft.baseline)
      continue;
    if (draft.baseline !== serverValue) {
      next[field] = {
        ...draft,
        value: serverValue,
        baseline: serverValue,
      };
      changed = true;
    }
  }
  return changed ? next : snapshot;
}

export function settingsDraftIsDirty(snapshot: SettingsDraftSnapshot) {
  return Object.values(snapshot).some(
    (draft) => draft !== undefined && draft.value !== draft.baseline,
  );
}
