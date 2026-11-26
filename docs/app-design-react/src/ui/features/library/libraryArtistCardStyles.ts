/** Tailwind utilities for the library artist gallery (React Aria GridList). */
export const libraryArtistCardStyles = {
  artistGrid:
    "grid grid-cols-[repeat(auto-fill,minmax(min(100%,260px),1fr))] gap-4",
  artistCard:
    "group relative flex min-w-0 flex-col gap-3 rounded-xl border border-[var(--line)] bg-[var(--surface)] p-4 text-left transition-colors hover:border-[var(--control)] hover:bg-[var(--raised)] data-[selected]:border-[color-mix(in_srgb,var(--accent)_45%,var(--line))] data-[selected]:bg-[var(--accent-soft)] data-[focus-visible]:outline-2 data-[focus-visible]:outline-[var(--accent)] data-[focus-visible]:outline-offset-2",
  artistArt:
    "grid size-12 shrink-0 place-items-center rounded-lg border border-[var(--line)] bg-[var(--raised)] text-base font-semibold text-[var(--ink)] data-[palette=forest]:bg-[color-mix(in_srgb,#486d59_16%,var(--raised))] data-[palette=dusk]:bg-[color-mix(in_srgb,#676e95_16%,var(--raised))] data-[palette=sand]:bg-[color-mix(in_srgb,#aa905f_18%,var(--raised))] data-[palette=slate]:bg-[color-mix(in_srgb,#4c717e_16%,var(--raised))]",
  artistCardLink:
    "min-w-0 no-underline hover:no-underline focus-visible:outline-none",
  artistMonogram: "block text-base font-semibold",
  artistOverlay: "min-w-0",
  artistCopy:
    "grid min-w-0 gap-0.5 [&_strong]:text-sm [&_strong]:font-semibold [&_strong]:[overflow-wrap:anywhere] [&>span]:text-xs [&>span]:text-[var(--muted)]",
  artistStats:
    "grid grid-cols-2 gap-x-4 gap-y-1.5 border-t border-[var(--line)] pt-3 text-xs [&>div]:flex [&>div]:min-w-0 [&>div]:flex-wrap [&>div]:items-baseline [&>div]:justify-between [&>div]:gap-x-2 [&>div]:gap-y-0.5 [&>div]:text-[var(--muted)] [&_dt]:m-0 [&_dd]:m-0 [&_dd]:ml-auto [&_dd]:whitespace-nowrap [&_strong]:font-medium [&_strong]:tabular-nums [&_strong]:text-[var(--ink)]",
  cardSelection: "absolute right-3 top-3 z-[2] grid place-items-center",
  empty:
    "col-span-full rounded-xl border border-[var(--line)] bg-[var(--surface)] px-6 py-12 text-center text-sm text-[var(--muted)]",
  tableCheckbox:
    "group grid size-8 cursor-pointer place-items-center data-[disabled]:cursor-default data-[disabled]:opacity-45",
  cardCheckbox:
    "grid size-5 cursor-pointer place-items-center rounded border border-[var(--control)] bg-[var(--surface)] text-[var(--button-text)] group-data-[selected]:border-[var(--accent)] group-data-[selected]:bg-[var(--accent)] group-data-[indeterminate]:border-[var(--accent)] group-data-[indeterminate]:bg-[var(--accent)] group-data-[focus-visible]:outline-2 group-data-[focus-visible]:outline-[var(--accent)] group-data-[focus-visible]:outline-offset-2",
  checkboxBox:
    "grid size-4 place-items-center rounded border border-[var(--control)] bg-[var(--surface)] text-[var(--button-text)] group-data-[selected]:border-[var(--accent)] group-data-[selected]:bg-[var(--accent)] group-data-[indeterminate]:border-[var(--accent)] group-data-[indeterminate]:bg-[var(--accent)] group-data-[focus-visible]:outline-2 group-data-[focus-visible]:outline-[var(--accent)] group-data-[focus-visible]:outline-offset-2",
  cardCheckboxBox:
    "grid size-4 place-items-center rounded border border-[var(--control)] bg-[var(--surface)] text-[var(--button-text)] group-data-[selected]:border-[var(--accent)] group-data-[selected]:bg-[var(--accent)] group-data-[indeterminate]:border-[var(--accent)] group-data-[indeterminate]:bg-[var(--accent)]",
} as const;
