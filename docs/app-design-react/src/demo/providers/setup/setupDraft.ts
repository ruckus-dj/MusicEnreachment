import type {
  DemoInstallation,
  ManagedToolPackage,
} from "../settings/ToolLifecycleDemo";
import type { DemoSettings } from "../settings/useSettingsScreenDemoProvider";

export const setupDraftStorageKey = "melotrove.setup-draft.v1";

/** Only Setup progress and evidence that cannot be re-derived belongs here. */
export type SetupSettingsDraft = {
  toolsPath?: string;
  pathChecked?: boolean;
  outputCandidate?: string;
  outputChecked?: boolean;
  publicationFormat?: "" | "original" | "mka" | "m4a";
  sourceCleanupEnabled?: boolean;
  publicationAutoCompleteAlbums?: boolean;
  musicBrainzMode?: "" | "public" | "self-hosted";
  musicBrainzURL?: string;
  musicBrainzDelay?: string;
  musicBrainzThrottle?: boolean;
  musicBrainzVerified?: boolean;
  musicBrainzVerifiedFingerprint?: string;
  lrclibEnabled?: boolean;
  hasAcoustIDApplicationKey?: boolean;
  /** Mocked successful AcoustID API check for the saved key. */
  acoustIDVerified?: boolean;
  selectedRelease?: Partial<Record<ManagedToolPackage, string>>;
  setupVerified?: Partial<Record<ManagedToolPackage, string>>;
  setupFailures?: Partial<Record<ManagedToolPackage, string>>;
  installations?: DemoInstallation[];
  unmanagedToolFiles?: string[];
};

export type SetupDraftSnapshot = {
  version: 1;
  step?: number;
  /** Uncommitted text from the directories step. */
  toolsDirectory?: string;
  /** Uncommitted LRCLIB choice from the publication step. */
  lrclib?: boolean;
  /** Uncommitted auto-complete-albums choice from the publication step. */
  publicationAutoCompleteAlbums?: boolean;
  /** MusicBrainz endpoint whose genre catalog was actually loaded. */
  genreCatalogEndpoint?: string;
  settings?: SetupSettingsDraft;
};

export function getSetupDraftStorage(): Storage | undefined {
  try {
    return typeof window === "undefined" ? undefined : window.sessionStorage;
  } catch {
    return undefined;
  }
}

function cloneDraft(draft: SetupDraftSnapshot): SetupDraftSnapshot {
  const settings = draft.settings;
  return {
    ...draft,
    ...(settings && {
      settings: {
        ...settings,
        ...(settings.selectedRelease && {
          selectedRelease: { ...settings.selectedRelease },
        }),
        ...(settings.setupVerified && {
          setupVerified: { ...settings.setupVerified },
        }),
        ...(settings.setupFailures && {
          setupFailures: { ...settings.setupFailures },
        }),
        ...(settings.installations && {
          installations: settings.installations.map((item) => ({ ...item })),
        }),
        ...(settings.unmanagedToolFiles && {
          unmanagedToolFiles: [...settings.unmanagedToolFiles],
        }),
      },
    }),
  };
}

/**
 * Only plain absolute HTTP(S) URLs without userinfo, query or fragment reach
 * tab storage, matching the application draft rules. Empty is allowed.
 */
