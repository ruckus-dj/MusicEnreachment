import { useEffect, useRef } from "react";
import { Link } from "react-aria-components";
import { AppButton } from "../../components/AppButton";
import { SourceAnalysisSteps } from "./SourceAnalysisSteps";
import {
  type SourceInspectorMessageKey,
  sourceInspectorMessage,
} from "./sourceInspectorMessages";
import {
  type SourceInspectorArtifact,
  type SourceInspectorArtifactState,
  type SourceInspectorLocale,
  type SourceInspectorViewProps,
  sourceInspectorDateTime,
  sourceInspectorNumber,
} from "./sourceInspectorModel";

const page =
  "mx-auto grid min-w-0 max-w-5xl gap-2 py-3 text-[13px] leading-[1.4] text-[var(--ink)]";
const breadcrumb = "flex flex-wrap items-center gap-2 text-[11px]";
const breadcrumbLink =
  "rounded-sm text-[var(--accent)] wrap-anywhere hover:underline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--focus)]";
const pageTitle = "m-0 text-xl font-semibold wrap-anywhere";
const panel =
  "grid min-w-0 gap-1 rounded-[5px] border border-[var(--line)] bg-[var(--surface)] p-2.5";
const heading = "m-0 text-sm font-semibold";
const note = "m-0 text-[11px] text-[var(--muted)] wrap-anywhere";
const alert =
  "my-2 rounded-[4px] border border-[var(--line)] bg-[var(--error-soft)] px-2.5 py-2 text-[var(--error)] wrap-anywhere";
const facts = "m-0 grid text-[13px]";
const fact =
  "grid min-w-0 gap-3 border-b border-[var(--line)] py-1.5 last:border-b-0 min-[600px]:grid-cols-[minmax(120px,1fr)_minmax(0,2fr)] max-[599px]:gap-0.5";
const term = "m-0 text-[var(--muted)]";
const detail = "m-0 min-w-0 wrap-anywhere";
const code = "font-mono text-[11px] wrap-anywhere";

const artifactStateMessages: Record<
  SourceInspectorArtifactState,
  SourceInspectorMessageKey
> = {
  unknown: "unknown",
  preparation: "artifactPreparation",
  acquiring: "artifactAcquiring",
  ready: "artifactReady",
  retained: "artifactRetained",
  cleanup_eligible: "cleanupEligible",
  cleanup_failed: "cleanupFailed",
};

const requestedStepMessages: Record<string, SourceInspectorMessageKey> = {
  sha256: "sha256",
  probe: "probeStep",
  fingerprint: "fingerprint",
  metadata: "metadata",
};

const availabilityMessages: Record<
  "available" | "unavailable" | "unknown",
  SourceInspectorMessageKey
> = {
  available: "sourceAvailable",
  unavailable: "sourceUnavailable",
  unknown: "sourceUnchecked",
};

