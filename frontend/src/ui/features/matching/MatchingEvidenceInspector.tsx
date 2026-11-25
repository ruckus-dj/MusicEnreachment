import {
  type EvidenceOverrideMessages,
  evidenceOverrideMessages,
} from "./evidenceOverrideMessages";
import type {
  MatchingWorkspaceCandidate,
  MatchingWorkspaceLocale,
  MatchingWorkspacePosition,
} from "./types";

export type MatchingEvidenceInspectorProps = {
  candidate?: MatchingWorkspaceCandidate;
  position: MatchingWorkspacePosition;
  rawTags?: Record<string, string[] | null>;
  /** Saved readable projection of rawTags; shown beside raw only when distinct. */
  displayTags?: Record<string, string[] | null>;
  /** Explicit locale; the portable module never reads an app i18n provider. */
  locale: MatchingWorkspaceLocale;
};

type AppliedFactor = {
  component: string;
  value?: number;
  weight?: number;
};

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function sameTags(
  left: Record<string, string[] | null>,
  right: Record<string, string[] | null>,
) {
  const leftKeys = Object.keys(left).sort();
  const rightKeys = Object.keys(right).sort();
  return (
    leftKeys.length === rightKeys.length &&
    leftKeys.every((key, index) => {
      if (key !== rightKeys[index]) return false;
      const leftValues = left[key] ?? [];
      const rightValues = right[key] ?? [];
      return (
        leftValues.length === rightValues.length &&
        leftValues.every(
          (value, valueIndex) => value === rightValues[valueIndex],
        )
      );
    })
  );
}

function fieldLabel(key: string): string {
  return key
    .replaceAll("_", " ")
    .replace(/[A-Z]/g, (letter) => ` ${letter.toLowerCase()}`)
    .trim();
}

function numberField(
  record: Record<string, unknown> | undefined,
  ...keys: string[]
): number | undefined {
  if (!record) return undefined;
  for (const key of keys) {
    const value = record[key];
    if (typeof value === "number" && Number.isFinite(value)) return value;
  }
  return undefined;
}

function recordField(
  record: Record<string, unknown> | undefined,
  ...keys: string[]
): Record<string, unknown> | undefined {
  if (!record) return undefined;
  for (const key of keys) {
    const value = record[key];
    if (isRecord(value)) return value;
  }
  return undefined;
}

function shortPercent(
  value: number | undefined,
  copy: EvidenceOverrideMessages,
) {
  return value === undefined
    ? copy.missingValue
    : `${Math.round(value * 100)}%`;
}

function savedFactors(
  profile: Record<string, unknown> | undefined,
): Record<string, unknown>[] {
  const factors = profile?.Factors ?? profile?.factors;
  return Array.isArray(factors) ? factors.filter(isRecord) : [];
}

function appliedFactors(
  profile: Record<string, unknown> | undefined,
): AppliedFactor[] {
  return savedFactors(profile).flatMap((factor) => {
    const state = factor.State ?? factor.state;
    if (typeof state === "string" && state !== "available") return [];
    const component = factor.Component ?? factor.component;
    if (typeof component !== "string" || !component) return [];
    return [
      {
        component: fieldLabel(component),
        value: numberField(factor, "Value", "value"),
        weight: numberField(factor, "Weight", "weight"),
      },
    ];
  });
}

function selectedEdge(
  candidate: MatchingWorkspaceCandidate | undefined,
  position: MatchingWorkspacePosition,
): Record<string, unknown> | undefined {
  const edges = (candidate?.edges ?? []).filter(isRecord);
  if (!edges.length) return undefined;
  const candidatePosition = candidate?.positions?.find(
    (item) => item.track_id === position.track_id,
  );
  if (!candidatePosition) return undefined;
  const { medium, position: trackPosition } = candidatePosition;
  return edges.find(
    (edge) =>
      numberField(edge, "medium") === medium &&
      numberField(edge, "position") === trackPosition,
  );
}

