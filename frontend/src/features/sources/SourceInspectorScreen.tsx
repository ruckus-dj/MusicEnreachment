import { useEffect, useRef } from "react";
import { Link } from "react-aria-components";
import type {
  SourceAnalysisStepResponse,
  SourceStagedArtifactResponse,
} from "../../api/generated/client.schemas";
import { AppButton } from "../../components/AppButton";
import { useI18n } from "../../i18n/I18nProvider";
import { SourceAnalysisSteps } from "./SourceAnalysisSteps";
import { numberFormat, sourceMessage } from "./sourceMessages";
import { renderSourceError, statusLabel } from "./sourcesApi";
import { useSourceInspector } from "./useSourceInspector";
import "./sources.css";

const stepNames = ["sha256", "probe", "fingerprint", "metadata"] as const;
type StepName = (typeof stepNames)[number];

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
  const heading = useRef<HTMLHeadingElement>(null);
  const alert = useRef<HTMLParagraphElement>(null);
  useEffect(() => {
    heading.current?.focus();
  }, []);
  useEffect(() => {
    if (error) alert.current?.focus();
  }, [error]);

  const stepsByName = new Map(
    (detail?.steps ?? []).map((current) => [current.name, current]),
  );
  const getStep = (name: StepName): SourceAnalysisStepResponse | undefined =>
    stepsByName.get(name);
  const steps = Object.fromEntries(
    stepNames.map((name) => {
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
  ) as Parameters<typeof SourceAnalysisSteps>[0]["steps"];
  const rootAvailable =
    detail?.root.enabled === true &&
    !detail.root.stale &&
    detail.root.status === "available";
  const active = pending || !!detail?.active_analysis_operation_id;
  const retryAvailable = Object.fromEntries(
    stepNames.map((name) => [
      name,
      rootAvailable && !active && getStep(name)?.state === "failed",
    ]),
  ) as Record<StepName, boolean>;
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

  return (
    <section
      className="sources-screen sources-inspector mx-auto max-w-5xl py-3"
      aria-labelledby="inspector-title"
    >
      <nav aria-label={t("breadcrumb")} className="sources-breadcrumb">
        <Link href="#/sources">{t("title")}</Link>
        <span aria-hidden="true">/</span>
        <Link href={`#/sources/${encodeURIComponent(sourceId)}`}>
          {t("sourceTitle")}
        </Link>
        <span aria-hidden="true">/</span>
        <span>{t("file")}</span>
      </nav>
      <h1 id="inspector-title" ref={heading} tabIndex={-1}>
        {t("inspector")}
      </h1>
      {loading && <p role="status">{t("loadingInspection")}</p>}
      {error && (
        <p ref={alert} tabIndex={-1} role="alert">
          {renderSourceError(error, locale)}
        </p>
      )}
      {error && (
        <AppButton
          isDisabled={loading || busy}
          onPress={() => void inspector.load()}
        >
          {t("retryLoad")}
        </AppButton>
      )}
      {detail && (
        <>
          <section className="sources-panel" aria-labelledby="identity-title">
            <h2 id="identity-title">{t("originalFile")}</h2>
            <dl className="sources-summary">
              <div>
                <dt>{t("relativePath")}</dt>
                <dd>
                  <code>{detail.relative_path}</code>
                </dd>
              </div>
              <div>
                <dt>{t("inventoryPath")}</dt>
                <dd>
                  <code>{detail.root.inventory_path ?? t("unknown")}</code>
                </dd>
              </div>
              <div>
                <dt>{t("size")}</dt>
                <dd>
                  {numberFormat(locale).format(detail.size_bytes)}{" "}
                  {t("bytesUnit")}
                </dd>
              </div>
              <div>
                <dt>{t("modified")}</dt>
                <dd>
                  {new Intl.DateTimeFormat(
                    locale === "ru" ? "ru-RU" : "en-US",
                    { dateStyle: "short", timeStyle: "short" },
                  ).format(new Date(detail.mtime))}
                </dd>
              </div>
              <div>
                <dt>{t("analysisImpact")}</dt>
                <dd>{t("analysisDoesNotChange")}</dd>
              </div>
              <div>
                <dt>{t("sourceAvailability")}</dt>
                <dd>{statusLabel(detail.root.status, locale)}</dd>
              </div>
            </dl>
            {!detail.root.enabled && (
              <p className="sources-note">{t("disabledAnalysis")}</p>
            )}
            {detail.root.stale && (
              <p className="sources-note">{t("staleInventory")}</p>
            )}
            {detail.root.status === "unavailable" && (
              <p className="sources-note">
                {t("unavailableResult")} {detail.root.safe_error}
              </p>
            )}
          </section>
          <section className="sources-panel" aria-labelledby="analysis-title">
            <h2 id="analysis-title">{t("technicalAnalysis")}</h2>
            {busy && <p role="status">{t("requestRunning")}</p>}
            {pending && (
              <p role="status">
                {operation?.state === "queued"
                  ? t("queuedStep")
                  : operation?.stage === "applying"
                    ? t("savingStep")
                    : t("runningStep")}
              </p>
            )}
            {streamError && pending && (
              <p role="status">{t("streamInterrupted")}</p>
            )}
          </section>
          <StagedArtifactStatus
            artifact={detail.staged_artifact}
            locale={locale}
          />
          <SourceAnalysisSteps
            steps={steps}
            sha256={
              sha256 && {
                value: sha256.value,
                provenance: getStep("sha256")?.reuse_origin,
              }
            }
            probeResult={detail.result}
            probeProvenance={getStep("probe")?.reuse_origin}
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
            retryAvailable={retryAvailable}
            onRetry={(name) => void inspector.action("retry", name)}
            fingerprintRerunAvailable={fingerprintRerunAvailable}
            onRerunFingerprint={() => void inspector.action("rerun")}
            activeFingerprintVersion={activeFingerprintVersion}
            matching={{
              eligible: detail.matching_eligible,
              reason: !detail.matching_eligible
                ? detail.result?.streams?.length === 0
                  ? t("noAudioSupport")
                  : detail.result?.streams && detail.result.streams.length > 1
                    ? t("multipleAudioSupport")
                    : undefined
                : undefined,
            }}
          />
        </>
      )}
    </section>
  );
}

function StagedArtifactStatus({
  artifact,
  locale,
}: {
  readonly artifact?: SourceStagedArtifactResponse;
  readonly locale: "en" | "ru";
}) {
  const t = (key: Parameters<typeof sourceMessage>[1]) =>
    sourceMessage(locale, key);
  const stateLabels = {
    unknown: t("unknown"),
    preparation: t("artifactPreparation"),
    acquiring: t("artifactAcquiring"),
    ready: t("artifactReady"),
    retained: t("artifactRetained"),
    cleanup_eligible: t("cleanupEligible"),
    cleanup_failed: t("cleanupFailed"),
  } as const;
  // The current server always sends an explicit projection, but legacy or
  // malformed payloads may omit it or leave fields out. Absent registry facts
  // stay explicitly unknown instead of being cast into a shape the server did
  // not send, and nullable requested_steps are read safely.
  const state = artifact?.state ?? "unknown";
  const stateLabel: string = stateLabels[state] ?? stateLabels.unknown;
  const requestedSteps = artifact?.requested_steps ?? [];
  const requestedStepsKnown = artifact?.requested_steps_known === true;
  const reusedFromDifferentOperation =
    !!artifact?.creator_operation_id &&
    !!artifact?.borrower_operation_id &&
    artifact.creator_operation_id !== artifact.borrower_operation_id;

  return (
    <section className="sources-panel" aria-labelledby="staged-artifact-title">
      <h2 id="staged-artifact-title">{t("stagedArtifact")}</h2>
      <p>
        {t("state")}: {stateLabel}
      </p>
      {state === "ready" && (
        <p className="sources-note">{t("artifactReadyNote")}</p>
      )}
      {state === "unknown" && (
        <p className="sources-note">{t("artifactUnknownNote")}</p>
      )}
      {requestedStepsKnown ? (
        <>
          <p>
            {t("requestedSteps")}:{" "}
            {requestedSteps.length > 0
              ? requestedSteps
                  .map((step) =>
                    t(
                      step === "sha256"
                        ? "sha256"
                        : step === "probe"
                          ? "probeStep"
                          : step === "fingerprint"
                            ? "fingerprint"
                            : step === "metadata"
                              ? "metadata"
                              : "unknown",
                    ),
                  )
                  .join(", ")
              : t("noSteps")}
          </p>
          {requestedSteps.length === 0 && (
            <p className="sources-note">{t("emptyStepsNote")}</p>
          )}
        </>
      ) : (
        <p className="sources-note">{t("requestedHistoryUnknown")}</p>
      )}
      {reusedFromDifferentOperation && (
        <p className="sources-note">{t("artifactReused")}</p>
      )}
      {state === "cleanup_failed" && artifact?.safe_error && (
        <p role="alert">
          {t("artifactCleanupFailed")} {artifact.safe_error}
        </p>
      )}
    </section>
  );
}
