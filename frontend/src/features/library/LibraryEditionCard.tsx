import type { ReactNode } from "react";

/**
 * Shared edition card frame, header placement and typography. Hosts supply the
 * title control, metadata and disclosure or deletion actions.
 */
export const libraryEditionCardStyles = {
  list: "border-t border-[var(--line)]",
  row: "my-3",
  card: "relative min-w-0 overflow-hidden rounded-xl border border-[var(--line)] bg-[var(--surface)] shadow-[0_5px_18px_color-mix(in_srgb,var(--ink)_5%,transparent)] transition-[border-color,box-shadow] duration-[160ms] data-[expanded]:border-[color-mix(in_srgb,var(--accent)_45%,var(--line))] data-[expanded]:shadow-[0_10px_28px_color-mix(in_srgb,var(--ink)_9%,transparent)]",
  header: "relative",
  headerGrid:
    "grid min-h-[94px] grid-cols-[68px_minmax(0,1fr)_auto] items-center gap-[13px] px-[15px] py-3 text-left max-[600px]:min-h-20 max-[600px]:grid-cols-[54px_minmax(0,1fr)_auto] max-[600px]:items-start max-[600px]:gap-[9px] max-[600px]:px-[9px] max-[600px]:py-[9px]",
  copy: "grid min-w-0 gap-[5px] text-left [&>:is(strong,h2)]:m-0 [&>:is(strong,h2)]:text-[clamp(14px,1.35vw,18px)] [&>:is(strong,h2)]:font-bold [&>:is(strong,h2)]:leading-[1.2] [&>:is(strong,h2)]:tracking-[-.015em] [&>:is(strong,h2)]:[overflow-wrap:anywhere]",
  meta: "block text-[12px] font-normal leading-[1.4] text-[var(--muted)] [overflow-wrap:anywhere]",
  panel: "border-t border-[var(--line)]",
} as const;

/**
 * Plain card frame used by the application; the prototype keeps its React Aria
 * disclosure wrapper and reuses only the shared style tokens.
 */
export function LibraryEditionCard({
  expanded,
  header,
  panel,
}: {
  expanded: boolean;
  header: ReactNode;
  panel?: ReactNode;
}) {
  return (
    <article className={libraryEditionCardStyles.row}>
      <div
        className={libraryEditionCardStyles.card}
        data-expanded={expanded ? "true" : undefined}
      >
        <div className={libraryEditionCardStyles.header}>{header}</div>
        {panel}
      </div>
    </article>
  );
}
