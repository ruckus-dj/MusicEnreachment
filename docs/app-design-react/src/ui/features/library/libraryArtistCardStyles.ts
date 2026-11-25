/** Tailwind utilities for library artist cards. */
export const libraryArtistCardStyles = {
  artistGrid:
    "grid grid-cols-[repeat(auto-fill,minmax(min(100%,224px),1fr))] gap-[14px] p-[clamp(12px,1.5vw,20px)] max-[600px]:grid-cols-1 max-[600px]:gap-[11px] max-[600px]:p-[10px]",
  artistCard:
    "min-w-0 transition-transform duration-[180ms] motion-reduce:transition-none [@media(hover:hover)_and_(pointer:fine)]:hover:-translate-y-[3px]",
  artistArt:
    "relative isolate aspect-[1.18/1] min-h-0 w-full min-w-0 overflow-hidden rounded-[11px] border border-[color-mix(in_srgb,var(--artist-color)_45%,var(--line))] bg-[linear-gradient(145deg,color-mix(in_srgb,var(--artist-color)_77%,#111),color-mix(in_srgb,var(--artist-color)_46%,#101820))] text-white shadow-[0_7px_20px_color-mix(in_srgb,var(--artist-color)_20%,transparent)] [--artist-color:#ac7059] transition-shadow duration-[180ms] data-[palette=forest]:[--artist-color:#486d59] data-[palette=dusk]:[--artist-color:#676e95] data-[palette=sand]:[--artist-color:#aa905f] data-[palette=slate]:[--artist-color:#4c717e] before:pointer-events-none before:absolute before:inset-[10%_8%_13%_31%] before:z-[-1] before:rounded-[48%_52%_42%_58%/54%_38%_62%_46%] before:border before:border-[#ffffff47] before:bg-[linear-gradient(135deg,#ffffff16,transparent_68%)] before:transition-transform before:duration-300 before:[transform:rotate(-18deg)] after:pointer-events-none after:absolute after:right-[14%] after:top-[12%] after:z-[-1] after:aspect-square after:w-[38%] after:rounded-full after:border after:border-[#ffffff30] after:bg-[radial-gradient(circle_at_35%_32%,#ffffff28,#ffffff05_68%)] after:transition-transform after:duration-300 [@media(hover:hover)_and_(pointer:fine)]:hover:shadow-[0_14px_32px_color-mix(in_srgb,var(--artist-color)_30%,transparent)] [@media(hover:hover)_and_(pointer:fine)]:hover:before:[transform:rotate(-18deg)_scale(1.04)] [@media(hover:hover)_and_(pointer:fine)]:hover:after:scale-105 motion-reduce:before:transition-none motion-reduce:after:transition-none motion-reduce:transition-none motion-reduce:hover:translate-y-0",
  artistCardLink:
    "absolute inset-0 flex flex-col justify-between overflow-hidden p-[17px] text-white no-underline hover:text-white hover:no-underline focus-visible:rounded-[inherit] focus-visible:outline focus-visible:outline-[3px] focus-visible:outline-[var(--focus)] focus-visible:-outline-offset-1",
  artistMonogram:
    "block size-[62px] text-center text-[42px] font-semibold leading-[62px] text-white [text-shadow:0_2px_15px_#0005]",
  artistOverlay:
    "-mx-[17px] -mb-[17px] -mt-[17px] grid gap-[11px] bg-[linear-gradient(transparent,#101415e8_22%,#101415f2)] px-[17px] pb-[15px] pt-[34px]",
  artistCopy:
    "grid min-w-0 gap-[3px] [&_strong]:text-[clamp(16px,1.3vw,20px)] [&_strong]:leading-[1.15] [&_strong]:tracking-[-.02em] [&_strong]:[overflow-wrap:anywhere] [&>span]:text-[11px] [&>span]:text-[#ffffffbf]",
  artistStats:
    "grid grid-cols-2 gap-x-[10px] gap-y-[6px] text-[10px] leading-[1.25] text-[#ffffffbd] [&>span]:min-w-0 [&_strong]:text-[11px] [&_strong]:text-white [&_strong]:[font-variant-numeric:tabular-nums]",
  cardSelection:
    "absolute left-[9px] top-[9px] z-[2] grid size-6 place-items-center",
  empty: "p-7 px-4 text-center text-[var(--muted)]",
  tableCheckbox:
    "group mx-auto grid h-8 w-[34px] flex-[0_0_34px] cursor-pointer place-items-center",
  cardCheckbox: "group grid size-6 cursor-pointer place-items-center",
  checkboxBox:
    "grid size-[18px] place-items-center rounded-[3px] border border-[var(--control)] text-sm leading-none text-[var(--button-text)] group-data-[selected]:border-[var(--accent)] group-data-[selected]:bg-[var(--accent)] group-data-[indeterminate]:border-[var(--accent)] group-data-[indeterminate]:bg-[var(--accent)] group-data-[focus-visible]:outline group-data-[focus-visible]:outline-2 group-data-[focus-visible]:outline-[var(--accent)] group-data-[focus-visible]:outline-offset-[3px]",
  cardCheckboxBox:
    "grid size-4 place-items-center rounded-[4px] border-[1.5px] border-[#ffffffd9] bg-[#1014154d] text-[11px] leading-none text-transparent transition-colors group-data-[hovered]:border-white group-data-[hovered]:bg-[#10141580] group-data-[selected]:border-white group-data-[selected]:bg-white group-data-[selected]:text-[#17201d] group-data-[indeterminate]:border-white group-data-[indeterminate]:bg-white group-data-[indeterminate]:text-[#17201d] group-data-[focus-visible]:outline group-data-[focus-visible]:outline-2 group-data-[focus-visible]:outline-white group-data-[focus-visible]:outline-offset-2 group-data-[focus-visible]:shadow-[0_0_0_4px_#10141580] group-data-[disabled]:opacity-[.45]",
} as const;
