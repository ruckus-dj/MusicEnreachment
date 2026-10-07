import type { SourceTechnicalResultResponse } from "../../api/generated/client.schemas";
import { AppButton } from "../../components/AppButton";
import { SourceTechnicalResult } from "./SourceTechnicalResult";

export type SourceAnalysisStepName = "sha256" | "probe" | "fingerprint";
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

export interface SourceAnalysisStepsProps {
  readonly steps: Readonly<
    Record<SourceAnalysisStepName, SourceAnalysisStepStatus>
  >;
  readonly sha256?: { readonly value: string; readonly provenance?: string };
  readonly probeResult?: SourceTechnicalResultResponse;
  readonly probeProvenance?: string;
  readonly fingerprint?: SourceAnalysisFingerprintSuccess;
  readonly retryAvailable: Readonly<Record<SourceAnalysisStepName, boolean>>;
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
  readonly label: string;
}> = [
  { name: "sha256", label: "SHA-256" },
  { name: "probe", label: "Технический анализ ffprobe" },
  { name: "fingerprint", label: "Акустический отпечаток" },
];

function stateLabel(state: SourceAnalysisStepState): string {
  switch (state) {
    case "not_requested":
      return "Не запрошено";
    case "loading":
      return "Загрузка";
    case "queued":
      return "В очереди";
    case "running":
      return "Выполняется";
    case "failed":
      return "Ошибка";
    case "succeeded":
      return "Завершено";
    case "skipped":
      return "Пропущено";
    case "pending":
      return "Ожидает выполнения";
  }
}

function isBusy(state: SourceAnalysisStepState): boolean {
  return (
    state === "loading" ||
    state === "queued" ||
    state === "running" ||
    state === "pending"
  );
}

export function SourceAnalysisSteps({
  steps,
  sha256,
  probeResult,
  probeProvenance,
  fingerprint,
  retryAvailable,
  onRetry,
  fingerprintRerunAvailable,
  onRerunFingerprint,
  activeFingerprintVersion,
  matching,
}: SourceAnalysisStepsProps) {
  const firstError = stepNames
    .map(({ name, label }) => ({ name, label, error: steps[name].safeError }))
    .find(({ error }) => error);

  return (
    <section className="sources-panel" aria-labelledby="analysis-steps-title">
      <h2 id="analysis-steps-title">Этапы анализа файла</h2>
      {firstError && (
        <p role="alert">
          Ошибка этапа «{firstError.label}»: {firstError.error}
        </p>
      )}
      <ul>
        {stepNames.map(({ name, label }) => {
          const step = steps[name];
          const busy = isBusy(step.state);
          const hasSuccess =
            (name === "sha256" && !!sha256) ||
            (name === "probe" && !!probeResult) ||
            (name === "fingerprint" && !!fingerprint);
          return (
            <li key={name} aria-label={label}>
              <h3>{label}</h3>
              <p>Состояние: {stateLabel(step.state)}</p>
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
                    Отпечаток: <code>{fingerprint.value}</code>
                  </p>
                  <p>
                    Версия fpcalc результата: {fingerprint.version}
                    {activeFingerprintVersion !== undefined &&
                      activeFingerprintVersion !== fingerprint.version &&
                      ` · Активная версия fpcalc: ${activeFingerprintVersion}`}
                  </p>
                  {activeFingerprintVersion === undefined && (
                    <p className="sources-note">
                      Активная версия fpcalc недоступна.
                    </p>
                  )}
                  {fingerprint.provenance && (
                    <p className="sources-note">{fingerprint.provenance}</p>
                  )}
                </>
              )}
              {step.state === "failed" && retryAvailable[name] && (
                <AppButton isDisabled={busy} onPress={() => onRetry(name)}>
                  Повторить этап «{label}»
                </AppButton>
              )}
              {name === "fingerprint" && (
                <AppButton
                  isDisabled={busy || !fingerprintRerunAvailable}
                  onPress={onRerunFingerprint}
                >
                  Повторно вычислить отпечаток
                </AppButton>
              )}
              {!hasSuccess &&
                step.state !== "failed" &&
                step.state !== "skipped" &&
                step.state !== "not_requested" && (
                  <p className="sources-note">
                    Успешный результат этапа отсутствует.
                  </p>
                )}
            </li>
          );
        })}
      </ul>
      <section aria-labelledby="matching-title">
        <h3 id="matching-title">Сопоставление</h3>
        <p>
          {matching.eligible ? "Доступно" : "Недоступно"}
          {matching.reason && ` · ${matching.reason}`}
        </p>
      </section>
    </section>
  );
}
