import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
	getSettingsDraftStorage,
	readSettingsDraft,
	mergeSettingsDraft,
	rebaseSettingsDraft,
	resetSettingsDraftMemory,
	settingsDraftConflicts,
	settingsDraftIsDirty,
	settingsDraftStorageKey,
	validConcurrency,
	writeSettingsDraft,
	type SettingsDraftSnapshot,
} from "./settingsDraft";

const baseline = {
	concurrency: "4",
	toolsPath: "/tools",
	toolsPathCandidate: "",
	musicBrainzMode: "public" as const,
	musicBrainzURL: "",
	musicBrainzDelay: "0",
};

beforeEach(() => {
	window.sessionStorage.clear();
	resetSettingsDraftMemory();
});
afterEach(() => vi.restoreAllMocks());

describe("settings text drafts", () => {
	it("round-trips in session storage and can be explicitly discarded", () => {
		const storage = window.sessionStorage;
		const snapshot: SettingsDraftSnapshot = {
			baseline,
			value: { ...baseline, concurrency: "not valid" },
		};
		writeSettingsDraft(storage, snapshot);
		expect(readSettingsDraft(storage)).toEqual(snapshot);
		writeSettingsDraft(storage, null);
		expect(storage.getItem(settingsDraftStorageKey)).toBeNull();
	});

	it("ignores malformed storage and tolerates unavailable storage", () => {
		const storage = window.sessionStorage;
		storage.setItem(settingsDraftStorageKey, "{bad json");
		expect(readSettingsDraft(storage)).toBeNull();
		expect(readSettingsDraft(undefined)).toBeNull();
		expect(() => writeSettingsDraft(undefined, null)).not.toThrow();
	});

	it("survives a throwing sessionStorage getter through the in-memory fallback", () => {
		vi.spyOn(window, "sessionStorage", "get").mockImplementation(() => {
			throw new DOMException("denied", "SecurityError");
		});
		const snapshot: SettingsDraftSnapshot = {
			baseline,
			value: { ...baseline, concurrency: "invalid" },
		};
		const storage = getSettingsDraftStorage();
		expect(storage).toBeUndefined();
		expect(writeSettingsDraft(storage, snapshot)).toBe(false);
		expect(readSettingsDraft(storage)).toEqual(snapshot);
	});

	it("keeps the newest snapshot and an explicit discard tombstone when storage throws", () => {
		const stale: SettingsDraftSnapshot = {
			baseline,
			value: { ...baseline, concurrency: "5" },
		};
		const newest: SettingsDraftSnapshot = {
			baseline,
			value: { ...baseline, concurrency: "invalid" },
		};
		const storage = {
			getItem: () => JSON.stringify(stale),
			setItem: () => {
				throw new DOMException("full", "QuotaExceededError");
			},
			removeItem: () => {
				throw new DOMException("denied", "SecurityError");
			},
		} as unknown as Storage;
		expect(readSettingsDraft(storage)).toEqual(stale);
		expect(writeSettingsDraft(storage, newest)).toBe(false);
		expect(readSettingsDraft(storage)).toEqual(newest);
		expect(writeSettingsDraft(storage, null)).toBe(false);
		expect(readSettingsDraft(storage)).toBeNull();
	});

	it("persists a discard tombstone if removeItem fails but setItem works", () => {
		let stored = JSON.stringify({ baseline, value: baseline });
		const storage = {
			getItem: () => stored,
			setItem: (_key: string, value: string) => {
				stored = value;
			},
			removeItem: () => {
				throw new Error("remove denied");
			},
		} as unknown as Storage;
		expect(writeSettingsDraft(storage, null)).toBe(true);
		resetSettingsDraftMemory();
		expect(readSettingsDraft(storage)).toBeNull();
	});

	it("tracks dirty fields and conflicts against captured per-field baselines", () => {
		const draft = { ...baseline, concurrency: "8" };
		expect(settingsDraftIsDirty(draft, baseline)).toBe(true);
		expect(
			settingsDraftConflicts(baseline, {
				...baseline,
				toolsPath: "/new-tools",
			}),
		).toEqual(["toolsPath"]);
		expect(
			settingsDraftConflicts(baseline, {
				...baseline,
				musicBrainzMode: "public",
			}),
		).toEqual([]);
		expect(
			settingsDraftConflicts(
				baseline,
				{ ...baseline, musicBrainzMode: "self-hosted" },
				draft,
			),
		).toEqual([]);
	});

	it("rebases untouched external values while preserving edited values and conflicts", () => {
		const snapshot: SettingsDraftSnapshot = {
			baseline,
			value: { ...baseline, concurrency: "8" },
		};
		const current = {
			...baseline,
			musicBrainzURL: "https://new.example",
			musicBrainzMode: "self-hosted" as const,
		};
		const rebased = rebaseSettingsDraft(snapshot, current);
		expect(rebased.value.concurrency).toBe("8");
		expect(rebased.baseline.concurrency).toBe("4");
		expect(rebased.value.musicBrainzURL).toBe("https://new.example");
		expect(rebased.baseline.musicBrainzMode).toBe("self-hosted");
		expect(
			settingsDraftConflicts(rebased.baseline, current, rebased.value),
		).toEqual([]);
	});

	it("rebases a clean tools path before a candidate begins and conflicts after it begins", () => {
		const clean = { value: baseline, baseline };
		const current = { ...baseline, toolsPath: "/new-tools" };
		const rebased = rebaseSettingsDraft(clean, current);
		expect(rebased.value.toolsPath).toBe("/new-tools");
		expect(rebased.baseline.toolsPath).toBe("/new-tools");
		const candidate = {
			...rebased,
			value: { ...rebased.value, toolsPathCandidate: "/candidate" },
		};
		const newer = { ...current, toolsPath: "/externally-updated" };
		expect(rebaseSettingsDraft(candidate, newer)).toBe(candidate);
		expect(
			settingsDraftConflicts(candidate.baseline, newer, candidate.value),
		).toEqual(["toolsPath"]);
	});

	it("merges only changed draft fields and keeps latest untouched settings", () => {
		const snapshot = {
			baseline,
			value: { ...baseline, concurrency: "8" },
		};
		const latest = {
			...baseline,
			musicBrainzURL: "https://latest.example",
			musicBrainzDelay: "5",
		};
		const merged = mergeSettingsDraft(snapshot, latest);
		expect(merged.concurrency).toBe("8");
		expect(merged.musicBrainzURL).toBe("https://latest.example");
		expect(merged.musicBrainzDelay).toBe("5");
	});

	it("retains a genuine edited-field conflict until its draft is explicitly merged", () => {
		const snapshot = {
			baseline,
			value: { ...baseline, musicBrainzURL: "https://mine.example" },
		};
		const current = { ...baseline, musicBrainzURL: "https://theirs.example" };
		const rebased = rebaseSettingsDraft(snapshot, current);
		expect(
			settingsDraftConflicts(rebased.baseline, current, rebased.value),
		).toEqual(["musicBrainzURL"]);
		expect(mergeSettingsDraft(rebased, current).musicBrainzURL).toBe(
			"https://mine.example",
		);
	});

	it("validates positive safe integer concurrency without normalizing the draft", () => {
		expect(validConcurrency("4")).toBe(true);
		expect(validConcurrency(" 04 ")).toBe(false);
		expect(validConcurrency("0")).toBe(false);
		expect(validConcurrency("9007199254740992")).toBe(false);
	});
});
