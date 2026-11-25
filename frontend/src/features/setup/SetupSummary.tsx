import { useEffect, useState } from "react";
import { listInstallations } from "../../api/generated/client";
import type {
  InstallationResponse,
  SetupStateBody,
} from "../../api/generated/client.schemas";
import { useI18n } from "../../i18n/I18nProvider";
import { SetupSummaryStepBody } from "./SetupScreenView";
import {
  type SetupFeedback,
  setupFeedback,
  setupFeedbackText,
  setupHttpError,
  setupMessages,
} from "./setupMessages";

function knownFormat(format: string) {
  return format === "source" || format === "mka" || format === "m4a"
    ? format
    : "unselected";
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

function toolStatus(
  id: string,
  installations: InstallationResponse[],
  loading: boolean,
  messages: (typeof setupMessages)["en"],
) {
  const tool = installations.find((item) => item.id === id);
  const version = tool?.version;
  const ready =
    tool?.active === true &&
    tool.state === "ready" &&
    Boolean(tool.verified_at) &&
    Boolean(version);
  if (loading) return messages.loading;
  if (!tool) return messages.unknownToolState;
  if (!ready) return `${messages.toolNotReady}${tool.state})`;
  return `${messages.ready}${version}`;
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

  const ffmpeg = installations.find((item) => item.id === ffmpegID);
  const fpcalc = installations.find((item) => item.id === fpcalcID);
  return (
    <SetupSummaryStepBody
      locale={locale}
      status={{
        healthy: state.configuration_health.healthy,
        problems: state.configuration_health.problems ?? [],
      }}
      values={{
        publication: {
          directory: state.settings.output_directory || messages.notSpecified,
          format: knownFormat(state.settings.publication_format),
          explanation: formatExplanation(
            state.settings.publication_format,
            messages,
          ),
          removeSources: state.settings.publication_remove_sources,
        },
        tools: {
          ffmpeg: toolStatus(ffmpegID, installations, loading, messages),
          fpcalc: toolStatus(fpcalcID, installations, loading, messages),
          notice: inventoryError
            ? `${messages.toolsInventoryUnavailable}${setupFeedbackText(inventoryError, locale)}`
            : undefined,
        },
        metadata: {
          musicbrainz: state.settings.musicbrainz_verified_at
            ? `${messages.connectionChecked}${state.settings.musicbrainz_mode === "self-hosted" ? state.settings.musicbrainz_base_url : messages.publicService}`
            : messages.connectionNotChecked,
          lrclibEnabled: state.settings.lrclib_enabled,
          hasAcoustIdKey: state.settings.has_acoustid_application_key,
          genres: state.genre_catalog.ready
            ? messages.genreCatalogLoaded
            : messages.genreCatalogMissing,
        },
        technical: {
          platform: `${state.platform.goos}/${state.platform.goarch}`,
          toolsDirectory:
            state.settings.tools_directory || messages.notSpecified,
          musicbrainzCheckedAt:
            state.settings.musicbrainz_verified_at || messages.noData,
          ffmpegCheckedAt: ffmpeg?.verified_at || messages.noData,
          fpcalcCheckedAt: fpcalc?.verified_at || messages.noData,
        },
        showNextStep: true,
      }}
    />
  );
}
