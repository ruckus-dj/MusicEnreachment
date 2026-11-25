const copy = {
  en: {
    heading: "Publication results",
    requested: "Positions to publish: {count}.",
    published: "Published positions: {count}.",
    failed: "Not published: {count}.",
    failure: "The position could not be published.",
    unresolved: "Positions without a confirmed outcome",
    stale: "The preview is stale; prepare it again.",
    uncertain:
      "Could not confirm whether publication started. Check Operations before retrying.",
    nothing:
      "No position required publication; current publications were not changed.",
  },
  ru: {
    heading: "Результаты публикации",
    requested: "Позиций к публикации: {count}.",
    published: "Опубликовано позиций: {count}.",
    failed: "Не опубликовано: {count}.",
    failure: "Позицию не удалось опубликовать.",
    unresolved: "Позиции без подтверждённого результата",
    stale: "Предпросмотр устарел; подготовьте его заново.",
    uncertain:
      "Не удалось подтвердить запуск публикации. Проверьте операции перед повтором.",
    nothing:
      "Ни одной позиции не требовалась публикация; действующие публикации не изменены.",
  },
} as const;

export type MatchingReceiptPublicationIssue = {
  id: string;
  label: string;
  kind: "stale" | "uncertain";
  detail?: string;
};

export function MatchingReceiptPublicationOutcomeView({
  locale,
  attempted,
  published,
  failures,
  issues,
}: {
  locale: "en" | "ru";
  attempted: number;
  published: number;
  failures: readonly { id: string; label: string; detail?: string }[];
  issues?: readonly MatchingReceiptPublicationIssue[];
}) {
  const text = copy[locale];
  return (
    <section
      role="status"
      aria-live="polite"
      className="rounded border border-[var(--line)] bg-[var(--raised)] p-3 text-sm"
    >
      <h2 className="m-0 text-sm font-semibold">{text.heading}</h2>
      <p className="mt-1">
        {text.requested.replace("{count}", String(attempted))}{" "}
        {text.published.replace("{count}", String(published))}
        {failures.length
          ? ` ${text.failed.replace("{count}", String(failures.length))}`
          : ""}
      </p>
      {!attempted && <p>{text.nothing}</p>}
      {failures.length ? (
        <ul className="mt-1 list-disc pl-5">
          {failures.map(({ id, label, detail }) => (
            <li key={id} className="wrap-anywhere">
              {label}: {detail || text.failure}
            </li>
          ))}
        </ul>
      ) : null}
      {issues?.length ? (
        <ul aria-label={text.unresolved} className="mt-1 list-disc pl-5">
          {issues.map(({ id, label, kind, detail }) => (
            <li key={id} className="wrap-anywhere">
              {label}: {detail || text[kind]}
            </li>
          ))}
        </ul>
      ) : null}
    </section>
  );
}
