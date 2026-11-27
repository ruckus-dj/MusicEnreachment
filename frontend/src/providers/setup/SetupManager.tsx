import {
  type ReactNode,
  useCallback,
  useEffect,
  useRef,
  useState,
} from "react";
import {
  checkSetupPaths,
  completeSetup,
  getSetupState,
  saveSetupRuntime,
} from "../../api/generated/client";
import type {
  SaveRuntimeBodyPublicationFormat,
  SetupStateBody,
  UpdateMusicBrainzBodyMode,
} from "../../api/generated/client.schemas";
import { useI18n } from "../../app/i18n/host";
import { useHashObservation } from "../../app/navigation/hashLocation";
import { pushHashState, replaceHashState } from "../../ui/app/hashLocation";
import {
  MeloTroveSetupBinder,
  MeloTroveSetupContent,
} from "../../ui/app/MeloTroveProvider";
import { SetupScreenView } from "../../ui/features/setup/SetupScreenView";
import {
  type SetupFeedback,
  setupFeedback,
  setupFeedbackText,
  setupHttpError,
  setupMessage,
  setupMessages,
} from "../../ui/features/setup/setupMessages";
import {
  isSetupHash,
  setupStepHref,
  setupStepIndex,
  setupSteps,
} from "../../ui/features/setup/setupStepRoute";
import { browseServerDirectory } from "../components/DirectoryPicker";
import {
  type SetupMetadataAction,
  SetupMetadataBinder,
  type SetupMetadataDraft,
  type SetupMusicBrainzFailure,
} from "./SetupMetadata";
import { SetupSummaryBinder } from "./SetupSummary";
import { SetupToolsBinder } from "./SetupTools";
import {
  clearSetupDraft,
  readSetupDraft,
  type SetupDraft,
  type SetupDraftProvider,
  type SetupDraftToolKind,
  setupDraftEntry,
  setupDraftURL,
  setupDraftValue,
  setupDraftVersion,
  writeSetupDraft,
} from "./setupDraft";
import { useSetupGenreCatalogProvider } from "./useSetupGenreCatalogProvider";

