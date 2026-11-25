import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { useOperationFollower } from "../../api/client/useOperationFollower";
import {
  activateToolInstallation,
  checkSettingsMusicbrainz,
  deleteAcoustidApplicationKey,
  deleteToolInstallation,
  getSettings,
  listInstallations,
  listOperations,
  preflightToolsRootMove,
  setAcoustidApplicationKey,
  startToolsRootMove,
  updateLogLevel,
  updateLrclibSetting,
  updateMusicbrainzSettings,
  updateSettings,
  updateSha256Setting,
} from "../../api/generated/client";
import type {
  CatalogBody,
  InstallationActionInputBodyPackageKind,
  InstallationResponse,
  InstallationsBody,
  InstallPreflightBody,
  MovePreflightBody,
  OperationResponse,
  SetupStateBody,
  UpdateLogLevelBodyLevel,
  UpdateMusicBrainzBodyMode,
  UpdateMusicBrainzExpectedBody,
  UpdateSettingsBodyPublicationFormat,
  UpdateSettingsExpectedBody,
} from "../../api/generated/client.schemas";
import { useI18n } from "../../app/i18n/host";
import {
  type HashAttempt,
  registerHashNavigationGuard,
  resolveHashNavigation,
  useHashLocation,
} from "../../app/navigation/hashLocation";
import {
  type ApplicationRouteProviderProps,
  ApplicationSettingsBinder,
  type ApplicationSettingsModel,
} from "../../ui/app/ApplicationRouteOutlet";
import type { SettingsGeneralFailureModel } from "../../ui/features/settings/SettingsGeneralSection";
import type {
  ManagedPackageCardModel,
  ManagedPackageCatalog,
  ManagedPackageLifeState,
} from "../../ui/features/settings/SettingsManagedPackageCards";
import type {
  SettingsConflictNoticeView,
  SettingsScreenViewProps,
} from "../../ui/features/settings/SettingsScreenView";
import {
  SettingsMessageError,
  type SettingsMessageKey,
  type SettingsMessageValues,
  settingsHttpError,
  settingsMessage,
} from "../../ui/features/settings/settingsMessages";
import { browseServerDirectory } from "../components/DirectoryPicker";
import { GitHubTokenFormProvider } from "../tools/GitHubTokenForm";
import {
  loadManagedToolCatalogs,
  preflightManagedToolInstall,
  startManagedToolInstall,
} from "../tools/toolWorkflow";
import { GenreCatalogPanelProvider } from "./GenreCatalogPanel";
import {
  getSettingsDraftStorage,
  readSettingsDraft,
  type SettingsDraftSnapshot,
  type SettingsDraftValue,
  settingsDraftIsDirty,
  writeSettingsDraft,
} from "./settingsDraft";
import {
  isSettingsTabID,
  parseSettingsRoute,
  type SettingsTabID,
  settingsTabFromHash,
  settingsTabHash,
} from "./settingsRoute";

const catalogCheckedKey = "melotrove.catalog-checked-at";
const cooldownMs = 24 * 60 * 60 * 1000;
let generalSettingsQueue: Promise<void> = Promise.resolve();
type Kind = InstallationActionInputBodyPackageKind;
type InstallPlan = { kind: Kind; release: string; plan: InstallPreflightBody };
type MovePlan = {
  directory: string;
  removeOld: boolean;
  sourceDirectory: string;
  inputRevision: number;
  sessionRevision: number;
  requestRevision: number;
  plan: MovePreflightBody;
};
type GeneralSettingKey = "publicationFormat" | "sha256" | "logLevel";
type ConflictGroup =
  | "concurrency"
  | "matchingThreshold"
  | "publicationTemplate"
  | "musicBrainz"
  | "workDirectory"
  | "toolsDirectory"
  | "outputDirectory";
type ConflictConfirmation = {
  group: ConflictGroup;
  currentValue: unknown;
  onConfirm: () => void;
};
type GeneralFailurePhase = "write" | "confirm";
type GeneralSettingFailure = {
  revision: number;
  phase: GeneralFailurePhase;
  value:
    | UpdateSettingsBodyPublicationFormat
    | boolean
    | UpdateLogLevelBodyLevel;
};
type MusicBrainzSavedSnapshot = {
  mode: UpdateMusicBrainzBodyMode;
  baseURL: string;
  delay: number;
  throttle: boolean;
};
type SettingsNotice = {
  key: SettingsMessageKey;
  values?: SettingsMessageValues;
};
type SettingsFeedback = string | SettingsNotice;
const settingsNotice = (
  key: SettingsMessageKey,
  values?: SettingsMessageValues,
): SettingsNotice => ({ key, values });

function persistedTextDraft(
  value: unknown,
  baseline: unknown,
  revision: number,
): SettingsDraftValue | undefined {
  if (typeof value !== "string" || typeof baseline !== "string")
    return undefined;
  return { value, baseline, revision };
}

type SettingsSaveOutcome = {
  status:
    | "saved"
    | "invalid"
    | "confirmation_pending"
    | "conflict"
    | "uncertain"
    | "failed";
  group:
    | "concurrency"
    | "matchingThreshold"
    | "publicationTemplate"
    | "workDirectory"
    | "musicBrainz"
    | "outputDirectory"
    | "toolsDirectory"
    | "all";
  validSavedTarget?: boolean;
  confirmationRead?: boolean;
  message?: SettingsFeedback;
};
class SettingsLocalizedError extends SettingsMessageError {}
class SettingsConflictError extends SettingsLocalizedError {}
class SettingsValidationError extends Error {
  readonly feedback: SettingsFeedback;

  constructor(reason: Error) {
    super();
    this.feedback =
      reason instanceof SettingsMessageError
        ? reason.descriptor
        : reason.message;
  }
}

function conflictLabel(group: ConflictGroup, locale: "en" | "ru"): string {
  switch (group) {
    case "concurrency":
      return settingsMessage(locale, "conflictConcurrency");
    case "matchingThreshold":
      return settingsMessage(locale, "conflictMatchingThreshold");
    case "publicationTemplate":
      return settingsMessage(locale, "conflictTemplate");
    case "musicBrainz":
      return settingsMessage(locale, "conflictMusicBrainz");
    case "workDirectory":
      return settingsMessage(locale, "conflictWorkDirectory");
    case "toolsDirectory":
      return settingsMessage(locale, "conflictToolsDirectory");
    case "outputDirectory":
      return settingsMessage(locale, "conflictOutputDirectory");
  }
}

function displaySettingValue(value: unknown) {
  return typeof value === "string" ? `«${value}»` : JSON.stringify(value);
}

function musicBrainzSavedSnapshot(
  settings: SetupStateBody["settings"],
): MusicBrainzSavedSnapshot {
  return {
    mode:
      settings.musicbrainz_mode === "self-hosted" ? "self-hosted" : "public",
    baseURL: settings.musicbrainz_base_url,
    delay: settings.musicbrainz_self_hosted_delay_seconds,
    throttle: settings.musicbrainz_self_hosted_throttle,
  };
}

type SectionDraft<T> = {
  value: T | undefined;
  baseline: T | undefined;
  revision: number;
};

function useSectionDraft<T>(
  equal: (left: T, right: T) => boolean = Object.is,
  restored?: { value: unknown; baseline: unknown; revision: number },
) {
  const [draft, setDraft] = useState<SectionDraft<T>>(() => ({
    value: restored?.value as T | undefined,
    baseline: restored?.baseline as T | undefined,
    revision: restored?.revision ?? 0,
  }));
  const current = useRef(draft);
  const pendingRevision = useRef<number | undefined>(undefined);
  const update = useCallback((value: T) => {
    const next = {
      ...current.current,
      value,
      revision: current.current.revision + 1,
    };
    current.current = next;
    setDraft(next);
  }, []);
  const sync = useCallback(
    (value: T) => {
      const previous = current.current;
      const pristine =
        pendingRevision.current === undefined &&
        (previous.value === undefined ||
          (previous.baseline !== undefined &&
            equal(previous.value, previous.baseline)));
      let baseline = pristine ? value : previous.baseline;
      let draftValue = pristine ? value : previous.value;
      if (
        !pristine &&
        typeof previous.value === "object" &&
        previous.value !== null &&
        typeof previous.baseline === "object" &&
        previous.baseline !== null &&
        typeof value === "object" &&
        value !== null
      ) {
        baseline = { ...previous.baseline };
        draftValue = { ...previous.value };
        for (const key of Object.keys(value)) {
          if (
            Object.is(
              (previous.value as Record<string, unknown>)[key],
              (previous.baseline as Record<string, unknown>)[key],
            )
          ) {
            (baseline as Record<string, unknown>)[key] = (
              value as Record<string, unknown>
            )[key];
            (draftValue as Record<string, unknown>)[key] = (
              value as Record<string, unknown>
            )[key];
          }
        }
      }
      const next = { ...previous, baseline, value: draftValue };
      current.current = next;
      setDraft(next);
    },
    [equal],
  );
  const capture = useCallback(
    () => ({
      value: current.current.value,
      baseline: current.current.baseline,
      revision: current.current.revision,
    }),
    [],
  );
  const beginSave = useCallback((revision: number) => {
    pendingRevision.current = revision;
  }, []);
  const endSave = useCallback((revision: number) => {
    if (pendingRevision.current === revision)
      pendingRevision.current = undefined;
  }, []);
  const acknowledge = useCallback((value: T, revision: number) => {
    const previous = current.current;
    const next = {
      ...previous,
      baseline: value,
      value: previous.revision === revision ? value : previous.value,
    };
    current.current = next;
    setDraft(next);
  }, []);
  const acknowledgeField = useCallback(
    <K extends keyof T>(
      field: K,
      submitted: T[K],
      saved: T[K],
      preserveDraft = false,
    ) => {
      const previous = current.current;
      if (
        typeof previous.value !== "object" ||
        previous.value === null ||
        typeof previous.baseline !== "object" ||
        previous.baseline === null
      )
        return;
      const baseline = { ...previous.baseline, [field]: saved };
      const value = {
        ...previous.value,
        [field]:
          !preserveDraft && Object.is(previous.value[field], submitted)
            ? saved
            : previous.value[field],
      };
      const next = { ...previous, value, baseline };
      current.current = next;
      setDraft(next);
    },
    [],
  );
  const dirty =
    draft.value !== undefined &&
    draft.baseline !== undefined &&
    !equal(draft.value, draft.baseline);
  const persisted = useCallback((): SettingsDraftValue | undefined => {
    const value = current.current.value;
    const baseline = current.current.baseline;
    if (typeof value !== "string" || typeof baseline !== "string")
      return undefined;
    return { value, baseline, revision: current.current.revision };
  }, []);
  const snapshot = useCallback(() => current.current, []);
  const isPristineAndIdle = useCallback(
    () =>
      pendingRevision.current === undefined &&
      (current.current.value === undefined ||
        (current.current.baseline !== undefined &&
          equal(current.current.value, current.current.baseline))),
    [equal],
  );
  return {
    value: draft.value,
    baseline: draft.baseline,
    revision: draft.revision,
    dirty,
    update,
    sync,
    capture,
    beginSave,
    endSave,
    acknowledge,
    acknowledgeField,
    persisted,
    snapshot,
    isPristineAndIdle,
  };
}

const sameMusicBrainz = (
  left: {
    mode: UpdateMusicBrainzBodyMode;
    baseURL: string;
    selfHostedThrottle: boolean;
    selfHostedDelaySeconds: string;
  },
  right: {
    mode: UpdateMusicBrainzBodyMode;
    baseURL: string;
    selfHostedThrottle: boolean;
    selfHostedDelaySeconds: string;
  },
) =>
  left.mode === right.mode &&
  left.baseURL === right.baseURL &&
  left.selfHostedThrottle === right.selfHostedThrottle &&
  left.selfHostedDelaySeconds === right.selfHostedDelaySeconds;

function validMusicBrainzBaseURL(value: string) {
  try {
    const url = new URL(value);
    return (
      (url.protocol === "http:" || url.protocol === "https:") &&
      url.hostname !== ""
    );
  } catch {
    return false;
  }
}

function matchingThresholdNumber(value: string): number | undefined {
  const normalized = value.trim().replace(",", ".");
  if (!/^(?:\d+(?:\.\d*)?|\.\d+)$/.test(normalized)) return undefined;
  const numeric = Number(normalized);
  if (!Number.isFinite(numeric) || numeric < 0 || numeric > 1) return undefined;
  return numeric;
}

function formatMatchingThreshold(value: number) {
  const fixed = value.toFixed(2);
  return Number(fixed) === value ? fixed : String(value);
}

function message(reason: unknown): SettingsFeedback {
  if (reason instanceof SettingsMessageError) return reason.descriptor;
  if (reason instanceof SettingsValidationError) return reason.feedback;
  if (reason instanceof Error) return reason.message;
  return settingsNotice("requestFailed");
}

function feedbackText(feedback: SettingsFeedback, locale: "en" | "ru") {
  return typeof feedback === "string"
    ? feedback
    : settingsMessage(locale, feedback.key, feedback.values);
}

function settingsSuccessful<
  T extends { status: number },
  S extends T["status"],
>(response: T, status: S): Extract<T, { status: S }> {
  if (response.status !== status) throw settingsHttpError(response);
  return response as Extract<T, { status: S }>;
}

function activeOperation(operation: OperationResponse) {
  return (
    operation.state === "queued" ||
    operation.state === "running" ||
    operation.state === "failed"
  );
}

type SettingsScreenData = {
  model: ApplicationSettingsModel;
  operations: OperationResponse[];
  onOperation: (snapshot: OperationResponse) => void | Promise<void>;
};

export function ProductionSettingsBinder({
  children,
}: ApplicationRouteProviderProps) {
  const { model, operations, onOperation } = useSettingsModel();
  return (
    <ApplicationSettingsBinder model={model}>
      {model.ready &&
        operations.map((operation) =>
          operation.state === "queued" || operation.state === "running" ? (
            <ToolOperationFollower
              key={operation.id}
              operation={operation}
              onSnapshot={onOperation}
            />
          ) : null,
        )}
      {children}
    </ApplicationSettingsBinder>
  );
}