export function setupDraftURL(value: string): string | undefined {
  const candidate = value.trim();
  if (candidate === "") return "";
  if (/^[a-z][a-z0-9+.-]*:\/\/[^/?#]*@/i.test(candidate)) return undefined;
  if (candidate.includes("?") || candidate.includes("#")) return undefined;
  try {
    const url = new URL(candidate);
    if (url.protocol !== "http:" && url.protocol !== "https:") return undefined;
    if (url.username || url.password) return undefined;
    if (url.search !== "" || url.hash !== "") return undefined;
    return candidate;
  } catch {
    return undefined;
  }
}

/** Store a derived genre endpoint only when its URL half is safe. */
export function setupDraftGenreEndpoint(value: string): string | undefined {
  if (value === "public") return value;
  const [kind, base] = value.split("|", 2);
  if (kind !== "self-hosted" || base === undefined) return undefined;
  const url = setupDraftURL(base);
  return url ? `${kind}|${url}` : undefined;
}

function setupDraftFingerprint(
  value: string,
  safeURL: string | undefined,
): string | undefined {
  if (safeURL === undefined) return undefined;
  try {
    const parsed: unknown = JSON.parse(value);
    if (!Array.isArray(parsed) || typeof parsed[1] !== "string")
      return undefined;
    const fingerprintURL = setupDraftURL(parsed[1]);
    return fingerprintURL === safeURL ? value : undefined;
  } catch {
    return undefined;
  }
}

function sanitizeSettings(settings: SetupSettingsDraft): SetupSettingsDraft {
  const {
    musicBrainzURL,
    musicBrainzVerifiedFingerprint,
    acoustIDVerified,
    hasAcoustIDApplicationKey,
    ...rest
  } = settings;
  const safeURL =
    musicBrainzURL === undefined ? undefined : setupDraftURL(musicBrainzURL);
  const fingerprint =
    musicBrainzVerifiedFingerprint === undefined
      ? undefined
      : setupDraftFingerprint(musicBrainzVerifiedFingerprint, safeURL);
  return {
    ...rest,
    ...(hasAcoustIDApplicationKey !== undefined && {
      hasAcoustIDApplicationKey,
    }),
    ...(acoustIDVerified === true &&
      hasAcoustIDApplicationKey === true && { acoustIDVerified: true }),
    ...(safeURL !== undefined && { musicBrainzURL: safeURL }),
    ...(fingerprint !== undefined && {
      musicBrainzVerifiedFingerprint: fingerprint,
    }),
  };
}

function sanitizeDraft(draft: SetupDraftSnapshot): SetupDraftSnapshot {
  const { genreCatalogEndpoint, ...rest } = draft;
  const endpoint =
    genreCatalogEndpoint === undefined
      ? undefined
      : setupDraftGenreEndpoint(genreCatalogEndpoint);
  return {
    ...rest,
    ...(endpoint !== undefined && { genreCatalogEndpoint: endpoint }),
    ...(draft.settings && { settings: sanitizeSettings(draft.settings) }),
  };
}

/** The exact Setup-owned settings allowlist persisted across a tab reload. */
export function setupSettingsDraft(settings: DemoSettings): SetupSettingsDraft {
  return sanitizeSettings({
    toolsPath: settings.toolsPath,
    pathChecked: settings.pathChecked,
    outputCandidate: settings.outputCandidate,
    outputChecked: settings.outputChecked,
    publicationFormat: settings.publicationFormat,
    sourceCleanupEnabled: settings.sourceCleanupEnabled,
    publicationAutoCompleteAlbums: settings.publicationAutoCompleteAlbums,
    musicBrainzMode: settings.musicBrainzMode,
    musicBrainzURL: settings.musicBrainzURL,
    musicBrainzDelay: settings.musicBrainzDelay,
    musicBrainzThrottle: settings.musicBrainzThrottle,
    musicBrainzVerified: settings.musicBrainzVerified,
    musicBrainzVerifiedFingerprint: settings.musicBrainzVerifiedFingerprint,
    lrclibEnabled: settings.lrclibEnabled,
    hasAcoustIDApplicationKey: settings.hasAcoustIDApplicationKey,
    acoustIDVerified: settings.acoustIDVerified,
    ...(settings.selectedRelease && {
      selectedRelease: { ...settings.selectedRelease },
    }),
    ...(settings.setupVerified && {
      setupVerified: { ...settings.setupVerified },
    }),
    ...(settings.setupFailures && {
      setupFailures: { ...settings.setupFailures },
    }),
    ...(settings.installations && {
      installations: settings.installations.map((item) => ({ ...item })),
    }),
    ...(settings.unmanagedToolFiles && {
      unmanagedToolFiles: [...settings.unmanagedToolFiles],
    }),
  });
}

/** Merge only the persisted Setup fields over the fresh demo defaults. */
export function restoreSetupSettings(
  base: DemoSettings,
  draft: SetupDraftSnapshot | null,
): DemoSettings {
  const settings = draft?.settings;
  if (!settings) return base;
  const safe = sanitizeSettings(settings);
  return {
    ...base,
    ...safe,
    ...(safe.selectedRelease && {
      selectedRelease: { ...safe.selectedRelease },
    }),
    ...(safe.setupVerified && {
      setupVerified: { ...safe.setupVerified },
    }),
    ...(safe.setupFailures && {
      setupFailures: { ...safe.setupFailures },
    }),
    ...(safe.installations && {
      installations: safe.installations.map((item) => ({ ...item })),
    }),
    ...(safe.unmanagedToolFiles && {
      unmanagedToolFiles: [...safe.unmanagedToolFiles],
    }),
  };
}

export function mergeSetupDraft(
  current: SetupDraftSnapshot | null,
  patch: Partial<Omit<SetupDraftSnapshot, "version">>,
): SetupDraftSnapshot {
  return sanitizeDraft({
    ...(current ?? { version: 1 }),
    ...patch,
    version: 1,
    ...(patch.settings !== undefined
      ? { settings: patch.settings }
      : current?.settings
        ? { settings: current.settings }
        : {}),
  });
}

const managedPackages: readonly ManagedToolPackage[] = ["ffmpeg", "fpcalc"];

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function managedPackage(value: unknown): ManagedToolPackage | undefined {
  return managedPackages.find((item) => item === value);
}

function stringRecord(
  value: unknown,
): Partial<Record<ManagedToolPackage, string>> | undefined {
  if (!isRecord(value)) return undefined;
  const result: Partial<Record<ManagedToolPackage, string>> = {};
  for (const key of managedPackages) {
    if (typeof value[key] === "string") result[key] = value[key];
  }
  return Object.keys(result).length ? result : undefined;
}

function installations(value: unknown): DemoInstallation[] | undefined {
  if (!Array.isArray(value)) return undefined;
  const result: DemoInstallation[] = [];
  for (const item of value.slice(0, 24)) {
    if (!isRecord(item)) continue;
    const packageKind = managedPackage(item.package);
    if (
      packageKind &&
      typeof item.id === "string" &&
      typeof item.release === "string" &&
      (item.status === "active" ||
        item.status === "installed" ||
        item.status === "available") &&
      typeof item.occupied === "boolean"
    ) {
      result.push({
        id: item.id,
        package: packageKind,
        release: item.release,
        status: item.status,
        occupied: item.occupied,
      });
    }
  }
  return result.length ? result : undefined;
}

function parseSettings(value: unknown): SetupSettingsDraft | undefined {
  if (!isRecord(value)) return undefined;
  const settings: SetupSettingsDraft = {};
  if (typeof value.toolsPath === "string") settings.toolsPath = value.toolsPath;
  if (typeof value.pathChecked === "boolean")
    settings.pathChecked = value.pathChecked;
  if (typeof value.outputCandidate === "string")
    settings.outputCandidate = value.outputCandidate;
  if (typeof value.outputChecked === "boolean")
    settings.outputChecked = value.outputChecked;
  if (
    value.publicationFormat === "" ||
    value.publicationFormat === "original" ||
    value.publicationFormat === "mka" ||
    value.publicationFormat === "m4a"
  )
    settings.publicationFormat = value.publicationFormat;
  if (typeof value.sourceCleanupEnabled === "boolean")
    settings.sourceCleanupEnabled = value.sourceCleanupEnabled;
  if (typeof value.publicationAutoCompleteAlbums === "boolean")
    settings.publicationAutoCompleteAlbums =
      value.publicationAutoCompleteAlbums;
  if (
    value.musicBrainzMode === "" ||
    value.musicBrainzMode === "public" ||
    value.musicBrainzMode === "self-hosted"
  )
    settings.musicBrainzMode = value.musicBrainzMode;
  if (typeof value.musicBrainzURL === "string") {
    const url = setupDraftURL(value.musicBrainzURL);
    if (url !== undefined) settings.musicBrainzURL = url;
  }
  if (typeof value.musicBrainzDelay === "string")
    settings.musicBrainzDelay = value.musicBrainzDelay;
  if (typeof value.musicBrainzThrottle === "boolean")
    settings.musicBrainzThrottle = value.musicBrainzThrottle;
  if (typeof value.musicBrainzVerified === "boolean")
    settings.musicBrainzVerified = value.musicBrainzVerified;
  if (typeof value.musicBrainzVerifiedFingerprint === "string") {
    const fingerprint = setupDraftFingerprint(
      value.musicBrainzVerifiedFingerprint,
      settings.musicBrainzURL,
    );
    if (fingerprint !== undefined)
      settings.musicBrainzVerifiedFingerprint = fingerprint;
  }
  if (typeof value.lrclibEnabled === "boolean")
    settings.lrclibEnabled = value.lrclibEnabled;
  if (typeof value.hasAcoustIDApplicationKey === "boolean")
    settings.hasAcoustIDApplicationKey = value.hasAcoustIDApplicationKey;
  if (typeof value.acoustIDVerified === "boolean")
    settings.acoustIDVerified = value.acoustIDVerified;
  const selectedRelease = stringRecord(value.selectedRelease);
  if (selectedRelease) settings.selectedRelease = selectedRelease;
  const setupVerified = stringRecord(value.setupVerified);
  if (setupVerified) settings.setupVerified = setupVerified;
  const setupFailures = stringRecord(value.setupFailures);
  if (setupFailures) settings.setupFailures = setupFailures;
  const storedInstallations = installations(value.installations);
  if (storedInstallations) settings.installations = storedInstallations;
  if (Array.isArray(value.unmanagedToolFiles)) {
    const files = value.unmanagedToolFiles
      .filter((item): item is string => typeof item === "string")
      .slice(0, 64);
    if (files.length) settings.unmanagedToolFiles = files;
  }
  return Object.keys(settings).length ? settings : undefined;
}

function parseDraft(value: unknown): SetupDraftSnapshot | null {
  if (!isRecord(value) || value.version !== 1) return null;
  const settings = parseSettings(value.settings);
  const endpoint =
    typeof value.genreCatalogEndpoint === "string"
      ? setupDraftGenreEndpoint(value.genreCatalogEndpoint)
      : undefined;
  return {
    version: 1,
    ...(typeof value.step === "number" &&
    Number.isInteger(value.step) &&
    value.step >= 0 &&
    value.step <= 3
      ? { step: value.step }
      : {}),
    ...(typeof value.toolsDirectory === "string"
      ? { toolsDirectory: value.toolsDirectory }
      : {}),
    ...(typeof value.lrclib === "boolean" ? { lrclib: value.lrclib } : {}),
    ...(typeof value.publicationAutoCompleteAlbums === "boolean"
      ? { publicationAutoCompleteAlbums: value.publicationAutoCompleteAlbums }
      : {}),
    ...(endpoint !== undefined && { genreCatalogEndpoint: endpoint }),
    ...(settings ? { settings } : {}),
  };
}

export function readSetupDraft(
  storage: Storage | undefined,
): SetupDraftSnapshot | null {
  try {
    const stored = storage?.getItem(setupDraftStorageKey);
    if (!stored) return null;
    const parsed = parseDraft(JSON.parse(stored));
    return parsed ? cloneDraft(parsed) : null;
  } catch {
    return null;
  }
}

export function writeSetupDraft(
  storage: Storage | undefined,
  draft: SetupDraftSnapshot | null,
): boolean {
  const safe = draft === null ? null : sanitizeDraft(draft);
  try {
    if (!storage) return false;
    if (safe) storage.setItem(setupDraftStorageKey, JSON.stringify(safe));
    else storage.removeItem(setupDraftStorageKey);
    return true;
  } catch {
    // The live host state remains authoritative for this page lifetime.
    return false;
  }
}
