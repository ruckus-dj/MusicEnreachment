// Presentation model for the file inspector. It is independent of the
// generated API client and of the provider that loads the detail: production
// maps a server response into this shape, while the design prototype supplies
// the same shape from demo data.

export type SourceInspectorLocale = "en" | "ru";

export type SourceInspectorStepName =
  | "sha256"
  | "probe"
  | "fingerprint"
  | "metadata";

export const sourceInspectorStepNames: readonly SourceInspectorStepName[] = [
  "sha256",
  "probe",
  "fingerprint",
  "metadata",
];

export type SourceInspectorStepState =
  | "not_requested"
  | "loading"
  | "pending"
  | "queued"
  | "running"
  | "succeeded"
  | "failed"
  | "skipped";

export type SourceInspectorRootStatus = "available" | "unavailable" | "unknown";

export interface SourceInspectorStepStatus {
  readonly state: SourceInspectorStepState;
  readonly safeError?: string;
  readonly skipReason?: string;
}

export interface SourceInspectorSHA256Success {
  readonly value: string;
  readonly provenance?: string;
}

export interface SourceInspectorMetadataSuccess {
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

export interface SourceInspectorTechnicalStream {
  readonly index?: number;
  readonly codecName?: string;
  readonly profile?: string;
  readonly durationMs?: number;
  readonly bitRate?: number;
  readonly sampleRateHz?: number;
  readonly sampleFormat?: string;
  readonly bitsPerSample?: number;
  readonly channels?: number;
  readonly channelLayout?: string;
}

export interface SourceInspectorTechnicalResult {
  readonly inspectedAt: string;
  readonly ffprobeVersion: string;
  readonly analysisPolicyVersion: number;
  readonly container: {
    readonly name?: string;
    readonly longName?: string;
    readonly durationMs?: number;
    readonly bitRate?: number;
  };
  readonly streams: readonly SourceInspectorTechnicalStream[];
  readonly tags: readonly {
    readonly key: string;
    readonly values: readonly string[];
  }[];
  readonly rawJson: unknown;
}

export type SourceInspectorArtifactState =
  | "unknown"
  | "preparation"
  | "acquiring"
  | "ready"
  | "retained"
  | "cleanup_eligible"
  | "cleanup_failed";

export interface SourceInspectorArtifact {
  readonly state: SourceInspectorArtifactState;
  readonly requestedSteps: readonly string[];
  readonly requestedStepsKnown: boolean;
  readonly reusedFromOtherOperation: boolean;
  readonly safeError?: string;
}

export interface SourceInspectorFileSummary {
  readonly relativePath?: string;
  readonly inventoryPath?: string;
  readonly sizeBytes?: number;
  readonly modifiedAt?: string;
  readonly availability: SourceInspectorRootStatus;
  readonly enabled: boolean;
  readonly stale: boolean;
  readonly safeError?: string;
}

export interface SourceInspectorMatching {
  readonly eligible: boolean;
  readonly reason?: string;
}

export interface SourceInspectorProbe {
  readonly result: SourceInspectorTechnicalResult;
  readonly provenance?: string;
}

export interface SourceInspectorViewProps {
  readonly locale: SourceInspectorLocale;
  readonly sourcesHref: string;
  readonly sourceHref: string;
  readonly file?: SourceInspectorFileSummary;
  readonly steps: Readonly<
    Partial<Record<SourceInspectorStepName, SourceInspectorStepStatus>>
  >;
  readonly sha256?: SourceInspectorSHA256Success;
  readonly probe?: SourceInspectorProbe;
  readonly metadata?: SourceInspectorMetadataSuccess;
  readonly artifact?: SourceInspectorArtifact;
  readonly retryAvailable: Readonly<
    Partial<Record<SourceInspectorStepName, boolean>>
  >;
  readonly onRetryStep: (step: SourceInspectorStepName) => void;
  readonly fingerprintRerunAvailable: boolean;
  readonly onRerunFingerprint: () => void;
  // Whether a saved fingerprint exists. The fingerprint value and its
  // version/provenance stay out of the inspector.
  readonly fingerprintReady: boolean;
  // Absent matching evidence renders as unknown instead of claiming either
  // availability or a reason that the provider did not supply.
  readonly matching?: SourceInspectorMatching;
  readonly loading: boolean;
  readonly busy: boolean;
  readonly pending: boolean;
  readonly operationState?: string;
  readonly operationStage?: string;
  readonly streamInterrupted: boolean;
  readonly error?: string;
  readonly onRetryLoad?: () => void;
}

export function sourceInspectorNumber(
  locale: SourceInspectorLocale,
  options?: Intl.NumberFormatOptions,
) {
  return new Intl.NumberFormat(locale === "ru" ? "ru-RU" : "en-US", options);
}

export function sourceInspectorDateTime(
  value: string,
  locale: SourceInspectorLocale,
) {
  return new Intl.DateTimeFormat(locale === "ru" ? "ru-RU" : "en-US", {
    dateStyle: "short",
    timeStyle: "short",
  }).format(new Date(value));
}

export function sourceInspectorMeasured(
  value: number | undefined,
  unit: string,
  locale: SourceInspectorLocale,
  unknown: string,
) {
  const number = sourceInspectorNumber(locale, { maximumFractionDigits: 3 });
  return value === undefined ? unknown : `${number.format(value)} ${unit}`;
}

export function sourceInspectorDuration(
  milliseconds: number | undefined,
  locale: SourceInspectorLocale,
  unknown: string,
) {
  if (milliseconds === undefined) return unknown;
  const seconds = Math.round(milliseconds / 1000);
  const format = (value: number) =>
    sourceInspectorNumber(locale, {
      minimumIntegerDigits: 2,
      useGrouping: false,
    }).format(value);
  return `${format(Math.floor(seconds / 3600))}:${format(Math.floor(seconds / 60) % 60)}:${format(seconds % 60)}`;
}

export function sourceInspectorStepBusy(state: SourceInspectorStepState) {
  return (
    state === "loading" ||
    state === "queued" ||
    state === "running" ||
    state === "pending"
  );
}
