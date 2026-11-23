import { useCallback, useRef, useState } from "react";
import {
  checkSetupPaths,
  completeSetup,
  getSetupState,
  saveSetupRuntime,
} from "../../api/generated/client";
import type {
  SaveRuntimeBodyPublicationFormat,
  SetupStateBody,
} from "../../api/generated/client.schemas";
import { AppButton } from "../../components/AppButton";
import { browseServerDirectory } from "../../components/DirectoryPicker";
import { useI18n } from "../../i18n/I18nProvider";
import { SetupDirectoryStepBody } from "./SetupDirectoryFields";
import { SetupMetadata, type SetupMetadataAction } from "./SetupMetadata";
import { SetupPublicationOptions } from "./SetupPublicationOptions";
import { SetupSummary } from "./SetupSummary";
import { SetupTools } from "./SetupTools";
import { SetupPlatformStep, SetupWizard, setupSteps } from "./SetupWizard";
import {
  type SetupFeedback,
  setupFeedback,
  setupFeedbackText,
  setupHttpError,
  setupMessage,
  setupMessages,
} from "./setupMessages";
import "./setup.css";

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
  return 3;
}
function restoredStep(state: SetupStateBody) {
  const readyStep = earliest(state);
  try {
    const stored = window.localStorage.getItem(stepKey);
    const step = (setupSteps as readonly string[]).indexOf(stored ?? "");
    if (step >= 0) return Math.min(step, readyStep);
  } catch {
    // Persisting a navigation hint is optional; server readiness is not.
  }
  // Verified MusicBrainz plus saved output does not prove that the later
  // LRCLIB write succeeded. Without a hint, revisit Publication.
  return Math.min(readyStep, 2);
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
}: {
  onCompleted: () => void;
  initialState: SetupStateBody;
}) {
  const { locale } = useI18n();
  const messages = setupMessages[locale];
  const [state, setState] = useState(initialState);
  const [step, setStep] = useState(() => restoredStep(initialState));
  const [toolsDirectory, setToolsDirectory] = useState(
    initialState.settings.tools_directory,
  );
  const [outputDirectory, setOutputDirectory] = useState(
    initialState.settings.output_directory,
  );
  const [format, setFormat] = useState<SaveRuntimeBodyPublicationFormat | "">(
    knownFormat(initialState.settings.publication_format) ?? "",
  );
  const [removeSources, setRemoveSources] = useState(
    initialState.settings.publication_remove_sources,
  );
  const [pending, setPending] = useState(false);
  const [toolsBlocked, setToolsBlocked] = useState(false);
  const [error, setError] = useState<SetupFeedback | null>(null);
  const [notice, setNotice] = useState<SetupFeedback | null>(null);
  const [summaryRefreshToken, setSummaryRefreshToken] = useState(0);
  const actionInFlight = useRef(false);
  const metadataAction = useRef<SetupMetadataAction | null>(null);
  const [metadataBusy, setMetadataBusy] = useState(false);
  const updateToolsBlocked = useCallback((blocked: boolean) => {
    setToolsBlocked(blocked);
  }, []);
  function navigate(next: number) {
    setError(null);
    setNotice(null);
    setStep(next);
    try {
      window.localStorage.setItem(stepKey, setupSteps[next]);
    } catch {
      // The current step is still available for this page session.
    }
  }
  async function refresh() {
    const response = await getSetupState({ cache: "no-store" });
    if (response.status !== 200) throw setupHttpError(response);
    setState(response.data);
    return response.data;
  }
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
    try {
      if (step === 0) {
        if (!toolsDirectory) throw setupMessage("toolsDirectoryRequired");
        const saved = await saveSetupRuntime({
          tools_directory: toolsDirectory,
        });
        if (saved.status !== 204) throw setupHttpError(saved);
        const fresh = await refresh();
        setToolsDirectory(fresh.settings.tools_directory);
      }
      if (step === 1) {
        const fresh = await refresh();
        if (
          !fresh.settings.active_ffmpeg_installation_id ||
          !fresh.settings.active_fpcalc_installation_id
        )
          throw setupMessage("activateTools");
      }
      if (step === 2) {
        if (!outputDirectory) throw setupMessage("outputDirectoryRequired");
        if (!format) throw setupMessage("chooseFormat");
        const checked = await checkSetupPaths({
          tools_directory: state.settings.tools_directory,
          output_directory: outputDirectory,
        });
        if (checked.status !== 200) throw setupHttpError(checked);
        const saved = await saveSetupRuntime({
          output_directory: outputDirectory,
          publication_format: format,
          publication_remove_sources: removeSources,
        });
        if (saved.status !== 204) throw setupHttpError(saved);
        const fresh = await refresh();
        setOutputDirectory(fresh.settings.output_directory);
        if (!(await metadataAction.current?.continue())) return;
      }
      if (step === 3) {
        const fresh = await refresh();
        if (!fresh.configuration_health.healthy) {
          const problems = fresh.configuration_health.problems;
          if (problems?.length) throw new Error(problems.join(", "));
          throw setupMessage("setupNotReady");
        }
        const completed = await completeSetup();
        if (completed.status !== 204) throw setupHttpError(completed);
        try {
          window.localStorage.removeItem(stepKey);
        } catch {
          // Setup completed on the server even if storage is unavailable.
        }
        onCompleted();
        return;
      }
      navigate(step + 1);
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
  return (
    <SetupWizard
      locale={locale}
      step={step}
      help={stepHelp}
      error={error ? setupFeedbackText(error, locale) : undefined}
      notice={notice ? setupFeedbackText(notice, locale) : undefined}
      pending={pending}
      onBack={() => {
        if (pending || (step === 2 && metadataAction.current?.isBusy())) return;
        navigate(step - 1);
      }}
      onNext={() => {
        void advance();
      }}
      nextDisabled={
        !state.platform.supported ||
        state.platform.diagnostic ||
        pending ||
        metadataBusy ||
        (step === 1 && toolsBlocked)
      }
      backDisabled={pending || metadataBusy}
    >
      {step === 0 && (
        <SetupPlatformStep
          locale={locale}
          goos={state.platform.goos}
          goarch={state.platform.goarch}
          supported={state.platform.supported}
          reason={state.platform.reason}
        />
      )}
      {step === 0 && (
        <SetupDirectoryStepBody
          kind="tools"
          selected={
            toolsDirectory !== "" &&
            toolsDirectory === state.settings.tools_directory
          }
          fields={[
            {
              kind: "tools",
              value: toolsDirectory,
              onSelect: setToolsDirectory,
            },
          ]}
          locale={locale}
          browse={browseServerDirectory}
          disabled={pending}
        />
      )}
      {step === 1 && (
        <SetupTools
          onBlockedChange={updateToolsBlocked}
          hasGitHubToken={state.settings.has_github_token}
          onGitHubTokenChanged={async () => {
            await refresh();
          }}
        />
      )}
      {step === 2 && (
        <div className="grid gap-3">
          <SetupDirectoryStepBody
            kind="output"
            fields={[
              {
                kind: "output",
                value: outputDirectory,
                onSelect: setOutputDirectory,
              },
            ]}
            locale={locale}
            browse={browseServerDirectory}
            disabled={pending}
          />
          <SetupPublicationOptions
            locale={locale}
            format={format}
            removeSources={removeSources}
            disabled={pending}
            onFormat={setFormat}
            onRemoveSources={setRemoveSources}
          />
          <SetupMetadata
            state={state}
            refresh={refresh}
            disabled={pending}
            onBusyChange={setMetadataBusy}
            actionRef={metadataAction}
          />
        </div>
      )}
      {step === 3 && (
        <div>
          <SetupSummary key={summaryRefreshToken} state={state} />
          <AppButton
            isDisabled={pending}
            onPress={() => {
              void verifySummary();
            }}
          >
            {messages.updateCheck}
          </AppButton>
        </div>
      )}
    </SetupWizard>
  );
  async function verifySummary() {
    setPending(true);
    setError(null);
    try {
      await refresh();
      setSummaryRefreshToken((token) => token + 1);
      setNotice(setupMessage("serverStateUpdated"));
    } catch (reason) {
      setError(setupFeedback(reason, "unknownError"));
    } finally {
      setPending(false);
    }
  }
}
