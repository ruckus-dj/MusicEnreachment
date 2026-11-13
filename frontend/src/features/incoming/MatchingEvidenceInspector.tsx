import type { MatchingReviewCandidate } from "../../api/generated/client.schemas";
import { useI18n } from "../../i18n/I18nProvider";
import {
  type EvidenceOverrideMessages,
  evidenceOverrideMessages,
} from "./evidenceOverrideMessages";

type MatchingEvidenceInspectorProps = {
  candidate?: MatchingReviewCandidate;
  rawTags?: Record<string, string[] | null>;
};

function displayValue(value: unknown, copy: EvidenceOverrideMessages): string {
  if (value === null) return "null";
  if (value === undefined) return copy.missingValue;
  if (typeof value === "string") return value || copy.emptyValue;
  if (typeof value === "number" || typeof value === "boolean")
    return String(value);
  try {
    return JSON.stringify(value, null, 2);
  } catch {
    return String(value);
  }
}

function fieldLabel(key: string): string {
  return key
    .replaceAll("_", " ")
    .replace(/[A-Z]/g, (letter) => ` ${letter.toLowerCase()}`)
    .trim();
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function EvidenceFields({
  record,
  copy,
}: {
  record: Record<string, unknown>;
  copy: EvidenceOverrideMessages;
}) {
  return (
    <dl className="mt-2 grid gap-2 text-sm sm:grid-cols-2">
      {Object.entries(record).map(([key, value]) => (
        <div key={key} className="min-w-0">
          <dt className="font-medium">{fieldLabel(key)}</dt>
          <dd className="overflow-x-auto whitespace-pre-wrap break-words">
            {displayValue(value, copy)}
          </dd>
        </div>
      ))}
    </dl>
  );
}

function MatchingScoreEvidence({
  label,
  value,
  copy,
}: {
  label: string;
  value: unknown;
  copy: EvidenceOverrideMessages;
}) {
  if (!isRecord(value)) {
    return (
      <div>
        <h6 className="font-medium">{label}</h6>
        <pre className="overflow-x-auto whitespace-pre-wrap">
          {displayValue(value, copy)}
        </pre>
      </div>
    );
  }

  const factors = value.Factors ?? value.factors;
  const scoreFields = { ...value };
  delete scoreFields.Factors;
  delete scoreFields.factors;
  const factorList = Array.isArray(factors) ? factors : undefined;

  return (
    <div>
      <h6 className="font-medium">{label}</h6>
      <EvidenceFields record={scoreFields} copy={copy} />
      {factorList ? (
        <div className="mt-2 space-y-2">
          {factorList.map((factor, index) => {
            if (!isRecord(factor))
              return (
                <pre
                  key={`factor-${displayValue(factor, copy)}`}
                  className="overflow-x-auto whitespace-pre-wrap"
                >
                  {displayValue(factor, copy)}
                </pre>
              );
            return (
              <div
                key={`${String(factor.Component ?? factor.component ?? "factor")}-${displayValue(factor, copy)}`}
                className="rounded border border-stone-200 p-2"
              >
                <h6 className="font-medium">
                  {displayValue(
                    factor.Component ??
                      factor.component ??
                      copy.factor(index + 1),
                    copy,
                  )}
                </h6>
                <EvidenceFields record={factor} copy={copy} />
              </div>
            );
          })}
        </div>
      ) : factors !== undefined ? (
        <pre className="overflow-x-auto whitespace-pre-wrap">
          {displayValue(factors, copy)}
        </pre>
      ) : null}
    </div>
  );
}

export function MatchingEvidenceInspector({
  candidate,
  rawTags,
}: MatchingEvidenceInspectorProps) {
  const { locale } = useI18n();
  const copy = evidenceOverrideMessages[locale];
  const edges = candidate?.edges ?? [];
  return (
    <section
      className="mt-3 rounded border border-stone-300 bg-stone-50 p-3"
      aria-label={copy.evidenceLabel}
    >
      <h4 className="font-medium">{copy.evidenceHeading}</h4>
      <p className="mt-1 text-sm text-stone-700">{copy.evidenceExplanation}</p>
      {candidate ? (
        <dl className="mt-2 grid gap-2 text-sm sm:grid-cols-2">
          <div>
            <dt className="font-medium">{copy.releaseConfidence}</dt>
            <dd>{displayValue(candidate.release_confidence, copy)}</dd>
          </div>
          {candidate.positions?.map((position) => (
            <div key={position.track_id}>
              <dt className="font-medium">
                {copy.positionConfidence(
                  position.medium + 1,
                  position.displayed_number || position.position + 1,
                )}
              </dt>
              <dd>{displayValue(position.assignment_confidence, copy)}</dd>
            </div>
          ))}
        </dl>
      ) : (
        <p className="mt-2 text-sm">{copy.missingCandidate}</p>
      )}
      {rawTags ? (
        <details className="mt-2 text-sm">
          <summary className="cursor-pointer font-medium">
            {copy.rawTags}
          </summary>
          <pre className="mt-2 overflow-x-auto whitespace-pre-wrap rounded bg-white p-2">
            {displayValue(rawTags, copy)}
          </pre>
        </details>
      ) : null}
      <h5 className="mt-3 font-medium">{copy.factorsAndProvenance}</h5>
      {edges.length ? (
        <div className="mt-2 space-y-2">
          {edges.map((edge, index) => {
            const record = isRecord(edge) ? edge : undefined;
            const profiles: Array<{ label: string; value: unknown }> = record
              ? [
                  { label: copy.releaseProfile, value: record.release_profile },
                  {
                    label: copy.assignmentProfile,
                    value: record.assignment_profile,
                  },
                ].filter(({ value }) => value !== undefined)
              : [];
            return (
              <details
                key={`${String(record?.factor ?? record?.name ?? "edge")}-${displayValue(edge, copy)}`}
                className="rounded border border-stone-300 bg-white p-2"
                open
              >
                <summary className="cursor-pointer text-sm font-medium">
                  {displayValue(
                    record?.factor ?? record?.name ?? copy.edge(index + 1),
                    copy,
                  )}
                </summary>
                {profiles.length ? (
                  <div className="mt-2 space-y-3">
                    {profiles.map(({ label, value }) => (
                      <MatchingScoreEvidence
                        key={label}
                        label={label}
                        value={value}
                        copy={copy}
                      />
                    ))}
                  </div>
                ) : record ? (
                  <EvidenceFields record={record} copy={copy} />
                ) : (
                  <pre className="mt-2 overflow-x-auto whitespace-pre-wrap">
                    {displayValue(edge, copy)}
                  </pre>
                )}
              </details>
            );
          })}
        </div>
      ) : (
        <p className="mt-1 text-sm">{copy.noFactors}</p>
      )}
      <p className="mt-2 text-xs text-stone-600">{copy.missingExplanation}</p>
    </section>
  );
}
