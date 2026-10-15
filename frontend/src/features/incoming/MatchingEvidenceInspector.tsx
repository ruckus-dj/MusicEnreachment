import type { MatchingReviewCandidate } from "../../api/generated/client.schemas";

type MatchingEvidenceInspectorProps = {
  candidate?: MatchingReviewCandidate;
  rawTags?: Record<string, string[] | null>;
};

function displayValue(value: unknown): string {
  if (value === null) return "null";
  if (value === undefined) return "не указано";
  if (typeof value === "string") return value || "пустое значение";
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

function EvidenceFields({ record }: { record: Record<string, unknown> }) {
  return (
    <dl className="mt-2 grid gap-2 text-sm sm:grid-cols-2">
      {Object.entries(record).map(([key, value]) => (
        <div key={key} className="min-w-0">
          <dt className="font-medium">{fieldLabel(key)}</dt>
          <dd className="overflow-x-auto whitespace-pre-wrap break-words">
            {displayValue(value)}
          </dd>
        </div>
      ))}
    </dl>
  );
}

function MatchingScoreEvidence({
  label,
  value,
}: {
  label: string;
  value: unknown;
}) {
  if (!isRecord(value)) {
    return (
      <div>
        <h6 className="font-medium">{label}</h6>
        <pre className="overflow-x-auto whitespace-pre-wrap">
          {displayValue(value)}
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
      <EvidenceFields record={scoreFields} />
      {factorList ? (
        <div className="mt-2 space-y-2">
          {factorList.map((factor, index) => {
            if (!isRecord(factor))
              return (
                <pre
                  key={`factor-${displayValue(factor)}`}
                  className="overflow-x-auto whitespace-pre-wrap"
                >
                  {displayValue(factor)}
                </pre>
              );
            return (
              <div
                key={`${String(factor.Component ?? factor.component ?? "factor")}-${displayValue(factor)}`}
                className="rounded border border-stone-200 p-2"
              >
                <h6 className="font-medium">
                  {displayValue(
                    factor.Component ??
                      factor.component ??
                      `Фактор ${index + 1}`,
                  )}
                </h6>
                <EvidenceFields record={factor} />
              </div>
            );
          })}
        </div>
      ) : factors !== undefined ? (
        <pre className="overflow-x-auto whitespace-pre-wrap">
          {displayValue(factors)}
        </pre>
      ) : null}
    </div>
  );
}

export function MatchingEvidenceInspector({
  candidate,
  rawTags,
}: MatchingEvidenceInspectorProps) {
  const edges = candidate?.edges ?? [];
  return (
    <section
      className="mt-3 rounded border border-stone-300 bg-stone-50 p-3"
      aria-label="Доказательства текущей оценки"
    >
      <h4 className="font-medium">Доказательства текущей оценки</h4>
      <p className="mt-1 text-sm text-stone-700">
        Показаны сохранённые значения и сведения об их происхождении. Числовые
        оценки и вклад в них интерфейс не рассчитывает.
      </p>
      {candidate ? (
        <dl className="mt-2 grid gap-2 text-sm sm:grid-cols-2">
          <div>
            <dt className="font-medium">Уверенность издания (сохранённая)</dt>
            <dd>{displayValue(candidate.release_confidence)}</dd>
          </div>
          {candidate.positions?.map((position) => (
            <div key={position.track_id}>
              <dt className="font-medium">
                Уверенность позиции · {position.track_id}
              </dt>
              <dd>{displayValue(position.assignment_confidence)}</dd>
            </div>
          ))}
        </dl>
      ) : (
        <p className="mt-2 text-sm">
          Кандидат для этого файла и издания отсутствует.
        </p>
      )}
      {rawTags ? (
        <details className="mt-2 text-sm">
          <summary className="cursor-pointer font-medium">
            Исходные теги файла
          </summary>
          <pre className="mt-2 overflow-x-auto whitespace-pre-wrap rounded bg-white p-2">
            {displayValue(rawTags)}
          </pre>
        </details>
      ) : null}
      <h5 className="mt-3 font-medium">Факторы и provenance</h5>
      {edges.length ? (
        <div className="mt-2 space-y-2">
          {edges.map((edge, index) => {
            const record = isRecord(edge) ? edge : undefined;
            const profiles: Array<{ label: string; value: unknown }> = record
              ? [
                  { label: "Профиль издания", value: record.release_profile },
                  {
                    label: "Профиль назначения",
                    value: record.assignment_profile,
                  },
                ].filter(({ value }) => value !== undefined)
              : [];
            return (
              <details
                key={`${String(record?.factor ?? record?.name ?? "edge")}-${displayValue(edge)}`}
                className="rounded border border-stone-300 bg-white p-2"
                open
              >
                <summary className="cursor-pointer text-sm font-medium">
                  {displayValue(
                    record?.factor ?? record?.name ?? `Связь ${index + 1}`,
                  )}
                </summary>
                {profiles.length ? (
                  <div className="mt-2 space-y-3">
                    {profiles.map(({ label, value }) => (
                      <MatchingScoreEvidence
                        key={label}
                        label={label}
                        value={value}
                      />
                    ))}
                  </div>
                ) : record ? (
                  <EvidenceFields record={record} />
                ) : (
                  <pre className="mt-2 overflow-x-auto whitespace-pre-wrap">
                    {displayValue(edge)}
                  </pre>
                )}
              </details>
            );
          })}
        </div>
      ) : (
        <p className="mt-1 text-sm">
          Факторы, веса и вклад не сохранены для этого кандидата.
        </p>
      )}
      <p className="mt-2 text-xs text-stone-600">
        Отсутствующие значения не заменяются вычислениями; неприменимые факторы
        отображаются только если они явно указаны в данных оценки.
      </p>
    </section>
  );
}
