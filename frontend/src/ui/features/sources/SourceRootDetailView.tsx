import type { ReactNode } from "react";
import {
	SourceRootOverviewView,
	type SourceRootOverviewViewProps,
} from "./SourceRootOverviewView";

export type SourceRootDetailViewProps = {
	locale: "en" | "ru";
	headingId: string;
	heading: ReactNode;
	status?: ReactNode;
	scan?: ReactNode;
	inventory?: ReactNode;
	overview?: Omit<SourceRootOverviewViewProps, "locale">;
	children?: ReactNode;
};

export const sourcePublicationsNote = {
	en: "An unavailable source does not remove existing managed publications.",
	ru: "Недоступность источника не удаляет существующие управляемые публикации.",
} as const;

// One section order for the prototype and the application: heading, request
// state, scan, inventory, root overview, then screen-specific sections. The
// adapter owns each slot's content and handlers.
export function SourceRootDetailView({
	locale,
	headingId,
	heading,
	status,
	scan,
	inventory,
	overview,
	children,
}: SourceRootDetailViewProps) {
	return (
		<section aria-labelledby={headingId} className="grid min-w-0 gap-[14px]">
			{heading}
			{status}
			{scan}
			{inventory}
			{overview && <SourceRootOverviewView locale={locale} {...overview} />}
			{children}
			{overview && <p className="m-0">{sourcePublicationsNote[locale]}</p>}
		</section>
	);
}
