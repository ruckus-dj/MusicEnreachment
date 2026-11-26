import { libraryEditionCardStyles } from "./LibraryEditionCard";

/** Tailwind utilities for the catalogue screens; kept literal for Tailwind scanning. */
export const catalogueStyles = {
  shell: "min-w-0 text-[var(--ink)]",
  pageHeader:
    "mb-6 flex flex-wrap items-start justify-between gap-x-6 gap-y-3 border-b border-[var(--line)] pb-5",
  pageTitle:
    "m-0 text-[26px]/[1.2] font-semibold tracking-[-.02em] wrap-anywhere max-[700px]:text-[22px]",
  pageSubtitle: "m-0 mt-1.5 max-w-[75ch] text-sm text-[var(--muted)]",
  pageHeaderAction: "flex flex-wrap items-center gap-2",
  controls: "mb-5",
  tabsPanel: "min-w-0",
  panelControls:
    "mb-4 grid grid-cols-[minmax(220px,1fr)_minmax(340px,1fr)] items-end gap-3 max-[1100px]:grid-cols-1 max-[600px]:gap-2",
  search:
    "min-h-9 rounded-[var(--control-radius)] border border-[var(--line)] bg-[var(--surface)] px-3 text-sm text-[var(--ink)] hover:border-[var(--control)] focus-within:border-[var(--accent)] focus-within:outline-2 focus-within:outline-[var(--accent)] focus-within:outline-offset-0 [&_.react-aria-Input]:min-h-8 [&_.react-aria-Input]:border-0 [&_.react-aria-Input]:bg-transparent [&_.react-aria-Input]:outline-none [&_.react-aria-Input]:shadow-none",
  select:
    "min-w-0 [&>.react-aria-Button]:flex [&>.react-aria-Button]:h-9 [&>.react-aria-Button]:w-full [&>.react-aria-Button]:items-center [&>.react-aria-Button]:justify-between [&>.react-aria-Button]:gap-2.5 [&>.react-aria-Button]:rounded-[var(--control-radius)] [&>.react-aria-Button]:border [&>.react-aria-Button]:border-[var(--line)] [&>.react-aria-Button]:bg-[var(--surface)] [&>.react-aria-Button]:px-3 [&>.react-aria-Button]:text-left [&>.react-aria-Button]:text-sm [&>.react-aria-Button]:text-[var(--ink)] [&>.react-aria-Button:hover]:border-[var(--control)] [&>.react-aria-Button:hover]:bg-[var(--raised)]",
  popover:
    "z-50 max-h-[320px] min-w-[var(--trigger-width)] overflow-auto rounded-xl border border-[var(--line)] bg-[var(--surface)] p-1 text-[var(--ink)] shadow-[0_12px_32px_rgb(0_0_0/0.14)] [&_.react-aria-ListBox]:grid [&_.react-aria-ListBox]:gap-0.5 [&_.react-aria-ListBox]:outline-none [&_.react-aria-ListBoxItem]:cursor-pointer [&_.react-aria-ListBoxItem]:rounded-lg [&_.react-aria-ListBoxItem]:px-2.5 [&_.react-aria-ListBoxItem]:py-2 [&_.react-aria-ListBoxItem]:text-sm [&_.react-aria-ListBoxItem[data-focused]]:bg-[var(--raised)] [&_.react-aria-ListBoxItem[data-selected]]:bg-[var(--accent-soft)] [&_.react-aria-ListBoxItem[data-selected]]:text-[var(--accent-strong)]",
  dockScreen:
    "pb-[calc(120px+env(safe-area-inset-bottom))] max-[600px]:pb-[calc(170px+env(safe-area-inset-bottom))]",
  list: "min-w-0 overflow-hidden rounded-lg border border-[var(--line)] bg-[var(--surface)]",
  mobileCatalogGroup:
    "grid gap-2 border-t border-[var(--line)] pt-3 first:border-0 first:pt-0",
  mobileCatalogTracks: "grid gap-2 border-t border-[var(--line)] pt-3",
  table:
    "w-full border-separate border-spacing-0 text-sm [font-variant-numeric:tabular-nums] [&_.react-aria-Column]:border-b [&_.react-aria-Column]:border-[var(--line)] [&_.react-aria-Column]:bg-[var(--raised)] [&_.react-aria-Column]:px-4 [&_.react-aria-Column]:py-2.5 [&_.react-aria-Column]:text-left [&_.react-aria-Column]:text-xs [&_.react-aria-Column]:font-medium [&_.react-aria-Column]:text-[var(--muted)] [&_.react-aria-Column]:whitespace-nowrap [&_.react-aria-Row]:bg-[var(--surface)] [&_.react-aria-Row]:transition-colors [&_.react-aria-Row:hover]:bg-[var(--raised)] [&_.react-aria-Row[data-selected]]:bg-[var(--accent-soft)] [&_.react-aria-Row+.react-aria-Row_.react-aria-Cell]:border-t [&_.react-aria-Row+.react-aria-Row_.react-aria-Cell]:border-[var(--line)] [&_.react-aria-Cell]:min-w-0 [&_.react-aria-Cell]:px-4 [&_.react-aria-Cell]:py-3 [&_.react-aria-Cell]:align-middle [&_.react-aria-Cell]:[overflow-wrap:anywhere] [&_.react-aria-Cell:first-child]:box-border [&_.react-aria-Cell:first-child]:w-12 [&_.react-aria-Cell:first-child]:min-w-12 [&_.react-aria-Cell:first-child]:max-w-12 [&_.react-aria-Cell:first-child]:px-0 [&_.react-aria-Cell:first-child]:text-center [&_.react-aria-Column:first-child]:box-border [&_.react-aria-Column:first-child]:w-12 [&_.react-aria-Column:first-child]:min-w-12 [&_.react-aria-Column:first-child]:max-w-12 [&_.react-aria-Column:first-child]:px-0 [&_.react-aria-Column:first-child]:text-center [&_.react-aria-Column]:align-middle [&_.react-aria-Cell:nth-child(2)]:align-middle [&_.react-aria-Cell_a]:font-medium [&_.react-aria-Cell_a]:text-[var(--accent-strong)] [&_.react-aria-Cell_a]:underline-offset-2 [&_.react-aria-Cell_a:hover]:underline [&_.filter-match]:bg-[linear-gradient(to_right,color-mix(in_srgb,var(--accent)_4%,transparent),transparent)] [&_.filter-match]:bg-left-top [&_.filter-match]:bg-no-repeat [&_.filter-match]:[background-size:9%_100%] [&_.filter-muted>td]:opacity-[.55] [&_.filter-muted:focus-within>td]:opacity-100",
  artistGrid:
    "grid grid-cols-[repeat(auto-fill,minmax(min(100%,260px),1fr))] gap-4",
  artistCard:
    "group relative flex min-w-0 flex-col gap-3 rounded-xl border border-[var(--line)] bg-[var(--surface)] p-4 text-left transition-colors hover:border-[var(--control)] hover:bg-[var(--raised)] data-[selected]:border-[color-mix(in_srgb,var(--accent)_45%,var(--line))] data-[selected]:bg-[var(--accent-soft)]",
  artistArt:
    "grid size-12 shrink-0 place-items-center rounded-lg border border-[var(--line)] bg-[var(--raised)] text-base font-semibold text-[var(--ink)]",
  artistCardLink:
    "min-w-0 no-underline hover:no-underline focus-visible:outline-none",
  artistMonogram: "block text-base font-semibold",
  artistOverlay: "min-w-0",
  artistCopy:
    "grid min-w-0 gap-0.5 [&_strong]:text-sm [&_strong]:font-semibold [&>span]:text-xs [&>span]:text-[var(--muted)]",
  artistStats:
    "grid grid-cols-2 gap-x-4 gap-y-1.5 border-t border-[var(--line)] pt-3 text-xs [&>span]:flex [&>span]:min-w-0 [&>span]:items-baseline [&>span]:justify-between [&>span]:gap-2 [&_strong]:font-medium [&_strong]:tabular-nums",
  cardSelection: "absolute right-3 top-3 z-[2] grid place-items-center",
  object: "min-w-0",
  hero: "mb-0 flex flex-nowrap items-start gap-x-6 gap-y-4 pb-6 max-[600px]:grid max-[600px]:grid-cols-[64px_minmax(0,1fr)] max-[600px]:items-start max-[600px]:gap-x-4",
  heroArt:
    "relative grid size-24 flex-none place-items-center overflow-hidden rounded-xl border border-[var(--line)] bg-[var(--raised)] text-[var(--ink)] max-[600px]:size-16",
  heroBody: "flex min-w-0 flex-1 flex-col justify-between gap-3 self-stretch",
  heroHeading: "min-w-0",
  heroFoot:
    "flex min-w-0 flex-wrap items-center justify-between gap-x-6 gap-y-2",
  heroStats: "m-0",
  heroBack:
    "mb-4 text-sm text-[var(--muted)] [&_a]:text-[var(--muted)] [&_a:hover]:text-[var(--ink)]",
  heroKicker: "mb-1 text-xs font-medium text-[var(--muted)]",
  heroGenre: "mt-1 text-sm text-[var(--muted)]",
  previewCount:
    "ml-1.5 inline-grid h-5 min-w-5 place-items-center rounded-full bg-[var(--raised)] px-1.5 text-xs tabular-nums",
  artistDanger:
    "[&_.ui-button--secondary.ui-button--danger]:border-[color-mix(in_srgb,var(--error)_35%,var(--line))] [&_.ui-button--secondary.ui-button--danger]:bg-[var(--surface)] [&_.ui-button--secondary.ui-button--danger]:text-[var(--error)]",
  objectHeader:
    "mb-5 flex flex-wrap items-start justify-between gap-3 border-b border-[var(--line)] pb-4 [&>div]:min-w-0 [&>div]:[overflow-wrap:anywhere] [&_h1]:text-[26px] [&_h1]:font-semibold [&_h1]:leading-[1.2] [&_h1]:tracking-[-.02em] [&_p]:max-w-[75ch] [&_p]:text-sm [&_p]:text-[var(--muted)]",
  panel:
    "mb-4 min-w-0 rounded-xl border border-[var(--line)] bg-[var(--surface)] p-5 [&_h2]:mb-3 [&_h2]:text-base [&_h2]:font-semibold [&_h3]:mb-2 [&_h3]:text-sm [&_h3]:font-semibold [&_p]:my-2 [&_p]:text-sm",
  facts:
    "my-3 grid grid-cols-[minmax(130px,.75fr)_minmax(0,2fr)] gap-x-4 gap-y-2 max-[600px]:grid-cols-1 max-[600px]:gap-y-0 [&_dt]:text-xs [&_dt]:text-[var(--muted)] [&_dd]:m-0 [&_dd]:text-sm [&_dd]:[overflow-wrap:anywhere] max-[600px]:[&_dd]:mb-2",
  actions: "my-3 flex flex-wrap items-center gap-2",
  secondary:
    "mt-1 block text-xs font-normal text-[var(--muted)] [overflow-wrap:anywhere]",
  sub: "mt-1 block text-xs font-normal text-[var(--muted)] [overflow-wrap:anywhere]",
  expander:
    "min-h-0 border-0 bg-transparent p-0 text-left text-sm font-medium text-[var(--accent-strong)] hover:underline",
  previewTable:
    "overflow-auto rounded-lg border border-[var(--line)] [&_table]:w-full [&_table]:min-w-[700px] [&_table]:border-collapse [&_table]:text-sm [&_th]:px-4 [&_th]:py-2.5 [&_th]:border-b [&_th]:border-[var(--line)] [&_td]:px-4 [&_td]:py-3 [&_td]:border-b [&_td]:border-[var(--line)] [&_th]:text-left [&_td]:text-left [&_th]:align-middle [&_td]:align-top [&_td:nth-child(2)]:align-middle [&_th]:[overflow-wrap:anywhere] [&_td]:[overflow-wrap:anywhere] [&_th]:bg-[var(--raised)] [&_th]:text-xs [&_th]:font-medium [&_th]:text-[var(--muted)] [&_tr:last-child>*]:border-b-0",
  editions: libraryEditionCardStyles.list,
  albumRow: libraryEditionCardStyles.row,
  albumCard: `${libraryEditionCardStyles.card} [&_.catalogue-edition-trigger]:after:content-['⌄'] [&_.catalogue-edition-trigger]:after:text-[18px] [&_.catalogue-edition-trigger]:after:text-[var(--accent)] [&_.catalogue-edition-trigger]:after:transition-transform [&_[data-expanded]_.catalogue-edition-trigger]:after:rotate-180`,
  albumHeader: libraryEditionCardStyles.header,
  albumTrigger: "min-w-0",
  albumDelete:
    "grid size-9 place-items-center rounded-lg border border-[var(--line)] bg-[var(--surface)] text-[var(--error)] hover:border-[var(--error)] hover:bg-[var(--error-soft)]",
  albumCopy: libraryEditionCardStyles.copy,
  albumMeta: libraryEditionCardStyles.meta,
  albumPanel: libraryEditionCardStyles.panel,
  albumActions: "px-5 py-4",
  artistPanel:
    "my-3 rounded-xl border border-[var(--line)] bg-[var(--surface)] p-5 [&_h2]:mb-3 [&_h2]:text-base [&_h2]:font-semibold [&_p]:my-1 [&_p]:text-sm [&_p]:text-[var(--muted)] [&_select]:h-9 [&_select]:rounded-lg [&_select]:border [&_select]:border-[var(--line)] [&_select]:bg-[var(--surface)] [&_select]:px-3",
  inlineTracks:
    "mt-3 grid min-w-[240px] gap-1 overflow-auto rounded-lg border border-[var(--line)] bg-[var(--raised)] p-2 [&>div]:grid [&>div]:grid-cols-[30px_minmax(100px,1fr)_auto] [&>div]:items-start [&>div]:gap-2 [&>div>span]:text-xs [&>div>span]:text-[var(--muted)]",
  inlineTable:
    "w-full min-w-[700px] border-collapse text-sm [&_th]:bg-[var(--surface)] [&_th]:text-xs [&_th]:font-medium [&_th]:text-[var(--muted)] [&_th]:text-left [&_th]:align-middle [&_th]:px-3 [&_th]:py-2 [&_td]:px-3 [&_td]:py-2.5 [&_td]:align-top [&_td:nth-child(2)]:align-middle [&_td]:[overflow-wrap:anywhere] [&_th]:border-b [&_th]:border-[var(--line)] [&_td]:border-b [&_td]:border-[var(--line)] [&_tr:last-child>*]:border-b-0",
  empty: "px-6 py-12 text-center text-sm text-[var(--muted)]",
  absPanel: "min-w-0",
  tableCheckbox:
    "group mx-auto grid size-8 cursor-pointer place-items-center data-[disabled]:cursor-default data-[disabled]:opacity-45",
  cardCheckbox: "group grid size-6 cursor-pointer place-items-center",
  checkboxBox:
    "grid size-4 place-items-center rounded border border-[var(--control)] bg-[var(--surface)] text-[var(--button-text)] group-data-[selected]:border-[var(--accent)] group-data-[selected]:bg-[var(--accent)] group-data-[indeterminate]:border-[var(--accent)] group-data-[indeterminate]:bg-[var(--accent)] group-data-[focus-visible]:outline-2 group-data-[focus-visible]:outline-[var(--accent)] group-data-[focus-visible]:outline-offset-2",
  cardCheckboxBox:
    "grid size-4 place-items-center rounded border border-[var(--control)] bg-[var(--surface)] text-[var(--button-text)] group-data-[hovered]:border-[var(--control)] group-data-[selected]:border-[var(--accent)] group-data-[selected]:bg-[var(--accent)] group-data-[indeterminate]:border-[var(--accent)] group-data-[indeterminate]:bg-[var(--accent)] group-data-[focus-visible]:outline-2 group-data-[focus-visible]:outline-[var(--accent)] group-data-[disabled]:opacity-45",
  dockDialog:
    "[&_[slot=title]]:mb-2 [&_[slot=title]]:text-lg [&_p]:my-2 [&_ul]:grid [&_ul]:gap-2 [&_code]:break-all",
  dockDialogFooter: "mt-4 flex flex-wrap justify-end gap-2",
  source:
    "group mb-1 flex min-w-0 cursor-pointer items-center gap-2 rounded-lg border border-[var(--line)] p-2.5 [overflow-wrap:anywhere] data-[selected]:border-[var(--accent)] data-[selected]:bg-[var(--accent-soft)] data-[focus-visible]:outline-2 data-[focus-visible]:outline-[var(--accent)] data-[focus-visible]:outline-offset-2 [&>span:last-child]:min-w-0 [&_strong]:[overflow-wrap:anywhere]",
  secondaryInline: "inline text-xs text-[var(--muted)]",
  radioDot:
    "size-4 flex-[0_0_16px] rounded-full border border-[var(--control)] group-data-[selected]:border-[5px] group-data-[selected]:border-[var(--accent)]",
  disclosure:
    "my-3 min-w-0 overflow-hidden rounded-xl border border-[var(--line)] bg-[var(--surface)] [&_summary]:cursor-pointer [&_summary]:text-sm [&_summary]:text-[var(--accent-strong)] [&_summary]:underline-offset-2 [&_summary:hover]:underline",
} as const;