function useSettingsModel(): SettingsScreenData {
  const { locale } = useI18n();
  const hash = useHashLocation();
  const t = (
    key: Parameters<typeof settingsMessage>[1],
    values?: Parameters<typeof settingsMessage>[2],
  ) => settingsMessage(locale, key, values);
  const [restoredSettingsDraft] = useState(() =>
    readSettingsDraft(getSettingsDraftStorage()),
  );
  const [state, setState] = useState<SetupStateBody>();
  const [activeSettingsTab, setActiveSettingsTab] = useState<SettingsTabID>(
    () => settingsTabFromHash(hash),
  );
  const [conflictConfirmation, setConflictConfirmation] =
    useState<ConflictConfirmation>();
  const [navigationAttempt, setNavigationAttempt] = useState<HashAttempt>();
  const [navigationError, setNavigationError] = useState<SettingsFeedback | "">(
    "",
  );
  const [activeConflicts, setActiveConflicts] = useState<
    Partial<Record<ConflictGroup, boolean>>
  >({});
  const authorizedConflictExpected = useRef<
    Partial<Record<ConflictGroup, unknown>>
  >({});
  const [installations, setInstallations] = useState<InstallationResponse[]>(
    [],
  );
  const [catalog, setCatalog] = useState<Partial<Record<Kind, CatalogBody>>>(
    {},
  );
  const [catalogBusy, setCatalogBusy] = useState(false);
  const [catalogError, setCatalogError] = useState<SettingsFeedback | "">("");
  const [checkedAt, setCheckedAt] = useState(
    () => localStorage.getItem(catalogCheckedKey) || "",
  );
  const [initialLoadComplete, setInitialLoadComplete] = useState(false);
  const [draftStorageWarning, setDraftStorageWarning] = useState(false);
  const [operations, setOperations] = useState<OperationResponse[]>([]);
  const [acoustIDApplicationKey, setAcoustIDApplicationKey] = useState("");
  const outputDirectory = useSectionDraft<string>(
    Object.is,
    restoredSettingsDraft?.outputDirectory,
  );
  const workDirectory = useSectionDraft<string>(
    Object.is,
    restoredSettingsDraft?.workDirectory,
  );
  const publicationFormat =
    useSectionDraft<UpdateSettingsBodyPublicationFormat>();
  const confirmedPublicationFormat = useRef<
    UpdateSettingsBodyPublicationFormat | undefined
  >(undefined);
  const publicationPathTemplate = useSectionDraft<string>(
    Object.is,
    restoredSettingsDraft?.publicationPathTemplate,
  );
  const publicationRemoveSources = useSectionDraft<boolean>();
  const publicationAutoCompleteAlbums = useSectionDraft<boolean>();
  const sourceFileConcurrency = useSectionDraft<string>(
    Object.is,
    restoredSettingsDraft?.sourceFileConcurrency,
  );
  const matchingThreshold = useSectionDraft<string>(
    Object.is,
    restoredSettingsDraft?.matchingPairThreshold,
  );
  const musicBrainz = useSectionDraft(
    sameMusicBrainz,
    (() => {
      const mode = restoredSettingsDraft?.musicBrainzMode;
      const url = restoredSettingsDraft?.musicBrainzURL;
      const delay = restoredSettingsDraft?.musicBrainzDelay;
      if (!mode || !url || !delay) return undefined;
      return {
        value: {
          mode: mode.value as UpdateMusicBrainzBodyMode,
          baseURL: url.value,
          selfHostedThrottle: false,
          selfHostedDelaySeconds: delay.value,
        },
        baseline: {
          mode: mode.baseline as UpdateMusicBrainzBodyMode,
          baseURL: url.baseline,
          selfHostedThrottle: false,
          selfHostedDelaySeconds: delay.baseline,
        },
        revision: Math.max(mode.revision, url.revision, delay.revision),
      };
    })(),
  );
  const lrclib = useSectionDraft<boolean>();
  const sha256 = useSectionDraft<boolean>();
  const logLevel = useSectionDraft<UpdateLogLevelBodyLevel>();
  const settingsQueue = useRef<Promise<void>>(Promise.resolve());
  const [selectedRelease, setSelectedRelease] = useState<
    Partial<Record<Kind, string>>
  >({});
  const [installDialog, setInstallDialog] = useState<InstallPlan>();
  const [outputDialog, setOutputDialog] = useState(false);
  const [moveDirectory, setMoveDirectory] = useState(
    () => restoredSettingsDraft?.toolsDirectoryCandidate?.value ?? "",
  );
  const [removeOld, setRemoveOld] = useState(false);
  const [movePlan, setMovePlan] = useState<MovePlan>();
  const [busy, setBusy] = useState(false);
  const [generalPendingCount, setGeneralPendingCount] = useState(0);
  const generalPending = useRef(0);
  const [generalFailures, setGeneralFailures] = useState<
    Partial<Record<GeneralSettingKey, GeneralSettingFailure>>
  >({});
  const [error, setError] = useState<SettingsFeedback | "">("");
  const [loadError, setLoadError] = useState<SettingsFeedback | "">("");
  const [installDialogError, setInstallDialogError] = useState<
    SettingsFeedback | ""
  >("");
  const [moveDialogError, setMoveDialogError] = useState<SettingsFeedback | "">(
    "",
  );
  const [outputDialogError, setOutputDialogError] = useState<
    SettingsFeedback | ""
  >("");
  const [notice, setNotice] = useState<SettingsNotice | null>(null);
  const heading = useRef<HTMLHeadingElement>(null);
  const latestNavigationAttempt = useRef<HashAttempt | undefined>(undefined);
  const capturedNavigationAnchor = useRef<HTMLAnchorElement | null>(null);
  const navigationReturnFocus = useRef<HTMLElement | null>(null);
  const navigationEditedControl = useRef<HTMLElement | null>(null);
  const navigationFocusRestorePending = useRef(false);
  const alert = useRef<HTMLParagraphElement>(null);
  const loadErrorAlert = useRef<HTMLParagraphElement>(null);
  const moveDialogAlert = useRef<HTMLParagraphElement>(null);
  const navigationDialogRef = useRef<HTMLDialogElement>(null);
  const catalogAlert = useRef<HTMLParagraphElement>(null);
  const automaticCatalogRequested = useRef(false);
  const catalogRequestInFlight = useRef(false);
  const screenLoadInFlight = useRef(false);
  const mountedRef = useRef(true);
  const mountGeneration = useRef(0);
  const screenLoadGeneration = useRef(0);
  const dialogReturnFocusRef = useRef<HTMLElement | null>(null);
  const pendingTabFocusTarget = useRef<HTMLElement | null>(null);
  const dialogFocusRestorePending = useRef(false);
  const moveDirectoryRef = useRef(
    restoredSettingsDraft?.toolsDirectoryCandidate?.value ?? "",
  );
  const [moveDirectoryBaselineValue, setMoveDirectoryBaselineValue] = useState(
    restoredSettingsDraft?.toolsDirectoryCandidate?.baseline,
  );
  const moveDirectoryBaseline = useRef<string | undefined>(
    moveDirectoryBaselineValue,
  );
  const removeOldRef = useRef(false);
  const moveInputRevision = useRef(0);
  const moveSessionRevision = useRef(0);
  const moveRequestRevision = useRef(0);
  const movePendingRequest = useRef(0);
  const admittedMoves = useRef(
    new Map<string, { directory: string; inputRevision: number }>(),
  );
  const installSessionRevision = useRef(0);
  const installRequestRevision = useRef(0);
  const installPendingRequest = useRef(0);
  const installStartRevision = useRef(0);
  const moveStartRevision = useRef(0);
  const effectiveBusy = busy || generalPendingCount > 0;
  const isEffectivelyBusy = () => busy || generalPending.current > 0;

  const showSettingsTab = useCallback((tab: SettingsTabID) => {
    setActiveSettingsTab(tab);
    if (typeof window === "undefined") return;
    const nextHash = settingsTabHash(tab);
    if (window.location.hash === nextHash) return;
    window.location.hash = nextHash;
  }, []);

  const selectSettingsTab = (value: string) => {
    if (!isSettingsTabID(value)) return;
    setActiveSettingsTab(value);
    if (typeof window === "undefined") return;
    const nextHash = settingsTabHash(value);
    if (window.location.hash !== nextHash) window.location.hash = nextHash;
  };

  useEffect(() => {
    setActiveSettingsTab(settingsTabFromHash(hash));
  }, [hash]);

  const focusSettingsTarget = useCallback(
    (target: HTMLElement | null) => {
      if (!target) return;
      const targetTab = settingsTargetTab(target);
      if (isSettingsTabID(targetTab) && targetTab !== activeSettingsTab) {
        pendingTabFocusTarget.current = target;
        showSettingsTab(targetTab);
        return;
      }
      pendingTabFocusTarget.current = null;
      requestSettingsFocus(target);
    },
    [activeSettingsTab, showSettingsTab],
  );

  const restoreDialogFocus = useCallback(() => {
    const trigger = dialogReturnFocusRef.current;
    dialogReturnFocusRef.current = null;
    focusSettingsTarget(trigger);
  }, [focusSettingsTarget]);

  const invalidateMovePlan = useCallback(() => {
    moveRequestRevision.current += 1;
    const hadPendingRequest = movePendingRequest.current !== 0;
    movePendingRequest.current = 0;
    setMovePlan(undefined);
    if (hadPendingRequest) setBusy(false);
  }, []);
  const setMoveDirectoryValue = useCallback(
    (directory: string) => {
      moveDirectoryRef.current = directory;
      moveInputRevision.current += 1;
      invalidateMovePlan();
      setMoveDirectory(directory);
    },
    [invalidateMovePlan],
  );
  const setMoveDirectoryBaseline = useCallback((baseline: string) => {
    moveDirectoryBaseline.current = baseline;
    setMoveDirectoryBaselineValue(baseline);
  }, []);
  function setRemoveOldValue(remove: boolean) {
    removeOldRef.current = remove;
    moveInputRevision.current += 1;
    invalidateMovePlan();
    setRemoveOld(remove);
  }

  const readSettings = useCallback(() => {
    const request = settingsQueue.current.then(() =>
      getSettings({ cache: "no-store" }),
    );
    settingsQueue.current = request.then(
      () => undefined,
      () => undefined,
    );
    return request;
  }, []);

  const syncForm = useCallback(
    (fresh: SetupStateBody, ack?: (fresh: SetupStateBody) => void) => {
      if (!mountedRef.current) return;
      setState(fresh);
      const settings = fresh.settings;
      outputDirectory.sync(settings.output_directory);
      workDirectory.sync(settings.work_directory);
      const freshPublicationFormat =
        settings.publication_format === "source"
          ? "source"
          : settings.publication_format === "m4a"
            ? "m4a"
            : "mka";
      const canAdoptPublicationFormat =
        publicationFormat.isPristineAndIdle() &&
        generalFailures.publicationFormat === undefined;
      publicationFormat.sync(freshPublicationFormat);
      if (canAdoptPublicationFormat)
        confirmedPublicationFormat.current = freshPublicationFormat;
      publicationPathTemplate.sync(settings.publication_path_template);
      publicationRemoveSources.sync(settings.publication_remove_sources);
      publicationAutoCompleteAlbums.sync(
        settings.publication_auto_complete_albums,
      );
      sourceFileConcurrency.sync(String(settings.source_file_concurrency));
      matchingThreshold.sync(
        formatMatchingThreshold(settings.matching_pair_threshold),
      );
      musicBrainz.sync({
        mode:
          settings.musicbrainz_mode === "self-hosted"
            ? "self-hosted"
            : "public",
        baseURL: settings.musicbrainz_base_url,
        selfHostedThrottle: settings.musicbrainz_self_hosted_throttle,
        selfHostedDelaySeconds: String(
          settings.musicbrainz_self_hosted_delay_seconds,
        ),
      });
      lrclib.sync(settings.lrclib_enabled);
      sha256.sync(settings.sha256_enabled);
      logLevel.sync(settings.log_level as UpdateLogLevelBodyLevel);
      const moveDirectoryDirty =
        moveDirectoryBaseline.current !== undefined &&
        moveDirectoryRef.current !== moveDirectoryBaseline.current;
      if (
        moveDirectoryBaseline.current !== undefined &&
        moveDirectoryBaseline.current !== settings.tools_directory
      )
        invalidateMovePlan();
      if (!moveDirectoryDirty) {
        if (moveDirectoryRef.current !== settings.tools_directory)
          setMoveDirectoryValue(settings.tools_directory);
        setMoveDirectoryBaseline(settings.tools_directory);
      }
      ack?.(fresh);
    },
    [
      setMoveDirectoryValue,
      setMoveDirectoryBaseline,
      generalFailures.publicationFormat,
      outputDirectory.sync,
      workDirectory.sync,
      publicationFormat.sync,
      publicationFormat.isPristineAndIdle,
      publicationPathTemplate.sync,
      publicationRemoveSources.sync,
      publicationAutoCompleteAlbums.sync,
      sourceFileConcurrency.sync,
      matchingThreshold.sync,
      musicBrainz.sync,
      lrclib.sync,
      sha256.sync,
      logLevel.sync,
      invalidateMovePlan,
    ],
  );

  const currentSettingsDraftSnapshot =
    useCallback((): SettingsDraftSnapshot => {
      const musicDraft = musicBrainz.snapshot();
      const outputDraft = outputDirectory.persisted();
      const workDraft = workDirectory.persisted();
      const templateDraft = publicationPathTemplate.persisted();
      const concurrencyDraft = sourceFileConcurrency.persisted();
      const thresholdDraft = matchingThreshold.persisted();
      return {
        ...(outputDraft ? { outputDirectory: outputDraft } : {}),
        ...(workDraft ? { workDirectory: workDraft } : {}),
        ...(templateDraft ? { publicationPathTemplate: templateDraft } : {}),
        ...(concurrencyDraft
          ? { sourceFileConcurrency: concurrencyDraft }
          : {}),
        ...(thresholdDraft ? { matchingPairThreshold: thresholdDraft } : {}),
        toolsDirectoryCandidate: {
          value: moveDirectoryRef.current,
          baseline: moveDirectoryBaseline.current ?? "",
          revision: moveInputRevision.current,
        },
        ...(musicDraft.value && musicDraft.baseline
          ? {
              musicBrainzMode: {
                value: musicDraft.value.mode,
                baseline: musicDraft.baseline.mode,
                revision: musicDraft.revision,
              },
              musicBrainzURL: {
                value: musicDraft.value.baseURL,
                baseline: musicDraft.baseline.baseURL,
                revision: musicDraft.revision,
              },
              musicBrainzDelay: {
                value: musicDraft.value.selfHostedDelaySeconds,
                baseline: musicDraft.baseline.selfHostedDelaySeconds,
                revision: musicDraft.revision,
              },
            }
          : {}),
      };
    }, [
      musicBrainz.snapshot,
      outputDirectory.persisted,
      workDirectory.persisted,
      publicationPathTemplate.persisted,
      sourceFileConcurrency.persisted,
      matchingThreshold.persisted,
    ]);

  const hasUnsavedSettingsTextDrafts = useCallback(
    () => settingsDraftIsDirty(currentSettingsDraftSnapshot()),
    [currentSettingsDraftSnapshot],
  );

  const shouldBlockNavigation = useCallback(
    (destination: string) =>
      parseSettingsRoute(destination) === undefined &&
      hasUnsavedSettingsTextDrafts(),
    [hasUnsavedSettingsTextDrafts],
  );

  const refreshState = useCallback(
    async (
      ack?: (fresh: SetupStateBody) => void,
      isCurrent: () => boolean = () => mountedRef.current,
    ) => {
      const generation = mountGeneration.current;
      const response = settingsSuccessful(await readSettings(), 200);
      if (
        !mountedRef.current ||
        mountGeneration.current !== generation ||
        !isCurrent()
      )
        return response.data;
      syncForm(response.data, ack);
      return response.data;
    },
    [readSettings, syncForm],
  );

  function confirmConflictingSave(
    group: ConflictGroup,
    currentValue: unknown,
    onConfirm: () => void,
  ): boolean {
    if (
      !(group in conflictValues) ||
      Object.hasOwn(authorizedConflictExpected.current, group)
    ) {
      onConfirm();
      return false;
    }
    setConflictConfirmation({ group, currentValue, onConfirm });
    return true;
  }

  function expectedForSave<T>(group: ConflictGroup, fallback: T): T {
    if (!Object.hasOwn(authorizedConflictExpected.current, group))
      return fallback;
    const expected = authorizedConflictExpected.current[group] as T;
    delete authorizedConflictExpected.current[group];
    return expected;
  }

  function continueEditingConflict(group: ConflictGroup) {
    setActiveConflicts((previous) => ({ ...previous, [group]: false }));
  }

  async function acceptSettingsWrite<
    T extends { status: number },
    S extends T["status"],
  >(response: T, expectedStatus: S) {
    if (response.status === 409) {
      try {
        await refreshState();
        setActiveConflicts((previous) => {
          const next = { ...previous };
          for (const group of Object.keys(next) as ConflictGroup[])
            if (group in conflictValues) next[group] = true;
          return next;
        });
      } catch (reason) {
        throw new SettingsConflictError(
          settingsNotice("settingsConflictRefreshFailed", {
            detail: message(reason),
          }),
        );
      }
      throw new SettingsConflictError(
        settingsNotice("settingsConflictDraftKept"),
      );
    }
    if (response.status === 400 || response.status === 422)
      throw new SettingsValidationError(settingsHttpError(response));
    return settingsSuccessful(response, expectedStatus);
  }

  const refreshInstallations = useCallback(
    async (isCurrent: () => boolean = () => mountedRef.current) => {
      const generation = mountGeneration.current;
      const [ffmpeg, fpcalc] = await Promise.all([
        listInstallations({ package_kind: "ffmpeg" }),
        listInstallations({ package_kind: "fpcalc" }),
      ]);
      settingsSuccessful(ffmpeg, 200);
      settingsSuccessful(fpcalc, 200);
      if (
        !mountedRef.current ||
        mountGeneration.current !== generation ||
        !isCurrent()
      )
        return;
      setInstallations([
        ...((ffmpeg.data as InstallationsBody).installations || []),
        ...((fpcalc.data as InstallationsBody).installations || []),
      ]);
    },
    [],
  );

  const refreshOperations = useCallback(
    async (isCurrent: () => boolean = () => mountedRef.current) => {
      const generation = mountGeneration.current;
      const response = settingsSuccessful(
        await listOperations({ tool_discovery: "install_and_move" }),
        200,
      );
      const active = (response.data.operations || []).filter(activeOperation);
      if (
        !mountedRef.current ||
        mountGeneration.current !== generation ||
        !isCurrent()
      )
        return active;
      setOperations(active);
      return active;
    },
    [],
  );

  const readRunningOperations = useCallback(async () => {
    const response = settingsSuccessful(
      await listOperations({ state: "running" }),
      200,
    );
    return (response.data.operations || []).length > 0;
  }, []);

  const loadScreen = useCallback(async () => {
    if (!mountedRef.current || screenLoadInFlight.current) return;
    const generalSettingsBarrier = generalSettingsQueue;
    screenLoadInFlight.current = true;
    const generation = ++screenLoadGeneration.current;
    const isCurrent = () =>
      mountedRef.current && screenLoadGeneration.current === generation;
    setBusy(true);
    setInitialLoadComplete(false);
    try {
      const results = await Promise.allSettled([
        generalSettingsBarrier.then(() => refreshState(undefined, isCurrent)),
        refreshInstallations(isCurrent),
        refreshOperations(isCurrent),
      ]);
      const failure = results.find(
        (result): result is PromiseRejectedResult =>
          result.status === "rejected",
      );
      if (failure) throw failure.reason;
      if (isCurrent()) {
        setLoadError("");
        setInitialLoadComplete(true);
      }
    } catch (reason) {
      if (isCurrent()) setLoadError(message(reason));
    } finally {
      if (isCurrent()) {
        screenLoadInFlight.current = false;
        setBusy(false);
      }
    }
  }, [refreshInstallations, refreshOperations, refreshState]);

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
      mountGeneration.current += 1;
      screenLoadGeneration.current += 1;
      screenLoadInFlight.current = false;
    };
  }, []);
  useEffect(() => {
    requestSettingsFocus(heading.current);
    void loadScreen();
  }, [loadScreen]);
  useEffect(() => {
    if (error && !installDialog && !movePlan)
      requestSettingsFocus(alert.current);
  }, [error, installDialog, movePlan]);
  useLayoutEffect(() => {
    if (moveDialogError && activeSettingsTab === "tools" && !navigationAttempt)
      requestSettingsFocus(moveDialogAlert.current);
  }, [activeSettingsTab, moveDialogError, navigationAttempt]);
  useLayoutEffect(() => {
    const target = pendingTabFocusTarget.current;
    if (!target) return;
    const targetTab = settingsTargetTab(target);
    if (targetTab && targetTab === activeSettingsTab) {
      pendingTabFocusTarget.current = null;
      requestSettingsFocus(target);
    }
  }, [activeSettingsTab]);
  useLayoutEffect(() => {
    if (
      dialogFocusRestorePending.current &&
      !installDialog &&
      !movePlan &&
      !outputDialog &&
      !effectiveBusy
    ) {
      dialogFocusRestorePending.current = false;
      restoreDialogFocus();
    }
  }, [
    effectiveBusy,
    installDialog,
    movePlan,
    outputDialog,
    restoreDialogFocus,
  ]);
  useLayoutEffect(() => {
    if (navigationAttempt || !navigationFocusRestorePending.current) return;
    navigationFocusRestorePending.current = false;
    if (settingsDialogIsOpen()) return;
    const target = [
      navigationReturnFocus.current,
      navigationEditedControl.current,
    ].find((candidate) => candidate?.isConnected);
    navigationReturnFocus.current = null;
    navigationEditedControl.current = null;
    focusSettingsTarget(target ?? null);
  }, [focusSettingsTarget, navigationAttempt]);
  useEffect(() => {
    if (catalogError) requestSettingsFocus(catalogAlert.current);
  }, [catalogError]);
  useEffect(() => {
    if (loadError) requestSettingsFocus(loadErrorAlert.current);
  }, [loadError]);

  const requestCatalog = useCallback(async () => {
    if (!mountedRef.current || catalogRequestInFlight.current) return;
    const generation = mountGeneration.current;
    const isCurrent = () =>
      mountedRef.current && mountGeneration.current === generation;
    catalogRequestInFlight.current = true;
    setCatalogBusy(true);
    setCatalogError("");
    try {
      const catalogs = await loadManagedToolCatalogs(settingsHttpError);
      if (!isCurrent()) return;
      setCatalog(catalogs);
      const timestamp = new Date().toISOString();
      localStorage.setItem(catalogCheckedKey, timestamp);
      setCheckedAt(timestamp);
    } catch (reason) {
      if (isCurrent()) setCatalogError(message(reason));
    } finally {
      catalogRequestInFlight.current = false;
      if (isCurrent()) setCatalogBusy(false);
    }
  }, []);

  useEffect(() => {
    if (!initialLoadComplete || !state || automaticCatalogRequested.current)
      return;
    const last = checkedAt ? Date.parse(checkedAt) : 0;
    if (!last || Date.now() - last >= cooldownMs) {
      automaticCatalogRequested.current = true;
      void requestCatalog();
    }
  }, [checkedAt, initialLoadComplete, requestCatalog, state]);

  const persistedSettingsDraftSnapshot = useMemo<SettingsDraftSnapshot>(() => {
    const output = persistedTextDraft(
      outputDirectory.value,
      outputDirectory.baseline,
      outputDirectory.revision,
    );
    const work = persistedTextDraft(
      workDirectory.value,
      workDirectory.baseline,
      workDirectory.revision,
    );
    const template = persistedTextDraft(
      publicationPathTemplate.value,
      publicationPathTemplate.baseline,
      publicationPathTemplate.revision,
    );
    const concurrency = persistedTextDraft(
      sourceFileConcurrency.value,
      sourceFileConcurrency.baseline,
      sourceFileConcurrency.revision,
    );
    const threshold = persistedTextDraft(
      matchingThreshold.value,
      matchingThreshold.baseline,
      matchingThreshold.revision,
    );
    const musicValue = musicBrainz.value;
    const musicBaseline = musicBrainz.baseline;
    return {
      ...(output ? { outputDirectory: output } : {}),
      ...(work ? { workDirectory: work } : {}),
      ...(template ? { publicationPathTemplate: template } : {}),
      ...(concurrency ? { sourceFileConcurrency: concurrency } : {}),
      ...(threshold ? { matchingPairThreshold: threshold } : {}),
      toolsDirectoryCandidate: {
        value: moveDirectory,
        baseline:
          moveDirectoryBaselineValue ?? state?.settings.tools_directory ?? "",
        revision: moveInputRevision.current,
      },
      ...(musicValue && musicBaseline
        ? {
            musicBrainzMode: {
              value: musicValue.mode,
              baseline: musicBaseline.mode,
              revision: musicBrainz.revision,
            },
            musicBrainzURL: {
              value: musicValue.baseURL,
              baseline: musicBaseline.baseURL,
              revision: musicBrainz.revision,
            },
            musicBrainzDelay: {
              value: musicValue.selfHostedDelaySeconds,
              baseline: musicBaseline.selfHostedDelaySeconds,
              revision: musicBrainz.revision,
            },
          }
        : {}),
    };
  }, [
    state,
    outputDirectory.value,
    outputDirectory.baseline,
    outputDirectory.revision,
    workDirectory.value,
    workDirectory.baseline,
    workDirectory.revision,
    publicationPathTemplate.value,
    publicationPathTemplate.baseline,
    publicationPathTemplate.revision,
    sourceFileConcurrency.value,
    sourceFileConcurrency.baseline,
    sourceFileConcurrency.revision,
    matchingThreshold.value,
    matchingThreshold.baseline,
    matchingThreshold.revision,
    musicBrainz.value,
    musicBrainz.baseline,
    musicBrainz.revision,
    moveDirectory,
    moveDirectoryBaselineValue,
  ]);

  useEffect(() => {
    if (!initialLoadComplete || !state) return;
    const dirty = settingsDraftIsDirty(persistedSettingsDraftSnapshot);
    const stored = writeSettingsDraft(
      getSettingsDraftStorage(),
      dirty ? persistedSettingsDraftSnapshot : null,
    );
    setDraftStorageWarning(!stored && dirty);
  }, [initialLoadComplete, persistedSettingsDraftSnapshot, state]);
  useLayoutEffect(() => {
    const captureNavigationAnchor = (event: MouseEvent) => {
      capturedNavigationAnchor.current = null;
      if (
        event.defaultPrevented ||
        event.button !== 0 ||
        event.metaKey ||
        event.ctrlKey ||
        event.shiftKey ||
        event.altKey
      )
        return;
      const target = event.target;
      if (!(target instanceof Element)) return;
      const anchor = target.closest<HTMLAnchorElement>("a[href]");
      if (
        !anchor ||
        anchor.hasAttribute("download") ||
        (anchor.target && anchor.target.toLowerCase() !== "_self")
      )
        return;
      const destination = new URL(anchor.href, window.location.href);
      const current = new URL(window.location.href);
      if (
        destination.origin === current.origin &&
        destination.pathname === current.pathname &&
        destination.search === current.search &&
        destination.hash &&
        destination.hash !== window.location.hash
      ) {
        capturedNavigationAnchor.current = anchor;
        queueMicrotask(() => {
          if (capturedNavigationAnchor.current === anchor)
            capturedNavigationAnchor.current = null;
        });
      }
    };
    window.addEventListener("click", captureNavigationAnchor, true);
    const unregister = registerHashNavigationGuard({
      shouldBlock: shouldBlockNavigation,
      onBlocked: (attempt) => {
        const anchor = capturedNavigationAnchor.current;
        capturedNavigationAnchor.current = null;
        navigationReturnFocus.current =
          anchor?.isConnected &&
          new URL(anchor.href, window.location.href).hash === attempt.to
            ? anchor
            : null;
        const activeControl = settingsFocusedControl(
          navigationDialogRef.current,
        );
        const templateDraft = publicationPathTemplate.snapshot();
        const templateDirty =
          templateDraft.value !== undefined &&
          templateDraft.baseline !== undefined &&
          templateDraft.value !== templateDraft.baseline;
        navigationEditedControl.current =
          activeControl ??
          (templateDirty
            ? resolveSettingsFocusTarget("publicationTemplate")
            : null);
        latestNavigationAttempt.current = attempt;
        setNavigationAttempt(attempt);
        setNavigationError("");
      },
    });
    return () => {
      window.removeEventListener("click", captureNavigationAnchor, true);
      unregister();
    };
  }, [publicationPathTemplate.snapshot, shouldBlockNavigation]);
  useEffect(() => {
    const handleBeforeUnload = (event: BeforeUnloadEvent) => {
      if (!hasUnsavedSettingsTextDrafts()) return;
      event.preventDefault();
      event.returnValue = "";
    };
    window.addEventListener("beforeunload", handleBeforeUnload);
    return () => window.removeEventListener("beforeunload", handleBeforeUnload);
  }, [hasUnsavedSettingsTextDrafts]);

  async function mutate<
    T extends { status: number },
    C extends { revision: number },
  >(
    action: (saved: C) => Promise<T>,
    status: T["status"],
    successText: SettingsNotice,
    capture: () => C,
    acknowledge: (fresh: SetupStateBody, revision: number) => void,
    beginDraftSave: (revision: number) => void,
    endDraftSave: (revision: number) => void,
    capturedDraft?: C,
    saveAlreadyStarted = false,
    manageBusy = true,
    onFailure?: (
      savedDraft: C,
      phase: GeneralFailurePhase,
      reason: unknown,
    ) => boolean,
    shouldAnnounce?: (savedDraft: C) => boolean,
    originatingGeneration = mountGeneration.current,
  ): Promise<SettingsSaveOutcome> {
    const savedDraft = capturedDraft ?? capture();
    const generation = originatingGeneration;
    const isCurrent = () =>
      mountedRef.current && mountGeneration.current === generation;
    if (!saveAlreadyStarted) beginDraftSave(savedDraft.revision);
    if (manageBusy && isCurrent()) setBusy(true);
    if (isCurrent()) {
      setError("");
      setNotice(null);
    }
    let phase: GeneralFailurePhase = "write";
    try {
      const response = await action(savedDraft);
      await acceptSettingsWrite(response, status);
      phase = "confirm";
      try {
        await refreshState((fresh) => acknowledge(fresh, savedDraft.revision));
      } catch (reason) {
        throw new SettingsLocalizedError(
          settingsNotice("settingsAcceptedRefreshFailed", {
            detail: message(reason),
          }),
        );
      }
      if (isCurrent() && (shouldAnnounce?.(savedDraft) ?? true))
        setNotice(successText);
      return { status: "saved", group: "all" };
    } catch (reason) {
      const conflict = reason instanceof SettingsConflictError;
      if (conflict) {
        setActiveConflicts((previous) => {
          const next = { ...previous };
          for (const group of Object.keys(next) as ConflictGroup[])
            if (group in conflictValues) next[group] = true;
          return next;
        });
      }
      const report = isCurrent()
        ? conflict
          ? true
          : (onFailure?.(savedDraft, phase, reason) ?? true)
        : false;
      if (isCurrent() && report) setError(message(reason));
      const status = conflict
        ? "conflict"
        : reason instanceof SettingsValidationError
          ? "invalid"
          : phase === "confirm"
            ? "uncertain"
            : "failed";
      return { status, group: "all", message: message(reason) };
    } finally {
      endDraftSave(savedDraft.revision);
      if (manageBusy && isCurrent()) setBusy(false);
    }
  }

  function mutateGeneral<
    T extends { status: number },
    C extends { revision: number },
  >(
    action: (saved: C) => Promise<T>,
    status: T["status"],
    successText: SettingsNotice,
    capture: () => C,
    acknowledge: (fresh: SetupStateBody, revision: number) => void,
    beginDraftSave: (revision: number) => void,
    endDraftSave: (revision: number) => void,
    onFailure: (
      savedDraft: C,
      phase: GeneralFailurePhase,
      reason: unknown,
    ) => boolean,
    shouldAnnounce: (savedDraft: C) => boolean,
  ) {
    const generation = mountGeneration.current;
    const savedDraft = capture();
    beginDraftSave(savedDraft.revision);
    generalPending.current += 1;
    if (mountedRef.current) setGeneralPendingCount(generalPending.current);
    const request = generalSettingsQueue.then(() =>
      mutate(
        action,
        status,
        successText,
        capture,
        acknowledge,
        beginDraftSave,
        endDraftSave,
        savedDraft,
        true,
        false,
        onFailure,
        shouldAnnounce,
        generation,
      ),
    );
    const settled = request.finally(() => {
      generalPending.current -= 1;
      if (mountedRef.current && mountGeneration.current === generation)
        setGeneralPendingCount(generalPending.current);
    });
    generalSettingsQueue = settled.then(
      () => undefined,
      () => undefined,
    );
    return settled;
  }

  function savePublicationFormat(value: UpdateSettingsBodyPublicationFormat) {
    publicationFormat.update(value);
    clearGeneralFailure("publicationFormat");
    void mutateGeneral(
      (savedDraft) =>
        updateSettings({
          publication_format: savedDraft.value ?? value,
          expected: {
            publication_format:
              confirmedPublicationFormat.current ??
              (savedDraft.baseline as UpdateSettingsBodyPublicationFormat) ??
              state?.settings.publication_format ??
              value,
          } satisfies UpdateSettingsExpectedBody,
        }),
      204,
      settingsNotice("publicationFormatSaved"),
      publicationFormat.capture,
      (fresh, revision) => {
        const confirmed =
          fresh.settings.publication_format === "source"
            ? "source"
            : fresh.settings.publication_format === "m4a"
              ? "m4a"
              : "mka";
        if (confirmed !== value)
          throw new SettingsLocalizedError(
            settingsNotice("serverDidNotConfirmFormat", { value, confirmed }),
          );
        confirmedPublicationFormat.current = confirmed;
        publicationFormat.acknowledge(confirmed, revision);
      },
      publicationFormat.beginSave,
      publicationFormat.endSave,
      (savedDraft, phase) =>
        recordGeneralFailure(
          "publicationFormat",
          savedDraft.revision,
          savedDraft.value ?? value,
          phase,
          publicationFormat.capture().revision,
        ),
      (savedDraft) =>
        publicationFormat.capture().revision === savedDraft.revision,
    );
  }

  async function saveConcurrency(): Promise<SettingsSaveOutcome> {
    if (isEffectivelyBusy()) return { status: "failed", group: "concurrency" };
    const draft = sourceFileConcurrency.capture();
    const current = state?.settings.source_file_concurrency;
    if (draft.value === undefined || current === undefined)
      return { status: "failed", group: "concurrency" };
    if (!sourceFileConcurrency.dirty)
      return { status: "saved", group: "concurrency" };
    if (!validConcurrency(draft.value))
      return {
        status: "invalid",
        group: "concurrency",
        message: settingsNotice("concurrencyMustBePositive"),
      };
    let attempt: Promise<SettingsSaveOutcome> | undefined;
    const confirmationPending = confirmConflictingSave(
      "concurrency",
      current,
      () => {
        attempt = mutate(
          (savedDraft) =>
            updateSettings({
              source_file_concurrency: Number(savedDraft.value),
              expected: {
                source_file_concurrency: Number(
                  expectedForSave(
                    "concurrency",
                    savedDraft.baseline ?? String(current),
                  ),
                ),
              } satisfies UpdateSettingsExpectedBody,
            }),
          204,
          settingsNotice("concurrencySaved"),
          sourceFileConcurrency.capture,
          (fresh, revision) =>
            sourceFileConcurrency.acknowledge(
              String(fresh.settings.source_file_concurrency),
              revision,
            ),
          sourceFileConcurrency.beginSave,
          sourceFileConcurrency.endSave,
          draft,
        ).then((result) => ({ ...result, group: "concurrency" }));
      },
    );
    if (confirmationPending)
      return { status: "confirmation_pending", group: "concurrency" };
    return (await attempt) ?? { status: "failed", group: "concurrency" };
  }

  async function saveMatchingThreshold(): Promise<SettingsSaveOutcome> {
    if (isEffectivelyBusy())
      return { status: "failed", group: "matchingThreshold" };
    const draft = matchingThreshold.capture();
    const current = state?.settings.matching_pair_threshold;
    if (draft.value === undefined || current === undefined)
      return { status: "failed", group: "matchingThreshold" };
    if (!matchingThreshold.dirty)
      return { status: "saved", group: "matchingThreshold" };
    const submitted = matchingThresholdNumber(draft.value);
    if (submitted === undefined)
      return {
        status: "invalid",
        group: "matchingThreshold",
        message: settingsNotice("matchingThresholdMustBeInRange"),
      };
    let attempt: Promise<SettingsSaveOutcome> | undefined;
    const confirmationPending = confirmConflictingSave(
      "matchingThreshold",
      current,
      () => {
        attempt = mutate(
          (savedDraft) => {
            const expectedSource = expectedForSave<unknown>(
              "matchingThreshold",
              savedDraft.baseline ?? formatMatchingThreshold(current),
            );
            const expected =
              typeof expectedSource === "number"
                ? expectedSource
                : (matchingThresholdNumber(String(expectedSource)) ?? current);
            return updateSettings({
              matching_pair_threshold:
                matchingThresholdNumber(savedDraft.value ?? "") ?? submitted,
              expected: {
                matching_pair_threshold: expected,
              } satisfies UpdateSettingsExpectedBody,
            });
          },
          204,
          settingsNotice("matchingThresholdSaved"),
          matchingThreshold.capture,
          (fresh, revision) =>
            matchingThreshold.acknowledge(
              formatMatchingThreshold(fresh.settings.matching_pair_threshold),
              revision,
            ),
          matchingThreshold.beginSave,
          matchingThreshold.endSave,
          draft,
        ).then((result) => ({ ...result, group: "matchingThreshold" }));
      },
    );
    if (confirmationPending)
      return { status: "confirmation_pending", group: "matchingThreshold" };
    return (await attempt) ?? { status: "failed", group: "matchingThreshold" };
  }

  async function savePublicationTemplate(): Promise<SettingsSaveOutcome> {
    if (isEffectivelyBusy())
      return { status: "failed", group: "publicationTemplate" };
    const draft = publicationPathTemplate.capture();
    const current = state?.settings.publication_path_template;
    if (current === undefined)
      return { status: "failed", group: "publicationTemplate" };
    if (!publicationPathTemplate.dirty)
      return { status: "saved", group: "publicationTemplate" };
    if (!draft.value)
      return {
        status: "invalid",
        group: "publicationTemplate",
        message: settingsNotice("templateRequired"),
      };
    let attempt: Promise<SettingsSaveOutcome> | undefined;
    const confirmationPending = confirmConflictingSave(
      "publicationTemplate",
      current,
      () => {
        attempt = mutate(
          (savedDraft) =>
            updateSettings({
              publication_path_template: savedDraft.value,
              expected: {
                publication_path_template: expectedForSave(
                  "publicationTemplate",
                  savedDraft.baseline ?? current,
                ),
              } satisfies UpdateSettingsExpectedBody,
            }),
          204,
          settingsNotice("templateSaved"),
          publicationPathTemplate.capture,
          (fresh, revision) =>
            publicationPathTemplate.acknowledge(
              fresh.settings.publication_path_template,
              revision,
            ),
          publicationPathTemplate.beginSave,
          publicationPathTemplate.endSave,
          draft,
        ).then((result) => ({ ...result, group: "publicationTemplate" }));
      },
    );
    if (confirmationPending)
      return { status: "confirmation_pending", group: "publicationTemplate" };
    return (
      (await attempt) ?? {
        status: "failed",
        group: "publicationTemplate",
      }
    );
  }

  async function saveWorkDirectory(): Promise<SettingsSaveOutcome> {
    if (isEffectivelyBusy())
      return { status: "failed", group: "workDirectory" };
    const draft = workDirectory.capture();
    const current = state?.settings.work_directory;
    if (draft.value === undefined || current === undefined)
      return { status: "failed", group: "workDirectory" };
    if (!workDirectory.dirty)
      return { status: "saved", group: "workDirectory" };
    let attempt: Promise<SettingsSaveOutcome> | undefined;
    const confirmationPending = confirmConflictingSave(
      "workDirectory",
      current,
      () => {
        attempt = mutate(
          (savedDraft) =>
            updateSettings({
              work_directory: savedDraft.value,
              expected: {
                work_directory: expectedForSave(
                  "workDirectory",
                  savedDraft.baseline ?? current,
                ),
              } satisfies UpdateSettingsExpectedBody,
            }),
          204,
          settingsNotice("workDirectoryChanged"),
          workDirectory.capture,
          (fresh, revision) =>
            workDirectory.acknowledge(fresh.settings.work_directory, revision),
          workDirectory.beginSave,
          workDirectory.endSave,
          draft,
        ).then((result) => ({ ...result, group: "workDirectory" }));
      },
    );
    if (confirmationPending)
      return { status: "confirmation_pending", group: "workDirectory" };
    return (await attempt) ?? { status: "failed", group: "workDirectory" };
  }

  function saveSha256(enabled: boolean) {
    sha256.update(enabled);
    clearGeneralFailure("sha256");
    void mutateGeneral(
      (savedDraft) =>
        updateSha256Setting({
          enabled: savedDraft.value ?? state?.settings.sha256_enabled ?? true,
        }),
      204,
      settingsNotice("shaSaved"),
      sha256.capture,
      (fresh, revision) =>
        sha256.acknowledge(fresh.settings.sha256_enabled, revision),
      sha256.beginSave,
      sha256.endSave,
      (savedDraft, phase) =>
        recordGeneralFailure(
          "sha256",
          savedDraft.revision,
          savedDraft.value ?? enabled,
          phase,
          sha256.capture().revision,
        ),
      (savedDraft) => sha256.capture().revision === savedDraft.revision,
    );
  }

  function savePublicationRemoveSources(enabled: boolean) {
    publicationRemoveSources.update(enabled);
    const savedDraft = publicationRemoveSources.capture();
    void mutate(
      (draft) =>
        updateSettings({
          publication_remove_sources: draft.value ?? enabled,
          expected: {
            publication_remove_sources:
              draft.baseline ??
              state?.settings.publication_remove_sources ??
              false,
          } satisfies UpdateSettingsExpectedBody,
        }),
      204,
      settingsNotice("sourceDeletionSaved"),
      publicationRemoveSources.capture,
      (fresh, revision) =>
        publicationRemoveSources.acknowledge(
          fresh.settings.publication_remove_sources,
          revision,
        ),
      publicationRemoveSources.beginSave,
      publicationRemoveSources.endSave,
      savedDraft,
    );
  }

  function savePublicationAutoCompleteAlbums(enabled: boolean) {
    publicationAutoCompleteAlbums.update(enabled);
    const savedDraft = publicationAutoCompleteAlbums.capture();
    void mutate(
      (draft) =>
        updateSettings({
          publication_auto_complete_albums: draft.value ?? enabled,
          expected: {
            publication_auto_complete_albums:
              draft.baseline ??
              state?.settings.publication_auto_complete_albums ??
              false,
          } satisfies UpdateSettingsExpectedBody,
        }),
      204,
      settingsNotice("albumCompletionSaved"),
      publicationAutoCompleteAlbums.capture,
      (fresh, revision) =>
        publicationAutoCompleteAlbums.acknowledge(
          fresh.settings.publication_auto_complete_albums,
          revision,
        ),
      publicationAutoCompleteAlbums.beginSave,
      publicationAutoCompleteAlbums.endSave,
      savedDraft,
    );
  }

  function saveLrclib(enabled: boolean) {
    lrclib.update(enabled);
    const savedDraft = lrclib.capture();
    void mutate(
      (draft) =>
        updateLrclibSetting({
          enabled: draft.value ?? enabled,
        }),
      204,
      settingsNotice("lrclibSaved"),
      lrclib.capture,
      (fresh, revision) =>
        lrclib.acknowledge(fresh.settings.lrclib_enabled, revision),
      lrclib.beginSave,
      lrclib.endSave,
      savedDraft,
    );
  }

  function saveLogLevel(value: UpdateLogLevelBodyLevel) {
    logLevel.update(value);
    clearGeneralFailure("logLevel");
    void mutateGeneral(
      (savedDraft) =>
        updateLogLevel({
          level:
            savedDraft.value ??
            (state?.settings.log_level as UpdateLogLevelBodyLevel) ??
            "info",
        }),
      204,
      settingsNotice("logLevelApplied"),
      logLevel.capture,
      (fresh, revision) =>
        logLevel.acknowledge(
          fresh.settings.log_level as UpdateLogLevelBodyLevel,
          revision,
        ),
      logLevel.beginSave,
      logLevel.endSave,
      (savedDraft, phase) =>
        recordGeneralFailure(
          "logLevel",
          savedDraft.revision,
          savedDraft.value ?? value,
          phase,
          logLevel.capture().revision,
        ),
      (savedDraft) => logLevel.capture().revision === savedDraft.revision,
    );
  }

  function recordGeneralFailure(
    setting: GeneralSettingKey,
    revision: number,
    value: GeneralSettingFailure["value"],
    phase: GeneralFailurePhase,
    currentRevision: number,
  ) {
    if (revision !== currentRevision) return false;
    setGeneralFailures((previous) => ({
      ...previous,
      [setting]: { revision, value, phase },
    }));
    return true;
  }

  function clearGeneralFailure(setting: GeneralSettingKey) {
    setGeneralFailures((previous) => {
      if (!previous[setting]) return previous;
      const next = { ...previous };
      delete next[setting];
      return next;
    });
  }

  function generalFailurePresentations(): SettingsGeneralFailureModel[] {
    return (["publicationFormat", "sha256", "logLevel"] as const).flatMap(
      (setting) => {
        const failure = generalFailures[setting];
        if (!failure) return [];
        const attempt: SettingsGeneralFailureModel["attempt"] =
          setting === "publicationFormat"
            ? {
                setting,
                value: failure.value as UpdateSettingsBodyPublicationFormat,
              }
            : setting === "sha256"
              ? { setting, value: failure.value as boolean }
              : { setting, value: String(failure.value) };
        return [
          {
            phase: failure.phase,
            attempt,
            disabled: effectiveBusy,
            onAction: () => retryGeneralFailure(setting),
          },
        ];
      },
    );
  }

  function retryGeneralFailure(setting: GeneralSettingKey) {
    const failure = generalFailures[setting];
    if (!failure) return;
    const currentRevision = () => {
      switch (setting) {
        case "publicationFormat":
          return publicationFormat.capture().revision;
        case "sha256":
          return sha256.capture().revision;
        case "logLevel":
          return logLevel.capture().revision;
      }
    };
    if (currentRevision() !== failure.revision) {
      clearGeneralFailure(setting);
      return;
    }
    if (failure.phase === "write") {
      switch (setting) {
        case "publicationFormat":
          savePublicationFormat(
            failure.value as UpdateSettingsBodyPublicationFormat,
          );
          return;
        case "sha256":
          saveSha256(failure.value as boolean);
          return;
        case "logLevel":
          saveLogLevel(failure.value as UpdateLogLevelBodyLevel);
          return;
      }
    }

    const generation = mountGeneration.current;
    generalPending.current += 1;
    if (mountedRef.current) setGeneralPendingCount(generalPending.current);
    const request = generalSettingsQueue.then(async () => {
      if (
        !mountedRef.current ||
        mountGeneration.current !== generation ||
        currentRevision() !== failure.revision
      )
        return;
      setError("");
      setNotice(null);
      try {
        const fresh = await refreshState();
        if (!mountedRef.current || mountGeneration.current !== generation)
          return;
        switch (setting) {
          case "publicationFormat": {
            const confirmed =
              fresh.settings.publication_format === "source"
                ? "source"
                : fresh.settings.publication_format === "m4a"
                  ? "m4a"
                  : "mka";
            if (confirmed !== failure.value)
              throw new SettingsLocalizedError(
                settingsNotice("serverDidNotConfirmFormat", {
                  value: String(failure.value),
                  confirmed,
                }),
              );
            confirmedPublicationFormat.current = confirmed;
            publicationFormat.acknowledge(confirmed, failure.revision);
            break;
          }
          case "sha256":
            sha256.acknowledge(fresh.settings.sha256_enabled, failure.revision);
            break;
          case "logLevel":
            logLevel.acknowledge(
              fresh.settings.log_level as UpdateLogLevelBodyLevel,
              failure.revision,
            );
            break;
        }
        if (
          mountedRef.current &&
          mountGeneration.current === generation &&
          currentRevision() === failure.revision
        ) {
          setGeneralFailures((previous) => {
            if (previous[setting]?.revision !== failure.revision)
              return previous;
            const next = { ...previous };
            delete next[setting];
            return next;
          });
          setNotice(settingsNotice("settingConfirmed"));
        }
      } catch (reason) {
        if (
          mountedRef.current &&
          mountGeneration.current === generation &&
          currentRevision() === failure.revision
        )
          setError(
            settingsNotice("settingsAcceptedRefreshFailed", {
              detail: message(reason),
            }),
          );
      }
    });
    const settled = request.finally(() => {
      generalPending.current -= 1;
      if (mountedRef.current && mountGeneration.current === generation)
        setGeneralPendingCount(generalPending.current);
    });
    generalSettingsQueue = settled.then(
      () => undefined,
      () => undefined,
    );
  }

  function closeOutputDialog(force = false) {
    if (isEffectivelyBusy() && !force) return;
    dialogFocusRestorePending.current = true;
    setOutputDialogError("");
    setOutputDialog(false);
  }

  async function confirmOutputDirectory() {
    if (
      "outputDirectory" in conflictValues &&
      !Object.hasOwn(authorizedConflictExpected.current, "outputDirectory")
    ) {
      const current = state?.settings.output_directory;
      if (current !== undefined)
        confirmConflictingSave("outputDirectory", current, () => {
          void confirmOutputDirectory();
        });
      return;
    }
    if (isEffectivelyBusy()) return;
    const savedDraft = outputDirectory.capture();
    const output = savedDraft.value;
    if (output === undefined) return;
    // This approval belongs to this attempt, even if the running-work check blocks it.
    const expectedOutput = expectedForSave(
      "outputDirectory",
      savedDraft.baseline ?? state?.settings.output_directory ?? "",
    );
    outputDirectory.beginSave(savedDraft.revision);
    setBusy(true);
    setOutputDialogError("");
    setNotice(null);
    try {
      if (await readRunningOperations()) {
        setOutputDialogError(settingsNotice("waitForRunningOperations"));
        return;
      }
      await acceptSettingsWrite(
        await updateSettings({
          output_directory: output,
          expected: {
            output_directory: expectedOutput,
          } satisfies UpdateSettingsExpectedBody,
        }),
        204,
      );
      try {
        await refreshState((fresh) =>
          outputDirectory.acknowledge(
            fresh.settings.output_directory,
            savedDraft.revision,
          ),
        );
      } catch (reason) {
        throw new SettingsLocalizedError(
          settingsNotice("outputDirectoryAcceptedRefreshFailed", {
            detail: message(reason),
          }),
        );
      }
      setNotice(settingsNotice("outputDirectoryChanged"));
      closeOutputDialog(true);
    } catch (reason) {
      setOutputDialogError(message(reason));
    } finally {
      outputDirectory.endSave(savedDraft.revision);
      setBusy(false);
    }
  }

  async function saveAcoustIDApplicationKey() {
    if (isEffectivelyBusy() || acoustIDApplicationKey.length === 0) return;
    setBusy(true);
    setError("");
    setNotice(null);
    try {
      settingsSuccessful(
        await setAcoustidApplicationKey({ key: acoustIDApplicationKey }),
        204,
      );
      setAcoustIDApplicationKey("");
      await refreshState();
      setNotice(settingsNotice("acoustIdSaved"));
    } catch (reason) {
      setError(message(reason));
    } finally {
      setBusy(false);
    }
  }

  async function deleteAcoustIDApplicationKey() {
    if (isEffectivelyBusy()) return;
    setBusy(true);
    setError("");
    setNotice(null);
    try {
      settingsSuccessful(await deleteAcoustidApplicationKey(), 204);
      setAcoustIDApplicationKey("");
      await refreshState();
      setNotice(settingsNotice("acoustIdDeleted"));
    } catch (reason) {
      setError(message(reason));
    } finally {
      setBusy(false);
    }
  }

  function validConcurrency(value: string) {
    return (
      /^[1-9]\d*$/.test(value.trim()) &&
      Number.isSafeInteger(Number(value.trim()))
    );
  }

  function saveMusicBrainzMode(mode: UpdateMusicBrainzBodyMode) {
    const before = musicBrainz.snapshot();
    const beforeValue = before.value;
    const beforeBaseline = before.baseline;
    if (!beforeValue || !beforeBaseline) return;
    if (mode === beforeBaseline.mode) {
      musicBrainz.update({ ...beforeValue, mode });
      return;
    }
    musicBrainz.update({ ...beforeValue, mode });
    if (
      mode === "self-hosted" &&
      !validMusicBrainzBaseURL(beforeBaseline.baseURL)
    )
      return;
    const currentSettings = state?.settings;
    if (!currentSettings) return;
    const currentValue: MusicBrainzSavedSnapshot =
      musicBrainzSavedSnapshot(currentSettings);
    if (
      "musicBrainz" in conflictValues &&
      !Object.hasOwn(authorizedConflictExpected.current, "musicBrainz")
    ) {
      confirmConflictingSave("musicBrainz", currentValue, () => {
        saveMusicBrainzMode(mode);
      });
      return;
    }
    const savedDraft = musicBrainz.capture();
    const submittedBaseURL = mode === "public" ? "" : beforeBaseline.baseURL;
    const replacingConflict = Object.hasOwn(
      authorizedConflictExpected.current,
      "musicBrainz",
    );
    const replacement = expectedForSave("musicBrainz", currentValue);
    const expected: UpdateMusicBrainzExpectedBody = {
      mode: replacingConflict ? replacement.mode : beforeBaseline.mode,
      base_url: replacingConflict
        ? replacement.baseURL
        : beforeBaseline.baseURL,
    };
    void mutate(
      () =>
        updateMusicbrainzSettings({
          mode,
          base_url: submittedBaseURL,
          expected,
        }),
      204,
      settingsNotice("musicBrainzSaved"),
      musicBrainz.capture,
      (fresh) => {
        musicBrainz.acknowledgeField(
          "mode",
          mode,
          fresh.settings.musicbrainz_mode === "self-hosted"
            ? "self-hosted"
            : "public",
        );
        musicBrainz.acknowledgeField(
          "baseURL",
          beforeValue.baseURL,
          fresh.settings.musicbrainz_base_url,
          beforeValue.baseURL !== beforeBaseline.baseURL,
        );
      },
      musicBrainz.beginSave,
      musicBrainz.endSave,
      savedDraft,
    );
  }

  function saveMusicBrainzThrottle(enabled: boolean) {
    const before = musicBrainz.snapshot();
    if (!before.value || !before.baseline) return;
    if (
      enabled === before.baseline.selfHostedThrottle ||
      before.baseline.mode !== "self-hosted"
    ) {
      musicBrainz.update({ ...before.value, selfHostedThrottle: enabled });
      return;
    }
    musicBrainz.update({ ...before.value, selfHostedThrottle: enabled });
    const saved = state?.settings;
    if (!saved) return;
    if (
      "musicBrainz" in conflictValues &&
      !Object.hasOwn(authorizedConflictExpected.current, "musicBrainz")
    ) {
      confirmConflictingSave(
        "musicBrainz",
        musicBrainzSavedSnapshot(saved),
        () => saveMusicBrainzThrottle(enabled),
      );
      return;
    }
    const savedDraft = musicBrainz.capture();
    const replacingConflict = Object.hasOwn(
      authorizedConflictExpected.current,
      "musicBrainz",
    );
    const replacement = expectedForSave(
      "musicBrainz",
      musicBrainzSavedSnapshot(saved),
    );
    const expected: UpdateMusicBrainzExpectedBody = {
      self_hosted_throttle: replacingConflict
        ? replacement.throttle
        : before.baseline.selfHostedThrottle,
    };
    void mutate(
      () =>
        updateMusicbrainzSettings({ self_hosted_throttle: enabled, expected }),
      204,
      settingsNotice("musicBrainzThrottleSaved"),
      musicBrainz.capture,
      (fresh) =>
        musicBrainz.acknowledgeField(
          "selfHostedThrottle",
          enabled,
          fresh.settings.musicbrainz_self_hosted_throttle,
        ),
      musicBrainz.beginSave,
      musicBrainz.endSave,
      savedDraft,
    );
  }

  async function checkMusicBrainz(): Promise<SettingsSaveOutcome> {
    const savedDraft = musicBrainz.capture();
    const payload = savedDraft.value;
    const baseline = savedDraft.baseline;
    if (!payload || !baseline)
      return { status: "failed", group: "musicBrainz" };
    const delay = Number(payload.selfHostedDelaySeconds);
    const delayIsValid =
      payload.selfHostedDelaySeconds.trim() !== "" &&
      Number.isFinite(delay) &&
      delay >= 0 &&
      delay <= 60;
    const targetChanged =
      payload.mode !== baseline.mode ||
      (payload.mode === "self-hosted" && payload.baseURL !== baseline.baseURL);
    const delayChanged =
      payload.mode === "self-hosted" &&
      payload.selfHostedDelaySeconds !== baseline.selfHostedDelaySeconds;
    const throttleChanged =
      payload.mode === "self-hosted" &&
      payload.selfHostedThrottle !== baseline.selfHostedThrottle;
    if (
      payload.mode === "self-hosted" &&
      targetChanged &&
      !validMusicBrainzBaseURL(payload.baseURL)
    ) {
      setError(settingsNotice("invalidMusicBrainzUrl"));
      return {
        status: "invalid",
        group: "musicBrainz",
        validSavedTarget: false,
        confirmationRead: false,
        message: settingsNotice("invalidMusicBrainzUrl"),
      };
    }
    if (payload.mode === "self-hosted" && delayChanged && !delayIsValid) {
      setError(settingsNotice("invalidMusicBrainzDelay"));
      return {
        status: "invalid",
        group: "musicBrainz",
        validSavedTarget: false,
        confirmationRead: false,
        message: settingsNotice("invalidMusicBrainzDelay"),
      };
    }
    if (
      (targetChanged || delayChanged || throttleChanged) &&
      "musicBrainz" in conflictValues &&
      !Object.hasOwn(authorizedConflictExpected.current, "musicBrainz")
    ) {
      const saved = state?.settings;
      if (saved)
        confirmConflictingSave(
          "musicBrainz",
          musicBrainzSavedSnapshot(saved),
          () => void checkMusicBrainz(),
        );
      return {
        status: "confirmation_pending",
        group: "musicBrainz",
        validSavedTarget: false,
        confirmationRead: false,
      };
    }
    setError("");
    setNotice(null);
    const shouldSaveDraft = targetChanged || delayChanged || throttleChanged;
    let targetAccepted = false;
    let targetConfirmed = !shouldSaveDraft;
    if (shouldSaveDraft) {
      musicBrainz.beginSave(savedDraft.revision);
      setBusy(true);
    }
    try {
      if (shouldSaveDraft) {
        const expected: UpdateMusicBrainzExpectedBody = {};
        const replacingConflict = Object.hasOwn(
          authorizedConflictExpected.current,
          "musicBrainz",
        );
        const currentSettings = state?.settings;
        const fallbackSnapshot: MusicBrainzSavedSnapshot = currentSettings
          ? musicBrainzSavedSnapshot(currentSettings)
          : {
              mode: baseline.mode,
              baseURL: baseline.baseURL,
              delay: Number(baseline.selfHostedDelaySeconds),
              throttle: baseline.selfHostedThrottle,
            };
        const replacement = expectedForSave("musicBrainz", fallbackSnapshot);
        const body: {
          mode?: UpdateMusicBrainzBodyMode;
          base_url?: string;
          self_hosted_delay_seconds?: number;
          self_hosted_throttle?: boolean;
          expected: UpdateMusicBrainzExpectedBody;
        } = { expected };
        const submittedBaseURL =
          payload.mode === "public" ? "" : payload.baseURL;
        if (targetChanged) {
          body.mode = payload.mode;
          body.base_url = submittedBaseURL;
          expected.mode = replacingConflict ? replacement.mode : baseline.mode;
          expected.base_url = replacingConflict
            ? replacement.baseURL
            : baseline.baseURL;
        }
        if (delayChanged) {
          body.self_hosted_delay_seconds = delay;
          expected.self_hosted_delay_seconds = replacingConflict
            ? replacement.delay
            : Number(baseline.selfHostedDelaySeconds);
        }
        if (throttleChanged) {
          body.self_hosted_throttle = payload.selfHostedThrottle;
          expected.self_hosted_throttle = replacingConflict
            ? replacement.throttle
            : baseline.selfHostedThrottle;
        }
        const saved = await updateMusicbrainzSettings(body);
        await acceptSettingsWrite(saved, 204);
        targetAccepted = true;
        try {
          await refreshState((fresh) => {
            const freshMode =
              fresh.settings.musicbrainz_mode === "self-hosted"
                ? "self-hosted"
                : "public";
            if (
              (targetChanged &&
                (freshMode !== payload.mode ||
                  fresh.settings.musicbrainz_base_url !== submittedBaseURL)) ||
              (delayChanged &&
                fresh.settings.musicbrainz_self_hosted_delay_seconds !==
                  delay) ||
              (throttleChanged &&
                fresh.settings.musicbrainz_self_hosted_throttle !==
                  payload.selfHostedThrottle)
            )
              throw new SettingsLocalizedError(
                settingsNotice("serverDidNotConfirmMusicBrainz"),
              );
            if (targetChanged) {
              musicBrainz.acknowledgeField(
                "mode",
                payload.mode,
                fresh.settings.musicbrainz_mode === "self-hosted"
                  ? "self-hosted"
                  : "public",
              );
              musicBrainz.acknowledgeField(
                "baseURL",
                payload.baseURL,
                fresh.settings.musicbrainz_base_url,
                payload.mode === "public" &&
                  payload.baseURL !== baseline.baseURL,
              );
            }
            if (delayChanged)
              musicBrainz.acknowledgeField(
                "selfHostedDelaySeconds",
                payload.selfHostedDelaySeconds,
                String(fresh.settings.musicbrainz_self_hosted_delay_seconds),
              );
            if (throttleChanged)
              musicBrainz.acknowledgeField(
                "selfHostedThrottle",
                payload.selfHostedThrottle,
                fresh.settings.musicbrainz_self_hosted_throttle,
              );
          });
          targetConfirmed = true;
        } catch (reason) {
          throw new SettingsLocalizedError(
            settingsNotice("musicBrainzAcceptedRefreshFailed", {
              detail: message(reason),
            }),
          );
        }
      }
      const checked = settingsSuccessful(await checkSettingsMusicbrainz(), 200);
      try {
        await refreshState();
      } catch (reason) {
        throw new SettingsLocalizedError(
          settingsNotice("musicBrainzSavedRefreshFailed", {
            detail: message(reason),
          }),
        );
      }
      if (!checked.data.success) {
        const failure: SettingsFeedback =
          checked.data.error || settingsNotice("musicBrainzUnavailable");
        setError(failure);
        return {
          status: "invalid",
          group: "musicBrainz",
          validSavedTarget: false,
          confirmationRead: targetConfirmed,
          message: failure,
        };
      }
      const currentMusicDraft = musicBrainz.capture();
      if (
        currentMusicDraft.value &&
        currentMusicDraft.baseline &&
        !sameMusicBrainz(currentMusicDraft.value, currentMusicDraft.baseline)
      ) {
        const failure = settingsNotice("unsavedMusicBrainzMode");
        setError(failure);
        return {
          status: "invalid",
          group: "musicBrainz",
          validSavedTarget: true,
          confirmationRead: targetConfirmed,
          message: failure,
        };
      }
      setNotice(settingsNotice("musicBrainzChecked"));
      return {
        status: "saved",
        group: "musicBrainz",
        validSavedTarget: true,
        confirmationRead: targetConfirmed,
      };
    } catch (reason) {
      setError(message(reason));
      return {
        status:
          reason instanceof SettingsConflictError
            ? "conflict"
            : reason instanceof SettingsValidationError
              ? "invalid"
              : targetAccepted && !targetConfirmed
                ? "uncertain"
                : "failed",
        group: "musicBrainz",
        validSavedTarget: false,
        confirmationRead: targetConfirmed,
        message: message(reason),
      };
    } finally {
      if (shouldSaveDraft) {
        musicBrainz.endSave(savedDraft.revision);
        setBusy(false);
      }
    }
  }

  async function saveEligibleTextDrafts(
    attempt: HashAttempt,
  ): Promise<SettingsSaveOutcome> {
    if (isEffectivelyBusy())
      return {
        status: "failed",
        group: "all",
        message: settingsNotice("waitForRequest"),
      };
    if (outputDirectory.dirty) {
      dialogReturnFocusRef.current =
        settingsNavigationSaveControl(navigationDialogRef.current) ??
        heading.current;
      setError("");
      setOutputDialogError("");
      setOutputDialog(true);
      return { status: "confirmation_pending", group: "outputDirectory" };
    }
    if (
      moveDirectoryBaseline.current !== undefined &&
      moveDirectoryRef.current !== moveDirectoryBaseline.current
    ) {
      const opener =
        settingsNavigationSaveControl(navigationDialogRef.current) ??
        heading.current;
      return preflightMove(opener, attempt);
    }

    const textSaves: Array<[boolean, () => Promise<SettingsSaveOutcome>]> = [
      [sourceFileConcurrency.dirty, saveConcurrency],
      [matchingThreshold.dirty, saveMatchingThreshold],
      [publicationPathTemplate.dirty, savePublicationTemplate],
      [workDirectory.dirty, saveWorkDirectory],
    ];
    for (const [dirty, save] of textSaves) {
      if (!dirty) continue;
      const outcome = await save();
      if (outcome.status !== "saved") return outcome;
      const currentDraft =
        outcome.group === "concurrency"
          ? sourceFileConcurrency.capture()
          : outcome.group === "matchingThreshold"
            ? matchingThreshold.capture()
            : outcome.group === "publicationTemplate"
              ? publicationPathTemplate.capture()
              : workDirectory.capture();
      const draftStillDirty =
        currentDraft.value !== undefined &&
        currentDraft.baseline !== undefined &&
        currentDraft.value !== currentDraft.baseline;
      if (draftStillDirty)
        return {
          status: "failed",
          group: outcome.group,
          message: settingsNotice("newChangesWhileSaving"),
        };
    }

    if (musicBrainz.dirty) return checkMusicBrainz();
    return { status: "saved", group: "all" };
  }

  function resolveNavigation(
    attempt: HashAttempt,
    resolution: "stay" | "leave",
  ) {
    if (latestNavigationAttempt.current?.id !== attempt.id) return false;
    const resolved = resolveHashNavigation(attempt.id, resolution);
    if (
      !resolved &&
      (resolution !== "stay" ||
        latestNavigationAttempt.current?.id !== attempt.id ||
        window.location.hash !== attempt.from)
    )
      return false;
    navigationFocusRestorePending.current = resolution === "stay";
    latestNavigationAttempt.current = undefined;
    setNavigationAttempt(undefined);
    setNavigationError("");
    return true;
  }

  function keepDraftAndLeave(attempt: HashAttempt) {
    const storage = getSettingsDraftStorage();
    const persisted = writeSettingsDraft(
      storage,
      currentSettingsDraftSnapshot(),
    );
    if (!persisted) {
      setDraftStorageWarning(true);
      setNavigationError(settingsNotice("errorSavingDraft"));
      return;
    }
    resolveNavigation(attempt, "leave");
  }

  function discardDraftAndLeave(attempt: HashAttempt) {
    const storage = getSettingsDraftStorage();
    if (!writeSettingsDraft(storage, null)) {
      writeSettingsDraft(storage, currentSettingsDraftSnapshot());
      setDraftStorageWarning(true);
      setNavigationError(settingsNotice("errorDiscardingDraft"));
      return;
    }
    if (!resolveNavigation(attempt, "leave"))
      writeSettingsDraft(storage, currentSettingsDraftSnapshot());
  }

  async function saveDraftAndLeave(attempt: HashAttempt) {
    setNavigationError("");
    const outcome = await saveEligibleTextDrafts(attempt);
    if (latestNavigationAttempt.current?.id !== attempt.id) return;
    if (outcome.status === "saved" && !hasUnsavedSettingsTextDrafts()) {
      resolveNavigation(attempt, "leave");
      return;
    }
    const messages: Record<SettingsSaveOutcome["status"], SettingsNotice> = {
      saved: settingsNotice("saveLeaveNewChanges"),
      invalid: settingsNotice("saveLeaveInvalid"),
      confirmation_pending: settingsNotice("saveLeaveConfirmation"),
      conflict: settingsNotice("saveLeaveConflict"),
      uncertain: settingsNotice("saveLeaveUncertain"),
      failed: settingsNotice("saveLeaveFailed"),
    };
    setNavigationError(outcome.message || messages[outcome.status]);
  }

  function closeInstallDialog() {
    dialogFocusRestorePending.current = true;
    installSessionRevision.current += 1;
    installRequestRevision.current += 1;
    installPendingRequest.current = 0;
    installStartRevision.current += 1;
    setInstallDialogError("");
    setBusy(false);
    setInstallDialog(undefined);
  }
  function closeMoveDialog() {
    dialogFocusRestorePending.current = true;
    moveSessionRevision.current += 1;
    invalidateMovePlan();
    moveStartRevision.current += 1;
    setMoveDialogError("");
    setBusy(false);
  }
  const loadCatalogNow = () => {
    void requestCatalog();
  };
  async function preflightInstall(kind: Kind) {
    const release = selectedRelease[kind];
    if (!release) {
      setError(settingsNotice("installVersionRequired"));
      return;
    }
    const sessionRevision = ++installSessionRevision.current;
    const requestRevision = ++installRequestRevision.current;
    installPendingRequest.current = requestRevision;
    setInstallDialogError("");
    setError("");
    setBusy(true);
    try {
      const plan = await preflightManagedToolInstall(
        kind,
        release,
        settingsHttpError,
      );
      if (
        sessionRevision !== installSessionRevision.current ||
        requestRevision !== installRequestRevision.current
      )
        return;
      if (plan.conflicts?.length) setInstallDialog({ kind, release, plan });
      else await startInstall(plan, [], sessionRevision);
    } catch (reason) {
      if (
        sessionRevision === installSessionRevision.current &&
        requestRevision === installRequestRevision.current
      )
        setError(message(reason));
    } finally {
      if (installPendingRequest.current === requestRevision) {
        installPendingRequest.current = 0;
        setBusy(false);
      }
    }
  }
  async function startInstall(
    plan: InstallPreflightBody,
    confirmed: string[],
    sessionRevision?: number,
  ) {
    const startRevision = ++installStartRevision.current;
    const started = await startManagedToolInstall(
      plan,
      confirmed,
      settingsHttpError,
    );
    setOperations((previous) => [
      ...previous.filter((item) => item.id !== started.id),
      started,
    ]);
    if (
      sessionRevision !== undefined &&
      (sessionRevision !== installSessionRevision.current ||
        startRevision !== installStartRevision.current)
    )
      return;
    closeInstallDialog();
  }
  async function confirmInstall() {
    if (!installDialog) return;
    const sessionRevision = installSessionRevision.current;
    const startRevision = ++installStartRevision.current;
    setBusy(true);
    setInstallDialogError("");
    try {
      const started = await startManagedToolInstall(
        installDialog.plan,
        [...(installDialog.plan.conflicts || [])],
        settingsHttpError,
      );
      setOperations((previous) => [
        ...previous.filter((item) => item.id !== started.id),
        started,
      ]);
      if (
        sessionRevision !== installSessionRevision.current ||
        startRevision !== installStartRevision.current
      )
        return;
      closeInstallDialog();
    } catch (reason) {
      if (
        sessionRevision === installSessionRevision.current &&
        startRevision === installStartRevision.current
      )
        setInstallDialogError(message(reason));
    } finally {
      if (
        sessionRevision === installSessionRevision.current &&
        startRevision === installStartRevision.current
      )
        setBusy(false);
    }
  }
  async function activate(item: InstallationResponse, kind: Kind) {
    setBusy(true);
    setError("");
    setNotice(null);
    try {
      settingsSuccessful(
        await activateToolInstallation(item.id, { package_kind: kind }),
        204,
      );
      await Promise.all([refreshState(), refreshInstallations()]);
      setNotice(
        settingsNotice("activatedVersion", { kind, version: item.version }),
      );
    } catch (reason) {
      setError(message(reason));
    } finally {
      setBusy(false);
    }
  }
  async function remove(item: InstallationResponse, kind: Kind) {
    if (
      item.active ||
      operations.some(
        (operation) =>
          operation.target_installation_id === item.id &&
          activeOperation(operation),
      )
    ) {
      setError(settingsNotice("cannotRemoveActive"));
      return;
    }
    setBusy(true);
    setError("");
    setNotice(null);
    try {
      settingsSuccessful(
        await deleteToolInstallation(item.id, { package_kind: kind }),
        204,
      );
      await Promise.all([refreshState(), refreshInstallations()]);
      setNotice(
        settingsNotice("removedVersion", { kind, version: item.version }),
      );
    } catch (reason) {
      setError(message(reason));
    } finally {
      setBusy(false);
    }
  }
  async function preflightMove(
    opener: HTMLElement | null,
    attempt?: HashAttempt,
  ): Promise<SettingsSaveOutcome> {
    const group = "toolsDirectory";
    if (!platformReady || isEffectivelyBusy()) {
      return {
        status: "failed",
        group,
        message: settingsNotice("waitForRequest"),
      };
    }
    if (
      "toolsDirectory" in conflictValues &&
      !Object.hasOwn(authorizedConflictExpected.current, "toolsDirectory")
    ) {
      const current = state?.settings.tools_directory;
      if (current !== undefined)
        confirmConflictingSave("toolsDirectory", current, () => {
          void preflightMove(opener, attempt).then((outcome) => {
            if (attempt && latestNavigationAttempt.current?.id === attempt.id)
              setNavigationError(
                outcome.message || settingsNotice("saveLeaveConfirmation"),
              );
          });
        });
      return { status: "confirmation_pending", group };
    }
    const directory = moveDirectoryRef.current;
    const removeOldFiles = removeOldRef.current;
    const inputRevision = moveInputRevision.current;
    const sessionRevision = moveSessionRevision.current;
    const requestRevision = ++moveRequestRevision.current;
    const originHash = window.location.hash;
    const ownsReview = () =>
      mountedRef.current &&
      requestRevision === moveRequestRevision.current &&
      inputRevision === moveInputRevision.current &&
      sessionRevision === moveSessionRevision.current &&
      (attempt
        ? latestNavigationAttempt.current?.id === attempt.id
        : !latestNavigationAttempt.current &&
          window.location.hash === originHash);
    movePendingRequest.current = requestRevision;
    const sourceDirectory = state?.settings.tools_directory || "";
    setBusy(true);
    setError("");
    setMoveDialogError("");
    try {
      const preflight = await preflightToolsRootMove({
        expected_tools_directory: expectedForSave(
          "toolsDirectory",
          moveDirectoryBaseline.current ?? sourceDirectory,
        ),
        new_tools_directory: directory,
        remove_old_files: removeOldFiles,
      });
      if (!ownsReview()) return { status: "failed", group };
      const response = await acceptSettingsWrite(preflight, 200);
      if (!ownsReview()) return { status: "failed", group };
      dialogReturnFocusRef.current = opener;
      setMovePlan({
        directory,
        removeOld: removeOldFiles,
        sourceDirectory,
        inputRevision,
        sessionRevision,
        requestRevision,
        plan: {
          ...response.data,
          conflicts: [...(response.data.conflicts || [])],
        },
      });
      return { status: "confirmation_pending", group };
    } catch (reason) {
      if (!ownsReview()) return { status: "failed", group };
      const detail = message(reason);
      if (reason instanceof SettingsConflictError) invalidateMovePlan();
      if (!attempt) setMoveDialogError(detail);
      return {
        status: reason instanceof SettingsConflictError ? "conflict" : "failed",
        group,
        message: detail,
      };
    } finally {
      if (movePendingRequest.current === requestRevision) {
        movePendingRequest.current = 0;
        setBusy(false);
      }
    }
  }
  async function confirmMove() {
    if (!platformReady || isEffectivelyBusy()) return;
    if (
      !movePlan ||
      movePlan.sourceDirectory !== state?.settings.tools_directory ||
      movePlan.inputRevision !== moveInputRevision.current ||
      movePlan.sessionRevision !== moveSessionRevision.current ||
      movePlan.requestRevision !== moveRequestRevision.current ||
      movePlan.directory !== moveDirectoryRef.current ||
      movePlan.removeOld !== removeOldRef.current
    ) {
      invalidateMovePlan();
      return;
    }
    const confirmedPlan = movePlan;
    const sessionRevision = moveSessionRevision.current;
    const startRevision = ++moveStartRevision.current;
    setBusy(true);
    setMoveDialogError("");
    try {
      const response = settingsSuccessful(
        await startToolsRootMove({
          preflight_token: confirmedPlan.plan.preflight_token,
          confirmed_conflicts: [...(confirmedPlan.plan.conflicts || [])],
        }),
        200,
      );
      admittedMoves.current.set(response.data.id, {
        directory: confirmedPlan.directory,
        inputRevision: confirmedPlan.inputRevision,
      });
      setOperations((previous) => [
        ...previous.filter((item) => item.id !== response.data.id),
        response.data,
      ]);
      if (response.data.state === "succeeded") void onOperation(response.data);
      if (
        sessionRevision !== moveSessionRevision.current ||
        startRevision !== moveStartRevision.current
      )
        return;
      setMovePlan(undefined);
      closeMoveDialog();
    } catch (reason) {
      if (
        sessionRevision === moveSessionRevision.current &&
        startRevision === moveStartRevision.current
      ) {
        invalidateMovePlan();
        const detail = message(reason);
        if (latestNavigationAttempt.current) {
          setNavigationError(detail);
          dialogFocusRestorePending.current = true;
        } else {
          setMoveDialogError(detail);
        }
      }
    } finally {
      if (
        sessionRevision === moveSessionRevision.current &&
        startRevision === moveStartRevision.current
      )
        setBusy(false);
    }
  }
  const onOperation = useCallback(
    async (snapshot: OperationResponse) => {
      setOperations((previous) =>
        previous.map((item) => (item.id === snapshot.id ? snapshot : item)),
      );
      if (snapshot.state === "succeeded") {
        try {
          const move = admittedMoves.current.get(snapshot.id);
          await Promise.all([
            refreshState((fresh) => {
              const savedDirectory = fresh.settings.tools_directory;
              if (
                move &&
                move.inputRevision === moveInputRevision.current &&
                move.directory === moveDirectoryRef.current &&
                move.directory === savedDirectory
              ) {
                setMoveDirectoryBaseline(savedDirectory);
              }
            }),
            refreshInstallations(),
          ]);
          admittedMoves.current.delete(snapshot.id);
          await refreshOperations();
        } catch (reason) {
          setError(message(reason));
        }
      } else if (snapshot.state === "failed") {
        admittedMoves.current.delete(snapshot.id);
      }
    },
    [
      refreshInstallations,
      refreshOperations,
      refreshState,
      setMoveDirectoryBaseline,
    ],
  );

  const platformReady = Boolean(
    state?.platform.supported && !state.platform.diagnostic,
  );
  const concurrencyDraft = sourceFileConcurrency.snapshot();
  const thresholdDraft = matchingThreshold.snapshot();
  const templateDraft = publicationPathTemplate.snapshot();
  const workDraft = workDirectory.snapshot();
  const outputDraft = outputDirectory.snapshot();
  const musicDraft = musicBrainz.snapshot();
  const conflictValues = useMemo(() => {
    const values: Partial<Record<ConflictGroup, unknown>> = {};
    if (
      state &&
      concurrencyDraft.value !== undefined &&
      concurrencyDraft.baseline !== undefined &&
      concurrencyDraft.value !== concurrencyDraft.baseline &&
      String(state.settings.source_file_concurrency) !==
        concurrencyDraft.baseline
    )
      values.concurrency = state.settings.source_file_concurrency;
    if (
      state &&
      thresholdDraft.value !== undefined &&
      thresholdDraft.baseline !== undefined &&
      thresholdDraft.value !== thresholdDraft.baseline &&
      formatMatchingThreshold(state.settings.matching_pair_threshold) !==
        thresholdDraft.baseline
    )
      values.matchingThreshold = state.settings.matching_pair_threshold;
    if (
      state &&
      templateDraft.value !== undefined &&
      templateDraft.baseline !== undefined &&
      templateDraft.value !== templateDraft.baseline &&
      state.settings.publication_path_template !== templateDraft.baseline
    )
      values.publicationTemplate = state.settings.publication_path_template;
    if (
      state &&
      workDraft.value !== undefined &&
      workDraft.baseline !== undefined &&
      workDraft.value !== workDraft.baseline &&
      state.settings.work_directory !== workDraft.baseline
    )
      values.workDirectory = state.settings.work_directory;
    if (
      state &&
      outputDraft.value !== undefined &&
      outputDraft.baseline !== undefined &&
      outputDraft.value !== outputDraft.baseline &&
      state.settings.output_directory !== outputDraft.baseline
    )
      values.outputDirectory = state.settings.output_directory;
    if (
      state &&
      musicDraft.value &&
      musicDraft.baseline &&
      (musicDraft.value.mode !== musicDraft.baseline.mode ||
        musicDraft.value.baseURL !== musicDraft.baseline.baseURL ||
        musicDraft.value.selfHostedDelaySeconds !==
          musicDraft.baseline.selfHostedDelaySeconds ||
        musicDraft.value.selfHostedThrottle !==
          musicDraft.baseline.selfHostedThrottle) &&
      ((state.settings.musicbrainz_mode === "self-hosted"
        ? "self-hosted"
        : "public") !== musicDraft.baseline.mode ||
        state.settings.musicbrainz_base_url !== musicDraft.baseline.baseURL ||
        String(state.settings.musicbrainz_self_hosted_delay_seconds) !==
          musicDraft.baseline.selfHostedDelaySeconds ||
        state.settings.musicbrainz_self_hosted_throttle !==
          musicDraft.baseline.selfHostedThrottle)
    ) {
      values.musicBrainz = {
        mode: state.settings.musicbrainz_mode,
        baseURL: state.settings.musicbrainz_base_url,
        delay: state.settings.musicbrainz_self_hosted_delay_seconds,
        throttle: state.settings.musicbrainz_self_hosted_throttle,
      };
    }
    const toolsCurrent = state?.settings.tools_directory;
    if (
      state &&
      moveDirectory !== moveDirectoryBaselineValue &&
      moveDirectoryBaselineValue !== undefined &&
      toolsCurrent !== moveDirectoryBaselineValue
    )
      values.toolsDirectory = toolsCurrent;
    return values;
  }, [
    state,
    concurrencyDraft.value,
    concurrencyDraft.baseline,
    thresholdDraft.value,
    thresholdDraft.baseline,
    templateDraft.value,
    templateDraft.baseline,
    workDraft.value,
    workDraft.baseline,
    outputDraft.value,
    outputDraft.baseline,
    musicDraft.value,
    musicDraft.baseline,
    moveDirectory,
    moveDirectoryBaselineValue,
  ]);
  useEffect(() => {
    setActiveConflicts((previous) => {
      const next = { ...previous };
      let changed = false;
      for (const group of Object.keys(previous) as ConflictGroup[])
        if (!Object.hasOwn(conflictValues, group)) {
          delete next[group];
          changed = true;
        }
      for (const group of Object.keys(conflictValues) as ConflictGroup[])
        if (!Object.hasOwn(previous, group)) {
          next[group] = true;
          changed = true;
        }
      return changed ? next : previous;
    });
  }, [conflictValues]);
  function takeSavedConflict(group: ConflictGroup) {
    const current = state?.settings;
    if (!current) return;
    switch (group) {
      case "concurrency": {
        const value = String(current.source_file_concurrency);
        sourceFileConcurrency.update(value);
        sourceFileConcurrency.acknowledge(
          value,
          sourceFileConcurrency.capture().revision,
        );
        break;
      }
      case "matchingThreshold": {
        const value = formatMatchingThreshold(current.matching_pair_threshold);
        matchingThreshold.update(value);
        matchingThreshold.acknowledge(
          value,
          matchingThreshold.capture().revision,
        );
        break;
      }
      case "publicationTemplate":
        publicationPathTemplate.update(current.publication_path_template);
        publicationPathTemplate.acknowledge(
          current.publication_path_template,
          publicationPathTemplate.capture().revision,
        );
        break;
      case "workDirectory":
        workDirectory.update(current.work_directory);
        workDirectory.acknowledge(
          current.work_directory,
          workDirectory.capture().revision,
        );
        break;
      case "outputDirectory":
        outputDirectory.update(current.output_directory);
        outputDirectory.acknowledge(
          current.output_directory,
          outputDirectory.capture().revision,
        );
        break;
      case "musicBrainz": {
        const value = musicBrainz.snapshot().value;
        if (value)
          musicBrainz.acknowledge(
            {
              ...value,
              mode:
                current.musicbrainz_mode === "self-hosted"
                  ? "self-hosted"
                  : "public",
              baseURL: current.musicbrainz_base_url,
              selfHostedThrottle: current.musicbrainz_self_hosted_throttle,
              selfHostedDelaySeconds: String(
                current.musicbrainz_self_hosted_delay_seconds,
              ),
            },
            musicBrainz.capture().revision,
          );
        break;
      }
      case "toolsDirectory":
        setMoveDirectoryBaseline(current.tools_directory);
        setMoveDirectoryValue(current.tools_directory);
        break;
    }
    setActiveConflicts((previous) => ({ ...previous, [group]: false }));
  }
  const view: SettingsScreenViewProps = {
    locale,
    headingRef: heading,
    notices: [
      ...(effectiveBusy
        ? [{ key: "request-in-progress", text: t("requestInProgress") }]
        : []),
      ...(draftStorageWarning
        ? [{ key: "draft-storage", text: t("draftMemoryWarning") }]
        : []),
      ...(!initialLoadComplete && !loadError
        ? [{ key: "loading", text: t("loadingSettings") }]
        : []),
    ],
    loadError: loadError
      ? {
          message: `${t("loadSettingsError")} ${feedbackText(loadError, locale)}`,
          retryLabel: t("retryLoading"),
          retryDisabled: effectiveBusy,
          alertRef: loadErrorAlert,
          onRetry: () => void loadScreen(),
        }
      : undefined,
    conflicts: Object.entries(conflictValues).flatMap(
      ([rawGroup, currentValue]): SettingsConflictNoticeView[] => {
        const group = rawGroup as ConflictGroup;
        if (!activeConflicts[group]) return [];
        const label = conflictLabel(group, locale);
        return [
          {
            key: group,
            label: t("settingsConflict", { label }),
            announcement: "status",
            summary: `${t("changedSavedValue", { label })} ${displaySettingValue(currentValue)}.`,
            disabled: effectiveBusy,
            takeSaved: {
              label: t("takeSavedValue", { label }),
              onPress: () => takeSavedConflict(group),
            },
            continueEditing: {
              label: t("continueEditing", { label }),
              onPress: () => continueEditingConflict(group),
            },
          },
        ];
      },
    ),
    ready:
      initialLoadComplete && state
        ? {
            health: {
              title: t("configurationHealth"),
              healthy: state.configuration_health.healthy,
              healthyText: t("healthyConfiguration"),
              unhealthyText: t("unhealthyConfiguration"),
              problems: state.configuration_health.problems ?? [],
              unavailableText: platformReady
                ? undefined
                : t("platformUnavailable"),
            },
            tabs: {
              selectedKey: activeSettingsTab,
              onSelectionChange: (key) => selectSettingsTab(String(key)),
              general: {
                locale,
                format: {
                  value: publicationFormat.value ?? "mka",
                  choices: ["source", "mka", "m4a"],
                  onChange: savePublicationFormat,
                },
                concurrency: {
                  value:
                    sourceFileConcurrency.value ??
                    String(state.settings.source_file_concurrency),
                  invalid:
                    sourceFileConcurrency.value !== undefined &&
                    !validConcurrency(sourceFileConcurrency.value),
                  dirty: sourceFileConcurrency.dirty,
                  saveDisabled:
                    effectiveBusy ||
                    (sourceFileConcurrency.value !== undefined &&
                      !validConcurrency(sourceFileConcurrency.value)) ||
                    !sourceFileConcurrency.value,
                  onChange: sourceFileConcurrency.update,
                  onSave: saveConcurrency,
                },
                matchingThreshold: {
                  value:
                    matchingThreshold.value ??
                    formatMatchingThreshold(
                      state.settings.matching_pair_threshold,
                    ),
                  invalid:
                    matchingThreshold.value !== undefined &&
                    matchingThresholdNumber(matchingThreshold.value) ===
                      undefined,
                  dirty: matchingThreshold.dirty,
                  saveDisabled:
                    effectiveBusy ||
                    (matchingThreshold.value !== undefined &&
                      matchingThresholdNumber(matchingThreshold.value) ===
                        undefined) ||
                    !matchingThreshold.value,
                  onChange: matchingThreshold.update,
                  onSave: saveMatchingThreshold,
                },
                sha256: {
                  enabled: sha256.value ?? state.settings.sha256_enabled,
                  onChange: saveSha256,
                },
                logLevel: {
                  value: logLevel.value ?? "info",
                  onChange: (value) =>
                    saveLogLevel(value as UpdateLogLevelBodyLevel),
                },
                cleanup: {
                  enabled:
                    publicationRemoveSources.value ??
                    state.settings.publication_remove_sources,
                  dirty: publicationRemoveSources.dirty,
                  onChange: savePublicationRemoveSources,
                },
                albumCompletion: {
                  enabled:
                    publicationAutoCompleteAlbums.value ??
                    state.settings.publication_auto_complete_albums,
                  dirty: publicationAutoCompleteAlbums.dirty,
                  onChange: savePublicationAutoCompleteAlbums,
                },
                failures: generalFailurePresentations(),
              },
              folders: {
                locale,
                browse: browseServerDirectory,
                busy: effectiveBusy,
                output: {
                  value: outputDirectory.value ?? "",
                  dirty: outputDirectory.dirty,
                  matchesSaved:
                    outputDirectory.value === state.settings.output_directory,
                  onChange: outputDirectory.update,
                  onReset: (trigger) => {
                    dialogReturnFocusRef.current = trigger as HTMLElement;
                    setError("");
                    setOutputDialogError("");
                    setOutputDialog(true);
                  },
                },
                template: {
                  value:
                    publicationPathTemplate.value ??
                    state.settings.publication_path_template,
                  dirty: publicationPathTemplate.dirty,
                  onChange: publicationPathTemplate.update,
                  onSave: savePublicationTemplate,
                },
                work: {
                  value: workDirectory.value ?? "",
                  dirty: workDirectory.dirty,
                  onChange: workDirectory.update,
                  onSave: saveWorkDirectory,
                },
              },
              tools: {
                root: {
                  locale,
                  savedDirectory: state.settings.tools_directory,
                  operationsHref: operations.length
                    ? "#/operations"
                    : undefined,
                },
                token: {
                  Component: GitHubTokenFormProvider,
                  props: {
                    hasToken: state.settings.has_github_token,
                    onChanged: async () => {
                      await refreshState();
                    },
                  },
                },
                catalog: {
                  locale,
                  refresh: {
                    checkedAt,
                    busy: catalogBusy,
                    onRefresh: loadCatalogNow,
                    error: catalogError
                      ? feedbackText(catalogError, locale)
                      : undefined,
                    errorRef: catalogAlert,
                  },
                },
                packages: {
                  locale,
                  packages: (["ffmpeg", "fpcalc"] as const).map(
                    (kind): ManagedPackageCardModel => {
                      const installed = installations.filter(
                        (item) => item.package_kind === kind,
                      );
                      const occupied = (item: InstallationResponse) =>
                        operations.some(
                          (op) =>
                            op.target_installation_id === item.id &&
                            activeOperation(op),
                        );
                      const body = catalog[kind];
                      const releases = body?.releases || [];
                      const catalogState: ManagedPackageCatalog =
                        body === undefined
                          ? catalogBusy
                            ? { state: "loading" }
                            : { state: "notLoaded" }
                          : releases.length === 0
                            ? { state: "empty", notice: body.notice }
                            : {
                                state: "loaded",
                                releases: releases.map((release) => ({
                                  identity: release.identity,
                                  source: release.source,
                                })),
                                notice: body.notice,
                              };
                      return {
                        kind,
                        installations: installed.map((item) => {
                          const lifeState: ManagedPackageLifeState =
                            item.state === "ready" ||
                            item.state === "preparing" ||
                            item.state === "failed"
                              ? item.state
                              : "unknown";
                          return {
                            id: item.id,
                            release: {
                              identity: item.version,
                              source: item.source_name,
                            },
                            active: item.active,
                            lifeState,
                            occupied: occupied(item),
                            activate: {
                              disabled:
                                !platformReady ||
                                effectiveBusy ||
                                item.active ||
                                occupied(item) ||
                                item.state !== "ready",
                              onPress: () => void activate(item, kind),
                            },
                            remove: {
                              disabled:
                                !platformReady ||
                                effectiveBusy ||
                                item.active ||
                                occupied(item),
                              onPress: () => void remove(item, kind),
                            },
                          };
                        }),
                        catalog: catalogState,
                        selectedRelease: selectedRelease[kind],
                        onReleaseChange: (identity) =>
                          setSelectedRelease((previous) => ({
                            ...previous,
                            [kind]: identity,
                          })),
                        downloadAndVerify: {
                          disabled:
                            !platformReady ||
                            effectiveBusy ||
                            !selectedRelease[kind],
                          onPress: (trigger) => {
                            dialogReturnFocusRef.current =
                              trigger as HTMLElement;
                            setError("");
                            void preflightInstall(kind);
                          },
                        },
                      };
                    },
                  ),
                },
                move: {
                  locale,
                  destination: {
                    value: moveDirectory,
                    onChange: (directory) => {
                      setMoveDialogError("");
                      setMoveDirectoryValue(directory);
                    },
                    browse: browseServerDirectory,
                  },
                  removeOld: {
                    checked: removeOld,
                    onChange: setRemoveOldValue,
                  },
                  error: moveDialogError
                    ? feedbackText(moveDialogError, locale)
                    : undefined,
                  errorRef: moveDialogAlert,
                  preflight: {
                    disabled: !platformReady || effectiveBusy,
                    onPress: (opener) => {
                      void preflightMove(
                        opener instanceof HTMLElement ? opener : null,
                      );
                    },
                  },
                },
              },
              providers: {
                locale,
                snapshot: {
                  busy: effectiveBusy,
                  acoustID: {
                    applicationKey: acoustIDApplicationKey,
                    hasKey: state.settings.has_acoustid_application_key,
                  },
                  musicBrainz: {
                    mode: musicBrainz.value?.mode ?? "public",
                    baseURL: musicBrainz.value?.baseURL ?? "",
                    throttle: musicBrainz.value?.selfHostedThrottle ?? false,
                    delaySeconds:
                      musicBrainz.value?.selfHostedDelaySeconds ?? "0.5",
                    verifiedAt: state.settings.musicbrainz_verified_at ?? null,
                    dirty: musicBrainz.dirty,
                  },
                  lrclib: {
                    enabled: lrclib.value ?? state.settings.lrclib_enabled,
                    dirty: lrclib.dirty,
                  },
                },
                actions: {
                  onAcoustIDApplicationKeyChange: setAcoustIDApplicationKey,
                  onSaveAcoustIDApplicationKey: () =>
                    void saveAcoustIDApplicationKey(),
                  onDeleteAcoustIDApplicationKey: () =>
                    void deleteAcoustIDApplicationKey(),
                  onMusicBrainzModeChange: saveMusicBrainzMode,
                  onMusicBrainzBaseURLChange: (baseURL) => {
                    const currentValue = musicBrainz.value;
                    if (currentValue)
                      musicBrainz.update({ ...currentValue, baseURL });
                  },
                  onMusicBrainzThrottleChange: saveMusicBrainzThrottle,
                  onMusicBrainzDelaySecondsChange: (selfHostedDelaySeconds) => {
                    const currentValue = musicBrainz.value;
                    if (currentValue)
                      musicBrainz.update({
                        ...currentValue,
                        selfHostedDelaySeconds,
                      });
                  },
                  onSaveAndCheckMusicBrainz: () => void checkMusicBrainz(),
                  onLrclibEnabledChange: saveLrclib,
                },
              },
              genres: {
                Component: GenreCatalogPanelProvider,
                props: {},
              },
            },
          }
        : undefined,
    feedback: error
      ? { message: feedbackText(error, locale), alertRef: alert }
      : undefined,
    notice: notice ? t(notice.key, notice.values) : undefined,
    refresh: state
      ? {
          label: t("refreshState"),
          disabled: effectiveBusy,
          onPress: () => void loadScreen(),
        }
      : undefined,
    dialogs: {
      leave: navigationAttempt
        ? {
            locale,
            dialogRef: navigationDialogRef,
            error: navigationError
              ? feedbackText(navigationError, locale)
              : undefined,
            actions: {
              stay: {
                onPress: () => resolveNavigation(navigationAttempt, "stay"),
              },
              keepDraft: {
                onPress: () => keepDraftAndLeave(navigationAttempt),
              },
              discardDraft: {
                onPress: () => discardDraftAndLeave(navigationAttempt),
              },
              save: {
                onPress: () => void saveDraftAndLeave(navigationAttempt),
              },
            },
          }
        : undefined,
      conflict: conflictConfirmation
        ? {
            title: t("replaceSavedTitle"),
            message: t("replaceSavedDescription", {
              label: conflictLabel(conflictConfirmation.group, locale),
              value: displaySettingValue(conflictConfirmation.currentValue),
            }),
            onDismiss: () => setConflictConfirmation(undefined),
            actions: {
              confirm: {
                label: t("replaceSaved"),
                onPress: () => {
                  authorizedConflictExpected.current[
                    conflictConfirmation.group
                  ] = conflictConfirmation.currentValue;
                  const confirm = conflictConfirmation.onConfirm;
                  setConflictConfirmation(undefined);
                  confirm();
                },
              },
              cancel: {
                label: t("cancel"),
                onPress: () => setConflictConfirmation(undefined),
              },
            },
          }
        : undefined,
      outputReset: outputDialog
        ? {
            locale,
            busy: effectiveBusy,
            feedback: !outputDialogError
              ? undefined
              : typeof outputDialogError !== "string" &&
                  outputDialogError.key === "waitForRunningOperations"
                ? { kind: "running" }
                : {
                    kind: "error",
                    detail: feedbackText(outputDialogError, locale),
                  },
            onConfirm: () => void confirmOutputDirectory(),
            onDismiss: closeOutputDialog,
          }
        : undefined,
      install: installDialog
        ? {
            locale,
            release: installDialog.release,
            targets: installDialog.plan.targets || [],
            conflicts: installDialog.plan.conflicts || [],
            busy: effectiveBusy,
            ...(installDialogError
              ? { error: feedbackText(installDialogError, locale) }
              : {}),
            onConfirm: () => void confirmInstall(),
            onDismiss: closeInstallDialog,
          }
        : undefined,
      move: movePlan
        ? {
            locale,
            sourceDirectory: movePlan.sourceDirectory,
            destinationDirectory: movePlan.directory,
            removeOld: movePlan.removeOld,
            managedCount: movePlan.plan.managed_file_count,
            conflicts: movePlan.plan.conflicts || [],
            busy: effectiveBusy,
            confirmDisabled: !platformReady,
            onConfirm: () => void confirmMove(),
            onDismiss: closeMoveDialog,
          }
        : undefined,
    },
  };
  return { model: view, operations, onOperation };
}

// Follows only tool operations for automatic settings refresh; progress,
// retry, and dismissal stay on the dedicated Operations screen.
function ToolOperationFollower({
  operation,
  onSnapshot,
}: {
  operation: OperationResponse;
  onSnapshot: (snapshot: OperationResponse) => void | Promise<void>;
}) {
  useOperationFollower(operation.id, {
    enabled: operation.state === "queued" || operation.state === "running",
    initialSnapshot: operation,
    onSnapshot: (snapshot) => {
      void onSnapshot(snapshot);
    },
    refreshOnStreamError: true,
  });
  return null;
}

import {
  requestSettingsFocus,
  resolveSettingsFocusTarget,
  settingsDialogIsOpen,
  settingsFocusedControl,
  settingsNavigationSaveControl,
  settingsTargetTab,
} from "../../ui/features/settings/settingsFocus";
