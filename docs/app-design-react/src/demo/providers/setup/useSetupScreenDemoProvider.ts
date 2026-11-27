import { useEffect, useRef, useState } from "react";
import { navigateHash } from "../../../ui/app/hashLocation";
import { meloTrovePaths } from "../../../ui/app/MeloTroveRoute";
import { settingsMessages } from "../../../ui/features/settings/settingsFormMessages";
import type { SetupScreenViewProps } from "../../../ui/features/setup/SetupScreenView";
import type { SetupToolCard } from "../../../ui/features/setup/SetupToolCardsView";
import { setupMessages } from "../../../ui/features/setup/setupMessages";
import { useSetupTriggerFocus } from "../../../ui/features/setup/useSetupTriggerFocus";
import {
  browseDemoDirectory,
  demoDirectoryStart,
} from "../../fixtures/directoryBrowseDemo";
import { useI18n } from "../demoPreferences";
import {
  isValidMusicBrainzConfiguration,
  musicBrainzConfigurationFingerprint,
} from "../settings/providerValidation";
import type {
  DemoSettings,
  SettingsScreenProps,
} from "../settings/SettingsScreen";
import {
  type DemoInstallPreflight,
  initialDemoUnmanagedToolFiles,
  installPreflightDemo,
  isAbsoluteDemoServerPath,
} from "../settings/ToolLifecycleDemo";
import { useGitHubTokenDemo } from "../tools/useGitHubTokenDemo";
import type { SetupDraftSnapshot } from "./setupDraft";
import {
  type SetupToolKind,
  setupToolExecutables,
  setupToolReleases,
} from "./setupToolsLifecycle";
import {
  setupGenreCatalogEndpoint,
  useSetupGenreCatalogDemoProvider,
} from "./useSetupGenreCatalogDemoProvider";
import type { SetupToolDownloadActivity } from "./useSetupToolDownloadsDemoProvider";

/** Demo scheduling: the in-memory connection check must remain visibly busy. */
export const demoMusicBrainzCheckDelayMs = 400;

type NoticeKey =
  | "toolsDirectoryRequired"
  | "missingPath"
  | "missingTools"
  | "missingPublication"
  | "missingGenres"
  | "musicBrainzUnavailable"
  | "summaryChecked"
  | "staleMove"
  | "invalidProvider";

type SetupAcoustIDNotice = "saved" | "deleted";

/**
 * The AcoustID draft lives only while the publication step is mounted. The
 * provider keeps the state, but resets it exactly as the former step-local
 * component did when the step is left or the platform check blocks the form.
 */
function useSetupAcoustIDDraftProvider({
  active,
  hasKey,
  verified,
  onChanged,
}: {
  active: boolean;
  hasKey: boolean;
  verified: boolean;
  onChanged: (change: { hasKey: boolean; verified: boolean }) => void;
}) {
  const [draft, setDraft] = useState("");
  const [notice, setNotice] = useState<SetupAcoustIDNotice>();
  useEffect(() => {
    if (active) return;
    setDraft("");
    setNotice(undefined);
  }, [active]);
  const changeKey = (next: { hasKey: boolean; verified: boolean }) => {
    onChanged(next);
    setDraft("");
    setNotice(next.hasKey ? "saved" : "deleted");
  };
  return {
    hasKey,
    verified,
    draft,
    notice,
    onDraftChange: setDraft,
    onSave: () => {
      if (!draft) return;
      // The prototype has no network: the save action stands in for a
      // successful AcoustID API check of the write-only draft.
      changeKey({ hasKey: true, verified: true });
    },
    onDelete: () => changeKey({ hasKey: false, verified: false }),
  };
}

