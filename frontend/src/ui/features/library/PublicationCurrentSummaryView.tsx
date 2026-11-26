import { FileFormatBadge } from "../../components/FileFormatBadge";

const messages = {
  en: {
    current: "Current publication",
    published: "Published",
    unpublished: "Not published",
    format: "Format",
    path: "Path",
    verified: "Validated",
    snapshot: "Read at",
  },
  ru: {
    current: "Текущая публикация",
    published: "Опубликовано",
    unpublished: "Не опубликовано",
    format: "Формат",
    path: "Путь",
    verified: "Проверено",
    snapshot: "Прочитано",
  },
} as const;

type PublicationDetails = {
  codec?: string;
  container?: string;
  path?: string;
  observed?: { kind: "verified" | "snapshot"; value: string };
};

export function PublicationCurrentSummaryView({
  locale,
  published,
}: {
  locale: "en" | "ru";
  published: boolean;
}) {
  const text = messages[locale];
  return (
    <section
      className="grid min-w-0 gap-2 rounded border border-[var(--line)] bg-[var(--raised)] px-3 py-3 text-sm text-[var(--ink)]"
      aria-label={text.current}
    >
      <h3 className="font-semibold">
        {published ? text.published : text.unpublished}
      </h3>
    </section>
  );
}

export function PublicationCurrentDetailsView({
  locale,
  details,
}: {
  locale: "en" | "ru";
  details: PublicationDetails;
}) {
  const text = messages[locale];
  const { observed } = details;
  const date =
    observed?.kind === "verified" ? new Date(observed.value) : undefined;
  const format = [details.container, details.codec].filter(Boolean).join(" · ");
  if (!format && !details.path && !observed) return null;
  return (
    <dl className="my-2 grid min-w-0 gap-x-4 gap-y-1 sm:grid-cols-[max-content_minmax(0,1fr)]">
      {format && (
        <>
          <dt className="text-[var(--muted)]">{text.format}</dt>
          <dd className="m-0">
            <FileFormatBadge value={format} />
          </dd>
        </>
      )}
      {details.path && (
        <>
          <dt className="text-[var(--muted)]">{text.path}</dt>
          <dd className="m-0 break-all font-mono">{details.path}</dd>
        </>
      )}
      {observed && (
        <>
          <dt className="text-[var(--muted)]">{text[observed.kind]}</dt>
          <dd className="m-0">
            {date && Number.isFinite(date.getTime()) ? (
              <time dateTime={observed.value}>
                {new Intl.DateTimeFormat(locale, {
                  dateStyle: "medium",
                  timeStyle: "short",
                }).format(date)}
              </time>
            ) : (
              observed.value
            )}
          </dd>
        </>
      )}
    </dl>
  );
}
