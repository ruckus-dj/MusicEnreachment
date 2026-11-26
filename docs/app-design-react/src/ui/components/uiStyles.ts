/**
 * Static Tailwind strings for the shared visual language.
 *
 * Values are literal utilities so the Tailwind scanner can see them. The
 * palette and radii live in `documentPresentation.ts`; these constants only
 * compose them. Do not add `@apply`, CSS selectors or runtime class building.
 */
export const ui = {
  page: "mx-auto w-full max-w-[1500px] px-6 py-6 max-[700px]:px-3 max-[700px]:py-4",
  pageHeader:
    "mb-6 flex flex-wrap items-start justify-between gap-x-6 gap-y-3 border-b border-[var(--line)] pb-5",
  pageHeading: "min-w-0",
  pageTitle:
    "m-0 text-[26px]/[1.2] font-semibold tracking-[-.02em] wrap-anywhere max-[700px]:text-[22px]",
  pageSubtitle: "m-0 mt-1.5 max-w-[75ch] text-sm text-[var(--muted)]",
  pageActions: "flex flex-wrap items-center gap-2",
  sectionTitle: "m-0 text-base font-semibold tracking-[-.01em]",
  panel: "min-w-0 rounded-xl border border-[var(--line)] bg-[var(--surface)]",
  panelPad: "p-5 max-[700px]:p-4",
  fieldLabel: "block text-sm font-medium text-[var(--ink)]",
  fieldHelp: "m-0 text-xs text-[var(--muted)]",
  fieldError: "m-0 text-xs font-medium text-[var(--error)]",
  input:
    "h-9 w-full min-w-0 rounded-lg border border-[var(--control)] bg-[var(--surface)] px-3 text-sm text-[var(--ink)] placeholder:text-[var(--muted)] hover:border-[var(--ink)] focus-visible:border-[var(--accent)] focus-visible:outline-2 focus-visible:outline-[var(--accent)] focus-visible:outline-offset-0 aria-[invalid=true]:border-[var(--error)] disabled:bg-[var(--raised)] disabled:text-[var(--muted)] pointer-coarse:h-10",
  textarea:
    "min-h-24 w-full min-w-0 rounded-lg border border-[var(--control)] bg-[var(--surface)] px-3 py-2 text-sm text-[var(--ink)] placeholder:text-[var(--muted)] hover:border-[var(--ink)] focus-visible:border-[var(--accent)] focus-visible:outline-2 focus-visible:outline-[var(--accent)] focus-visible:outline-offset-0 aria-[invalid=true]:border-[var(--error)] disabled:bg-[var(--raised)] disabled:text-[var(--muted)]",
  selectTrigger:
    "react-aria-Button flex h-9 w-full min-w-0 cursor-pointer items-center justify-between gap-2 rounded-lg border border-[var(--control)] bg-[var(--surface)] px-3 text-left text-sm text-[var(--ink)] hover:border-[var(--ink)] hover:bg-[var(--raised)] focus-visible:outline-2 focus-visible:outline-[var(--accent)] focus-visible:outline-offset-0 disabled:cursor-not-allowed disabled:bg-[var(--raised)] disabled:text-[var(--muted)] pointer-coarse:h-10",
  selectValue:
    "react-aria-SelectValue min-w-0 overflow-hidden text-ellipsis whitespace-nowrap",
  popover:
    "react-aria-Popover z-50 max-h-[320px] min-w-[var(--trigger-width)] overflow-auto rounded-xl border border-[var(--line)] bg-[var(--surface)] p-1 text-[var(--ink)] shadow-[0_12px_32px_rgb(0_0_0/0.14)]",
  listBox: "react-aria-ListBox grid gap-0.5 outline-none",
  option:
    "react-aria-ListBoxItem cursor-pointer rounded-lg px-2.5 py-2 text-sm text-[var(--ink)] outline-none data-[focused]:bg-[var(--raised)] data-[selected]:bg-[var(--accent-soft)] data-[selected]:text-[var(--accent-strong)] data-[focus-visible]:outline-2 data-[focus-visible]:outline-[var(--accent)]",
  toolbar: "flex flex-wrap items-center gap-2",
  tableFrame:
    "min-w-0 overflow-hidden rounded-lg border border-[var(--line)] bg-[var(--surface)]",
  racTable:
    "w-full border-separate border-spacing-0 text-sm [font-variant-numeric:tabular-nums] [&_.react-aria-Column]:border-b [&_.react-aria-Column]:border-[var(--line)] [&_.react-aria-Column]:bg-[var(--raised)] [&_.react-aria-Column]:px-4 [&_.react-aria-Column]:py-2.5 [&_.react-aria-Column]:text-left [&_.react-aria-Column]:text-xs [&_.react-aria-Column]:font-medium [&_.react-aria-Column]:text-[var(--muted)] [&_.react-aria-Column]:whitespace-nowrap [&_.react-aria-Row]:bg-[var(--surface)] [&_.react-aria-Row:hover]:bg-[var(--raised)] [&_.react-aria-Row[data-selected]]:bg-[var(--accent-soft)] [&_.react-aria-Row+.react-aria-Row_.react-aria-Cell]:border-t [&_.react-aria-Row+.react-aria-Row_.react-aria-Cell]:border-[var(--line)] [&_.react-aria-Cell]:min-w-0 [&_.react-aria-Cell]:px-4 [&_.react-aria-Cell]:py-3 [&_.react-aria-Cell]:align-middle [&_.react-aria-Cell]:wrap-anywhere [&_.react-aria-Row]:min-h-14 [&_.react-aria-Cell_a]:font-medium [&_.react-aria-Cell_a]:text-[var(--accent-strong)] [&_.react-aria-Cell_a]:underline-offset-2 [&_.react-aria-Cell_a:hover]:underline",
  nativeTable:
    "w-full border-separate border-spacing-0 text-sm [font-variant-numeric:tabular-nums] [&_th]:border-b [&_th]:border-[var(--line)] [&_th]:bg-[var(--raised)] [&_th]:px-4 [&_th]:py-2.5 [&_th]:text-left [&_th]:text-xs [&_th]:font-medium [&_th]:text-[var(--muted)] [&_th]:whitespace-nowrap [&_tr:hover]:bg-[var(--raised)] [&_tr+tr>td]:border-t [&_tr+tr>td]:border-[var(--line)] [&_td]:px-4 [&_td]:py-3 [&_td]:align-middle [&_td]:wrap-anywhere [&_a]:font-medium [&_a]:text-[var(--accent-strong)] [&_a]:underline-offset-2 [&_a:hover]:underline",
  badge:
    "inline-flex w-max max-w-full items-center gap-1 rounded-full border px-2 py-0.5 text-xs font-medium",
  badgeTone: {
    neutral: "border-[var(--line)] bg-[var(--raised)] text-[var(--muted)]",
    /** Quiet technical metadata; not a status. */
    metadata: "border-[var(--line)] bg-[var(--raised)] text-[var(--ink)]",
    /** File formats and codecs; deliberately not a status colour. */
    format:
      "border-[color-mix(in_srgb,var(--accent)_22%,transparent)] bg-[var(--accent-soft)] text-[var(--accent-strong)]",
    info: "border-[color-mix(in_srgb,var(--info)_25%,transparent)] bg-[var(--info-soft)] text-[var(--info)]",
    warning:
      "border-[color-mix(in_srgb,var(--warning)_30%,transparent)] bg-[var(--warning-soft)] text-[var(--warning)]",
    error:
      "border-[color-mix(in_srgb,var(--error)_25%,transparent)] bg-[var(--error-soft)] text-[var(--error)]",
    success:
      "border-[color-mix(in_srgb,var(--success)_25%,transparent)] bg-[var(--success-soft)] text-[var(--success)]",
  },
  notice:
    "m-0 rounded-lg border border-[var(--line)] bg-[var(--raised)] px-3.5 py-2.5 text-sm text-[var(--ink)]",
  noticeSuccess:
    "m-0 rounded-lg border border-[color-mix(in_srgb,var(--success)_25%,transparent)] bg-[var(--success-soft)] px-3.5 py-2.5 text-sm text-[var(--success)]",
  noticeError:
    "m-0 rounded-lg border border-[color-mix(in_srgb,var(--error)_25%,transparent)] bg-[var(--error-soft)] px-3.5 py-2.5 text-sm text-[var(--error)]",
  muted: "text-[var(--muted)]",
} as const;

export type UiBadgeTone = keyof typeof ui.badgeTone;
