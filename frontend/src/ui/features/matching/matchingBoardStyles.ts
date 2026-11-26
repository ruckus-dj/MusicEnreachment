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
    "min-w-0 rounded-xl border border-[var(--line)] bg-[var(--surface)] p-4 [&_summary:focus-visible]:outline-2 [&_summary:focus-visible]:outline-[var(--focus)] [&_summary:focus-visible]:outline-offset-2 min-[1400px]:min-h-0 min-[1400px]:overflow-y-auto",
  heading: "m-0 mb-2 text-base font-semibold leading-6 tracking-[-0.01em]",
  summary: "m-0 mb-2 text-xs leading-5 text-[var(--muted)]",
  instructions: "m-0 text-xs leading-5 text-[var(--muted)]",
  list: "m-0 mt-2.5 flex list-none flex-col gap-0 border-t border-[var(--line)] p-0",
  listItem: "min-w-0 border-b border-[var(--line)]",
  position:
    "relative min-w-0 rounded-lg border-0 px-3 py-2.5 hover:bg-[var(--raised)] focus-within:bg-[var(--raised)] data-[inspected]:bg-[var(--accent-soft)]",
  positionDrop:
    "relative flex min-w-0 flex-col gap-1.5 rounded-[var(--control-radius)] data-[drop-target]:outline data-[drop-target]:outline-1 data-[drop-target]:outline-dashed data-[drop-target]:outline-offset-[-2px] data-[drop-target]:outline-[var(--accent)]",
  positionHead: "flex min-w-0 items-center gap-1.5",
  positionTitle:
    "flex min-w-0 flex-1 items-baseline gap-1.5 border-0 bg-transparent p-0 text-left font-[inherit] text-[var(--ink)]",
  positionTitleButton: "cursor-pointer hover:text-[var(--accent)]",
  positionTitleStatic: "cursor-default",
  positionNumber:
    "min-w-[24px] text-right text-xs leading-5 tabular-nums text-[var(--muted)]",
  positionName:
    "min-w-0 overflow-hidden text-ellipsis whitespace-nowrap text-sm font-medium leading-5",
  positionMedium: "flex-none text-xs leading-4 text-[var(--muted)]",
  assigned: "flex min-h-6 min-w-0 flex-wrap items-center gap-1.5",
  assignedFile:
    "inline-flex min-w-0 max-w-full items-center gap-1 rounded-full border border-[var(--line)] bg-[var(--surface)] py-0 pl-2.5 pr-1 text-xs leading-6 hover:bg-[var(--raised)]",
  savedFile:
    "inline-flex min-w-0 max-w-full items-center gap-1 rounded-full border border-dashed border-[var(--line)] bg-transparent py-0 pl-2.5 pr-2 text-xs leading-6 text-[var(--muted)]",
  savedFileButton: "cursor-pointer text-left font-[inherit]",
  fileLabel:
    "min-w-0 max-w-[32ch] cursor-pointer overflow-hidden text-ellipsis whitespace-nowrap border-0 bg-transparent p-0 font-[inherit] text-[var(--ink)]",
  fileLabelButton: "hover:text-[var(--accent)]",
  autoMarker:
    "flex-none rounded-sm bg-[var(--accent-soft)] px-1 py-0 text-xs leading-4 text-[var(--accent-strong)]",
  score: "flex-none text-xs tabular-nums text-[var(--muted)]",
  dragButton:
    "inline-flex size-7 flex-none cursor-pointer items-center justify-center rounded-md border border-[var(--line)] bg-[var(--surface)] p-0 text-[var(--muted)] enabled:hover:border-[var(--control)] enabled:hover:text-[var(--ink)] disabled:cursor-not-allowed data-[dragging]:opacity-50",
  removeButton:
    "inline-flex size-7 flex-none cursor-pointer items-center justify-center rounded-md border border-transparent bg-transparent p-0 text-[var(--muted)] enabled:hover:bg-[var(--raised)] enabled:hover:text-[var(--ink)] disabled:cursor-not-allowed",
  addButton:
    "ml-0.5 min-h-8 flex-none cursor-pointer rounded-md border border-[var(--line)] bg-[var(--surface)] px-2.5 py-0 text-sm font-[inherit] text-[var(--ink)] enabled:hover:border-[var(--control)] enabled:hover:bg-[var(--raised)] disabled:cursor-not-allowed disabled:opacity-[0.45]",
  empty: "text-xs text-[var(--muted)]",
  source:
    "grid min-w-0 grid-cols-[minmax(0,1fr)_auto] items-center gap-2 rounded-lg border-0 bg-transparent px-2 py-2 hover:bg-[var(--raised)] focus-within:bg-[var(--raised)] data-[selected]:bg-[var(--accent-soft)]",
  sourceSelect:
    "flex min-w-0 cursor-pointer flex-col gap-px border-0 bg-transparent px-0.5 py-0 text-left font-[inherit] text-[var(--ink)] disabled:cursor-not-allowed disabled:opacity-60",
  sourceName:
    "overflow-hidden text-ellipsis whitespace-nowrap text-sm leading-5",
  sourceMeta:
    "overflow-hidden text-ellipsis whitespace-nowrap text-xs leading-5 text-[var(--muted)]",
  sourceStatus: "mb-0 mt-2 min-h-8 text-xs leading-5 text-[var(--muted)]",
  dropHint:
    "pointer-events-none absolute inset-0 z-10 flex items-center justify-center rounded-[var(--control-radius)] border border-dashed border-[var(--accent)] bg-[color-mix(in_srgb,var(--accent-soft)_92%,transparent)] p-2 text-center text-xs font-medium text-[var(--accent-strong)]",
  focusRing:
    "focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--focus)]",
  dragHandleIcon: "size-4",
} as const;

/** Join optional utility strings without duplicating spaces. */
export function matchingBoardClasses(
  ...values: readonly (string | false | null | undefined)[]
): string {
  return values.filter(Boolean).join(" ");
}