const stepKey = "melotrove.setup.step";
function earliest(state: SetupStateBody) {
  const s = state.settings;
  if (!s.tools_directory) return 0;
  if (!s.active_ffmpeg_installation_id || !s.active_fpcalc_installation_id)
    return 1;
  if (
    !s.output_directory ||
    !knownFormat(s.publication_format) ||
    !s.musicbrainz_verified_at
  )
    return 2;
  if (!state.genre_catalog.ready || !state.musicbrainz_configuration_identity)
    return 2;
  return 3;
}
function storedStep() {
  try {
    const stored = window.localStorage.getItem(stepKey);
    const step = (setupSteps as readonly string[]).indexOf(stored ?? "");
    return step >= 0 ? step : undefined;
  } catch {
    return undefined;
  }
}
function fallbackStep(state: SetupStateBody) {
  const readyStep = earliest(state);
  // Verified MusicBrainz plus saved output does not prove that the later
  // LRCLIB write succeeded. Without a hint, revisit Publication.
  return Math.min(storedStep() ?? Math.min(readyStep, 2), readyStep);
}
function rememberStep(step: number) {
  try {
    window.localStorage.setItem(stepKey, setupSteps[step]);
  } catch {
    // The current step is still available for this page session.
  }
}
function setupMode(value: string): UpdateMusicBrainzBodyMode {
  return value === "self-hosted" ? "self-hosted" : "public";
}
function providerDraftMatches(
  provider: SetupDraftProvider,
  state: SetupStateBody,
) {
  const savedBaseURL = setupDraftURL(state.settings.musicbrainz_base_url);
  return (
    savedBaseURL !== undefined &&
    provider.identity === state.musicbrainz_configuration_identity &&
    provider.savedMode === setupMode(state.settings.musicbrainz_mode) &&
    provider.savedBaseURL === savedBaseURL
  );
}
function hasPendingMetadataDraft(
  state: SetupStateBody,
  draft: SetupDraft,
): boolean {
  const metadata = draft.metadata;
  if (!metadata) return false;
  const provider = metadata.provider;
  if (provider && providerDraftMatches(provider, state)) {
    const savedBaseURL = setupDraftURL(state.settings.musicbrainz_base_url);
    if (provider.mode !== setupMode(state.settings.musicbrainz_mode))
      return true;
    if (savedBaseURL === undefined || provider.baseURL !== savedBaseURL)
      return true;
  }
  const lrclib = setupDraftValue(
    metadata.lrclib,
    state.settings.lrclib_enabled,
  );
  return lrclib !== undefined && lrclib !== state.settings.lrclib_enabled;
}
function hasPendingPublicationDraft(
  state: SetupStateBody,
  draft: SetupDraft,
): boolean {
  const tools = setupDraftValue(
    draft.directories?.tools,
    state.settings.tools_directory,
  );
  if (tools !== undefined && tools !== state.settings.tools_directory)
    return true;
  const output = setupDraftValue(
    draft.directories?.output,
    state.settings.output_directory,
  );
  if (output !== undefined && output !== state.settings.output_directory)
    return true;
  const format = setupDraftValue(
    draft.publication?.format,
    state.settings.publication_format,
  );
  if (format !== undefined && format !== state.settings.publication_format)
    return true;
  const autoCompleteAlbums = setupDraftValue(
    draft.publication?.autoCompleteAlbums,
    state.settings.publication_auto_complete_albums,
  );
  if (
    autoCompleteAlbums !== undefined &&
    autoCompleteAlbums !== state.settings.publication_auto_complete_albums
  )
    return true;
  const removeSources = setupDraftValue(
    draft.publication?.removeSources,
    state.settings.publication_remove_sources,
  );
  return (
    removeSources !== undefined &&
    removeSources !== state.settings.publication_remove_sources
  );
}
/**
 * Resolves a step request against saved readiness and unsaved form drafts.
 * A direct Summary URL works only when the server proof and any applicable
 * drafts are settled; otherwise it canonicalises to Publication.
 */
function routeTarget(
  state: SetupStateBody,
  requested: number | undefined,
  draft: SetupDraft,
  providerDirty: boolean,
) {
  const readyStep = earliest(state);
  const candidate = requested ?? fallbackStep(state);
  const summaryBlocked =
    providerDirty ||
    hasPendingMetadataDraft(state, draft) ||
    hasPendingPublicationDraft(state, draft);
  const bounded =
    candidate === 3 && summaryBlocked ? Math.min(readyStep, 2) : candidate;
  return Math.max(0, Math.min(bounded, readyStep));
}
function currentSetupHashStep() {
  if (typeof window === "undefined") return undefined;
  const hash = window.location.hash;
  return isSetupHash(hash) ? setupStepIndex(hash) : undefined;
}
function knownFormat(
  value: string,
): SaveRuntimeBodyPublicationFormat | undefined {
  return value === "source" || value === "mka" || value === "m4a"
    ? value
    : undefined;
}

