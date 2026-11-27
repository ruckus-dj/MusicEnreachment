import { AppButton } from "../../components/AppButton";
import { SourceTechnicalResult } from "./SourceTechnicalResult";
import {
  type SourceInspectorMessageKey,
  sourceInspectorMessage,
} from "./sourceInspectorMessages";
import {
  type SourceInspectorLocale,
  type SourceInspectorMetadataSuccess,
  type SourceInspectorProbe,
  type SourceInspectorSHA256Success,
  type SourceInspectorStepName,
  type SourceInspectorStepStatus,
  sourceInspectorStepBusy,
} from "./sourceInspectorModel";

const panel =
  "grid min-w-0 gap-1 rounded-lg border border-[var(--line)] bg-[var(--surface)] p-2.5";
const heading = "m-0 text-sm font-semibold";
const note = "m-0 text-xs text-[var(--muted)] wrap-anywhere";
const alert =
  "my-2 rounded-lg border border-[var(--line)] bg-[var(--error-soft)] px-2.5 py-2 text-[var(--error)] wrap-anywhere";
const stepsList = "m-0 grid list-none gap-2 p-0";
const stepPanel =
  "grid min-w-0 gap-1 rounded border border-[var(--line)] bg-[var(--surface)] p-2";
const facts = "m-0 grid text-sm";
const fact =
  "grid min-w-0 gap-3 border-b border-[var(--line)] py-1.5 last:border-b-0 min-[600px]:grid-cols-[minmax(120px,1fr)_minmax(0,2fr)] max-[599px]:gap-0.5";
const term = "m-0 text-[var(--muted)]";
const detail = "m-0 min-w-0 wrap-anywhere";
const code = "font-mono text-xs wrap-anywhere";

const stepLabels: ReadonlyArray<{
  readonly name: SourceInspectorStepName;
  readonly label: "sha256" | "probeStep" | "fingerprint" | "metadata";
}> = [
  { name: "sha256", label: "sha256" },
  { name: "probe", label: "probeStep" },
  { name: "fingerprint", label: "fingerprint" },
  { name: "metadata", label: "metadata" },
];

const stateMessages: Record<
  SourceInspectorStepStatus["state"],
  SourceInspectorMessageKey
> = {
  not_requested: "notRequested",
  loading: "loadingState",
  pending: "pending",
  queued: "queued",
  running: "running",
  succeeded: "succeeded",
  failed: "failed",
  skipped: "skipped",
};

export interface SourceAnalysisStepsProps {
  readonly locale: SourceInspectorLocale;
  readonly steps: Readonly<
    Partial<Record<SourceInspectorStepName, SourceInspectorStepStatus>>
  >;
  readonly sha256?: SourceInspectorSHA256Success;
  readonly probe?: SourceInspectorProbe;
  readonly fingerprintReady: boolean;
  readonly metadata?: SourceInspectorMetadataSuccess;
  readonly retryAvailable: Readonly<
    Partial<Record<SourceInspectorStepName, boolean>>
  >;
  readonly onRetry: (step: SourceInspectorStepName) => void;
  readonly fingerprintRerunAvailable: boolean;
  readonly onRerunFingerprint: () => void;
  // Absent matching evidence renders as unknown rather than as unavailable.
  readonly matching?: { readonly eligible: boolean; readonly reason?: string };
}