export function MatchingEvidenceInspector({
  candidate,
  position,
  rawTags,
  displayTags,
  locale,
}: MatchingEvidenceInspectorProps) {
  const copy = evidenceOverrideMessages[locale];
  const candidatePosition = candidate?.positions?.find(
    (item) => item.track_id === position.track_id,
  );
  const edge = selectedEdge(candidate, position);
  const assignmentProfile = recordField(
    edge,
    "assignment_profile",
    "assignmentProfile",
  );
  const releaseProfile = recordField(edge, "release_profile", "releaseProfile");
  const matchConfidence =
    numberField(assignmentProfile, "Confidence", "confidence") ??
    (typeof candidatePosition?.assignment_confidence === "number"
      ? candidatePosition.assignment_confidence
      : undefined);
  const factors = appliedFactors(assignmentProfile);
  const savedFactorCount = savedFactors(assignmentProfile).length;
  const skippedFactorCount = savedFactorCount - factors.length;
  const number = position.displayed_number || `${position.position + 1}`;
  const readableTags =
    rawTags && displayTags && !sameTags(rawTags, displayTags)
      ? displayTags
      : undefined;

  return (
    <section
      className="mt-3 rounded border border-[var(--line)] bg-[var(--raised)] p-3"
      aria-label={copy.evidenceLabel}
    >
      <h4 className="font-medium">{copy.evidenceHeading}</h4>
      <p className="mt-1 text-sm text-[var(--muted)]">
        {copy.evidenceExplanation}
      </p>
      <div className="mt-2 rounded border border-[var(--line)] bg-[var(--surface)] p-2">
        <p className="text-sm font-medium">
          {copy.trackPosition(position.medium + 1, number, position.title)}
        </p>
        {candidate ? (
          edge ? (
            <>
              <p className="mt-1 text-sm">
                <span className="text-[var(--muted)]">
                  {copy.matchConfidence}
                  {": "}
                </span>
                <span className="text-lg font-semibold tabular-nums">
                  {shortPercent(matchConfidence, copy)}
                </span>
              </p>
              {savedFactorCount ? (
                <p className="mt-1 text-xs text-[var(--muted)]">
                  {copy.scoreSummary(factors.length, savedFactorCount)}
                </p>
              ) : null}
            </>
          ) : (
            <p className="mt-1 text-sm">
              {candidate.edges?.length
                ? copy.noPositionEvidence
                : copy.noFactors}
            </p>
          )
        ) : (
          <p className="mt-1 text-sm">{copy.missingCandidate}</p>
        )}
      </div>
      {rawTags ? (
        <details className="mt-2 text-sm">
          <summary className="cursor-pointer font-medium">
            {copy.rawTags}
          </summary>
          {readableTags ? (
            <div className="mt-2 grid gap-2 sm:grid-cols-2">
              <div className="min-w-0">
                <p className="font-medium">{copy.originalTags}</p>
                <pre className="mt-1 overflow-x-auto overscroll-x-contain whitespace-pre-wrap rounded bg-[var(--surface)] p-2">
                  {JSON.stringify(rawTags, null, 2)}
                </pre>
              </div>
              <div className="min-w-0">
                <p className="font-medium">{copy.readableTags}</p>
                <pre className="mt-1 overflow-x-auto overscroll-x-contain whitespace-pre-wrap rounded bg-[var(--surface)] p-2">
                  {JSON.stringify(readableTags, null, 2)}
                </pre>
              </div>
            </div>
          ) : (
            <pre className="mt-2 overflow-x-auto overscroll-x-contain whitespace-pre-wrap rounded bg-[var(--surface)] p-2">
              {JSON.stringify(rawTags, null, 2)}
            </pre>
          )}
        </details>
      ) : null}
      {edge ? (
        <details className="mt-2 text-sm">
          <summary className="cursor-pointer font-medium">
            {copy.diagnostics}
          </summary>
          <div className="mt-2 space-y-2">
            <dl className="grid gap-1 sm:grid-cols-2">
              <div>
                <dt className="font-medium">{copy.releaseMatch}</dt>
                <dd className="tabular-nums">
                  {shortPercent(
                    numberField(releaseProfile, "Confidence", "confidence"),
                    copy,
                  )}
                </dd>
              </div>
              <div>
                <dt className="font-medium">{copy.assignmentMatch}</dt>
                <dd className="tabular-nums">
                  {shortPercent(matchConfidence, copy)}
                </dd>
              </div>
            </dl>
            {factors.length ? (
              <ul className="space-y-1">
                {factors.map((factor) => (
                  <li
                    key={factor.component}
                    className="flex flex-wrap justify-between gap-2"
                  >
                    <span>{factor.component}</span>
                    <span className="tabular-nums text-[var(--muted)]">
                      {shortPercent(factor.value, copy)}
                      {factor.weight
                        ? ` · ${copy.factorWeight(factor.weight)}`
                        : ""}
                    </span>
                  </li>
                ))}
              </ul>
            ) : (
              <p>{copy.noAppliedFactors}</p>
            )}
            {skippedFactorCount > 0 ? (
              <p className="text-xs text-[var(--muted)]">
                {copy.factorsNotApplied(skippedFactorCount)}
              </p>
            ) : null}
          </div>
        </details>
      ) : null}
    </section>
  );
}
