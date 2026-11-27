import { beforeEach, expect, it } from "vitest";
import { initialDemoSettings } from "../settings/SettingsScreen";
import {
  getSetupDraftStorage,
  mergeSetupDraft,
  readSetupDraft,
  restoreSetupSettings,
  setupDraftStorageKey,
  setupSettingsDraft,
  writeSetupDraft,
} from "./setupDraft";

beforeEach(() => window.sessionStorage.clear());

it("persists AcoustID verification separately from the write-only key", () => {
  const storage = getSetupDraftStorage();
  const settings = {
    ...initialDemoSettings,
    hasAcoustIDApplicationKey: true,
    acoustIDVerified: true,
  };
  writeSetupDraft(
    storage,
    mergeSetupDraft(null, { settings: setupSettingsDraft(settings) }),
  );

  const stored = storage?.getItem(setupDraftStorageKey) ?? "";
  expect(JSON.parse(stored).settings).toEqual(
    expect.objectContaining({
      hasAcoustIDApplicationKey: true,
      acoustIDVerified: true,
    }),
  );

  const restored = readSetupDraft(storage);
  expect(restoreSetupSettings(initialDemoSettings, restored)).toEqual(
    expect.objectContaining({
      hasAcoustIDApplicationKey: true,
      acoustIDVerified: true,
    }),
  );
});

it("does not promote a legacy saved key without recorded verification", () => {
  const storage = getSetupDraftStorage();
  storage?.setItem(
    setupDraftStorageKey,
    JSON.stringify({
      version: 1,
      settings: { hasAcoustIDApplicationKey: true },
    }),
  );

  const restored = restoreSetupSettings(
    initialDemoSettings,
    readSetupDraft(storage),
  );
  expect(restored.hasAcoustIDApplicationKey).toBe(true);
  expect(restored.acoustIDVerified).not.toBe(true);
});

it("drops persisted verification when no saved key backs it", () => {
  const storage = getSetupDraftStorage();
  storage?.setItem(
    setupDraftStorageKey,
    JSON.stringify({
      version: 1,
      settings: { acoustIDVerified: true },
    }),
  );

  const restored = restoreSetupSettings(
    initialDemoSettings,
    readSetupDraft(storage),
  );
  expect(restored.acoustIDVerified).not.toBe(true);
});

it("persists the committed auto-complete albums setting", () => {
  const storage = getSetupDraftStorage();
  writeSetupDraft(
    storage,
    mergeSetupDraft(null, {
      settings: setupSettingsDraft({
        ...initialDemoSettings,
        publicationAutoCompleteAlbums: true,
      }),
    }),
  );

  const stored = JSON.parse(storage?.getItem(setupDraftStorageKey) ?? "");
  expect(stored.settings).toEqual(
    expect.objectContaining({ publicationAutoCompleteAlbums: true }),
  );

  const restored = restoreSetupSettings(
    initialDemoSettings,
    readSetupDraft(storage),
  );
  expect(restored.publicationAutoCompleteAlbums).toBe(true);
});

it("keeps an explicit false draft separate from the committed setting", () => {
  const storage = getSetupDraftStorage();
  writeSetupDraft(
    storage,
    mergeSetupDraft(null, {
      settings: setupSettingsDraft({
        ...initialDemoSettings,
        publicationAutoCompleteAlbums: true,
      }),
      publicationAutoCompleteAlbums: false,
    }),
  );

  const stored = JSON.parse(storage?.getItem(setupDraftStorageKey) ?? "");
  expect(stored.publicationAutoCompleteAlbums).toBe(false);
  expect(stored.settings.publicationAutoCompleteAlbums).toBe(true);

  const restored = readSetupDraft(storage);
  expect(restored?.publicationAutoCompleteAlbums).toBe(false);
  expect(
    restoreSetupSettings(initialDemoSettings, restored)
      .publicationAutoCompleteAlbums,
  ).toBe(true);
});
