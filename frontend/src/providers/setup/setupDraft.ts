import type {
  SaveRuntimeBodyPublicationFormat,
  UpdateMusicBrainzBodyMode,
} from "../../api/generated/client.schemas";

/** Tab-scoped, non-secret Setup form drafts. */
export const setupDraftKey = "melotrove.setup.draft";
export const setupDraftVersion = 1;

export const setupDraftToolKinds = ["ffmpeg", "fpcalc"] as const;
export type SetupDraftToolKind = (typeof setupDraftToolKinds)[number];

export interface SetupDraftValue<Value, Saved = Value> {
  value: Value;
  /** Saved server value this draft was edited from. */
  saved: Saved;
}

export interface SetupDraftProvider {
  /** Server configuration identity this provider draft was edited from. */
  identity: string;
  savedMode: UpdateMusicBrainzBodyMode;
  savedBaseURL: string;
  mode: UpdateMusicBrainzBodyMode;
  baseURL: string;
}

export interface SetupDraft {
  version: typeof setupDraftVersion;
  directories?: {
    tools?: SetupDraftValue<string>;
    output?: SetupDraftValue<string>;
  };
  publication?: {
    format?: SetupDraftValue<SaveRuntimeBodyPublicationFormat, string>;
    autoCompleteAlbums?: SetupDraftValue<boolean>;
    removeSources?: SetupDraftValue<boolean>;
  };
  metadata?: {
    provider?: SetupDraftProvider;
    lrclib?: SetupDraftValue<boolean>;
  };
  tools?: Partial<Record<SetupDraftToolKind, string>>;
}

/**
 * Returns a URL suitable for tab storage. Empty values are allowed. Only plain
 * absolute HTTP(S) URLs without userinfo, query or fragment are accepted so
 * credentials and one-time parameters never reach sessionStorage.
 */
