import { useEffect, useState } from "react";
import { listInstallations } from "../../api/generated/client";
import type {
  InstallationResponse,
  SetupStateBody,
} from "../../api/generated/client.schemas";
import { useI18n } from "../../i18n/I18nProvider";
import {
  type SetupFeedback,
  setupFeedback,
  setupFeedbackText,
  setupHttpError,
  setupMessages,
} from "./setupMessages";
import "./SetupSummary.css";

function formatName(format: string, messages: (typeof setupMessages)["en"]) {
  if (format === "source") return messages.sourceFormat;
  if (format === "mka") return "MKA";
  if (format === "m4a") return "M4A";
  return messages.unselected;
}

function formatExplanation(
  format: string,
  messages: (typeof setupMessages)["en"],
) {
  if (format === "source") return messages.sourceFormatExplanation;
  if (format === "mka") return messages.mkaExplanation;
  if (format === "m4a") return messages.m4aExplanation;
  return "";
}

function ToolStatus({
  id,
  installations,
  loading,
  messages,
}: {
  id: string;
  installations: InstallationResponse[];
  loading: boolean;
  messages: (typeof setupMessages)["en"];
}) {
  const tool = installations.find((item) => item.id === id);
  const version = tool?.version;
  const ready =
    tool?.active === true &&
    tool.state === "ready" &&
    Boolean(tool.verified_at) &&
    Boolean(version);
  if (loading) return <span>{messages.loading}</span>;
  if (!tool) return <span>{messages.unknownToolState}</span>;
  if (!ready)
    return (
      <span>
        {messages.toolNotReady}
        {tool.state})
      </span>
    );
  return (
    <span>
      {messages.ready}
      {version}
    </span>
  );
}

export function SetupSummary({ state }: { state: SetupStateBody }) {
  const { locale } = useI18n();
  const messages = setupMessages[locale];
  const [installations, setInstallations] = useState<InstallationResponse[]>(
    [],
  );
  const [loading, setLoading] = useState(true);
  const [inventoryError, setInventoryError] = useState<SetupFeedback | null>(
    null,
  );
  const ffmpegID = state.settings.active_ffmpeg_installation_id || "";
  const fpcalcID = state.settings.active_fpcalc_installation_id || "";

  useEffect(() => {
    let current = true;
    setLoading(true);
    setInventoryError(null);
    setInstallations([]);
    void listInstallations(undefined, { cache: "no-store" })
      .then((response) => {
        if (response.status !== 200) throw setupHttpError(response);
        if (current) setInstallations(response.data.installations || []);
      })
      .catch((reason: unknown) => {
        if (current)
          setInventoryError(setupFeedback(reason, "toolsInventoryLoadError"));
      })
      .finally(() => {
        if (current) setLoading(false);
      });
    return () => {
      current = false;
    };
  }, []);

  const healthy = state.configuration_health.healthy;
  return (
    <div className="setup-summary-content">
      <h3>
        {healthy ? messages.setupReadyTitle : messages.setupNeedsAttention}
      </h3>
      {!healthy && (
        <div role="alert">
          <p>{messages.setupNotReadyIntro}</p>
          {state.configuration_health.problems?.length ? (
            <ul>
              {state.configuration_health.problems.map((problem) => (
                <li key={problem}>{problem}</li>
              ))}
            </ul>
          ) : (
            <p>{messages.serverNotReady}</p>
          )}
        </div>
      )}
      <section aria-labelledby="summary-publication">
        <h4 id="summary-publication">{messages.publication}</h4>
        <dl>
          <div>
            <dt>{messages.serverDirectory}</dt>
            <dd>{state.settings.output_directory || messages.notSpecified}</dd>
          </div>
          <div>
            <dt>{messages.format}</dt>
            <dd>{formatName(state.settings.publication_format, messages)}</dd>
          </div>
          <div>
            <dt>{messages.whatWillHappen}</dt>
            <dd>
              {formatExplanation(state.settings.publication_format, messages)}
            </dd>
          </div>
          <div>
            <dt>{messages.sourceFilesAfterPublication}</dt>
            <dd>
              {state.settings.publication_remove_sources
                ? messages.remove
                : messages.keep}
            </dd>
          </div>
        </dl>
      </section>
      <section aria-labelledby="summary-tools">
        <h4 id="summary-tools">{messages.tools}</h4>
        {inventoryError && (
          <p role="status">
            {messages.toolsInventoryUnavailable}
            {setupFeedbackText(inventoryError, locale)}
          </p>
        )}
        <dl>
          <div>
            <dt>{messages.ffmpeg}</dt>
            <dd>
              <ToolStatus
                id={ffmpegID}
                installations={installations}
                loading={loading}
                messages={messages}
              />
            </dd>
          </div>
          <div>
            <dt>{messages.fpcalc}</dt>
            <dd>
              <ToolStatus
                id={fpcalcID}
                installations={installations}
                loading={loading}
                messages={messages}
              />
            </dd>
          </div>
        </dl>
      </section>
      <section aria-labelledby="summary-metadata">
        <h4 id="summary-metadata">{messages.metadataAndLyrics}</h4>
        <dl>
          <div>
            <dt>MusicBrainz</dt>
            <dd>
              {state.settings.musicbrainz_verified_at
                ? `${messages.connectionChecked}${state.settings.musicbrainz_mode === "self-hosted" ? state.settings.musicbrainz_base_url : messages.publicService}`
                : messages.connectionNotChecked}
            </dd>
          </div>
          <div>
            <dt>{messages.lrclibLyrics}</dt>
            <dd>
              {state.settings.lrclib_enabled
                ? messages.enabledAvailableForSearch
                : messages.disabled}
            </dd>
          </div>
          <div>
            <dt>{messages.acoustid}</dt>
            <dd>
              {state.settings.has_acoustid_application_key
                ? messages.keySet
                : messages.keyNotSetOptional}
            </dd>
          </div>
        </dl>
      </section>
      <details>
        <summary>{messages.technicalDetails}</summary>
        <dl>
          <div>
            <dt>{messages.platform}</dt>
            <dd>
              {state.platform.goos}/{state.platform.goarch}
            </dd>
          </div>
          <div>
            <dt>{messages.toolsDirectorySummary}</dt>
            <dd>{state.settings.tools_directory || messages.notSpecified}</dd>
          </div>
          <div>
            <dt>{messages.lastMusicBrainzCheck}</dt>
            <dd>{state.settings.musicbrainz_verified_at || messages.noData}</dd>
          </div>
          <div>
            <dt>{messages.lastFfmpegCheck}</dt>
            <dd>
              {installations.find((item) => item.id === ffmpegID)
                ?.verified_at || messages.noData}
            </dd>
          </div>
          <div>
            <dt>{messages.lastFpcalcCheck}</dt>
            <dd>
              {installations.find((item) => item.id === fpcalcID)
                ?.verified_at || messages.noData}
            </dd>
          </div>
        </dl>
      </details>
      <p>{messages.nextStep}</p>
    </div>
  );
}
