import { useId } from "react";

/** Saved raw tags in stored order. Read-only. */
export interface TagSnapshotRow {
  readonly key: string;
  readonly values: readonly string[];
}

export function TagSnapshotView({
  title,
  rows,
  empty,
  unknown,
  headingLevel = 2,
}: {
  readonly title: string;
  readonly rows: readonly TagSnapshotRow[];
  readonly empty?: string;
  readonly unknown?: string;
  readonly headingLevel?: 2 | 3;
}) {
  const heading = useId();
  const Heading = headingLevel === 2 ? "h2" : "h3";
  return (
    <section
      className="grid min-w-0 gap-2 rounded border border-[var(--line)] p-3"
      aria-labelledby={heading}
    >
      <Heading id={heading} className="m-0 text-sm font-semibold">
        {title}
      </Heading>
      <dl className="m-0 grid gap-2 sm:grid-cols-2">
        {rows.map(({ key, values }) => (
          <div key={key} className="min-w-0 wrap-anywhere">
            <dt className="text-xs text-[var(--muted)]">
              <code>{key}</code>
            </dt>
            <dd className="m-0 whitespace-pre-wrap text-sm">
              {values.length ? values.join("\n") : unknown || "—"}
            </dd>
          </div>
        ))}
      </dl>
      {!rows.length && empty && (
        <p className="m-0 text-sm text-[var(--muted)]">{empty}</p>
      )}
    </section>
  );
}
