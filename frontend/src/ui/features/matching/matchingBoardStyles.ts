/**
 * Static Tailwind utility strings for the portable assignment board.
 *
 * The production feature previously kept these rules in `matching-board.css`.
 * The portable copy is a drop-in standalone module, so the same rules live in
 * reusable static utility strings instead of a stylesheet. Class strings are
 * plain constants: no runtime class translation, no `@apply`.
 */
export const matchingBoardClass = {
  panel:
    "min-w-0 rounded-[var(--control-radius)] border border-[var(--line)] bg-[var(--surface)] p-3 [&_summary:focus-visible]:outline-2 [&_summary:focus-visible]:outline-[var(--focus)] [&_summary:focus-visible]:outline-offset-2 min-[1400px]:min-h-0 min-[1400px]:overflow-y-auto",
  heading: "m-0 mb-1 text-[14px] font-semibold leading-5 tracking-[-0.01em]",
  summary: "m-0 mb-1 text-[12px] leading-[17px] text-[var(--muted)]",
  instructions: "m-0 text-[12px] leading-[17px] text-[var(--muted)]",
  list: "m-0 mt-2.5 flex list-none flex-col gap-0 border-t border-[var(--line)] p-0",
  listItem: "min-w-0 border-b border-[var(--line)]",
  position:
    "relative min-w-0 border-0 px-2 py-1.5 hover:bg-[color-mix(in_srgb,var(--accent-soft)_34%,transparent)] focus-within:bg-[color-mix(in_srgb,var(--accent-soft)_34%,transparent)] data-[inspected]:bg-[var(--accent-soft)] data-[inspected]:shadow-[inset_2px_0_0_var(--accent)]",
  positionDrop:
    "relative flex min-w-0 flex-col gap-1 rounded-[var(--control-radius)] data-[drop-target]:outline data-[drop-target]:outline-1 data-[drop-target]:outline-dashed data-[drop-target]:outline-offset-[-2px] data-[drop-target]:outline-[var(--accent)]",
  positionHead: "flex min-w-0 items-center gap-1.5",
  positionTitle:
    "flex min-w-0 flex-1 items-baseline gap-1.5 border-0 bg-transparent p-0 text-left font-[inherit] text-[var(--ink)]",
  positionTitleButton: "cursor-pointer hover:text-[var(--accent)]",
  positionTitleStatic: "cursor-default",
  positionNumber:
    "min-w-[22px] text-right text-[11px] leading-4 tabular-nums text-[var(--muted)]",
  positionName:
    "min-w-0 overflow-hidden text-ellipsis whitespace-nowrap text-[12px] font-semibold leading-4",
  positionMedium:
    "flex-none text-[10px] uppercase leading-4 tracking-[0.03em] text-[var(--muted)]",
  assigned: "flex min-h-[22px] min-w-0 flex-wrap items-center gap-1",
  assignedFile:
    "inline-flex min-w-0 max-w-full items-center gap-1 rounded-full border border-transparent bg-[var(--raised)] py-0 pl-2 pr-[3px] text-[11px] leading-5 hover:border-[var(--line)]",
  savedFile:
    "inline-flex min-w-0 max-w-full items-center gap-1 rounded-full border border-dashed border-[var(--line)] bg-transparent py-0 pl-2 pr-2 text-[11px] leading-5 text-[var(--muted)]",
  savedFileButton: "cursor-pointer text-left font-[inherit]",
  fileLabel:
    "min-w-0 max-w-[32ch] cursor-pointer overflow-hidden text-ellipsis whitespace-nowrap border-0 bg-transparent p-0 font-[inherit] text-[var(--ink)]",
  fileLabelButton: "hover:text-[var(--accent)]",
  autoMarker:
    "flex-none rounded-[3px] bg-[var(--accent-soft)] px-[3px] py-0 text-[9px] uppercase leading-[15px] tracking-[0.04em] text-[var(--accent)]",
  score: "flex-none text-[10px] tabular-nums text-[var(--muted)]",
  dragButton:
    "inline-flex size-[22px] flex-none cursor-pointer items-center justify-center rounded-[3px] border border-[var(--line)] bg-[var(--surface)] p-0 text-[var(--muted)] enabled:hover:border-[var(--accent)] enabled:hover:text-[var(--accent)] disabled:cursor-not-allowed data-[dragging]:opacity-50",
  removeButton:
    "inline-flex size-[20px] flex-none cursor-pointer items-center justify-center rounded-[3px] border border-transparent bg-transparent p-0 text-[14px] text-[var(--muted)] enabled:hover:border-[var(--accent)] enabled:hover:text-[var(--accent)] disabled:cursor-not-allowed",
  addButton:
    "ml-0.5 min-h-[22px] flex-none cursor-pointer rounded-[3px] border border-[var(--line)] bg-[var(--surface)] px-[7px] py-0 text-[10px] font-[inherit] text-[var(--ink)] enabled:hover:border-[var(--accent)] enabled:hover:text-[var(--accent)] disabled:cursor-not-allowed disabled:opacity-[0.45]",
  empty: "text-[11px] text-[var(--muted)]",
  source:
    "grid min-w-0 grid-cols-[minmax(0,1fr)_auto] items-center gap-1.5 border-0 bg-transparent py-[5px] pl-1 pr-1.5 hover:bg-[color-mix(in_srgb,var(--accent-soft)_34%,transparent)] focus-within:bg-[color-mix(in_srgb,var(--accent-soft)_34%,transparent)] data-[selected]:bg-[var(--accent-soft)] data-[selected]:shadow-[inset_2px_0_0_var(--accent)]",
  sourceSelect:
    "flex min-w-0 cursor-pointer flex-col gap-px border-0 bg-transparent px-0.5 py-0 text-left font-[inherit] text-[var(--ink)] disabled:cursor-not-allowed disabled:opacity-60",
  sourceName:
    "overflow-hidden text-ellipsis whitespace-nowrap text-[12px] leading-4",
  sourceMeta:
    "overflow-hidden text-ellipsis whitespace-nowrap text-[11px] leading-[15px] text-[var(--muted)]",
  sourceStatus:
    "mb-0 mt-2 min-h-[30px] text-[11px] leading-[15px] text-[var(--muted)]",
  dropHint:
    "pointer-events-none absolute inset-0 z-10 flex items-center justify-center rounded-[var(--control-radius)] border border-dashed border-[var(--accent)] bg-[color-mix(in_srgb,var(--accent-soft)_92%,transparent)] p-1 text-center text-[10px] font-semibold text-[var(--accent)]",
  focusRing:
    "focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--focus)]",
  dragHandleIcon: "size-3.5",
} as const;

/** Join optional utility strings without duplicating spaces. */
export function matchingBoardClasses(
  ...values: readonly (string | false | null | undefined)[]
): string {
  return values.filter(Boolean).join(" ");
}