export function setupDraftURL(value: string): string | undefined {
  const candidate = value.trim();
  if (candidate === "") return "";
  // Reject credential-shaped authority even if URL parsing would tolerate it.
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

/** The draft value only when it was edited from the currently saved value. */
export function setupDraftValue<Value, Saved>(
  entry: SetupDraftValue<Value, Saved> | undefined,
  saved: Saved,
): Value | undefined {
  return entry && Object.is(entry.saved, saved) ? entry.value : undefined;
}

export function setupDraftEntry<Value, Saved>(
  value: Value,
  saved: Saved,
): SetupDraftValue<Value, Saved> | undefined {
  return Object.is(value, saved) ? undefined : { value, saved };
}

function hasSetupDraftContent(draft: SetupDraft): boolean {
  return Boolean(
    draft.directories?.tools ||
      draft.directories?.output ||
      draft.publication?.format ||
      draft.publication?.autoCompleteAlbums ||
      draft.publication?.removeSources ||
      draft.metadata?.provider ||
      draft.metadata?.lrclib ||
      (draft.tools && Object.keys(draft.tools).length > 0),
  );
}

function setupDraftStorage(): Storage | undefined {
  if (typeof window === "undefined") return undefined;
  try {
    return window.sessionStorage;
  } catch {
    return undefined;
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function isString(value: unknown): value is string {
  return typeof value === "string";
}

function isBoolean(value: unknown): value is boolean {
  return typeof value === "boolean";
}

function isPublicationFormat(
  value: unknown,
): value is SaveRuntimeBodyPublicationFormat {
  return value === "source" || value === "mka" || value === "m4a";
}

function isMusicBrainzMode(value: unknown): value is UpdateMusicBrainzBodyMode {
  return value === "public" || value === "self-hosted";
}

function isDraftURL(value: unknown): value is string {
  return typeof value === "string" && setupDraftURL(value) !== undefined;
}

function readDraftValue<Value, Saved>(
  value: unknown,
  isValue: (candidate: unknown) => candidate is Value,
  isSaved: (candidate: unknown) => candidate is Saved,
): SetupDraftValue<Value, Saved> | undefined {
  if (!isRecord(value)) return undefined;
  if (!isValue(value.value) || !isSaved(value.saved)) return undefined;
  return { value: value.value, saved: value.saved };
}

function readToolSelection(
  value: unknown,
): Partial<Record<SetupDraftToolKind, string>> | undefined {
  if (!isRecord(value)) return undefined;
  const tools: Partial<Record<SetupDraftToolKind, string>> = {};
  for (const kind of setupDraftToolKinds) {
    const selected = value[kind];
    if (typeof selected === "string" && selected !== "") tools[kind] = selected;
  }
  return Object.keys(tools).length > 0 ? tools : undefined;
}

function readProvider(value: unknown): SetupDraftProvider | undefined {
  if (!isRecord(value)) return undefined;
  if (!isString(value.identity)) return undefined;
  if (!isMusicBrainzMode(value.savedMode)) return undefined;
  if (!isDraftURL(value.savedBaseURL)) return undefined;
  if (!isMusicBrainzMode(value.mode)) return undefined;
  if (!isDraftURL(value.baseURL)) return undefined;
  return {
    identity: value.identity,
    savedMode: value.savedMode,
    savedBaseURL: value.savedBaseURL,
    mode: value.mode,
    baseURL: value.baseURL,
  };
}

function parseSetupDraft(value: unknown): SetupDraft | undefined {
  if (!isRecord(value) || value.version !== setupDraftVersion) return undefined;
  const draft: SetupDraft = { version: setupDraftVersion };
  if (isRecord(value.directories)) {
    const directories: NonNullable<SetupDraft["directories"]> = {
      tools: readDraftValue(value.directories.tools, isString, isString),
      output: readDraftValue(value.directories.output, isString, isString),
    };
    if (directories.tools || directories.output)
      draft.directories = directories;
  }
  if (isRecord(value.publication)) {
    const publication: NonNullable<SetupDraft["publication"]> = {
      format: readDraftValue(
        value.publication.format,
        isPublicationFormat,
        isString,
      ),
      autoCompleteAlbums: readDraftValue(
        value.publication.autoCompleteAlbums,
        isBoolean,
        isBoolean,
      ),
      removeSources: readDraftValue(
        value.publication.removeSources,
        isBoolean,
        isBoolean,
      ),
    };
    if (
      publication.format ||
      publication.autoCompleteAlbums ||
      publication.removeSources
    )
      draft.publication = publication;
  }
  if (isRecord(value.metadata)) {
    const metadata: NonNullable<SetupDraft["metadata"]> = {
      provider: readProvider(value.metadata.provider),
      lrclib: readDraftValue(value.metadata.lrclib, isBoolean, isBoolean),
    };
    if (metadata.provider || metadata.lrclib) draft.metadata = metadata;
  }
  const tools = readToolSelection(value.tools);
  if (tools) draft.tools = tools;
  return hasSetupDraftContent(draft) ? draft : undefined;
}

/** Reads the current tab draft; unavailable or corrupt storage means no draft. */
export function readSetupDraft(
  storage: Storage | undefined = setupDraftStorage(),
): SetupDraft | undefined {
  if (!storage) return undefined;
  try {
    const raw = storage.getItem(setupDraftKey);
    if (!raw) return undefined;
    return parseSetupDraft(JSON.parse(raw));
  } catch {
    return undefined;
  }
}

/** Persists only non-empty drafts; storage failures never interrupt Setup. */
export function writeSetupDraft(
  draft: SetupDraft,
  storage: Storage | undefined = setupDraftStorage(),
): void {
  if (!storage) return;
  try {
    if (!hasSetupDraftContent(draft)) {
      storage.removeItem(setupDraftKey);
      return;
    }
    storage.setItem(setupDraftKey, JSON.stringify(draft));
  } catch {
    // Draft persistence is optional; server state and current inputs remain.
  }
}

export function clearSetupDraft(
  storage: Storage | undefined = setupDraftStorage(),
): void {
  if (!storage) return;
  try {
    storage.removeItem(setupDraftKey);
  } catch {
    // Setup completion does not depend on storage availability.
  }
}
