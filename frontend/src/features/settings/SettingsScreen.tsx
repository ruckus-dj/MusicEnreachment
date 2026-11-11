import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import {
  apiResponseError,
  errorMessage as sharedErrorMessage,
} from "../../api/client/errors";
import { successful } from "../../api/client/response";
import { useOperationFollower } from "../../api/client/useOperationFollower";
import {
  activateToolInstallation,
  checkSettingsMusicbrainz,
  deleteAcoustidApplicationKey,
  deleteToolInstallation,
  dismissOperation,
  getSettings,
  listInstallations,
  listOperations,
  preflightToolsRootMove,
  retryOperation,
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
import { AppButton } from "../../components/AppButton";
import { DirectoryPathField } from "../../components/DirectoryPathField";
import { useI18n } from "../../i18n/I18nProvider";
import {
  type HashAttempt,
  registerHashNavigationGuard,
  resolveHashNavigation,
} from "../../navigation/hashLocation";
import { GitHubTokenForm } from "../tools/GitHubTokenForm";
import {
  loadManagedToolCatalogs,
  preflightManagedToolInstall,
  startManagedToolInstall,
} from "../tools/toolWorkflow";
import { GenreCatalogPanel } from "./GenreCatalogPanel";
import { SettingsGeneralSection } from "./SettingsGeneralSection";
import {
  getSettingsDraftStorage,
  readSettingsDraft,
  type SettingsDraftSnapshot,
  type SettingsDraftValue,
  settingsDraftIsDirty,
  writeSettingsDraft,
} from "./settingsDraft";
import "../setup/setup.css";
import "./settings.css";

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
  | "publicationTemplate"
  | "musicBrainz"
  | "workDirectory"
  | "toolsDirectory"
  | "outputDirectory";
type ConflictConfirmation = {
  group: ConflictGroup;
  label: string;
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
    | "publicationTemplate"
    | "workDirectory"
    | "musicBrainz"
    | "outputDirectory"
    | "toolsDirectory"
    | "all";
  validSavedTarget?: boolean;
  confirmationRead?: boolean;
  message?: string;
};
class SettingsConflictError extends Error {}
class SettingsValidationError extends Error {}

function conflictLabel(group: ConflictGroup) {
  switch (group) {
    case "concurrency":
      return "параллельность анализа";
    case "publicationTemplate":
      return "шаблон пути публикации";
    case "musicBrainz":
      return "настройки MusicBrainz";
    case "workDirectory":
      return "рабочая папка";
    case "toolsDirectory":
      return "каталог инструментов";
    case "outputDirectory":
      return "каталог публикации";
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

function message(reason: unknown): string {
  return sharedErrorMessage(reason, "Не удалось выполнить запрос.");
}
function activeOperation(operation: OperationResponse) {
  return (
    operation.state === "queued" ||
    operation.state === "running" ||
    operation.state === "failed"
  );
}

export function SettingsScreen() {
  const { locale } = useI18n();
  const [restoredSettingsDraft] = useState(() =>
    readSettingsDraft(getSettingsDraftStorage()),
  );
  const [state, setState] = useState<SetupStateBody>();
  const [conflictConfirmation, setConflictConfirmation] =
    useState<ConflictConfirmation>();
  const [navigationAttempt, setNavigationAttempt] = useState<HashAttempt>();
  const [navigationError, setNavigationError] = useState("");
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
  const [catalogError, setCatalogError] = useState("");
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
  const sourceFileConcurrency = useSectionDraft<string>(
    Object.is,
    restoredSettingsDraft?.sourceFileConcurrency,
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
  const [moveDialog, setMoveDialog] = useState(false);
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
  const [error, setError] = useState("");
  const [loadError, setLoadError] = useState("");
  const [installDialogError, setInstallDialogError] = useState("");
  const [moveDialogError, setMoveDialogError] = useState("");
  const [outputDialogError, setOutputDialogError] = useState("");
  const [notice, setNotice] = useState("");
  const heading = useRef<HTMLHeadingElement>(null);
  const latestNavigationAttempt = useRef<HashAttempt | undefined>(undefined);
  const capturedNavigationAnchor = useRef<HTMLAnchorElement | null>(null);
  const navigationReturnFocus = useRef<HTMLElement | null>(null);
  const navigationEditedControl = useRef<HTMLElement | null>(null);
  const navigationFocusRestorePending = useRef(false);
  const alert = useRef<HTMLParagraphElement>(null);
  const loadErrorAlert = useRef<HTMLParagraphElement>(null);
  const installDialogAlert = useRef<HTMLParagraphElement>(null);
  const moveDialogAlert = useRef<HTMLParagraphElement>(null);
  const outputDialogAlert = useRef<HTMLParagraphElement>(null);
  const outputDialogRef = useRef<HTMLDialogElement>(null);
  const conflictDialogRef = useRef<HTMLDialogElement>(null);
  const navigationDialogRef = useRef<HTMLDialogElement>(null);
  const catalogAlert = useRef<HTMLParagraphElement>(null);
  const automaticCatalogRequested = useRef(false);
  const catalogRequestInFlight = useRef(false);
  const screenLoadInFlight = useRef(false);
  const mountedRef = useRef(true);
  const mountGeneration = useRef(0);
  const screenLoadGeneration = useRef(0);
  const installDialogRef = useRef<HTMLDialogElement>(null);
  const moveDialogRef = useRef<HTMLDialogElement>(null);
  const dialogReturnFocusRef = useRef<HTMLElement | null>(null);
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
  const installSessionRevision = useRef(0);
  const installRequestRevision = useRef(0);
  const installPendingRequest = useRef(0);
  const installStartRevision = useRef(0);
  const moveStartRevision = useRef(0);
  const effectiveBusy = busy || generalPendingCount > 0;
  const isEffectivelyBusy = () => busy || generalPending.current > 0;

  const restoreDialogFocus = useCallback(() => {
    const trigger = dialogReturnFocusRef.current;
    dialogReturnFocusRef.current = null;
    trigger?.focus();
  }, []);

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
      sourceFileConcurrency.sync(String(settings.source_file_concurrency));
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
      sourceFileConcurrency.sync,
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
      return {
        ...(outputDraft ? { outputDirectory: outputDraft } : {}),
        ...(workDraft ? { workDirectory: workDraft } : {}),
        ...(templateDraft ? { publicationPathTemplate: templateDraft } : {}),
        ...(concurrencyDraft
          ? { sourceFileConcurrency: concurrencyDraft }
          : {}),
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
    ]);

  const hasUnsavedSettingsTextDrafts = useCallback(
    () => settingsDraftIsDirty(currentSettingsDraftSnapshot()),
    [currentSettingsDraftSnapshot],
  );

  const refreshState = useCallback(
    async (
      ack?: (fresh: SetupStateBody) => void,
      isCurrent: () => boolean = () => mountedRef.current,
    ) => {
      const generation = mountGeneration.current;
      const response = successful(await readSettings(), 200);
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
    label: string,
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
    setConflictConfirmation({ group, label, currentValue, onConfirm });
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
          `Настройки изменились на сервере. Черновик сохранён, но текущее состояние не удалось обновить: ${message(reason)}`,
        );
      }
      throw new SettingsConflictError(
        "Настройки изменились на сервере. Текущий снимок обновлён; черновик сохранён для разрешения конфликта.",
      );
    }
    if (response.status === 400 || response.status === 422)
      throw new SettingsValidationError(apiResponseError(response).message);
    return successful(response, expectedStatus);
  }

  const refreshInstallations = useCallback(
    async (isCurrent: () => boolean = () => mountedRef.current) => {
      const generation = mountGeneration.current;
      const [ffmpeg, fpcalc] = await Promise.all([
        listInstallations({ package_kind: "ffmpeg" }),
        listInstallations({ package_kind: "fpcalc" }),
      ]);
      successful(ffmpeg, 200);
      successful(fpcalc, 200);
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
      const response = successful(await listOperations(), 200);
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
    heading.current?.focus();
    void loadScreen();
  }, [loadScreen]);
  useEffect(() => {
    if (error && !installDialog && !moveDialog) alert.current?.focus();
  }, [error, installDialog, moveDialog]);
  useLayoutEffect(() => {
    if (installDialogError && installDialogRef.current?.open)
      installDialogAlert.current?.focus();
  }, [installDialogError]);
  useLayoutEffect(() => {
    if (moveDialogError && moveDialogRef.current?.open)
      moveDialogAlert.current?.focus();
  }, [moveDialogError]);
  useLayoutEffect(() => {
    if (outputDialogError && outputDialogRef.current?.open)
      outputDialogAlert.current?.focus();
  }, [outputDialogError]);
  useLayoutEffect(() => {
    if (
      dialogFocusRestorePending.current &&
      !installDialog &&
      !moveDialog &&
      !outputDialog &&
      !effectiveBusy
    ) {
      dialogFocusRestorePending.current = false;
      restoreDialogFocus();
    }
  }, [
    effectiveBusy,
    installDialog,
    moveDialog,
    outputDialog,
    restoreDialogFocus,
  ]);
  useLayoutEffect(() => {
    if (installDialog) showSettingsDialog(installDialogRef.current);
  }, [installDialog]);
  useLayoutEffect(() => {
    if (moveDialog) showSettingsDialog(moveDialogRef.current);
  }, [moveDialog]);
  useLayoutEffect(() => {
    if (outputDialog) showSettingsDialog(outputDialogRef.current);
  }, [outputDialog]);
  useLayoutEffect(() => {
    if (conflictConfirmation) showSettingsDialog(conflictDialogRef.current);
  }, [conflictConfirmation]);
  useLayoutEffect(() => {
    if (navigationAttempt) showSettingsDialog(navigationDialogRef.current);
  }, [navigationAttempt]);
  useLayoutEffect(() => {
    if (navigationAttempt || !navigationFocusRestorePending.current) return;
    navigationFocusRestorePending.current = false;
    if (document.querySelector("dialog[open]")) return;
    const target = [
      navigationReturnFocus.current,
      navigationEditedControl.current,
    ].find((candidate) => candidate?.isConnected);
    navigationReturnFocus.current = null;
    navigationEditedControl.current = null;
    target?.focus();
  }, [navigationAttempt]);
  useEffect(() => {
    if (catalogError) catalogAlert.current?.focus();
  }, [catalogError]);
  useEffect(() => {
    if (loadError) loadErrorAlert.current?.focus();
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
      const catalogs = await loadManagedToolCatalogs();
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
    const musicValue = musicBrainz.value;
    const musicBaseline = musicBrainz.baseline;
    return {
      ...(output ? { outputDirectory: output } : {}),
      ...(work ? { workDirectory: work } : {}),
      ...(template ? { publicationPathTemplate: template } : {}),
      ...(concurrency ? { sourceFileConcurrency: concurrency } : {}),
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
      shouldBlock: hasUnsavedSettingsTextDrafts,
      onBlocked: (attempt) => {
        const anchor = capturedNavigationAnchor.current;
        capturedNavigationAnchor.current = null;
        navigationReturnFocus.current =
          anchor?.isConnected &&
          new URL(anchor.href, window.location.href).hash === attempt.to
            ? anchor
            : null;
        const activeControl =
          document.activeElement instanceof HTMLElement &&
          document.activeElement !== document.body &&
          !navigationDialogRef.current?.contains(document.activeElement)
            ? document.activeElement
            : null;
        const templateDraft = publicationPathTemplate.snapshot();
        const templateDirty =
          templateDraft.value !== undefined &&
          templateDraft.baseline !== undefined &&
          templateDraft.value !== templateDraft.baseline;
        navigationEditedControl.current =
          activeControl ??
          (templateDirty
            ? document.querySelector<HTMLElement>(
                '[aria-describedby="publication-template-help"]',
              )
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
  }, [hasUnsavedSettingsTextDrafts, publicationPathTemplate.snapshot]);
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
    successText: string,
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
      setNotice("");
    }
    let phase: GeneralFailurePhase = "write";
    try {
      const response = await action(savedDraft);
      await acceptSettingsWrite(response, status);
      phase = "confirm";
      try {
        await refreshState((fresh) => acknowledge(fresh, savedDraft.revision));
      } catch (reason) {
        throw new Error(
          `Настройки приняты сервером, но не удалось подтвердить их чтением: ${message(reason)}`,
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
    successText: string,
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
      "Формат публикации сохранён.",
      publicationFormat.capture,
      (fresh, revision) => {
        const confirmed =
          fresh.settings.publication_format === "source"
            ? "source"
            : fresh.settings.publication_format === "m4a"
              ? "m4a"
              : "mka";
        if (confirmed !== value)
          throw new Error(
            `Сервер не подтвердил формат публикации ${value}; сейчас сохранено ${confirmed}.`,
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
        message: "Параллельность должна быть положительным целым числом.",
      };
    let attempt: Promise<SettingsSaveOutcome> | undefined;
    const confirmationPending = confirmConflictingSave(
      "concurrency",
      "Параллельность анализа",
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
          "Параллельность анализа сохранена.",
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
        message: "Укажите шаблон пути публикации.",
      };
    let attempt: Promise<SettingsSaveOutcome> | undefined;
    const confirmationPending = confirmConflictingSave(
      "publicationTemplate",
      "Шаблон пути публикации",
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
          "Шаблон пути публикации сохранён.",
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
      "Рабочая папка",
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
          "Рабочая папка изменена.",
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
      "Настройка SHA-256 сохранена.",
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
      "Настройка удаления исходников сохранена.",
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

  function saveLrclib(enabled: boolean) {
    lrclib.update(enabled);
    const savedDraft = lrclib.capture();
    void mutate(
      (draft) =>
        updateLrclibSetting({
          enabled: draft.value ?? enabled,
        }),
      204,
      "Настройка LRCLIB сохранена.",
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
      "Уровень журнала применён.",
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
      setNotice("");
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
              throw new Error(
                `Сервер не подтвердил принятое значение формата публикации (${String(failure.value)}); сейчас сохранено ${confirmed}.`,
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
          setNotice("Настройка подтверждена сервером.");
        }
      } catch (reason) {
        if (
          mountedRef.current &&
          mountGeneration.current === generation &&
          currentRevision() === failure.revision
        )
          setError(
            `Настройка принята сервером, но не удалось подтвердить её чтением: ${message(reason)}`,
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
    const dialog = outputDialogRef.current;
    if (dialog?.open) dialog.close();
  }

  const runningOperation = operations.some(
    (operation) => operation.state === "running",
  );

  async function confirmOutputDirectory() {
    if (
      "outputDirectory" in conflictValues &&
      !Object.hasOwn(authorizedConflictExpected.current, "outputDirectory")
    ) {
      const current = state?.settings.output_directory;
      if (current !== undefined)
        confirmConflictingSave(
          "outputDirectory",
          "Каталог публикации",
          current,
          () => {
            void confirmOutputDirectory();
          },
        );
      return;
    }
    if (isEffectivelyBusy() || runningOperation) return;
    const savedDraft = outputDirectory.capture();
    const output = savedDraft.value;
    if (output === undefined) return;
    outputDirectory.beginSave(savedDraft.revision);
    setBusy(true);
    setOutputDialogError("");
    setNotice("");
    try {
      await acceptSettingsWrite(
        await updateSettings({
          output_directory: output,
          expected: {
            output_directory: expectedForSave(
              "outputDirectory",
              savedDraft.baseline ?? state?.settings.output_directory ?? "",
            ),
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
        throw new Error(
          `Каталог публикации принят сервером, но не удалось подтвердить его чтением: ${message(reason)}`,
        );
      }
      setNotice("Каталог публикации изменён.");
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
    setNotice("");
    try {
      successful(
        await setAcoustidApplicationKey({ key: acoustIDApplicationKey }),
        204,
      );
      setAcoustIDApplicationKey("");
      await refreshState();
      setNotice("Ключ приложения AcoustID сохранён.");
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
    setNotice("");
    try {
      successful(await deleteAcoustidApplicationKey(), 204);
      setAcoustIDApplicationKey("");
      await refreshState();
      setNotice("Ключ приложения AcoustID удалён.");
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
      confirmConflictingSave(
        "musicBrainz",
        "настройки MusicBrainz",
        currentValue,
        () => {
          saveMusicBrainzMode(mode);
        },
      );
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
      "Режим MusicBrainz сохранён.",
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
        "настройки MusicBrainz",
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
      "Ограничение частоты MusicBrainz сохранено.",
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
      setError("Сначала задайте корректный MusicBrainz base URL.");
      return {
        status: "invalid",
        group: "musicBrainz",
        validSavedTarget: false,
        confirmationRead: false,
        message: "Сначала задайте корректный MusicBrainz base URL.",
      };
    }
    if (payload.mode === "self-hosted" && delayChanged && !delayIsValid) {
      setError("Интервал запросов должен быть числом от 0 до 60 секунд.");
      return {
        status: "invalid",
        group: "musicBrainz",
        validSavedTarget: false,
        confirmationRead: false,
        message: "Интервал запросов должен быть числом от 0 до 60 секунд.",
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
          "настройки MusicBrainz",
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
    setNotice("");
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
              throw new Error(
                "Сервер не подтвердил отправленные настройки MusicBrainz.",
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
          throw new Error(
            `Настройки MusicBrainz приняты сервером, но не удалось подтвердить их чтением: ${message(reason)}`,
          );
        }
      }
      const checked = successful(await checkSettingsMusicbrainz(), 200);
      try {
        await refreshState();
      } catch (reason) {
        throw new Error(
          `MusicBrainz сохранён и проверен, но не удалось подтвердить состояние чтением: ${message(reason)}`,
        );
      }
      if (!checked.data.success) {
        const failure = checked.data.error || "MusicBrainz недоступен.";
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
        const failure =
          "В текущем режиме остались несохранённые изменения MusicBrainz.";
        setError(failure);
        return {
          status: "invalid",
          group: "musicBrainz",
          validSavedTarget: true,
          confirmationRead: targetConfirmed,
          message: failure,
        };
      }
      setNotice("MusicBrainz проверен.");
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

  async function saveEligibleTextDrafts(): Promise<SettingsSaveOutcome> {
    if (isEffectivelyBusy())
      return {
        status: "failed",
        group: "all",
        message: "Дождитесь завершения текущего запроса.",
      };
    if (outputDirectory.dirty) {
      dialogReturnFocusRef.current =
        navigationDialogRef.current?.querySelector(
          "button[data-settings-navigation-save]",
        ) ?? heading.current;
      setError("");
      setOutputDialogError("");
      setOutputDialog(true);
      return { status: "confirmation_pending", group: "outputDirectory" };
    }
    if (
      moveDirectoryBaseline.current !== undefined &&
      moveDirectoryRef.current !== moveDirectoryBaseline.current
    ) {
      dialogReturnFocusRef.current =
        navigationDialogRef.current?.querySelector(
          "button[data-settings-navigation-save]",
        ) ?? heading.current;
      setError("");
      setMoveDialogError("");
      moveSessionRevision.current += 1;
      moveInputRevision.current += 1;
      setMovePlan(undefined);
      setMoveDialog(true);
      return { status: "confirmation_pending", group: "toolsDirectory" };
    }

    const textSaves: Array<[boolean, () => Promise<SettingsSaveOutcome>]> = [
      [sourceFileConcurrency.dirty, saveConcurrency],
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
          message:
            "Во время сохранения появились новые несохранённые изменения.",
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
      setNavigationError(
        locale === "ru"
          ? "Не удалось сохранить черновик в хранилище браузера. Останьтесь на странице и повторите попытку."
          : "The draft could not be saved to browser storage. Stay on this page and try again.",
      );
      return;
    }
    resolveNavigation(attempt, "leave");
  }

  function discardDraftAndLeave(attempt: HashAttempt) {
    const storage = getSettingsDraftStorage();
    if (!writeSettingsDraft(storage, null)) {
      writeSettingsDraft(storage, currentSettingsDraftSnapshot());
      setDraftStorageWarning(true);
      setNavigationError(
        locale === "ru"
          ? "Не удалось удалить сохранённый черновик. Останьтесь на странице и повторите попытку."
          : "The saved draft could not be discarded. Stay on this page and try again.",
      );
      return;
    }
    if (!resolveNavigation(attempt, "leave"))
      writeSettingsDraft(storage, currentSettingsDraftSnapshot());
  }

  async function saveDraftAndLeave(attempt: HashAttempt) {
    setNavigationError("");
    const outcome = await saveEligibleTextDrafts();
    if (latestNavigationAttempt.current?.id !== attempt.id) return;
    if (outcome.status === "saved" && !hasUnsavedSettingsTextDrafts()) {
      resolveNavigation(attempt, "leave");
      return;
    }
    const messages: Record<SettingsSaveOutcome["status"], string> =
      locale === "ru"
        ? {
            saved:
              "Появились новые несохранённые изменения. Проверьте настройки перед выходом.",
            invalid: "Исправьте некорректные настройки перед выходом.",
            confirmation_pending:
              "Завершите открытое подтверждение настроек, затем повторите сохранение.",
            conflict:
              "Настройки изменились на сервере. Разрешите конфликт перед выходом.",
            uncertain:
              "Не удалось подтвердить сохранение. Проверьте состояние и повторите попытку.",
            failed: "Не удалось сохранить настройки перед выходом.",
          }
        : {
            saved:
              "New unsaved changes appeared. Review the settings before leaving.",
            invalid: "Correct the invalid settings before leaving.",
            confirmation_pending:
              "Complete the open settings confirmation, then try saving again.",
            conflict:
              "Settings changed on the server. Resolve the conflict before leaving.",
            uncertain:
              "The save could not be confirmed. Review the current state and try again.",
            failed: "Settings could not be saved before leaving.",
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
    const dialog = installDialogRef.current;
    if (dialog?.open) dialog.close();
    else {
      setInstallDialog(undefined);
    }
  }
  function closeMoveDialog() {
    dialogFocusRestorePending.current = true;
    moveSessionRevision.current += 1;
    invalidateMovePlan();
    moveStartRevision.current += 1;
    setMoveDialogError("");
    setBusy(false);
    const dialog = moveDialogRef.current;
    if (dialog?.open) dialog.close();
    else {
      setMoveDialog(false);
    }
  }
  function onDialogKeyDown(
    event: React.KeyboardEvent<HTMLDialogElement>,
    close: () => void,
  ) {
    if (
      event.target instanceof Element &&
      event.target.closest("dialog") !== event.currentTarget
    )
      return;
    if (event.key === "Escape") {
      event.preventDefault();
      close();
      return;
    }
    if (event.key !== "Tab") return;
    const controls = event.currentTarget.querySelectorAll<HTMLElement>(
      'button:not(:disabled), input:not(:disabled), select:not(:disabled), textarea:not(:disabled), a[href], [tabindex]:not([tabindex="-1"])',
    );
    if (controls.length === 0) return;
    const first = controls[0];
    const last = controls[controls.length - 1];
    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      first.focus();
    } else if (!event.currentTarget.contains(document.activeElement)) {
      event.preventDefault();
      (event.shiftKey ? last : first).focus();
    }
  }
  const loadCatalogNow = () => {
    void requestCatalog();
  };
  async function preflightInstall(kind: Kind) {
    const release = selectedRelease[kind];
    if (!release) {
      setError("Выберите версию для установки.");
      return;
    }
    const sessionRevision = ++installSessionRevision.current;
    const requestRevision = ++installRequestRevision.current;
    installPendingRequest.current = requestRevision;
    setInstallDialogError("");
    setError("");
    setBusy(true);
    try {
      const plan = await preflightManagedToolInstall(kind, release);
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
    const started = await startManagedToolInstall(plan, confirmed);
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
      const started = await startManagedToolInstall(installDialog.plan, [
        ...(installDialog.plan.conflicts || []),
      ]);
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
    setNotice("");
    try {
      successful(
        await activateToolInstallation(item.id, { package_kind: kind }),
        204,
      );
      await Promise.all([refreshState(), refreshInstallations()]);
      setNotice(`${kind} ${item.version} активирован.`);
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
      setError("Активную или занятую операцией версию удалить нельзя.");
      return;
    }
    setBusy(true);
    setError("");
    setNotice("");
    try {
      successful(
        await deleteToolInstallation(item.id, { package_kind: kind }),
        204,
      );
      await Promise.all([refreshState(), refreshInstallations()]);
      setNotice(`${kind} ${item.version} удалён.`);
    } catch (reason) {
      setError(message(reason));
    } finally {
      setBusy(false);
    }
  }
  async function preflightMove() {
    if (
      "toolsDirectory" in conflictValues &&
      !Object.hasOwn(authorizedConflictExpected.current, "toolsDirectory")
    ) {
      const current = state?.settings.tools_directory;
      if (current !== undefined)
        confirmConflictingSave(
          "toolsDirectory",
          "Каталог инструментов",
          current,
          () => {
            void preflightMove();
          },
        );
      return;
    }
    const directory = moveDirectoryRef.current;
    const removeOldFiles = removeOldRef.current;
    const inputRevision = moveInputRevision.current;
    const sessionRevision = moveSessionRevision.current;
    const requestRevision = ++moveRequestRevision.current;
    movePendingRequest.current = requestRevision;
    const sourceDirectory = state?.settings.tools_directory || "";
    setBusy(true);
    setError("");
    setMoveDialogError("");
    try {
      const response = await acceptSettingsWrite(
        await preflightToolsRootMove({
          expected_tools_directory: expectedForSave(
            "toolsDirectory",
            moveDirectoryBaseline.current ?? sourceDirectory,
          ),
          new_tools_directory: directory,
          remove_old_files: removeOldFiles,
        }),
        200,
      );
      if (
        requestRevision !== moveRequestRevision.current ||
        inputRevision !== moveInputRevision.current ||
        sessionRevision !== moveSessionRevision.current ||
        !moveDialog
      )
        return;
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
    } catch (reason) {
      if (reason instanceof SettingsConflictError) invalidateMovePlan();
      if (
        (reason instanceof SettingsConflictError ||
          requestRevision === moveRequestRevision.current) &&
        inputRevision === moveInputRevision.current &&
        sessionRevision === moveSessionRevision.current &&
        moveDialog
      )
        setMoveDialogError(message(reason));
    } finally {
      if (movePendingRequest.current === requestRevision) {
        movePendingRequest.current = 0;
        setBusy(false);
      }
    }
  }
  async function confirmMove() {
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
      const response = successful(
        await startToolsRootMove({
          preflight_token: confirmedPlan.plan.preflight_token,
          confirmed_conflicts: [...(confirmedPlan.plan.conflicts || [])],
        }),
        200,
      );
      setOperations((previous) => [
        ...previous.filter((item) => item.id !== response.data.id),
        response.data,
      ]);
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
        setMoveDialogError(message(reason));
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
          await Promise.all([refreshState(), refreshInstallations()]);
          await refreshOperations();
        } catch (reason) {
          setError(message(reason));
        }
      }
    },
    [refreshInstallations, refreshOperations, refreshState],
  );

  const platformReady = Boolean(
    state?.platform.supported && !state.platform.diagnostic,
  );
  const persistedTime = checkedAt
    ? new Date(checkedAt).toLocaleString()
    : "Каталог ещё не проверялся.";
  const concurrencyDraft = sourceFileConcurrency.snapshot();
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
  return (
    <section
      aria-labelledby="settings-title"
      className="settings-screen mx-auto max-w-5xl py-3"
    >
      <h1 ref={heading} tabIndex={-1} id="settings-title">
        Настройки
      </h1>
      <p className="setup-help">
        После завершения Setup проблемы конфигурации отображаются здесь и не
        возвращают приложение в мастер.
      </p>
      {effectiveBusy && <p role="status">Выполняется запрос…</p>}
      {draftStorageWarning && (
        <p role="status">
          Черновик настроек сохранён только в памяти страницы; после её закрытия
          восстановить изменения не удастся.
        </p>
      )}
      {!initialLoadComplete && !loadError && (
        <p role="status">Загрузка настроек и состояния сервера…</p>
      )}
      {loadError && (
        <>
          <p ref={loadErrorAlert} tabIndex={-1} role="alert">
            Не удалось загрузить настройки и состояние сервера: {loadError}
          </p>
          <AppButton
            isDisabled={effectiveBusy}
            onPress={() => void loadScreen()}
          >
            Повторить загрузку
          </AppButton>
        </>
      )}
      {initialLoadComplete && state && (
        <>
          <section
            aria-labelledby="health-title"
            className="setup-panel settings-panel"
          >
            <h2 id="health-title">Состояние конфигурации</h2>
            <p role={state.configuration_health.healthy ? "status" : "alert"}>
              {state.configuration_health.healthy
                ? "Конфигурация исправна."
                : "Есть проблемы конфигурации."}
            </p>
            {!!state.configuration_health.problems?.length && (
              <ul>
                {state.configuration_health.problems.map((problem) => (
                  <li key={problem}>{problem}</li>
                ))}
              </ul>
            )}
            {!platformReady && (
              <p role="alert">
                Платформа недоступна: операции с инструментами отключены.
                Настройки можно исправлять.
              </p>
            )}
          </section>
          <SettingsGeneralSection
            publicationFormat={publicationFormat.value ?? "mka"}
            onPublicationFormatChange={savePublicationFormat}
            sourceFileConcurrency={
              sourceFileConcurrency.value ??
              String(state.settings.source_file_concurrency)
            }
            concurrencyInvalid={
              sourceFileConcurrency.value !== undefined &&
              !validConcurrency(sourceFileConcurrency.value)
            }
            concurrencySavingDisabled={effectiveBusy}
            concurrencyDirty={sourceFileConcurrency.dirty}
            onConcurrencyChange={sourceFileConcurrency.update}
            onSaveConcurrency={saveConcurrency}
            sha256Enabled={sha256.value ?? state.settings.sha256_enabled}
            onSha256Change={saveSha256}
            logLevel={logLevel.value ?? "info"}
            onLogLevelChange={saveLogLevel}
          />
          {(["publicationFormat", "sha256", "logLevel"] as const).map(
            (setting) => {
              const failure = generalFailures[setting];
              if (!failure) return null;
              const label =
                setting === "publicationFormat"
                  ? "формат публикации"
                  : setting === "sha256"
                    ? "SHA-256"
                    : "уровень журнала";
              const value =
                setting === "publicationFormat"
                  ? failure.value === "source"
                    ? "Исходный формат"
                    : failure.value === "m4a"
                      ? "M4A"
                      : "MKA remux"
                  : setting === "sha256"
                    ? failure.value
                      ? "включено"
                      : "выключено"
                    : String(failure.value);
              return (
                <fieldset
                  key={setting}
                  className="grid gap-2 rounded border border-[var(--warning)] bg-[var(--warning-soft)] p-3"
                  aria-label={`Повторное сохранение: ${label}`}
                >
                  <p className="m-0" role="status">
                    {failure.phase === "write"
                      ? `Не удалось сохранить ${label}: ${value}.`
                      : `Значение ${label} (${value}) принято сервером, но чтение для подтверждения не удалось.`}
                  </p>
                  <AppButton
                    isDisabled={effectiveBusy}
                    onPress={() => retryGeneralFailure(setting)}
                  >
                    {failure.phase === "write"
                      ? `Повторить сохранение: ${label} (${value})`
                      : `Повторить подтверждение: ${label} (${value})`}
                  </AppButton>
                </fieldset>
              );
            },
          )}
          <div className="settings-grid">
            <section
              aria-labelledby="runtime-title"
              className="setup-panel settings-panel"
            >
              <h2 id="runtime-title">Публикация</h2>
              <DirectoryPathField
                id="settings-output-directory"
                label="Output directory"
                value={outputDirectory.value ?? ""}
                onSelect={outputDirectory.update}
                disabled={effectiveBusy}
              />
              {outputDirectory.dirty && (
                <p>Есть несохранённые изменения публикации.</p>
              )}
              <label>
                Шаблон пути публикации
                <input
                  aria-describedby="publication-template-help"
                  maxLength={4096}
                  value={
                    publicationPathTemplate.value ??
                    state.settings.publication_path_template
                  }
                  onChange={(event) =>
                    publicationPathTemplate.update(event.target.value)
                  }
                />
              </label>
              <p id="publication-template-help">
                Токены в фигурных скобках подставляются из метаданных; для
                отсутствующего значения задайте альтернативу через |. Шаблон
                может содержать только имя файла без каталогов.
              </p>
              {publicationPathTemplate.dirty && (
                <p>Есть несохранённый шаблон пути публикации.</p>
              )}
              <AppButton
                isDisabled={effectiveBusy || !publicationPathTemplate.value}
                onPress={savePublicationTemplate}
              >
                Сохранить шаблон пути
              </AppButton>
              <label className="settings-inline">
                <input
                  type="checkbox"
                  checked={
                    publicationRemoveSources.value ??
                    state.settings.publication_remove_sources
                  }
                  onChange={(event) =>
                    savePublicationRemoveSources(event.target.checked)
                  }
                />{" "}
                Разрешить удаление исходников после публикации
              </label>
              <p role="note">
                Выключено по умолчанию. При включении удаление в будущем может
                применяться только после успешной проверки публикации; изменение
                настройки не удаляет существующие файлы и не запускает
                обработку.
              </p>
              {publicationRemoveSources.dirty && (
                <p>Есть несохранённая настройка удаления исходников.</p>
              )}
              <AppButton
                isDisabled={
                  effectiveBusy ||
                  runningOperation ||
                  outputDirectory.value === state.settings.output_directory
                }
                onPress={(event) => {
                  dialogReturnFocusRef.current = event.target as HTMLElement;
                  setError("");
                  setOutputDialogError("");
                  setOutputDialog(true);
                }}
              >
                Сменить output
              </AppButton>
            </section>
            <section
              aria-labelledby="work-directory-title"
              className="setup-panel settings-panel"
            >
              <h2 id="work-directory-title">Обработка источников</h2>
              <DirectoryPathField
                id="settings-work-directory"
                label="Work directory для staged-копий"
                value={workDirectory.value ?? ""}
                onSelect={workDirectory.update}
                disabled={effectiveBusy}
              />
              <p>
                Настройка не требуется для Setup или регистрации source root, но
                обязательна для обработки staged-источников. Каталог нельзя
                изменить, пока существуют операции или сохранённые копии.
              </p>
              {workDirectory.dirty && (
                <p>Есть несохранённые изменения рабочей папки.</p>
              )}
              <AppButton
                isDisabled={effectiveBusy || !workDirectory.dirty}
                onPress={saveWorkDirectory}
              >
                Сохранить рабочую папку
              </AppButton>
            </section>
            <section
              aria-labelledby="providers-title"
              className="setup-panel settings-panel"
            >
              <h2 id="providers-title">Провайдеры метаданных</h2>
              <label>
                Ключ приложения AcoustID
                <input
                  type="password"
                  autoComplete="new-password"
                  value={acoustIDApplicationKey}
                  onChange={(event) =>
                    setAcoustIDApplicationKey(event.target.value)
                  }
                  maxLength={4096}
                />
              </label>
              <p>
                {state.settings.has_acoustid_application_key
                  ? "Ключ сохранён. Его значение нельзя прочитать из настроек."
                  : "Ключ не задан; поиск AcoustID недоступен."}
              </p>
              <div className="settings-actions">
                <AppButton
                  isDisabled={
                    effectiveBusy || acoustIDApplicationKey.length === 0
                  }
                  onPress={() => void saveAcoustIDApplicationKey()}
                >
                  Сохранить ключ AcoustID
                </AppButton>
                {state.settings.has_acoustid_application_key && (
                  <AppButton
                    isDisabled={effectiveBusy}
                    onPress={() => void deleteAcoustIDApplicationKey()}
                  >
                    Удалить ключ AcoustID
                  </AppButton>
                )}
              </div>
              <label>
                MusicBrainz mode
                <select
                  value={musicBrainz.value?.mode ?? "public"}
                  onChange={(event) =>
                    saveMusicBrainzMode(
                      event.target.value as UpdateMusicBrainzBodyMode,
                    )
                  }
                >
                  <option value="public">Public</option>
                  <option value="self-hosted">Self-hosted</option>
                </select>
              </label>
              {musicBrainz.value?.mode === "self-hosted" && (
                <>
                  <label>
                    MusicBrainz base URL
                    <input
                      value={musicBrainz.value?.baseURL ?? ""}
                      onChange={(event) => {
                        const currentValue = musicBrainz.value;
                        if (currentValue === undefined) return;
                        musicBrainz.update({
                          ...currentValue,
                          baseURL: event.target.value,
                        });
                      }}
                    />
                  </label>
                  <label className="settings-inline">
                    <input
                      type="checkbox"
                      checked={musicBrainz.value?.selfHostedThrottle ?? false}
                      onChange={(event) =>
                        saveMusicBrainzThrottle(event.target.checked)
                      }
                    />{" "}
                    Ограничивать частоту запросов к self-hosted MusicBrainz
                  </label>
                  <label>
                    Интервал запросов (секунды)
                    <input
                      type="text"
                      inputMode="decimal"
                      min="0"
                      max="60"
                      step="any"
                      required
                      value={musicBrainz.value?.selfHostedDelaySeconds ?? "0.5"}
                      onChange={(event) => {
                        const currentValue = musicBrainz.value;
                        if (currentValue === undefined) return;
                        musicBrainz.update({
                          ...currentValue,
                          selfHostedDelaySeconds: event.target.value,
                        });
                      }}
                    />
                  </label>
                </>
              )}
              <p>
                Последняя проверка:{" "}
                {state.settings.musicbrainz_verified_at || "не проверено"}
              </p>
              {musicBrainz.dirty && (
                <p>Есть несохранённые изменения MusicBrainz.</p>
              )}
              <AppButton
                isDisabled={effectiveBusy}
                onPress={() => void checkMusicBrainz()}
              >
                Сохранить и проверить MusicBrainz
              </AppButton>
              <label className="settings-inline">
                <input
                  type="checkbox"
                  checked={lrclib.value ?? state.settings.lrclib_enabled}
                  onChange={(event) => saveLrclib(event.target.checked)}
                />{" "}
                LRCLIB включён
              </label>
              {lrclib.dirty && <p>Есть несохранённые изменения LRCLIB.</p>}
            </section>
            <section
              aria-labelledby="tools-root-title"
              className="setup-panel settings-panel"
            >
              <h2 id="tools-root-title">Каталог инструментов</h2>
              <p>
                Текущий Tools directory:{" "}
                <code>{state.settings.tools_directory}</code>
              </p>
              <AppButton
                isDisabled={!platformReady || effectiveBusy}
                onPress={(event) => {
                  dialogReturnFocusRef.current = event.target as HTMLElement;
                  setError("");
                  setMoveDialogError("");
                  moveSessionRevision.current += 1;
                  moveInputRevision.current += 1;
                  setMovePlan(undefined);
                  setMoveDialog(true);
                }}
              >
                Перенести каталог
              </AppButton>
            </section>
          </div>
          <GenreCatalogPanel />
          <section
            aria-labelledby="managed-title"
            className="setup-panel settings-panel"
          >
            <GitHubTokenForm
              hasToken={state.settings.has_github_token}
              onChanged={async () => {
                await refreshState();
              }}
            />
            <h2 id="managed-title">Managed tools</h2>
            <p>Последняя успешная проверка каталога: {persistedTime}</p>
            {catalogError && (
              <p ref={catalogAlert} tabIndex={-1} role="alert">
                {catalogError}
              </p>
            )}
            <AppButton isDisabled={catalogBusy} onPress={loadCatalogNow}>
              {catalogBusy ? "Обновление каталога…" : "Обновить каталог"}
            </AppButton>
            <div className="settings-grid">
              {(["ffmpeg", "fpcalc"] as const).map((kind) => {
                const installed = installations.filter(
                  (item) => item.package_kind === kind,
                );
                const releases = catalog[kind]?.releases || [];
                const occupied = (item: InstallationResponse) =>
                  operations.some(
                    (op) =>
                      op.target_installation_id === item.id &&
                      activeOperation(op),
                  );
                return (
                  <section
                    key={kind}
                    aria-labelledby={`${kind}-title`}
                    className="setup-tool"
                  >
                    <h3 id={`${kind}-title`}>
                      {kind === "ffmpeg" ? "FFmpeg package" : "fpcalc"}
                    </h3>
                    <h4>Активная версия</h4>
                    <ul>
                      {installed
                        .filter((item) => item.active)
                        .map((item) => (
                          <li key={item.id}>{item.version} — активна</li>
                        ))}
                      {!installed.some((item) => item.active) && (
                        <li>Нет активной версии</li>
                      )}
                    </ul>
                    <h4>Установленные версии</h4>
                    <ul>
                      {installed.map((item) => (
                        <li key={item.id}>
                          {item.version} ({item.state}){" "}
                          {item.active && "— активна"}
                          <AppButton
                            isDisabled={
                              !platformReady ||
                              effectiveBusy ||
                              item.active ||
                              occupied(item) ||
                              item.state !== "ready"
                            }
                            onPress={() => void activate(item, kind)}
                          >
                            Активировать {item.version}
                          </AppButton>
                          <AppButton
                            isDisabled={
                              !platformReady ||
                              effectiveBusy ||
                              item.active ||
                              occupied(item)
                            }
                            onPress={() => void remove(item, kind)}
                          >
                            Удалить {item.version}
                          </AppButton>
                        </li>
                      ))}
                    </ul>
                    <h4>Доступные версии</h4>
                    {catalog[kind] ? (
                      <>
                        {catalog[kind]?.notice && (
                          <p>{catalog[kind]?.notice}</p>
                        )}
                        <label>
                          Версия для установки
                          <select
                            aria-label={`Версия ${kind}`}
                            value={selectedRelease[kind] || ""}
                            onChange={(event) =>
                              setSelectedRelease((previous) => ({
                                ...previous,
                                [kind]: event.target.value,
                              }))
                            }
                          >
                            <option value="">Выберите версию</option>
                            {releases.map((release) => (
                              <option
                                key={release.identity}
                                value={release.identity}
                              >
                                {release.identity} ({release.source})
                              </option>
                            ))}
                          </select>
                        </label>
                        <AppButton
                          isDisabled={
                            !platformReady ||
                            effectiveBusy ||
                            !selectedRelease[kind]
                          }
                          onPress={(event) => {
                            dialogReturnFocusRef.current =
                              event.target as HTMLElement;
                            setError("");
                            void preflightInstall(kind);
                          }}
                        >
                          Установить без активации
                        </AppButton>
                      </>
                    ) : (
                      <p>
                        {catalogBusy
                          ? "Загрузка каталога…"
                          : "Каталог пока не загружен."}
                      </p>
                    )}
                  </section>
                );
              })}
            </div>
          </section>
          {!!operations.length && (
            <section
              aria-labelledby="operations-title"
              className="setup-panel settings-panel"
            >
              <h2 id="operations-title">Текущие операции</h2>
              {operations.map((operation) => (
                <OperationRow
                  key={operation.id}
                  operation={operation}
                  onSnapshot={onOperation}
                  onDismiss={async (id) => {
                    successful(await dismissOperation(id), 204);
                    setOperations((items) =>
                      items.filter((item) => item.id !== id),
                    );
                  }}
                  onRetry={async (id) => {
                    const retried = successful(await retryOperation(id), 200);
                    setOperations((items) =>
                      items.map((item) =>
                        item.id === id ? retried.data : item,
                      ),
                    );
                  }}
                />
              ))}
            </section>
          )}
        </>
      )}
      {error && (
        <p ref={alert} tabIndex={-1} role="alert">
          {error}
        </p>
      )}
      {notice && <p role="status">{notice}</p>}
      {state && (
        <AppButton isDisabled={effectiveBusy} onPress={() => void loadScreen()}>
          Обновить состояние
        </AppButton>
      )}
      {Object.entries(conflictValues).map(([rawGroup, currentValue]) => {
        const group = rawGroup as ConflictGroup;
        if (!activeConflicts[group]) return null;
        const label = conflictLabel(group);
        return (
          <section
            key={group}
            aria-label={`Конфликт настроек: ${label}`}
            className="setup-panel settings-panel"
            role="status"
          >
            <p>
              Сохранённое значение для «{label}» изменилось:{" "}
              {displaySettingValue(currentValue)}.
            </p>
            <AppButton
              isDisabled={effectiveBusy}
              onPress={() => takeSavedConflict(group)}
            >
              Взять сохранённое: {label}
            </AppButton>
            <AppButton
              isDisabled={effectiveBusy}
              onPress={() => continueEditingConflict(group)}
            >
              Продолжить редактирование: {label}
            </AppButton>
          </section>
        );
      })}
      {navigationAttempt && (
        <dialog
          ref={navigationDialogRef}
          aria-modal="true"
          aria-labelledby="settings-navigation-title"
          aria-describedby="settings-navigation-description"
          className="settings-dialog"
          onCancel={(event) => {
            event.preventDefault();
            event.stopPropagation();
            resolveNavigation(navigationAttempt, "stay");
          }}
          onKeyDown={(event) => {
            if (
              event.target instanceof Element &&
              event.target.closest("dialog") !== event.currentTarget
            )
              return;
            if (event.key === "Escape") {
              event.preventDefault();
              event.stopPropagation();
              resolveNavigation(navigationAttempt, "stay");
              return;
            }
            onDialogKeyDown(event, () =>
              resolveNavigation(navigationAttempt, "stay"),
            );
          }}
        >
          <h2 id="settings-navigation-title">
            {locale === "ru" ? "Несохранённые настройки" : "Unsaved settings"}
          </h2>
          <p id="settings-navigation-description">
            {locale === "ru"
              ? "В настройках есть несохранённые текстовые изменения. Что сделать перед переходом?"
              : "Settings contain unsaved text changes. What would you like to do before leaving?"}
          </p>
          {navigationError && <p role="alert">{navigationError}</p>}
          <AppButton
            onPress={() => resolveNavigation(navigationAttempt, "stay")}
          >
            {locale === "ru" ? "Остаться" : "Stay"}
          </AppButton>
          <AppButton onPress={() => keepDraftAndLeave(navigationAttempt)}>
            {locale === "ru" ? "Уйти, сохранив черновик" : "Leave with draft"}
          </AppButton>
          <AppButton onPress={() => discardDraftAndLeave(navigationAttempt)}>
            {locale === "ru"
              ? "Удалить черновик и уйти"
              : "Discard draft and leave"}
          </AppButton>
          <AppButton
            data-settings-navigation-save
            onPress={() => void saveDraftAndLeave(navigationAttempt)}
          >
            {locale === "ru" ? "Сохранить и уйти" : "Save and leave"}
          </AppButton>
        </dialog>
      )}
      {conflictConfirmation && (
        <dialog
          ref={conflictDialogRef}
          aria-labelledby="conflict-confirm-title"
          aria-describedby="conflict-confirm-description"
          className="settings-dialog"
          onClose={() => setConflictConfirmation(undefined)}
          onCancel={(event) => {
            event.preventDefault();
            setConflictConfirmation(undefined);
          }}
          onKeyDown={(event) =>
            onDialogKeyDown(event, () => setConflictConfirmation(undefined))
          }
        >
          <h2 id="conflict-confirm-title">Заменить сохранённое значение?</h2>
          <p id="conflict-confirm-description">
            Для «{conflictConfirmation.label}» сейчас сохранено:{" "}
            {displaySettingValue(conflictConfirmation.currentValue)}.
            Подтвердите замену этим черновиком. При новом конфликте потребуется
            подтвердить ещё раз.
          </p>
          <AppButton
            onPress={() => {
              authorizedConflictExpected.current[conflictConfirmation.group] =
                conflictConfirmation.currentValue;
              const confirm = conflictConfirmation.onConfirm;
              setConflictConfirmation(undefined);
              confirm();
            }}
          >
            Заменить сохранённое значение
          </AppButton>
          <AppButton onPress={() => setConflictConfirmation(undefined)}>
            Отмена
          </AppButton>
        </dialog>
      )}
      {outputDialog && (
        <dialog
          ref={outputDialogRef}
          aria-modal="true"
          aria-labelledby="output-dialog-title"
          aria-describedby="output-dialog-warning"
          className="settings-dialog settings-dialog--destructive"
          onClose={() => {
            dialogFocusRestorePending.current = true;
            setOutputDialog(false);
            setOutputDialogError("");
          }}
          onKeyDown={(event) => onDialogKeyDown(event, closeOutputDialog)}
          onCancel={(event) => {
            event.preventDefault();
            closeOutputDialog();
          }}
        >
          <h2 id="output-dialog-title">Сменить каталог публикации?</h2>
          <p id="output-dialog-warning">
            Изменение output отменит все операции в очереди и очистит ссылки на
            прежние файлы в приложении. Сами прежние файлы не будут удалены.
          </p>
          {outputDialogError && (
            <p ref={outputDialogAlert} tabIndex={-1} role="alert">
              {outputDialogError}
            </p>
          )}
          <AppButton
            isDisabled={effectiveBusy || runningOperation}
            onPress={() => void confirmOutputDirectory()}
          >
            Подтвердить смену output
          </AppButton>
          <AppButton
            isDisabled={effectiveBusy}
            onPress={() => closeOutputDialog()}
          >
            Отмена
          </AppButton>
        </dialog>
      )}
      {installDialog && (
        <dialog
          ref={installDialogRef}
          aria-modal="true"
          aria-labelledby="install-dialog-title"
          className="settings-dialog"
          onClose={() => {
            dialogFocusRestorePending.current = true;
            setInstallDialog(undefined);
            setInstallDialogError("");
          }}
          onKeyDown={(event) => onDialogKeyDown(event, closeInstallDialog)}
          onCancel={(event) => {
            event.preventDefault();
            closeInstallDialog();
          }}
        >
          <h2 id="install-dialog-title">
            Подтверждение установки {installDialog.release}
          </h2>
          <p>Установка не активирует выбранную версию.</p>
          <ConflictList
            conflicts={installDialog.plan.conflicts || []}
            targets={installDialog.plan.targets || []}
          />
          {installDialogError && (
            <p ref={installDialogAlert} tabIndex={-1} role="alert">
              {installDialogError}
            </p>
          )}
          <AppButton
            isDisabled={effectiveBusy}
            onPress={() => void confirmInstall()}
          >
            Подтвердить перечисленные конфликты
          </AppButton>
          <AppButton isDisabled={effectiveBusy} onPress={closeInstallDialog}>
            Отмена
          </AppButton>
        </dialog>
      )}
      {moveDialog && (
        <dialog
          ref={moveDialogRef}
          aria-modal="true"
          aria-labelledby="move-dialog-title"
          className="settings-dialog"
          onClose={() => {
            dialogFocusRestorePending.current = true;
            setMoveDialog(false);
            setMoveDialogError("");
          }}
          onKeyDown={(event) => onDialogKeyDown(event, closeMoveDialog)}
          onCancel={(event) => {
            event.preventDefault();
            closeMoveDialog();
          }}
        >
          <h2 id="move-dialog-title">Перенос Tools directory</h2>
          <DirectoryPathField
            id="settings-move-tools-directory"
            label="Новый Tools directory"
            value={moveDirectory}
            onSelect={setMoveDirectoryValue}
          />
          <label className="settings-inline">
            <input
              type="checkbox"
              checked={removeOld}
              onChange={(event) => setRemoveOldValue(event.target.checked)}
            />{" "}
            Удалить прежние управляемые файлы после успешного переноса
          </label>
          {moveDialogError && (
            <p ref={moveDialogAlert} tabIndex={-1} role="alert">
              {moveDialogError}
            </p>
          )}
          {!movePlan ? (
            <AppButton
              isDisabled={effectiveBusy}
              onPress={() => void preflightMove()}
            >
              Проверить перенос
            </AppButton>
          ) : (
            <>
              <p>
                Исходный каталог: <code>{movePlan.sourceDirectory}</code>
              </p>
              <p>
                Каталог назначения: <code>{movePlan.directory}</code>
              </p>
              <p>
                Удалить прежние управляемые файлы:{" "}
                {movePlan.removeOld ? "Да" : "Нет"}
              </p>
              <p>Управляемых файлов: {movePlan.plan.managed_file_count}</p>
              <p>Конфликтов: {movePlan.plan.conflicts?.length || 0}</p>
              <ConflictList conflicts={movePlan.plan.conflicts || []} />
              <AppButton
                isDisabled={effectiveBusy}
                onPress={() => void confirmMove()}
              >
                Подтвердить перенос
              </AppButton>
            </>
          )}
          <AppButton
            isDisabled={effectiveBusy}
            onPress={() => {
              setMovePlan(undefined);
              closeMoveDialog();
            }}
          >
            Отмена
          </AppButton>
        </dialog>
      )}
    </section>
  );
}

function showSettingsDialog(dialog: HTMLDialogElement | null) {
  if (!dialog || dialog.open) return;
  dialog.showModal();
  const firstControl = dialog.querySelector<HTMLElement>(
    "button:not(:disabled), input:not(:disabled), select:not(:disabled), textarea:not(:disabled)",
  );
  firstControl?.focus();
}

function ConflictList({
  conflicts,
  targets = [],
}: {
  conflicts: string[];
  targets?: string[];
}) {
  return (
    <div>
      <h3>Целевые файлы</h3>
      <ul>
        {targets.map((path) => (
          <li key={path}>
            <code>{path}</code>
          </li>
        ))}
      </ul>
      <h3>Конфликты для подтверждения</h3>
      {conflicts.length ? (
        <ul>
          {conflicts.map((path) => (
            <li key={path}>
              <code>{path}</code>
            </li>
          ))}
        </ul>
      ) : (
        <p>Конфликтов нет.</p>
      )}
    </div>
  );
}

function OperationRow({
  operation: initial,
  onSnapshot,
  onRetry,
  onDismiss,
}: {
  operation: OperationResponse;
  onSnapshot: (snapshot: OperationResponse) => void | Promise<void>;
  onRetry: (id: string) => Promise<void>;
  onDismiss: (id: string) => Promise<void>;
}) {
  const [operation, setOperation] = useState(initial);
  useEffect(() => setOperation(initial), [initial]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const errorRef = useRef<HTMLParagraphElement>(null);
  useOperationFollower(operation.id, {
    enabled: operation.state === "queued" || operation.state === "running",
    initialSnapshot: operation,
    onSnapshot: (snapshot) => {
      setOperation(snapshot);
      setError("");
      void onSnapshot(snapshot);
    },
    onSnapshotRead: (_snapshot, _source, accepted) => {
      if (!accepted) setError("");
    },
    onHttpError: apiResponseError,
    onError: (reason) => setError(message(reason)),
    onStreamError: (reason) => setError(reason.message),
    refreshOnStreamError: true,
  });
  useEffect(() => {
    if (error) errorRef.current?.focus();
  }, [error]);
  async function action(run: () => Promise<void>) {
    setBusy(true);
    setError("");
    try {
      await run();
    } catch (reason) {
      setError(message(reason));
    } finally {
      setBusy(false);
    }
  }
  const pending = operation.state === "queued" || operation.state === "running";
  return (
    <fieldset
      aria-label={`Операция ${operation.id}`}
      className="setup-operation"
    >
      <p role="status">
        {operation.kind}: {operation.state}, {operation.stage},{" "}
        {operation.bytes_completed}
        {operation.bytes_total == null ? "" : ` / ${operation.bytes_total}`}{" "}
        bytes. {operation.safe_error || ""}
      </p>
      {pending && <p>Операция выполняется; состояние обновляется с сервера.</p>}
      {operation.state === "failed" && (
        <>
          <AppButton
            isDisabled={busy}
            onPress={() => void action(() => onRetry(operation.id))}
          >
            Повторить операцию {operation.id}
          </AppButton>
          <AppButton
            isDisabled={busy}
            onPress={() => void action(() => onDismiss(operation.id))}
          >
            Закрыть ошибку {operation.id}
          </AppButton>
        </>
      )}
      {error && (
        <p ref={errorRef} tabIndex={-1} role="alert">
          {error}
        </p>
      )}
    </fieldset>
  );
}
