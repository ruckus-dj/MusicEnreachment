import type { ReactNode } from "react";

const workspaceGrid = [
	"incoming-matching-workspace grid min-h-0 gap-3",
	"min-[1024px]:grid-cols-[minmax(0,1.6fr)_minmax(260px,1fr)] min-[1024px]:[&>[data-matching-inspector]]:col-span-full",
	"min-[1024px]:max-[1400px]:[&>:is([data-matching-positions],[data-matching-sources])]:min-h-0 min-[1024px]:max-[1400px]:[&>:is([data-matching-positions],[data-matching-sources])]:max-h-[clamp(340px,66dvh,680px)] min-[1024px]:max-[1400px]:[&>:is([data-matching-positions],[data-matching-sources])]:overflow-y-auto",
	"min-[1024px]:max-[1400px]:[&>[data-matching-sources]]:flex min-[1024px]:max-[1400px]:[&>[data-matching-sources]]:flex-col min-[1024px]:max-[1400px]:[&_.matching-board-source-status]:mt-auto",
		"min-[1400px]:h-[clamp(520px,72dvh,880px)] min-[1400px]:shrink-0 min-[1400px]:grid-rows-[minmax(0,1fr)_minmax(180px,min(32dvh,340px))]",
].join(" ");

export function MatchingWorkspaceLayout({ children }: { children: ReactNode }) {
	return (
		<div data-matching-workspace className={workspaceGrid}>
			{children}
		</div>
	);
}

export const matchingCandidatesPanel =
	"mb-3 min-w-0 rounded border border-[var(--line)] bg-[var(--surface)] p-3";

export const matchingInspectorPanel =
	"min-w-0 rounded border border-[var(--line)] bg-[var(--surface)] p-3 min-[1400px]:grid min-[1400px]:min-h-0 min-[1400px]:grid-cols-[minmax(0,1fr)_minmax(220px,auto)] min-[1400px]:gap-3";

export const matchingInspectorContent =
	"min-w-0 min-[1400px]:min-h-0 min-[1400px]:overflow-y-auto";

export const matchingInspectorActions =
	"flex flex-wrap content-start items-start gap-2 min-[1400px]:min-h-0 min-[1400px]:min-w-[220px] min-[1400px]:flex-col min-[1400px]:overflow-y-auto";
