import type { SourceTechnicalResultResponse } from "../../api/generated/client.schemas";
import { AppButton } from "../../components/AppButton";
import { useI18n } from "../../i18n/I18nProvider";
import { SourceTechnicalResult } from "./SourceTechnicalResult";
import { sourceMessage } from "./sourceMessages";

export type SourceAnalysisStepName =
  | "sha256"
  | "probe"
  | "fingerprint"
  | "metadata";
export type SourceAnalysisStepState =
  | "not_requested"
  | "loading"
  | "queued"
  | "running"
  | "failed"
  | "succeeded"
  | "skipped"
  | "pending";

export interface SourceAnalysisStepStatus {
  readonly state: SourceAnalysisStepState;
  readonly safeError?: string;
  readonly skipReason?: string;
}

export interface SourceAnalysisFingerprintSuccess {
  readonly value: string;
  readonly version: string;
  readonly provenance?: string;
}

export interface SourceAnalysisMetadataSuccess {
  // Tag values are nullable in the API projection: a present key with a null
  // value means the tag exists but its values are unknown. Keep the key and
  // render the unknown text instead of dropping it or crashing on join.
  readonly tags: Readonly<Record<string, readonly string[] | null>>;
  readonly provenance: {
    readonly name: string;
    readonly version: string;
    readonly contract: string;
  };
}

export interface SourceAnalysisStepsProps {
  readonly steps: Readonly<
    Partial<Record<SourceAnalysisStepName, SourceAnalysisStepStatus>>
  >;
  readonly sha256?: { readonly value: string; readonly provenance?: string };
  readonly probeResult?: SourceTechnicalResultResponse;
  readonly probeProvenance?: string;
  readonly fingerprint?: SourceAnalysisFingerprintSuccess;
  readonly metadata?: SourceAnalysisMetadataSuccess;
  readonly retryAvailable: Readonly<
    Partial<Record<SourceAnalysisStepName, boolean>>
  >;
  readonly onRetry: (step: SourceAnalysisStepName) => void;
  readonly fingerprintRerunAvailable: boolean;
  readonly onRerunFingerprint: () => void;
  // The active fpcalc version is optional: an absent value means the active
  // version is unknown and must never be compared as if it were a real version.
  readonly activeFingerprintVersion?: string;
  readonly matching: { readonly eligible: boolean; readonly reason?: string };
}

const stepNames: ReadonlyArray<{
  readonly name: SourceAnalysisStepName;
  readonly label: "sha256" | "probeStep" | "fingerprint" | "metadata";
}> = [
  { name: "sha256", label: "sha256" },
  { name: "probe", label: "probeStep" },
  { name: "fingerprint", label: "fingerprint" },
  { name: "metadata", label: "metadata" },
];

function isBusy(state: SourceAnalysisStepState): boolean {
  return (
    state === "loading" ||
    state === "queued" ||
    state === "running" ||
    state === "pending"
  );
}

function stateMessageKey(state: SourceAnalysisStepState) {
  switch (state) {
    case "not_requested":
      return "notRequested";
    case "loading":
      return "loadingState";
    default:
      return state;
  }
}