export type SetupScreenProps = SettingsScreenProps & {
  /** Step requested by the browser URL, when it names one. */
  step?: number;
  /** Tab-local step hint used when the URL does not name a step. */
  fallbackStep?: number;
  /** Route integration: clamp the requested step and canonicalize the URL. */
  stepSync?: boolean;
  onStepChange?: (step: number, options?: { replace?: boolean }) => void;
  /** Current tool activity supplied by the prototype data host, if available. */
  toolActivity?: Partial<SetupToolDownloadActivity>;
  initialDraft?: Pick<
    SetupDraftSnapshot,
    "toolsDirectory" | "lrclib" | "publicationAutoCompleteAlbums"
  >;
  onDraftChange?: (draft: {
    toolsDirectory?: string;
    lrclib?: boolean;
    publicationAutoCompleteAlbums?: boolean;
  }) => void;
  /** Endpoint whose genre catalog was already loaded in this tab. */
  initialGenreCatalogEndpoint?: string;
  onGenreCatalogReady?: (endpoint: string) => void;
};

/**
 * Demo provider for Setup. Owns step selection, validation, tool catalog
 * state, directory checks and transient drafts; the view owns all markup.
 */
export function useSetupScreenDemoProvider({
  settings,
  onSettings,
  step: savedStep,
  fallbackStep,
  stepSync = false,
  onStepChange,
  toolActivity,
  initialDraft,
  onDraftChange,
  initialGenreCatalogEndpoint,
  onGenreCatalogReady,
}: SetupScreenProps): SetupScreenViewProps {
  const { locale } = useI18n();
  const copy = settingsMessages[locale];
  const setupCopy = setupMessages[locale];
  const [localStep, setLocalStep] = useState(savedStep ?? fallbackStep ?? 0);
  const [seenRouteStep, setSeenRouteStep] = useState(savedStep);
  if (savedStep !== seenRouteStep) {
    setSeenRouteStep(savedStep);
    if (savedStep !== undefined) setLocalStep(savedStep);
  }
  const [toolsDirectory, setToolsDirectory] = useState(
    initialDraft?.toolsDirectory ?? settings.toolsPath,
  );
  const [notice, setNotice] = useState<NoticeKey | "">("");
  const lrclibDefault = initialDraft?.lrclib ?? settings.lrclibEnabled ?? true;
  const [lrclibDraft, setLrclibDraft] = useState(lrclibDefault);
  const autoCompleteAlbumsDefault =
    initialDraft?.publicationAutoCompleteAlbums ??
    settings.publicationAutoCompleteAlbums ??
    false;
  const [autoCompleteAlbumsDraft, setAutoCompleteAlbumsDraft] = useState(
    autoCompleteAlbumsDefault,
  );
  const [musicBrainzChanged, setMusicBrainzChanged] = useState(false);
  const [musicBrainzInvalid, setMusicBrainzInvalid] = useState(false);
  const [musicBrainzVerificationPending, setMusicBrainzVerificationPending] =
    useState(false);
  const checkTimer = useRef<ReturnType<typeof setTimeout> | undefined>(
    undefined,
  );
  const urlEdited = useRef(false);
  const latestSettings = useRef(settings);
  latestSettings.current = settings;
  const [installPlan, setInstallPlan] = useState<DemoInstallPreflight | null>(
    null,
  );
  const [installPlanChanged, setInstallPlanChanged] = useState(false);
  const installTriggerFocus = useSetupTriggerFocus();
  const update = (values: Partial<DemoSettings>) =>
    onSettings(
      values.hasAcoustIDApplicationKey === undefined
        ? { ...settings, ...values }
        : {
            ...settings,
            ...values,
            // A direct key replacement keeps the tick only when the caller
            // also records the mocked API check.
            acoustIDVerified:
              values.acoustIDVerified === true &&
              values.hasAcoustIDApplicationKey === true,
          },
    );
  const toolSelection = settings.selectedRelease ?? {};
  const installations = settings.installations ?? [];
  const unmanagedToolFiles =
    settings.unmanagedToolFiles ?? initialDemoUnmanagedToolFiles;
  const activeInstallation = (kind: SetupToolKind) =>
    installations.find(
      (item) => item.package === kind && item.status === "active",
    );
  const verifiedInstallation = (kind: SetupToolKind) => {
    const active = activeInstallation(kind);
    const selected = toolSelection[kind];
    if (
      active &&
      selected &&
      settings.setupVerified?.[kind] === selected &&
      active.release === selected
    ) {
      return active;
    }
    return undefined;
  };
  const ffmpeg = verifiedInstallation("ffmpeg");
  const fpcalc = verifiedInstallation("fpcalc");
  const toolsReady = Boolean(ffmpeg && fpcalc);
  const platformMismatch = settings.platformMismatch ?? false;
  const outputPath = settings.outputCandidate;
  const format =
    settings.publicationFormat === "original"
      ? "source"
      : (settings.publicationFormat ?? "");
  const musicBrainzMode = settings.musicBrainzMode ?? "";
  const musicBrainzURL = settings.musicBrainzURL ?? "";
  const providerReady = isValidMusicBrainzConfiguration({
    mode: musicBrainzMode,
    url: musicBrainzURL,
  });
  const providerFingerprint = musicBrainzConfigurationFingerprint({
    mode: musicBrainzMode,
    url: musicBrainzURL,
    throttle: settings.musicBrainzThrottle,
    delay: settings.musicBrainzDelay,
  });
  const providerVerified =
    settings.musicBrainzVerified === true &&
    settings.musicBrainzVerifiedFingerprint === providerFingerprint;
  const publicationReady = Boolean(
    outputPath &&
      format &&
      settings.outputChecked &&
      providerReady &&
      providerVerified,
  );
  const publicationAutoCompleteAlbumsDirty =
    autoCompleteAlbumsDraft !==
    (settings.publicationAutoCompleteAlbums ?? false);
  useEffect(
    () => () => {
      if (checkTimer.current !== undefined) {
        clearTimeout(checkTimer.current);
        checkTimer.current = undefined;
      }
    },
    [],
  );
  /**
   * The demo check performs no network work. It only settles a tab-local
   * proof for the exact mode, endpoint and request settings that were on
   * screen when it started.
   */
  const checkProvider = (mode: "" | "public" | "self-hosted", url: string) => {
    if (platformMismatch || checkTimer.current !== undefined) return;
    if (mode === "") return;
    if (!isValidMusicBrainzConfiguration({ mode, url })) {
      setMusicBrainzInvalid(true);
      setMusicBrainzChanged(true);
      return;
    }
    setMusicBrainzInvalid(false);
    setMusicBrainzChanged(true);
    setMusicBrainzVerificationPending(true);
    const fingerprint = musicBrainzConfigurationFingerprint({
      mode,
      url,
      throttle: latestSettings.current.musicBrainzThrottle,
      delay: latestSettings.current.musicBrainzDelay,
    });
    checkTimer.current = setTimeout(() => {
      checkTimer.current = undefined;
      setMusicBrainzVerificationPending(false);
      const current = latestSettings.current;
      const currentFingerprint = musicBrainzConfigurationFingerprint({
        mode: current.musicBrainzMode ?? "",
        url: current.musicBrainzURL ?? "",
        throttle: current.musicBrainzThrottle,
        delay: current.musicBrainzDelay,
      });
      if (currentFingerprint !== fingerprint) return;
      setMusicBrainzChanged(false);
      onSettings({
        ...current,
        musicBrainzVerified: true,
        musicBrainzVerifiedFingerprint: fingerprint,
      });
    }, demoMusicBrainzCheckDelayMs);
  };
  const genreCatalogEndpoint = setupGenreCatalogEndpoint(
    musicBrainzMode,
    musicBrainzURL,
  );
  const genreCatalog = useSetupGenreCatalogDemoProvider({
    endpoint: genreCatalogEndpoint,
    // A persisted catalog is proof only while the same configuration is also
    // known to have passed the separate connection check.
    initialReadyEndpoint: providerVerified
      ? initialGenreCatalogEndpoint
      : undefined,
  });
  const genreCatalogReady = providerVerified && genreCatalog.state === "ready";

  useEffect(() => {
    if (!genreCatalogReady || !genreCatalogEndpoint) return;
    onGenreCatalogReady?.(genreCatalogEndpoint);
  }, [genreCatalogEndpoint, genreCatalogReady, onGenreCatalogReady]);

  const nextTool = (["ffmpeg", "fpcalc"] as const).find(
    (kind) => !verifiedInstallation(kind),
  );

  const routed = stepSync;
  const requestedStep = localStep;
  // A URL names a destination, not proof. Keep the wizard at the first step
  // that the tab-locally restored evidence actually reaches.
  const pathCommitted =
    settings.pathChecked && toolsDirectory.trim() === settings.toolsPath.trim();
  const readyStep =
    platformMismatch || !pathCommitted
      ? 0
      : !toolsReady
        ? 1
        : !(publicationReady && genreCatalogReady) ||
            publicationAutoCompleteAlbumsDirty
          ? 2
          : 3;
  const step = routed ? Math.min(requestedStep, readyStep) : requestedStep;

  useEffect(() => {
    if (!routed) return;
    if (savedStep !== undefined && step === savedStep) return;
    onStepChange?.(step, { replace: true });
  }, [onStepChange, routed, savedStep, step]);

  const setStep = (nextStep: number) => {
    if (nextStep === 2) {
      const lrclib = settings.lrclibEnabled ?? true;
      setLrclibDraft(lrclib);
      onDraftChange?.({ lrclib });
    }
    setNotice("");
    setLocalStep(nextStep);
    onStepChange?.(nextStep);
  };
  const changeToolsDirectory = (value: string) => {
    setToolsDirectory(value);
    onDraftChange?.({ toolsDirectory: value });
  };
  const changeLrclibDraft = (enabled: boolean) => {
    setLrclibDraft(enabled);
    onDraftChange?.({ lrclib: enabled });
  };
  // The checkbox only records a tab-local draft; Continue commits it so
  // automatic matching cannot start from an unconfirmed click.
  const changeAutoCompleteAlbumsDraft = (enabled: boolean) => {
    setAutoCompleteAlbumsDraft(enabled);
    onDraftChange?.({ publicationAutoCompleteAlbums: enabled });
  };
  const chooseOutputPath = (path: string) => {
    const candidate = path.trim();
    const accepted = isAbsoluteDemoServerPath(candidate);
    update({
      outputCandidate: candidate,
      outputChecked: accepted,
      musicBrainzVerified: false,
    });
    setNotice(accepted ? "" : "missingPublication");
  };
  const preflightInstall = (kind: SetupToolKind, release: string) =>
    // Mirror the server: a clean preflight starts directly, and only exact
    // conflicts retain a plan for explicit approval.
    installPreflightDemo(
      settings.toolsPath,
      kind,
      release,
      installations,
      unmanagedToolFiles,
    );
  const beginInstall = (preflight: DemoInstallPreflight) => {
    // The host owns the admitted download lifecycle and settles settings only
    // while the frozen root and token still match the current state.
    toolActivity?.start?.(preflight);
    setInstallPlan(null);
    setNotice("");
  };
  const requestInstall = (kind: SetupToolKind, target: EventTarget | null) => {
    const release = toolSelection[kind];
    if (!release || platformMismatch || installPlan) return;
    if (!isAbsoluteDemoServerPath(settings.toolsPath)) {
      setNotice("missingPath");
      return;
    }
    setInstallPlanChanged(false);
    installTriggerFocus.capture(target);
    const preflight = preflightInstall(kind, release);
    if (!preflight.conflicts.length) {
      beginInstall(preflight);
      return;
    }
    setInstallPlan(preflight);
  };
  const confirmInstall = () => {
    if (!installPlan) return;
    const release = toolSelection[installPlan.kind];
    const fresh =
      !platformMismatch &&
      release &&
      isAbsoluteDemoServerPath(settings.toolsPath)
        ? preflightInstall(installPlan.kind, release)
        : undefined;
    if (!fresh || fresh.token !== installPlan.token) {
      setInstallPlanChanged(true);
      return;
    }
    beginInstall(fresh);
  };
  const cancelInstall = () => {
    setInstallPlan(null);
    setInstallPlanChanged(false);
    installTriggerFocus.restore();
  };
  const next = () => {
    if (platformMismatch) return;
    if (step === 0) {
      const candidate = toolsDirectory.trim();
      if (!isAbsoluteDemoServerPath(candidate)) {
        setNotice(candidate ? "missingPath" : "toolsDirectoryRequired");
        return;
      }
      changeToolsDirectory(candidate);
      if (candidate !== settings.toolsPath || !settings.pathChecked) {
        update({
          toolsPath: candidate,
          pathChecked: true,
          ...(candidate !== settings.toolsPath ? { setupVerified: {} } : {}),
        });
      }
    } else if (step === 1) {
      if (!toolsReady) {
        setNotice("missingTools");
        return;
      }
    } else if (step === 2) {
      const validOutput = isAbsoluteDemoServerPath(outputPath.trim());
      const validProvider = providerReady;
      if (!validProvider && musicBrainzMode === "self-hosted") {
        setNotice("invalidProvider");
        return;
      }
      if (!validOutput || !validProvider || !format) {
        setNotice("missingPublication");
        return;
      }
      if (!providerVerified) {
        setNotice("musicBrainzUnavailable");
        return;
      }
      if (!genreCatalogReady) {
        setNotice("missingGenres");
        return;
      }
      // Continue only commits the step. Connection and catalog proof were
      // earned by their own checks and are not re-created here.
      update({
        outputChecked: true,
        genresLoaded: true,
        lrclibEnabled: lrclibDraft,
        publicationAutoCompleteAlbums: autoCompleteAlbumsDraft,
      });
      setStep(3);
      return;
    } else {
      if (!genreCatalogReady) {
        setNotice("missingGenres");
        return;
      }
      if (
        !settings.pathChecked ||
        !toolsReady ||
        !publicationReady ||
        !isAbsoluteDemoServerPath(outputPath.trim())
      ) {
        setNotice("staleMove");
        return;
      }
      update({ currentOutput: outputPath });
      navigateHash(meloTrovePaths.home);
      return;
    }
    setStep(Math.min(step + 1, 3));
  };
  const canProceed =
    step === 0
      ? true
      : step === 1
        ? toolsReady
        : step === 2
          ? Boolean(
              outputPath &&
                musicBrainzMode &&
                format &&
                providerVerified &&
                genreCatalogReady,
            )
          : settings.pathChecked &&
            toolsReady &&
            publicationReady &&
            genreCatalogReady;
  const noticeText =
    notice === "summaryChecked"
      ? locale === "ru"
        ? "Настройки прототипа проверены."
        : "Prototype selections checked."
      : notice === "toolsDirectoryRequired"
        ? setupCopy.toolsDirectoryRequired
        : notice === "musicBrainzUnavailable"
          ? setupCopy.musicBrainzUnavailable
          : notice
            ? copy[notice]
            : "";

  const acoustID = useSetupAcoustIDDraftProvider({
    active: step === 2 && !platformMismatch,
    hasKey: settings.hasAcoustIDApplicationKey ?? false,
    verified:
      settings.hasAcoustIDApplicationKey === true &&
      settings.acoustIDVerified === true,
    onChanged: ({ hasKey, verified }) =>
      update({
        hasAcoustIDApplicationKey: hasKey,
        acoustIDVerified: verified,
      }),
  });
  const githubToken = useGitHubTokenDemo(
    {
      locale,
      hasGitHubToken: settings.hasGitHubToken ?? false,
      onGitHubTokenChange: (hasGitHubToken) => update({ hasGitHubToken }),
    },
    step === 1,
  );
  // Only the portable activity fields belong to the view model; the host's
  // start and duplicate-package authority stay out of the presentation tree.
  const toolActivityView = toolActivity
    ? {
        operations: toolActivity.operations,
        completed: toolActivity.completed,
        operationError: toolActivity.operationError,
        loadingOperations: toolActivity.loadingOperations,
      }
    : undefined;

  return {
    locale,
    wizard: {
      step,
      help: [
        setupCopy.toolsDirectoryHelp,
        setupCopy.toolsHelp,
        `${setupCopy.publicationHelp} ${setupCopy.metadataHelp}`,
        locale === "ru"
          ? "Итог составлен по выбранным данным прототипа. Если что-то не готово, вернитесь к нужному шагу."
          : "The summary uses the prototype’s selections. If anything is not ready, return to the relevant step.",
      ][step],
      error: platformMismatch
        ? copy.conflicts
        : step === 0
          ? noticeText || undefined
          : undefined,
      notice: step === 0 ? undefined : noticeText || undefined,
      nextDisabled: !canProceed || platformMismatch,
      onBack: () => setStep(step - 1),
      onNext: next,
    },
    directories: {
      kind: "tools",
      selected: settings.pathChecked && toolsDirectory === settings.toolsPath,
      fields: [
        {
          kind: "tools",
          value: toolsDirectory,
          onSelect: changeToolsDirectory,
        },
      ],
      locale,
      browse: browseDemoDirectory,
      resolveInitialPath: demoDirectoryStart,
    },
    tools: {
      ...toolActivityView,
      description: copy.toolDescription,
      cards: {
        locale,
        tools: (["ffmpeg", "fpcalc"] as const).map((kind): SetupToolCard => {
          const verified = verifiedInstallation(kind);
          if (verified) {
            return {
              kind,
              state: "ready",
              versions: [
                {
                  id: verified.id,
                  version: verified.release,
                  active: verified.status === "active",
                },
              ],
            };
          }
          const selected = toolSelection[kind];
          const pendingDownload = toolActivity?.busyKinds?.has(kind) ?? false;
          return {
            kind,
            state: "install",
            releases: setupToolReleases[kind].map((identity) => ({
              identity,
            })),
            selected: selected ?? null,
            pending: pendingDownload,
            onSelect: (identity) =>
              update({
                selectedRelease: {
                  ...toolSelection,
                  [kind]: identity ?? undefined,
                },
                setupVerified: {
                  ...settings.setupVerified,
                  [kind]: undefined,
                },
                setupFailures: {
                  ...settings.setupFailures,
                  [kind]: undefined,
                },
              }),
            action: {
              disabled:
                !selected ||
                platformMismatch ||
                Boolean(installPlan) ||
                pendingDownload,
              onPress: (event) => requestInstall(kind, event.target),
            },
            verification: { state: "available" },
            executables: setupToolExecutables(kind),
          };
        }),
        nextTool: toolsReady ? undefined : nextTool,
      },
      confirmation: installPlan
        ? {
            title: setupCopy.confirmOverwrite,
            packageName: setupCopy[installPlan.kind],
            release: installPlan.release,
            executables: setupToolExecutables(installPlan.kind),
            conflicts: installPlan.conflicts,
            confirmLabel: setupCopy.confirmOverwriteButton,
            cancelLabel: setupCopy.cancel,
            error: installPlanChanged ? copy.staleInstall : undefined,
            onConfirm: confirmInstall,
            onCancel: cancelInstall,
          }
        : undefined,
      token: githubToken,
    },
    publication: {
      directory: {
        kind: "output",
        fields: [
          {
            kind: "output",
            value: outputPath,
            onSelect: chooseOutputPath,
          },
        ],
        locale,
        browse: browseDemoDirectory,
        resolveInitialPath: demoDirectoryStart,
      },
      options: {
        locale,
        format,
        autoCompleteAlbums: autoCompleteAlbumsDraft,
        removeSources: settings.sourceCleanupEnabled ?? false,
        onFormat: (value) =>
          update({
            publicationFormat: value === "source" ? "original" : value,
          }),
        onAutoCompleteAlbums: changeAutoCompleteAlbumsDraft,
        onRemoveSources: (sourceCleanupEnabled) =>
          update({ sourceCleanupEnabled }),
      },
      metadata: {
        providers: {
          locale,
          mode: musicBrainzMode,
          baseURL: musicBrainzURL,
          verification: providerVerified
            ? "verified"
            : musicBrainzChanged
              ? "changed"
              : "untested",
          verificationError: musicBrainzInvalid
            ? copy.invalidProvider
            : undefined,
          verificationPending: musicBrainzVerificationPending,
          disabled: platformMismatch || musicBrainzVerificationPending,
          lrclib: { enabled: lrclibDraft, onChange: changeLrclibDraft },
          onVerify: () => {
            urlEdited.current = false;
            checkProvider(musicBrainzMode, musicBrainzURL);
          },
          onBaseURLBlur: () => {
            if (!urlEdited.current || musicBrainzMode !== "self-hosted") return;
            urlEdited.current = false;
            if (!musicBrainzURL.trim()) return;
            checkProvider("self-hosted", musicBrainzURL);
          },
          onModeChange: (mode) => {
            urlEdited.current = false;
            setMusicBrainzInvalid(false);
            setMusicBrainzChanged(true);
            update({ musicBrainzMode: mode, musicBrainzVerified: false });
            // The public service proves itself as soon as it is chosen; a
            // self-hosted address waits until the user finishes editing it.
            if (mode === "public") checkProvider("public", "");
          },
          onBaseURLChange: (musicBrainzURL) => {
            urlEdited.current = true;
            setMusicBrainzInvalid(false);
            setMusicBrainzChanged(true);
            update({ musicBrainzURL, musicBrainzVerified: false });
          },
        },
        acoustID: platformMismatch ? undefined : { locale, ...acoustID },
      },
      genres: {
        locale,
        state: genreCatalog.state,
        count: genreCatalog.count,
        disabled:
          musicBrainzMode === "" || platformMismatch || !providerVerified,
        requiresVerification: !providerVerified,
        // Loading the catalog never doubles as a connection check: it only
        // reads the fixture for the already verified configuration.
        onLoad: () => {
          if (!providerVerified) return;
          genreCatalog.load();
        },
      },
    },
    summary: {
      locale,
      status: {
        healthy:
          !platformMismatch &&
          settings.pathChecked &&
          toolsReady &&
          publicationReady &&
          genreCatalogReady &&
          isAbsoluteDemoServerPath(outputPath.trim()),
        problems: [
          ...(platformMismatch ? [copy.conflicts] : []),
          ...(!settings.pathChecked ? [copy.missingPath] : []),
          ...(!toolsReady ? [copy.missingTools] : []),
          ...(!publicationReady || !isAbsoluteDemoServerPath(outputPath.trim())
            ? [copy.missingPublication]
            : []),
          ...(!genreCatalogReady ? [copy.missingGenres] : []),
        ],
      },
      values: {
        publication: {
          directory: outputPath || setupCopy.notSpecified,
          format: format || "unselected",
          explanation:
            format === "source"
              ? setupCopy.sourceFormatExplanation
              : format === "mka"
                ? setupCopy.mkaExplanation
                : format === "m4a"
                  ? setupCopy.m4aExplanation
                  : "",
          removeSources: settings.sourceCleanupEnabled ?? false,
          autoCompleteAlbums: settings.publicationAutoCompleteAlbums ?? false,
        },
        tools: {
          ffmpeg: ffmpeg
            ? `${setupCopy.ready}${ffmpeg.release}`
            : setupCopy.unknownToolState,
          fpcalc: fpcalc
            ? `${setupCopy.ready}${fpcalc.release}`
            : setupCopy.unknownToolState,
        },
        metadata: {
          lrclibEnabled: settings.lrclibEnabled ?? true,
          hasAcoustIdKey: settings.hasAcoustIDApplicationKey ?? false,
          musicbrainz: providerVerified
            ? `${setupCopy.connectionChecked}${musicBrainzMode === "self-hosted" ? musicBrainzURL : setupCopy.publicService}`
            : setupCopy.connectionNotChecked,
          genres: genreCatalogReady
            ? setupCopy.genreCatalogLoaded
            : setupCopy.genreCatalogMissing,
        },
        technical: {
          platform: setupCopy.noData,
          toolsDirectory: settings.toolsPath || setupCopy.notSpecified,
          musicbrainzCheckedAt: setupCopy.noData,
          ffmpegCheckedAt: setupCopy.noData,
          fpcalcCheckedAt: setupCopy.noData,
        },
        showNextStep: true,
      },
    },
    summaryRecheck: {
      label: setupCopy.updateCheck,
      disabled: platformMismatch,
      onPress: () => {
        if (!providerVerified) {
          setStep(2);
          setNotice("musicBrainzUnavailable");
          return;
        }
        if (!genreCatalogReady) {
          setStep(2);
          setNotice("missingGenres");
          return;
        }
        setNotice("summaryChecked");
      },
    },
  };
}
