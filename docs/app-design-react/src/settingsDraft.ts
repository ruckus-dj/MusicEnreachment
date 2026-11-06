export const settingsDraftStorageKey = "melotrove.settings-text-draft.v1";
const discardedDraftMarker = JSON.stringify({ version: 1, discarded: true });
let memoryDraft: SettingsDraftSnapshot | null | undefined;

function cloneSnapshot(snapshot: SettingsDraftSnapshot): SettingsDraftSnapshot {
	return {
		value: { ...snapshot.value },
		baseline: { ...snapshot.baseline },
	};
}

/** Reset the route-independent fallback; intended for isolated tests. */
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

export type SettingsTextDraft = {
	concurrency: string;
	toolsPath: string;
	toolsPathCandidate: string;
	musicBrainzMode: "" | "public" | "self-hosted";
	musicBrainzURL: string;
	musicBrainzDelay: string;
};

export type SettingsDraftSnapshot = {
	value: SettingsTextDraft;
	baseline: SettingsTextDraft;
};

export type SettingsDraftField = Exclude<
	keyof SettingsTextDraft,
	"toolsPathCandidate"
>;

export function rebaseSettingsDraft(
	snapshot: SettingsDraftSnapshot,
	current: SettingsTextDraft,
): SettingsDraftSnapshot {
	let changed = false;
	const value = { ...snapshot.value };
	const baseline = { ...snapshot.baseline };
	const fields: SettingsDraftField[] = [
		"concurrency",
		"toolsPath",
		"musicBrainzMode",
		"musicBrainzURL",
		"musicBrainzDelay",
	];
	for (const field of fields) {
		const locallyChanged =
			value[field] !== baseline[field] ||
			(field === "toolsPath" && value.toolsPathCandidate !== "");
		if (!locallyChanged && baseline[field] !== current[field]) {
			Object.assign(value, { [field]: current[field] });
			Object.assign(baseline, { [field]: current[field] });
			changed = true;
		}
	}
	return changed ? { value, baseline } : snapshot;
}

export function mergeSettingsDraft(
	snapshot: SettingsDraftSnapshot,
	current: SettingsTextDraft,
	fields: SettingsDraftField[] = [
		"concurrency",
		"toolsPath",
		"musicBrainzMode",
		"musicBrainzURL",
		"musicBrainzDelay",
	],
): SettingsTextDraft {
	const merged = {
		...current,
		toolsPathCandidate: snapshot.value.toolsPathCandidate,
	};
	for (const field of fields) {
		Object.assign(merged, {
			[field]:
				snapshot.value[field] !== snapshot.baseline[field]
					? snapshot.value[field]
					: current[field],
		});
	}
	return merged;
}

export function readSettingsDraft(
	storage: Storage | undefined,
): SettingsDraftSnapshot | null {
	if (memoryDraft !== undefined)
		return memoryDraft === null ? null : cloneSnapshot(memoryDraft);
	try {
		const value = storage?.getItem(settingsDraftStorageKey);
		if (!value) return null;
		if (value === discardedDraftMarker) {
			memoryDraft = null;
			return null;
		}
		const parsed = JSON.parse(value) as Partial<SettingsDraftSnapshot>;
		if (
			typeof parsed === "object" &&
			parsed !== null &&
			"discarded" in parsed &&
			parsed.discarded === true
		) {
			memoryDraft = null;
			return null;
		}
		if (!parsed.value || !parsed.baseline) return null;
		const valid = (draft: Partial<SettingsTextDraft>) =>
			typeof draft.concurrency === "string" &&
			typeof draft.toolsPath === "string" &&
			typeof draft.toolsPathCandidate === "string" &&
			typeof draft.musicBrainzURL === "string" &&
			typeof draft.musicBrainzDelay === "string" &&
			(draft.musicBrainzMode === "" ||
				draft.musicBrainzMode === "public" ||
				draft.musicBrainzMode === "self-hosted");
		if (!valid(parsed.value) || !valid(parsed.baseline)) return null;
		return parsed as SettingsDraftSnapshot;
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
			storage?.setItem(settingsDraftStorageKey, JSON.stringify(snapshot));
			return true;
		}
		try {
			storage.removeItem(settingsDraftStorageKey);
			return true;
		} catch {
			// Persist a tombstone when deletion is unavailable so stale drafts cannot return.
			storage.setItem(settingsDraftStorageKey, discardedDraftMarker);
			return true;
		}
	} catch {
		// The in-memory value remains authoritative for this page lifetime.
		return false;
	}
}

export function settingsDraftIsDirty(
	value: SettingsTextDraft,
	baseline: SettingsTextDraft,
) {
	return (
		value.toolsPathCandidate !== "" ||
		Object.keys(value).some(
			(key) =>
				key !== "toolsPathCandidate" &&
				value[key as keyof SettingsTextDraft] !==
					baseline[key as keyof SettingsTextDraft],
		)
	);
}

export function settingsDraftConflicts(
	baseline: SettingsTextDraft,
	current: SettingsTextDraft,
	value?: SettingsTextDraft,
) {
	return Object.keys(baseline).filter(
		(key) =>
			key !== "toolsPathCandidate" &&
			baseline[key as keyof SettingsTextDraft] !==
				current[key as keyof SettingsTextDraft] &&
			(!value ||
				value[key as keyof SettingsTextDraft] !==
					baseline[key as keyof SettingsTextDraft] ||
				(key === "toolsPath" && value.toolsPathCandidate !== "")),
	) as (keyof SettingsTextDraft)[];
}

export function validConcurrency(value: string) {
	return (
		/^[1-9]\d*$/.test(value.trim()) &&
		Number.isSafeInteger(Number(value.trim()))
	);
}