export function SetupManager({
  onCompleted,
  initialState,
  children,
}: {
  onCompleted: () => void;
  initialState: SetupStateBody;
  children?: ReactNode;
}) {
  const { locale } = useI18n();
  const messages = setupMessages[locale];
  const observation = useHashObservation();
  const hash = observation.hash;
  const [restoredDraft] = useState(() => readSetupDraft());
  const draftRef = useRef<SetupDraft>(
    restoredDraft ?? { version: setupDraftVersion },
  );
  const [state, setState] = useState(initialState);
  const [verificationFailure, setVerificationFailure] =
    useState<SetupMusicBrainzFailure | null>(null);
  const notifiedCompletion = useRef(false);
  useEffect(() => {
    if (!state.completed || notifiedCompletion.current) return;
    notifiedCompletion.current = true;
    try {
      window.localStorage.removeItem(stepKey);
    } catch {
      // Setup completed on the server even if storage is unavailable.
    }
    clearSetupDraft();
    onCompleted();
  }, [onCompleted, state.completed]);
  const [step, setStep] = useState(() =>
    routeTarget(
      initialState,
      isSetupHash(hash) ? setupStepIndex(hash) : undefined,
      draftRef.current,
      false,
    ),
  );
  const [toolsDirectory, setToolsDirectory] = useState(
    () =>
      setupDraftValue(
        restoredDraft?.directories?.tools,
        initialState.settings.tools_directory,
      ) ?? initialState.settings.tools_directory,
  );
  const [outputDirectory, setOutputDirectory] = useState(
    () =>
      setupDraftValue(
        restoredDraft?.directories?.output,
        initialState.settings.output_directory,
      ) ?? initialState.settings.output_directory,
  );
  const [format, setFormat] = useState<SaveRuntimeBodyPublicationFormat | "">(
    () =>
      setupDraftValue(
        restoredDraft?.publication?.format,
        initialState.settings.publication_format,
      ) ??
      knownFormat(initialState.settings.publication_format) ??
      "",
  );
  const [autoCompleteDraft, setAutoCompleteDraft] = useState(
    () => restoredDraft?.publication?.autoCompleteAlbums,
  );
  const autoCompleteAlbums =
    setupDraftValue(
      autoCompleteDraft,
      state.settings.publication_auto_complete_albums,
    ) ?? state.settings.publication_auto_complete_albums;
  const [removeSources, setRemoveSources] = useState(
    () =>
      setupDraftValue(
        restoredDraft?.publication?.removeSources,
        initialState.settings.publication_remove_sources,
      ) ?? initialState.settings.publication_remove_sources,
  );
  const [toolSelection, setToolSelection] = useState<
    Partial<Record<SetupDraftToolKind, string>>
  >(() => restoredDraft?.tools ?? {});
  const [pending, setPending] = useState(false);
  const [toolsBlocked, setToolsBlocked] = useState(false);
  const [error, setError] = useState<SetupFeedback | null>(null);
  const catalogError = useRef<SetupFeedback | null>(null);
  const [notice, setNotice] = useState<SetupFeedback | null>(null);
  const [summaryRefreshToken, setSummaryRefreshToken] = useState(0);
  const actionInFlight = useRef(false);
  const metadataAction = useRef<SetupMetadataAction | null>(null);
  const [metadataBusy, setMetadataBusy] = useState(false);
  const [providerDirty, setProviderDirty] = useState(false);
  const summaryIdentity = useRef<string | undefined>(
    initialState.genre_catalog.ready
      ? initialState.musicbrainz_configuration_identity
      : undefined,
  );
  const stateRef = useRef(state);
  stateRef.current = state;
  const refreshSequence = useRef(0);
  const appliedRefresh = useRef(0);
  const stepRef = useRef(step);
  stepRef.current = step;
  const providerDirtyRef = useRef(providerDirty);
  providerDirtyRef.current =
    providerDirty ||
    (verificationFailure?.mode === setupMode(state.settings.musicbrainz_mode) &&
      verificationFailure.baseURL === state.settings.musicbrainz_base_url);
  function updateDraft(mutate: (draft: SetupDraft) => SetupDraft) {
    const next = mutate(draftRef.current);
    draftRef.current = next;
    writeSetupDraft(next);
  }
  function selectToolsDirectory(value: string) {
    setToolsDirectory(value);
    updateDraft((draft) => ({
      ...draft,
      directories: {
        ...draft.directories,
        tools: setupDraftEntry(
          value,
          stateRef.current.settings.tools_directory,
        ),
      },
    }));
  }
  function selectOutputDirectory(value: string) {
    setOutputDirectory(value);
    updateDraft((draft) => ({
      ...draft,
      directories: {
        ...draft.directories,
        output: setupDraftEntry(
          value,
          stateRef.current.settings.output_directory,
        ),
      },
    }));
  }
  function selectFormat(value: SaveRuntimeBodyPublicationFormat) {
    setFormat(value);
    updateDraft((draft) => ({
      ...draft,
      publication: {
        ...draft.publication,
        format: setupDraftEntry(
          value,
          stateRef.current.settings.publication_format,
        ),
      },
    }));
  }
  function selectAutoCompleteAlbums(value: boolean) {
    const entry = setupDraftEntry(
      value,
      stateRef.current.settings.publication_auto_complete_albums,
    );
    setAutoCompleteDraft(entry);
    updateDraft((draft) => ({
      ...draft,
      publication: {
        ...draft.publication,
        autoCompleteAlbums: entry,
      },
    }));
  }
  function selectRemoveSources(value: boolean) {
    setRemoveSources(value);
    updateDraft((draft) => ({
      ...draft,
      publication: {
        ...draft.publication,
        removeSources: setupDraftEntry(
          value,
          stateRef.current.settings.publication_remove_sources,
        ),
      },
    }));
  }
  function rememberMetadataDraft(values: SetupMetadataDraft) {
    const setup = stateRef.current;
    const settings = setup.settings;
    const savedMode = setupMode(settings.musicbrainz_mode);
    const savedBaseURL = setupDraftURL(settings.musicbrainz_base_url);
    const baseURL = setupDraftURL(values.baseURL);
    const providerChanged =
      values.mode !== savedMode ||
      savedBaseURL === undefined ||
      baseURL === undefined ||
      baseURL !== savedBaseURL;
    updateDraft((draft) => ({
      ...draft,
      metadata: {
        ...draft.metadata,
        provider:
          providerChanged && baseURL !== undefined && savedBaseURL !== undefined
            ? {
                identity: setup.musicbrainz_configuration_identity,
                savedMode,
                savedBaseURL,
                mode: values.mode,
                baseURL,
              }
            : undefined,
        lrclib: setupDraftEntry(values.lrclib, settings.lrclib_enabled),
      },
    }));
  }
  function rememberToolSelection(
    selection: Partial<Record<SetupDraftToolKind, string>>,
  ) {
    setToolSelection(selection);
    updateDraft((draft) => ({
      ...draft,
      tools: Object.keys(selection).length > 0 ? selection : undefined,
    }));
  }
  const updateToolsBlocked = useCallback((blocked: boolean) => {
    setToolsBlocked(blocked);
  }, []);
  function navigate(
    next: number,
    identity?: string,
    options?: { from?: number; replace?: boolean },
  ) {
    if (options?.from !== undefined) {
      const hashStep = currentSetupHashStep();
      if (
        stepRef.current !== options.from ||
        (hashStep !== undefined && hashStep !== options.from)
      )
        return false;
    }
    const target = Math.max(0, Math.min(next, setupSteps.length - 1));
    setError(null);
    setNotice(null);
    if (target === 3)
      summaryIdentity.current =
        identity ?? stateRef.current.musicbrainz_configuration_identity;
    setStep(target);
    stepRef.current = target;
    rememberStep(target);
    const href = setupStepHref(target);
    if (
      typeof window !== "undefined" &&
      isSetupHash(window.location.hash) &&
      window.location.hash !== href
    ) {
      if (options?.replace) replaceHashState(href);
      else pushHashState(href);
    }
    return true;
  }
  function stillOnStep(step: number) {
    const hashStep = currentSetupHashStep();
    return (
      stepRef.current === step && (hashStep === undefined || hashStep === step)
    );
  }
  // Adopt Back/Forward or a copied Setup URL. Server refreshes do not re-run
  // this effect, so an in-progress Summary proof keeps its existing handling.
  useEffect(() => {
    const currentHash = observation.hash;
    if (!isSetupHash(currentHash)) return;
    const target = routeTarget(
      stateRef.current,
      setupStepIndex(currentHash),
      draftRef.current,
      providerDirtyRef.current,
    );
    if (target === 3 && stepRef.current !== 3) {
      summaryIdentity.current =
        stateRef.current.musicbrainz_configuration_identity;
    }
    setStep(target);
    stepRef.current = target;
    rememberStep(target);
    const canonical = setupStepHref(target);
    if (currentHash !== canonical) replaceHashState(canonical);
  }, [observation]);
  const refresh = useCallback(
    async (shouldApply: () => boolean = () => true) => {
      const sequence = ++refreshSequence.current;
      const response = await getSetupState({ cache: "no-store" });
      if (sequence < appliedRefresh.current) return stateRef.current;
      if (response.status !== 200) throw setupHttpError(response);
      if (shouldApply()) {
        appliedRefresh.current = sequence;
        stateRef.current = response.data;
        setState(response.data);
      }
      return response.data;
    },
    [],
  );
  const reportCatalogError = useCallback((reason: unknown) => {
    const feedback = setupFeedback(reason, "unknownError");
    catalogError.current = feedback;
    setError(feedback);
  }, []);
  const clearCatalogError = useCallback(() => {
    const feedback = catalogError.current;
    catalogError.current = null;
    if (feedback)
      setError((current) => (current === feedback ? null : current));
  }, []);
  const verifiedIdentity = useCallback(
    () => metadataAction.current?.verifiedIdentity(),
    [],
  );
  const genreCatalog = useSetupGenreCatalogProvider({
    state,
    enabled: step === 2,
    verifiedIdentity,
    refresh,
    onError: reportCatalogError,
    onClearError: clearCatalogError,
  });
  async function advance() {
    if (
      actionInFlight.current ||
      (step === 2 &&
        (!metadataAction.current || metadataAction.current.isBusy()))
    )
      return;
    actionInFlight.current = true;
    setPending(true);
    setError(null);
    setNotice(null);
    const from = step;
    try {
      if (step === 0) {
        if (!toolsDirectory) throw setupMessage("toolsDirectoryRequired");
        const saved = await saveSetupRuntime({
          tools_directory: toolsDirectory,
        });
        if (saved.status !== 204) throw setupHttpError(saved);
        const fresh = await refresh(() => stillOnStep(from));
        if (!stillOnStep(from)) return;
        setToolsDirectory(fresh.settings.tools_directory);
      }
      if (step === 1) {
        const fresh = await refresh(() => stillOnStep(from));
        if (!stillOnStep(from)) return;
        if (
          !fresh.settings.active_ffmpeg_installation_id ||
          !fresh.settings.active_fpcalc_installation_id
        )
          throw setupMessage("activateTools");
      }
      if (step === 2) {
        if (!outputDirectory) throw setupMessage("outputDirectoryRequired");
        if (!format) throw setupMessage("chooseFormat");
        const expectedIdentity = state.musicbrainz_configuration_identity;
        if (providerDirty || !state.genre_catalog.ready || !expectedIdentity)
          throw setupMessage("genreCatalogRequired");
        const checked = await checkSetupPaths({
          tools_directory: state.settings.tools_directory,
          output_directory: outputDirectory,
        });
        if (checked.status !== 200) throw setupHttpError(checked);
        if (!stillOnStep(from)) return;
        const autoCompleteAlbumsDraft = setupDraftValue(
          draftRef.current.publication?.autoCompleteAlbums,
          stateRef.current.settings.publication_auto_complete_albums,
        );
        const saved = await saveSetupRuntime({
          output_directory: outputDirectory,
          publication_format: format,
          ...(autoCompleteAlbumsDraft !== undefined
            ? { publication_auto_complete_albums: autoCompleteAlbumsDraft }
            : {}),
          publication_remove_sources: removeSources,
        });
        if (saved.status !== 204) throw setupHttpError(saved);
        if (!stillOnStep(from)) return;
        const fresh = await refresh(() => stillOnStep(from));
        if (!stillOnStep(from)) return;
        setOutputDirectory(fresh.settings.output_directory);
        const action = metadataAction.current;
        if (!stillOnStep(from) || !action || !(await action.continue())) return;
        if (!stillOnStep(from)) return;
        const live = await refresh(() => stillOnStep(from));
        if (!stillOnStep(from)) return;
        if (
          !live.genre_catalog.ready ||
          !live.musicbrainz_configuration_identity
        )
          throw setupMessage("genreCatalogRequired");
        if (live.musicbrainz_configuration_identity !== expectedIdentity)
          throw setupMessage("genreCatalogOutdated");
        navigate(3, live.musicbrainz_configuration_identity, { from });
        return;
      }
      if (step === 3) {
        const expectedIdentity = summaryIdentity.current;
        const fresh = await refresh(() => stillOnStep(from));
        if (!stillOnStep(from)) return;
        if (fresh.completed) return;
        if (
          !expectedIdentity ||
          !fresh.genre_catalog.ready ||
          fresh.musicbrainz_configuration_identity !== expectedIdentity
        ) {
          navigate(2, undefined, { from, replace: true });
          throw setupMessage("genreCatalogOutdated");
        }
        if (!fresh.configuration_health.healthy) {
          const problems = fresh.configuration_health.problems;
          if (problems?.length) throw new Error(problems.join(", "));
          throw setupMessage("setupNotReady");
        }
        try {
          const completed = await completeSetup({
            configuration_identity: expectedIdentity,
          });
          if (completed.status !== 204) throw setupHttpError(completed);
          if (stillOnStep(from)) setState({ ...fresh, completed: true });
        } catch (reason) {
          // Another tab or an ambiguous POST may already have finished Setup.
          if (!stillOnStep(from)) return;
          const current = await refresh(() => stillOnStep(from)).catch(
            () => undefined,
          );
          if (!current?.completed) throw reason;
        }
        return;
      }
      navigate(from + 1, undefined, { from });
    } catch (reason) {
      setError(setupFeedback(reason, "unknownError"));
    } finally {
      actionInFlight.current = false;
      setPending(false);
    }
  }
  const stepHelp = [
    messages.toolsDirectoryHelp,
    messages.toolsHelp,
    `${messages.publicationHelp} ${messages.metadataHelp}`,
    messages.summaryHelp,
  ][step];
  const storedProvider = draftRef.current.metadata?.provider;
  const providerDraft =
    storedProvider && providerDraftMatches(storedProvider, state)
      ? storedProvider
      : undefined;
  const metadataDraft: SetupMetadataDraft = {
    mode: providerDraft?.mode ?? setupMode(state.settings.musicbrainz_mode),
    baseURL: providerDraft?.baseURL ?? state.settings.musicbrainz_base_url,
    lrclib:
      setupDraftValue(
        draftRef.current.metadata?.lrclib,
        state.settings.lrclib_enabled,
      ) ?? state.settings.lrclib_enabled,
  };
  const catalogRequiresVerification =
    providerDirty ||
    !state.settings.musicbrainz_verified_at ||
    !state.musicbrainz_configuration_identity;
  const catalogViewState =
    genreCatalog.state === "loading"
      ? "loading"
      : catalogRequiresVerification
        ? "idle"
        : genreCatalog.state;
  const model = (
    <SetupScreenView
      locale={locale}
      wizard={{
        step,
        help: stepHelp,
        error: error ? setupFeedbackText(error, locale) : undefined,
        notice: notice ? setupFeedbackText(notice, locale) : undefined,
        pending,
        onBack: () => {
          if (pending || (step === 2 && metadataAction.current?.isBusy()))
            return;
          navigate(step - 1);
        },
        onNext: () => {
          void advance();
        },
        nextDisabled:
          !state.platform.supported ||
          state.platform.diagnostic ||
          pending ||
          metadataBusy ||
          (step === 1 && toolsBlocked) ||
          (step === 2 && (providerDirty || catalogViewState !== "ready")) ||
          (step === 3 && catalogViewState !== "ready"),
        backDisabled: pending || metadataBusy,
      }}
      platform={{
        locale,
        goos: state.platform.goos,
        goarch: state.platform.goarch,
        supported: state.platform.supported,
        reason: state.platform.reason,
      }}
      directories={{
        kind: "tools",
        selected:
          toolsDirectory !== "" &&
          toolsDirectory === state.settings.tools_directory,
        fields: [
          {
            kind: "tools",
            value: toolsDirectory,
            onSelect: selectToolsDirectory,
          },
        ],
        locale,
        browse: browseServerDirectory,
        disabled: pending,
      }}
      tools={{
        Component: SetupToolsBinder,
        props: {
          onBlockedChange: updateToolsBlocked,
          hasGitHubToken: state.settings.has_github_token,
          onGitHubTokenChanged: async () => {
            await refresh();
          },
          initialSelected: toolSelection,
          onSelectedChange: rememberToolSelection,
        },
      }}
      publication={{
        directory: {
          kind: "output",
          fields: [
            {
              kind: "output",
              value: outputDirectory,
              onSelect: selectOutputDirectory,
            },
          ],
          locale,
          browse: browseServerDirectory,
          disabled: pending,
        },
        options: {
          locale,
          format,
          autoCompleteAlbums,
          removeSources,
          disabled: pending,
          onFormat: selectFormat,
          onAutoCompleteAlbums: selectAutoCompleteAlbums,
          onRemoveSources: selectRemoveSources,
        },
        metadata: {
          Component: SetupMetadataBinder,
          props: {
            state,
            refresh,
            initialDraft: metadataDraft,
            onDraftChange: rememberMetadataDraft,
            verificationFailure,
            onVerificationFailure: setVerificationFailure,
            disabled: pending,
            onBusyChange: setMetadataBusy,
            onConfigurationDirtyChange: setProviderDirty,
            actionRef: metadataAction,
          },
        },
        genres: {
          locale,
          state: catalogViewState,
          count: catalogRequiresVerification ? undefined : genreCatalog.count,
          requiresVerification: catalogRequiresVerification,
          disabled:
            pending ||
            metadataBusy ||
            catalogRequiresVerification ||
            !state.platform.supported ||
            state.platform.diagnostic,
          onLoad: genreCatalog.load,
        },
      }}
      summary={{
        Component: SetupSummaryBinder,
        props: { state },
        key: summaryRefreshToken,
      }}
      summaryRecheck={{
        label: messages.updateCheck,
        disabled: pending,
        onPress: () => void verifySummary(),
      }}
    />
  ).props;
  return (
    <MeloTroveSetupBinder model={model}>
      {children ?? <MeloTroveSetupContent />}
    </MeloTroveSetupBinder>
  );
  async function verifySummary() {
    setPending(true);
    setError(null);
    try {
      const expectedIdentity = summaryIdentity.current;
      const fresh = await refresh(() => stillOnStep(3));
      if (!stillOnStep(3)) return;
      if (fresh.completed) return;
      if (
        !expectedIdentity ||
        !fresh.genre_catalog.ready ||
        fresh.musicbrainz_configuration_identity !== expectedIdentity
      ) {
        navigate(2, undefined, { from: 3, replace: true });
        throw setupMessage("genreCatalogOutdated");
      }
      setSummaryRefreshToken((token) => token + 1);
      setNotice(setupMessage("serverStateUpdated"));
    } catch (reason) {
      setError(setupFeedback(reason, "unknownError"));
    } finally {
      setPending(false);
    }
  }
}
