import type { SourceTechnicalResultResponse } from "../../api/generated/client.schemas";
import { useI18n } from "../../i18n/I18nProvider";
import { SourceInspectorView } from "./SourceInspectorView";
import {
  type SourceInspectorStepName,
  type SourceInspectorTechnicalResult,
  type SourceInspectorViewProps,
  sourceInspectorStepNames,
} from "./sourceInspectorModel";
import { sourceMessage } from "./sourceMessages";
import { renderSourceError } from "./sourcesApi";
import { useSourceInspector } from "./useSourceInspector";

export function SourceInspectorScreen({
  sourceId,
  locationId,
}: {
  readonly sourceId: string;
  readonly locationId: string;
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
  const artifact = detail && {
    state: detail.staged_artifact.state,
    requestedSteps: detail.staged_artifact.requested_steps ?? [],
    requestedStepsKnown: detail.staged_artifact.requested_steps_known === true,
    reusedFromOtherOperation:
      !!detail.staged_artifact.creator_operation_id &&
      !!detail.staged_artifact.borrower_operation_id &&
      detail.staged_artifact.creator_operation_id !==
        detail.staged_artifact.borrower_operation_id,
    safeError: detail.staged_artifact.safe_error,
  };

  return (
    <SourceInspectorView
      locale={locale}
      sourcesHref="#/sources"
      sourceHref={`#/sources/${encodeURIComponent(sourceId)}`}
      file={file}
      steps={steps}
      sha256={
        sha256 && {
          value: sha256.value,
          provenance: getStep("sha256")?.reuse_origin,
        }
      }
      probe={
        detail?.result && {
          result: technicalResult(detail.result),
          provenance: getStep("probe")?.reuse_origin,
        }
      }
      fingerprint={
        fingerprint && {
          value: fingerprint.value,
          version: fingerprint.version,
          provenance: getStep("fingerprint")?.reuse_origin,
        }
      }
      metadata={
        metadata && {
          tags: metadata.tags,
          provenance: metadata.provenance,
        }
      }
      artifact={artifact}
      retryAvailable={retryAvailable}
      onRetryStep={(name) => void inspector.action("retry", name)}
      fingerprintRerunAvailable={fingerprintRerunAvailable}
      onRerunFingerprint={() => void inspector.action("rerun")}
      activeFingerprintVersion={activeFingerprintVersion}
      matching={
        detail && {
          eligible: detail.matching_eligible,
          reason: !detail.matching_eligible
            ? detail.result?.streams?.length === 0
              ? t("noAudioSupport")
              : detail.result?.streams && detail.result.streams.length > 1
                ? t("multipleAudioSupport")
                : undefined
            : undefined,
        }
      }
      loading={loading}
      busy={busy}
      pending={pending}
      operationState={operation?.state}
      operationStage={operation?.stage}
      streamInterrupted={!!streamError}
      error={error ? renderSourceError(error, locale) : undefined}
      onRetryLoad={() => void inspector.load()}
    />
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
