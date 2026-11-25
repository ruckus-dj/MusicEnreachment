import type { ReactNode } from "react";

export function WorksMovementSectionView({
  label,
  summary,
  worksLabel,
  movementLabel,
  works,
  movement,
  worksDataKeys,
  movementDataKeys,
}: {
  label: string;
  summary: string;
  worksLabel: string;
  movementLabel: string;
  works?: ReactNode;
  movement?: ReactNode;
  worksDataKeys?: string[];
  movementDataKeys?: string[];
}) {
  const dataKeys = (keys?: string[]) =>
    keys?.length ? { "data-metadata-keys": keys.join(",") } : {};

  return (
    <details className="min-w-0">
      <summary className="w-full cursor-pointer rounded border border-[var(--line)] p-2 text-sm font-semibold hover:bg-[var(--raised)] focus-visible:outline-2 focus-visible:outline-[var(--focus)] focus-visible:outline-offset-2">
        {label} · {summary}
      </summary>
      {works ? (
        <section
          {...dataKeys(worksDataKeys)}
          className="grid gap-2 rounded border border-[var(--line)] p-2"
        >
          <h3 className="m-0 text-sm font-semibold">{worksLabel}</h3>
          {works}
        </section>
      ) : null}
      {movement ? (
        <section
          {...dataKeys(movementDataKeys)}
          className="grid gap-2 rounded border border-[var(--line)] p-2"
        >
          <h3 className="m-0 text-sm font-semibold">{movementLabel}</h3>
          {movement}
        </section>
      ) : null}
    </details>
  );
}
