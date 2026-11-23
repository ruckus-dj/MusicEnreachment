import type { ReactNode } from "react";
import {
	SourceRootOverviewView,
	type SourceRootOverviewViewProps,
} from "./SourceRootOverviewView";

export type SourceRootDetailViewProps = {
	locale: "en" | "ru";
	heading: ReactNode;
	status?: ReactNode;
	scan?: ReactNode;
	inventory?: ReactNode;
	overview?: Omit<SourceRootOverviewViewProps, "locale">;
	children?: ReactNode;
};

// One section order for the prototype and the application: heading, request
// state, scan, inventory, root overview, then screen-specific sections. The
// adapter owns each slot's content and handlers.
export function SourceRootDetailView({
	locale,
	heading,
	status,
	scan,
	inventory,
	overview,
	children,
}: SourceRootDetailViewProps) {
	return (
		<>
			{heading}
			{status}
			{scan}
			{inventory}
			{overview && <SourceRootOverviewView locale={locale} {...overview} />}
			{children}
		</>
	);
}