export function SourceAnalysisSteps({
  steps,
  sha256,
  probeResult,
  probeProvenance,
  fingerprint,
  metadata,
  retryAvailable,
  onRetry,
  fingerprintRerunAvailable,
  onRerunFingerprint,
  activeFingerprintVersion,
  matching,
}: SourceAnalysisStepsProps) {
  const { locale } = useI18n();
  const t = (
    key: Parameters<typeof sourceMessage>[1],
    values?: Record<string, string | number>,
  ) => sourceMessage(locale, key, values);
  const unknown = t("unknown");
  const stepLabel = (name: SourceAnalysisStepName) =>
    t(stepNames.find((step) => step.name === name)?.label ?? "metadata");
  const firstError = stepNames
    .map(({ name }) => ({
      name,
      label: stepLabel(name),
      error: steps[name]?.safeError,
    }))
    .find(({ error }) => error);

  return (
    <section className="sources-panel" aria-labelledby="analysis-steps-title">
      <h2 id="analysis-steps-title">{t("analysisSteps")}</h2>
      {firstError && (
        <p role="alert">
          {t("stepError", { step: firstError.label })} {firstError.error}
        </p>
      )}
      <ul>
        {stepNames.map(({ name }) => {
          const label = stepLabel(name);
          const step = steps[name] ?? { state: "not_requested" as const };
          const busy = isBusy(step.state);
          const hasSuccess =
            (name === "sha256" && !!sha256) ||
            (name === "probe" && !!probeResult) ||
            (name === "fingerprint" && !!fingerprint);
          const successful = hasSuccess || (name === "metadata" && !!metadata);
          return (
            <li key={name} aria-label={label}>
              <h3>{label}</h3>
              <p>
                {t("state")}: {t(stateMessageKey(step.state))}
              </p>
              {step.safeError && <p role="alert">{step.safeError}</p>}
              {step.skipReason && (
                <p className="sources-note">{step.skipReason}</p>
              )}
              {name === "sha256" && sha256 && (
                <p>
                  SHA-256: <code>{sha256.value}</code>
                  {sha256.provenance && <> · {sha256.provenance}</>}
                </p>
              )}
              {name === "probe" && probeResult && (
                <>
                  {probeProvenance && (
                    <p className="sources-note">{probeProvenance}</p>
                  )}
                  <SourceTechnicalResult result={probeResult} />
                </>
              )}
              {name === "fingerprint" && fingerprint && (
                <>
                  <p>
                    {t("fingerprintValue")} <code>{fingerprint.value}</code>
                  </p>
                  <p>
                    {t("resultVersion")} {fingerprint.version}
                    {activeFingerprintVersion !== undefined &&
                      activeFingerprintVersion !== fingerprint.version &&
                      ` · ${t("activeVersion")} ${activeFingerprintVersion}`}
                  </p>
                  {activeFingerprintVersion === undefined && (
                    <p className="sources-note">
                      {t("activeVersionUnavailable")}
                    </p>
                  )}
                  {fingerprint.provenance && (
                    <p className="sources-note">{fingerprint.provenance}</p>
                  )}
                </>
              )}
              {name === "metadata" && metadata && (
                <>
                  <p>
                    {t("metadataSource")} {metadata.provenance.name}{" "}
                    {metadata.provenance.version}
                  </p>
                  <p className="sources-note">{metadata.provenance.contract}</p>
                  {Object.entries(metadata.tags).length > 0 ? (
                    <dl className="sources-summary">
                      {Object.entries(metadata.tags).map(([tag, values]) => (
                        <div key={tag}>
                          <dt>{tag}</dt>
                          <dd>
                            {values === null ? unknown : values.join(" · ")}
                          </dd>
                        </div>
                      ))}
                    </dl>
                  ) : (
                    <p className="sources-note">{t("noSavedTags")}</p>
                  )}
                </>
              )}
              {step.state === "failed" && retryAvailable[name] && (
                <AppButton isDisabled={busy} onPress={() => onRetry(name)}>
                  {t("retryStep", { step: label })}
                </AppButton>
              )}
              {name === "fingerprint" && (
                <AppButton
                  isDisabled={busy || !fingerprintRerunAvailable}
                  onPress={onRerunFingerprint}
                >
                  {t("rerunFingerprint")}
                </AppButton>
              )}
              {!successful &&
                step.state !== "failed" &&
                step.state !== "skipped" &&
                step.state !== "not_requested" && (
                  <p className="sources-note">{t("noSuccessfulStep")}</p>
                )}
            </li>
          );
        })}
      </ul>
      <section aria-labelledby="matching-title">
        <h3 id="matching-title">{t("matching")}</h3>
        <p>
          {matching.eligible ? t("available") : t("unavailable")}
          {matching.reason && ` · ${matching.reason}`}
        </p>
      </section>
    </section>
  );
}
