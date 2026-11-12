import { useCallback, useEffect, useRef, useState } from "react";
import {
  Label,
  RadioButton,
  RadioField,
  RadioGroup,
} from "react-aria-components";
import { apiResponseError } from "../../api/client/errors";
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
import { DirectoryPathField } from "../../components/DirectoryPathField";
import { useI18n } from "../../i18n/I18nProvider";
import { SetupMetadata } from "./SetupMetadata";
import { SetupSummary } from "./SetupSummary";
import { SetupTools } from "./SetupTools";
import {
  type SetupFeedback,
  setupFeedback,
  setupFeedbackText,
  setupMessage,
  setupMessages,
} from "./setupMessages";
import "./setup.css";

const stepKey = "melotrove.setup.step";
const stepCount = 6;
function earliest(state: SetupStateBody) {
  const s = state.settings;
  if (!s.tools_directory && !s.output_directory) return 0;
  if (!s.tools_directory || !s.output_directory) return 1;
  if (!s.active_ffmpeg_installation_id || !s.active_fpcalc_installation_id)
    return 2;
  if (!s.publication_format) return 3;
  if (!s.musicbrainz_verified_at) return 4;
  return 5;
}
function restoredStep(state: SetupStateBody) {
  const stored = window.localStorage.getItem(stepKey);
  const step = stored === null ? NaN : Number(stored);
  return Number.isInteger(step) && step >= 0 && step < stepCount
    ? Math.min(step, earliest(state))
    : earliest(state);
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
  const steps = messages.steps;
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
  const heading = useRef<HTMLHeadingElement>(null);
  const alert = useRef<HTMLParagraphElement>(null);
  const updateToolsBlocked = useCallback((blocked: boolean) => {
    setToolsBlocked(blocked);
  }, []);
  function navigate(next: number) {
    setError(null);
    setNotice(null);
    setStep(next);
    window.localStorage.setItem(stepKey, String(next));
  }
  useEffect(() => {
    if (step >= 0 && step < stepCount) heading.current?.focus();
  }, [step]);
  useEffect(() => {
    if (error) alert.current?.focus();
  }, [error]);
  async function refresh() {
    const response = await getSetupState({ cache: "no-store" });
    if (response.status !== 200) throw apiResponseError(response);
    setState(response.data);
    setToolsDirectory(response.data.settings.tools_directory);
    setOutputDirectory(response.data.settings.output_directory);
    const freshFormat = knownFormat(response.data.settings.publication_format);
    if (freshFormat) setFormat(freshFormat);
    setRemoveSources(response.data.settings.publication_remove_sources);
    return response.data;
  }
  async function advance() {
    setPending(true);
    setError(null);
    setNotice(null);
    try {
      if (step === 1) {
        if (!toolsDirectory || !outputDirectory)
          throw setupMessage("directoryRequired");
        const checked = await checkSetupPaths({
          tools_directory: toolsDirectory,
          output_directory: outputDirectory,
        });
        if (checked.status !== 200) throw apiResponseError(checked);
        const saved = await saveSetupRuntime({
          tools_directory: toolsDirectory,
          output_directory: outputDirectory,
        });
        if (saved.status !== 204) throw apiResponseError(saved);
        await refresh();
      }
      if (step === 2) {
        const fresh = await refresh();
        if (
          !fresh.settings.active_ffmpeg_installation_id ||
          !fresh.settings.active_fpcalc_installation_id
        )
          throw setupMessage("activateTools");
      }
      if (step === 3) {
        if (!format) throw setupMessage("chooseFormat");
        const saved = await saveSetupRuntime({
          publication_format: format,
          publication_remove_sources: removeSources,
        });
        if (saved.status !== 204) throw apiResponseError(saved);
        await refresh();
      }
      if (step === 5) {
        const fresh = await refresh();
        if (!fresh.configuration_health.healthy) {
          const problems = fresh.configuration_health.problems;
          if (problems?.length) throw new Error(problems.join(", "));
          throw setupMessage("setupNotReady");
        }
        const completed = await completeSetup();
        if (completed.status !== 204) throw apiResponseError(completed);
        window.localStorage.removeItem(stepKey);
        onCompleted();
        return;
      }
      navigate(step + 1);
    } catch (reason) {
      setError(setupFeedback(reason, "unknownError"));
    } finally {
      setPending(false);
    }
  }
  return (
    <section aria-labelledby="setup-title" className="mx-auto max-w-6xl py-3">
      <h1 id="setup-title" className="text-xl font-semibold">
        {messages.firstRun}
      </h1>
      <ol aria-label={messages.setupSteps} className="setup-stepper">
        {steps.map((name, index) => (
          <li key={name} aria-current={index === step ? "step" : undefined}>
            {index + 1}. {name}
          </li>
        ))}
      </ol>
      <div className="setup-layout">
        <div className="setup-panel">
          <h2 ref={heading} tabIndex={-1} className="font-semibold">
            {steps[step]}
          </h2>
          {step === 0 && (
            <p>
              {messages.server}: {state.platform.goos}/{state.platform.goarch}.{" "}
              {state.platform.supported
                ? messages.platformSupported
                : state.platform.reason}
            </p>
          )}
          {step === 1 && (
            <div className="grid gap-3">
              <DirectoryPathField
                id="setup-tools-directory"
                label={messages.toolsDirectory}
                value={toolsDirectory}
                onSelect={setToolsDirectory}
                disabled={pending}
                aria-describedby="setup-directory-help"
              />
              <DirectoryPathField
                id="setup-output-directory"
                label={messages.outputDirectory}
                value={outputDirectory}
                onSelect={setOutputDirectory}
                disabled={pending}
                aria-describedby="setup-directory-help"
              />
              <p id="setup-directory-help">{messages.directoryHelp}</p>
            </div>
          )}
          {step === 2 && (
            <SetupTools
              onBlockedChange={updateToolsBlocked}
              hasGitHubToken={state.settings.has_github_token}
              onGitHubTokenChanged={async () => {
                await refresh();
              }}
            />
          )}
          {step === 3 && (
            <div>
              <RadioGroup
                isDisabled={pending}
                value={format}
                onChange={(value) => {
                  if (value === "source" || value === "mka" || value === "m4a")
                    setFormat(value);
                }}
              >
                <Label>{messages.format}</Label>
                <RadioField value="source">
                  <RadioButton>{messages.sourceFormat}</RadioButton>
                </RadioField>
                <RadioField value="mka">
                  <RadioButton>{messages.mkaRemux}</RadioButton>
                </RadioField>
                <RadioField value="m4a">
                  <RadioButton>M4A</RadioButton>
                </RadioField>
              </RadioGroup>
              <label className="mt-4 flex items-start gap-2">
                <input
                  type="checkbox"
                  checked={removeSources}
                  disabled={pending}
                  onChange={(event) => setRemoveSources(event.target.checked)}
                />
                <span>
                  <span className="block">{messages.removeSources}</span>
                  <span className="block text-sm">
                    {messages.preserveSourcesDescription}
                  </span>
                </span>
              </label>
            </div>
          )}
          {step === 4 && (
            <SetupMetadata
              state={state}
              refresh={refresh}
              onContinue={() => navigate(5)}
              onBack={() => navigate(3)}
            />
          )}
          {step === 5 && (
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
          {error && (
            <p ref={alert} tabIndex={-1} role="alert">
              {setupFeedbackText(error, locale)}
            </p>
          )}
          {notice && <p role="status">{setupFeedbackText(notice, locale)}</p>}
          {pending && <p role="status">{messages.requestInProgress}</p>}
          {step !== 4 && (
            <div className="setup-navigation">
              <AppButton
                isDisabled={step === 0 || pending}
                onPress={() => navigate(step - 1)}
              >
                {messages.back}
              </AppButton>
              <AppButton
                isDisabled={pending || (step === 2 && toolsBlocked)}
                onPress={advance}
              >
                {step === 5 ? messages.finishSetup : messages.continue}
              </AppButton>
            </div>
          )}
        </div>
        <aside
          className="setup-panel setup-help"
          aria-label={messages.aboutCurrentStep}
        >
          <h2>{messages.importantToKnow}</h2>
          {step === 0 && <p>{messages.platformHelp}</p>}
          {step === 1 && <p>{messages.directoryHelpDetails}</p>}
          {step === 2 && <p>{messages.toolsHelp}</p>}
          {step === 3 && <p>{messages.publicationHelp}</p>}
          {step === 4 && <p>{messages.metadataHelp}</p>}
          {step === 5 && <p>{messages.summaryHelp}</p>}
        </aside>
      </div>
    </section>
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