export function SourceAnalysisSteps({
  locale,
  steps,
  sha256,
  probe,
  fingerprintReady,
  metadata,
  retryAvailable,
  onRetry,
  fingerprintRerunAvailable,
  onRerunFingerprint,
  matching,
}: SourceAnalysisStepsProps) {
  const t = (
    key: SourceInspectorMessageKey,
    values?: Record<string, string | number>,
  ) => sourceInspectorMessage(locale, key, values);
  const unknown = t("unknown");
  const stepLabel = (name: SourceInspectorStepName) =>
    t(stepLabels.find((step) => step.name === name)?.label ?? "metadata");
  const firstError = stepLabels
    .map(({ name }) => ({
      name,
      label: stepLabel(name),
      error: steps[name]?.safeError,
    }))
    .find(({ error }) => error);

  return (
    <section className={panel} aria-labelledby="analysis-steps-title">
      <h2 id="analysis-steps-title" className={heading}>
        {t("analysisSteps")}
      </h2>
      {firstError && (
        <p role="alert" className={alert}>
          {t("stepError", { step: firstError.label })} {firstError.error}
        </p>
      )}
      <ul className={stepsList}>
        {stepLabels.map(({ name }) => {
          const label = stepLabel(name);
          const step = steps[name] ?? { state: "not_requested" as const };
          const busy = sourceInspectorStepBusy(step.state);
          const successful =
            (name === "sha256" && !!sha256) ||
            (name === "probe" && !!probe) ||
            (name === "metadata" && !!metadata);
          const stateMessage =
            name === "fingerprint" && step.state === "succeeded"
              ? fingerprintReady
                ? "fingerprintReady"
                : "fingerprintNotReady"
              : stateMessages[step.state];
          return (
            <li key={name} className={stepPanel} aria-label={label}>
              <h3 tabIndex={-1} className={heading}>
                {label}
              </h3>
              <p className="m-0">
                {t("state")}: {t(stateMessage)}
              </p>
              {step.safeError && (
                <p role="alert" className={alert}>
                  {step.safeError}
                </p>
              )}
              {step.skipReason && <p className={note}>{step.skipReason}</p>}
              {name === "sha256" && sha256 && (
                <p className="m-0 wrap-anywhere">
                  SHA-256: <code className={code}>{sha256.value}</code>
                  {sha256.provenance && <> · {sha256.provenance}</>}
                </p>
              )}
              {name === "probe" && probe && (
                <>
                  {probe.provenance && (
                    <p className={note}>{probe.provenance}</p>
                  )}
                  <SourceTechnicalResult
                    locale={locale}
                    result={probe.result}
                  />
                </>
              )}
              {name === "fingerprint" &&
                fingerprintReady &&
                step.state !== "succeeded" && (
                  <p className={note}>{t("savedFingerprintAvailable")}</p>
                )}
              {name === "metadata" && metadata && (
                <>
                  <p className="m-0">
                    {t("metadataSource")} {metadata.provenance.name}{" "}
                    {metadata.provenance.version}
                  </p>
                  <p className={note}>{metadata.provenance.contract}</p>
                  {Object.entries(metadata.tags).length > 0 ? (
                    <dl className={facts}>
                      {Object.entries(metadata.tags).map(([tag, values]) => (
                        <div key={tag} className={fact}>
                          <dt className={term}>{tag}</dt>
                          <dd className={detail}>
                            {values === null ? unknown : values.join(" · ")}
                          </dd>
                        </div>
                      ))}
                    </dl>
                  ) : (
                    <p className={note}>{t("noSavedTags")}</p>
                  )}
                </>
              )}
              {step.state === "failed" && retryAvailable[name] && (
                <AppButton
                  isDisabled={busy}
                  onPress={(event) => {
                    event.target.closest("li")?.querySelector("h3")?.focus();
                    onRetry(name);
                  }}
                >
                  {t("retryStep", { step: label })}
                </AppButton>
              )}
              {name === "fingerprint" && (
                <AppButton
                  isDisabled={busy || !fingerprintRerunAvailable}
                  onPress={(event) => {
                    event.target.closest("li")?.querySelector("h3")?.focus();
                    onRerunFingerprint();
                  }}
                >
                  {t("rerunFingerprint")}
                </AppButton>
              )}
              {name !== "fingerprint" &&
                !successful &&
                step.state === "succeeded" && (
                  <p className={note}>{t("noSuccessfulStep")}</p>
                )}
            </li>
          );
        })}
      </ul>
      <section
        aria-labelledby="matching-title"
        className="grid gap-1 border-t border-[var(--line)] pt-2"
      >
        <h3 id="matching-title" className={heading}>
          {t("matching")}
        </h3>
        <p className="m-0">
          {matching
            ? matching.eligible
              ? t("available")
              : t("unavailable")
            : t("unknown")}
          {matching?.reason && ` · ${matching.reason}`}
        </p>
      </section>
    </section>
  );
}
