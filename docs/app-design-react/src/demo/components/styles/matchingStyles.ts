// Literal Tailwind utilities shared by the matching and publication views.
export const surfacePanel =
	"min-w-0 rounded-md border border-[var(--line)] bg-[var(--surface)] p-[13px] shadow-[0_1px_2px_#00000008]";

export const previewTableWrap =
	"min-w-0 overflow-auto rounded-[5px] border border-[var(--line)] bg-[var(--surface)]";

export const comparisonTableWrap =
	"min-w-0 overflow-auto rounded-[4px] border border-[var(--line)]";

export const table = "w-full border-collapse text-left text-[11px]";

export const previewTable = "w-full border-collapse text-left text-[11px]";

export const tableCell =
	"border-b border-[var(--line)] p-[7px] text-left align-top";

export const previewTableCell =
	"border-b border-[var(--line)] p-2 text-left align-top [overflow-wrap:anywhere]";

export const previewTableHeading =
	"sticky top-0 z-[1] border-b border-[var(--line)] bg-[var(--raised)] p-2 text-left align-top [overflow-wrap:anywhere]";

export const metadataPanel =
	"mb-2.5 min-w-0 rounded-[5px] border border-[var(--line)] bg-[var(--surface)] p-2.5";

export const comparisonControls =
	"my-2 grid grid-cols-[repeat(2,minmax(180px,1fr))] gap-2.5 [&>*]:min-w-0 max-[600px]:grid-cols-1";

export const comparisonRow =
	"[&:hover]:bg-[color-mix(in_srgb,var(--accent-soft)_36%,var(--surface))]";

export const comparisonRowHeading = `${tableCell} font-semibold`;

export const comparisonDifference = "font-bold text-[var(--warning)]";

export const comparisonNote =
	"my-2 text-[11px] text-[var(--muted)] [overflow-wrap:anywhere]";

export const objectActions = "my-2 flex flex-wrap items-center gap-2";

export const dialogFooter =
	"flex flex-wrap justify-end gap-2 border-t border-[var(--line)] pt-2";
