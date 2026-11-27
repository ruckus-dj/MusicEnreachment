import type { ReactNode } from "react";
import type { SourceTechnicalResultResponse } from "../../api/generated/client.schemas";
import { useI18n } from "../../app/i18n/host";
import {
  MeloTroveSourcesBinder,
  MeloTroveSourcesContent,
} from "../../ui/app/MeloTroveProvider";
import {
  type SourceInspectorStepName,
  type SourceInspectorTechnicalResult,
  type SourceInspectorViewProps,
  sourceInspectorStepNames,
} from "../../ui/features/sources/sourceInspectorModel";
import { sourceMessage } from "../../ui/features/sources/sourceMessages";
import {
  sourceDetailHref,
  sourcesHref,
} from "../../ui/features/sources/sourcesRoute";
import { renderSourceError } from "./sourcesApi";
import { useSourceInspector } from "./useSourceInspector";

export function SourceInspectorScreen({
  sourceId,
  locationId,
  children,
}: {
  readonly sourceId: string;
  readonly locationId: string;
  readonly children?: ReactNode;
}) {
  const { locale } = useI18n();
  const t = (
    key: Parameters<typeof sourceMessage>[1],
    values?: Record<string, string | number>,
  ) => sourceMessage(locale, key, values);
  const inspector = useSourceInspector(sourceId, locationId);
  const { detail, operation, loading, busy, pending, error, streamError } =
    inspector;

  const stepsByName = new Map(
    (detail?.steps ?? []).map((current) => [current.name, current]),
  );
  const getStep = (name: SourceInspectorStepName) => stepsByName.get(name);
  const steps = Object.fromEntries(
    sourceInspectorStepNames.map((name) => {
      const current = getStep(name);
      return [
        name,
        {
          // Absent work is not a loading state: the API has no requested step yet.
          state: current?.state ?? "not_requested",
          safeError: current?.safe_error,
          skipReason: current?.skip_reason,
        },
      ];
    }),
  ) as SourceInspectorViewProps["steps"];
  const rootAvailable =
    detail?.root.enabled === true &&
    !detail.root.stale &&
    detail.root.status === "available";
  const active = pending || !!detail?.active_analysis_operation_id;
  const retryAvailable = Object.fromEntries(
    sourceInspectorStepNames.map((name) => [
      name,
      rootAvailable && !active && getStep(name)?.state === "failed",
    ]),
  ) as Record<SourceInspectorStepName, boolean>;
  const sha256 = getStep("sha256")?.sha256;
  const fingerprint = getStep("fingerprint")?.fingerprint;
  const metadata = getStep("metadata")?.metadata;
  const fingerprintStepState = getStep("fingerprint")?.state;
  // The service only reruns a fingerprint whose latest step succeeded and whose
  // stored result was produced by a different active fpcalc version. Mirror that
  // exactly instead of enabling a rerun for any retained fingerprint.
  const activeFingerprintVersion = detail?.active_fpcalc_version;
  const fingerprintRerunAvailable =
    rootAvailable &&
    !!fingerprint &&
    fingerprintStepState === "succeeded" &&
    !!activeFingerprintVersion &&
    activeFingerprintVersion !== fingerprint.version &&
    !active &&
    !busy &&
    !loading &&
    !error;

  const file = detail && {
    relativePath: detail.relative_path,
    inventoryPath: detail.root.inventory_path,
    sizeBytes: detail.size_bytes,
    modifiedAt: detail.mtime,
    availability: detail.root.status,
    enabled: detail.root.enabled,
    stale: detail.root.stale,
    safeError: detail.root.safe_error,
  };
  const stagedArtifact = detail?.staged_artifact;
  const artifact = stagedArtifact
    ? {
        state: stagedArtifact.state,
        requestedSteps: stagedArtifact.requested_steps ?? [],
        requestedStepsKnown: stagedArtifact.requested_steps_known,
        reusedFromOtherOperation:
          !!stagedArtifact.creator_operation_id &&
          !!stagedArtifact.borrower_operation_id &&
          stagedArtifact.creator_operation_id !==
            stagedArtifact.borrower_operation_id,
        safeError: stagedArtifact.safe_error,
      }
    : undefined;

  return (
    <MeloTroveSourcesBinder
      model={{
        view: "location",
        locale,
        sourcesHref,
        sourceHref: sourceDetailHref(sourceId),
        file,
        steps,
        sha256: sha256 && {
          value: sha256.value,
          provenance: getStep("sha256")?.reuse_origin,
        },
        probe: detail?.result && {
          result: technicalResult(detail.result),
          provenance: getStep("probe")?.reuse_origin,
        },
        fingerprintReady: !!fingerprint,
        metadata: metadata && {
          tags: metadata.tags,
          provenance: metadata.provenance,
        },
        artifact,
        retryAvailable,
        onRetryStep: (name) => void inspector.action("retry", name),
        fingerprintRerunAvailable,
        onRerunFingerprint: () => void inspector.action("rerun"),
        matching: detail && {
          eligible: detail.matching_eligible,
          reason: !detail.matching_eligible
            ? detail.result?.streams?.length === 0
              ? t("noAudioSupport")
              : detail.result?.streams && detail.result.streams.length > 1
                ? t("multipleAudioSupport")
                : undefined
            : undefined,
        },
        loading,
        busy,
        pending,
        operationState: operation?.state,
        operationStage: operation?.stage,
        streamInterrupted: !!streamError,
        error: error ? renderSourceError(error, locale) : undefined,
        onRetryLoad: () => void inspector.load(),
      }}
    >
      {children ?? <MeloTroveSourcesContent />}
    </MeloTroveSourcesBinder>
  );
}

function technicalResult(
  result: SourceTechnicalResultResponse,
): SourceInspectorTechnicalResult {
  return {
    inspectedAt: result.inspected_at,
    ffprobeVersion: result.ffprobe_version,
    analysisPolicyVersion: result.analysis_policy_version,
    container: {
      name: result.container.name,
      longName: result.container.long_name,
      durationMs: result.container.duration_ms,
      bitRate: result.container.bit_rate,
    },
    streams: (result.streams ?? []).map((stream) => ({
      index: stream.index,
      codecName: stream.codec_name,
      profile: stream.profile,
      durationMs: stream.duration_ms,
      bitRate: stream.bit_rate,
      sampleRateHz: stream.sample_rate_hz,
      sampleFormat: stream.sample_format,
      bitsPerSample: stream.bits_per_sample,
      channels: stream.channels,
      channelLayout: stream.channel_layout,
    })),
    tags: Object.entries(result.tags).map(([key, values]) => ({
      key,
      values: values ?? [],
    })),
    rawJson: result.raw_json,
  };
}