export function SourceInspectorView(props: SourceInspectorViewProps) {
  const {
    locale,
    sourcesHref,
    sourceHref,
    file,
    steps,
    sha256,
    probe,
    fingerprint,
    metadata,
    artifact,
    retryAvailable,
    onRetryStep,
    fingerprintRerunAvailable,
    onRerunFingerprint,
    activeFingerprintVersion,
    matching,
    loading,
    busy,
    pending,
    operationState,
    operationStage,
    streamInterrupted,
    error,
    onRetryLoad,
  } = props;
  const t = (
    key: SourceInspectorMessageKey,
    values?: Record<string, string | number>,
  ) => sourceInspectorMessage(locale, key, values);
  const headingRef = useRef<HTMLHeadingElement>(null);
  const alertRef = useRef<HTMLParagraphElement>(null);
  const hadError = useRef(false);
  useEffect(() => {
    headingRef.current?.focus();
  }, []);
  useEffect(() => {
    // Focus the notice when an error appears, not when its localized text
    // changes: switching RU/EN must not move focus inside the inspector.
    if (error && !hadError.current) alertRef.current?.focus();
    hadError.current = Boolean(error);
  }, [error]);

  return (
    <section className={page} aria-labelledby="inspector-title">
      <nav aria-label={t("breadcrumb")} className={breadcrumb}>
        <Link href={sourcesHref} className={breadcrumbLink}>
          {t("title")}
        </Link>
        <span aria-hidden="true">/</span>
        <Link href={sourceHref} className={breadcrumbLink}>
          {t("sourceTitle")}
        </Link>
        <span aria-hidden="true">/</span>
        <span>{t("file")}</span>
      </nav>
      <h1
        id="inspector-title"
        ref={headingRef}
        tabIndex={-1}
        className={pageTitle}
      >
        {t("inspector")}
      </h1>
      {loading && <p role="status">{t("loadingInspection")}</p>}
      {error && (
        <p ref={alertRef} tabIndex={-1} role="alert" className={alert}>
          {error}
        </p>
      )}
      {error && onRetryLoad && (
        <div>
          <AppButton isDisabled={loading || busy} onPress={onRetryLoad}>
            {t("retryLoad")}
          </AppButton>
        </div>
      )}
      {file && (
        <>
          <section className={panel} aria-labelledby="identity-title">
            <h2 id="identity-title" className={heading}>
              {t("originalFile")}
            </h2>
            <dl className={facts}>
              <div className={fact}>
                <dt className={term}>{t("relativePath")}</dt>
                <dd className={detail}>
                  <code className={code}>
                    {file.relativePath ?? t("unknown")}
                  </code>
                </dd>
              </div>
              <div className={fact}>
                <dt className={term}>{t("inventoryPath")}</dt>
                <dd className={detail}>
                  <code className={code}>
                    {file.inventoryPath ?? t("unknown")}
                  </code>
                </dd>
              </div>
              <div className={fact}>
                <dt className={term}>{t("size")}</dt>
                <dd className={detail}>
                  {file.sizeBytes === undefined
                    ? t("unknown")
                    : `${sourceInspectorNumber(locale).format(file.sizeBytes)} ${t("bytesUnit")}`}
                </dd>
              </div>
              <div className={fact}>
                <dt className={term}>{t("modified")}</dt>
                <dd className={detail}>
                  {file.modifiedAt === undefined
                    ? t("unknown")
                    : sourceInspectorDateTime(file.modifiedAt, locale)}
                </dd>
              </div>
              <div className={fact}>
                <dt className={term}>{t("analysisImpact")}</dt>
                <dd className={detail}>{t("analysisDoesNotChange")}</dd>
              </div>
              <div className={fact}>
                <dt className={term}>{t("sourceAvailability")}</dt>
                <dd className={detail}>
                  {t(availabilityMessages[file.availability])}
                </dd>
              </div>
            </dl>
            {!file.enabled && <p className={note}>{t("disabledAnalysis")}</p>}
            {file.stale && <p className={note}>{t("staleInventory")}</p>}
            {file.availability === "unavailable" && (
              <p className={note}>
                {t("unavailableResult")} {file.safeError}
              </p>
            )}
          </section>
          <section className={panel} aria-labelledby="analysis-title">
            <h2 id="analysis-title" className={heading}>
              {t("technicalAnalysis")}
            </h2>
            {busy && <p role="status">{t("requestRunning")}</p>}
            {pending && (
              <p role="status">
                {operationState === "queued"
                  ? t("queuedStep")
                  : operationStage === "applying"
                    ? t("savingStep")
                    : t("runningStep")}
              </p>
            )}
            {streamInterrupted && pending && (
              <p role="status">{t("streamInterrupted")}</p>
            )}
          </section>
          <StagedArtifactStatus locale={locale} artifact={artifact} />
          <SourceAnalysisSteps
            locale={locale}
            steps={steps}
            sha256={sha256}
            probe={probe}
            fingerprint={fingerprint}
            metadata={metadata}
            retryAvailable={retryAvailable}
            onRetry={onRetryStep}
            fingerprintRerunAvailable={fingerprintRerunAvailable}
            onRerunFingerprint={onRerunFingerprint}
            activeFingerprintVersion={activeFingerprintVersion}
            matching={matching}
          />
        </>
      )}
    </section>
  );
}

function StagedArtifactStatus({
  locale,
  artifact,
}: {
  readonly locale: SourceInspectorLocale;
  readonly artifact?: SourceInspectorArtifact;
}) {
  const t = (key: SourceInspectorMessageKey) =>
    sourceInspectorMessage(locale, key);
  // The current server always sends an explicit projection, but legacy or
  // malformed payloads may omit it or leave fields out. Absent registry facts
  // stay explicitly unknown instead of being cast into a shape the server did
  // not send, and nullable requested_steps are read safely.
  const state = artifact?.state ?? "unknown";
  const stateLabel = t(artifactStateMessages[state] ?? "unknown");
  const requestedSteps = artifact?.requestedSteps ?? [];
  const requestedStepsKnown = artifact?.requestedStepsKnown === true;

  return (
    <section className={panel} aria-labelledby="staged-artifact-title">
      <h2 id="staged-artifact-title" className={heading}>
        {t("stagedArtifact")}
      </h2>
      <p className="m-0">
        {t("state")}: {stateLabel}
      </p>
      {state === "ready" && <p className={note}>{t("artifactReadyNote")}</p>}
      {state === "unknown" && (
        <p className={note}>{t("artifactUnknownNote")}</p>
      )}
      {requestedStepsKnown ? (
        <>
          <p className="m-0">
            {t("requestedSteps")}:{" "}
            {requestedSteps.length > 0
              ? requestedSteps
                  .map((step) => t(requestedStepMessages[step] ?? "unknown"))
                  .join(", ")
              : t("noSteps")}
          </p>
          {requestedSteps.length === 0 && (
            <p className={note}>{t("emptyStepsNote")}</p>
          )}
        </>
      ) : (
        <p className={note}>{t("requestedHistoryUnknown")}</p>
      )}
      {artifact?.reusedFromOtherOperation && (
        <p className={note}>{t("artifactReused")}</p>
      )}
      {state === "cleanup_failed" && artifact?.safeError && (
        <p role="alert" className={alert}>
          {t("artifactCleanupFailed")} {artifact.safeError}
        </p>
      )}
    </section>
  );
}
