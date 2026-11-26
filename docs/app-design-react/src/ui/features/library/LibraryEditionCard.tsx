import type { ReactNode } from "react";

/**
 * Shared edition card frame, header placement and typography. Hosts supply the
 * title control, metadata and disclosure or deletion actions.
 */
export const libraryEditionCardStyles = {
  list: "border-t border-[var(--line)]",
  row: "my-4",
  card: "relative min-w-0 overflow-hidden rounded-xl border border-[var(--line)] bg-[var(--surface)] transition-[border-color,background-color] duration-150 data-[expanded]:border-[var(--control)]",
  header: "relative",
  headerGrid:
    "grid min-h-[80px] grid-cols-[56px_minmax(0,1fr)_auto] items-center gap-4 px-4 py-3 text-left max-[600px]:min-h-[72px] max-[600px]:grid-cols-[44px_minmax(0,1fr)_auto] max-[600px]:items-start max-[600px]:gap-3 max-[600px]:px-3",
  copy: "grid min-w-0 gap-1 text-left [&>:is(strong,h2)]:m-0 [&>:is(strong,h2)]:text-[15px] [&>:is(strong,h2)]:font-semibold [&>:is(strong,h2)]:leading-[1.3] [&>:is(strong,h2)]:[overflow-wrap:anywhere]",
  meta: "block text-xs font-normal leading-[1.5] text-[var(--muted)] [overflow-wrap:anywhere]",
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
