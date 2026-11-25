import type { ComponentProps, ReactNode } from "react";
import { SourceDetailFeedbackView } from "./SourceDetailFeedbackView";
import { SourceNoticeView } from "./SourcePageFeedbackView";
import { SourceRootHeadingView } from "./SourceRootHeadingView";
import {
	SourceRootOverviewView,
	type SourceRootOverviewViewProps,
} from "./SourceRootOverviewView";
import { SourceRootRemovalDialog } from "./SourceRootRemovalDialog";
import {
	SourceRootSettingsView,
	type SourceRootSettingsViewProps,
} from "./SourceRootSettingsView";

/** Structured read feedback, or a demo-only persistent status notice. */
export type SourceRootDetailFeedback =
	| Omit<ComponentProps<typeof SourceDetailFeedbackView>, "locale">
	| string;

export type SourceRootDetailViewProps = {
	locale: "en" | "ru";
	headingId: string;
	heading: Omit<ComponentProps<typeof SourceRootHeadingView>, "locale">;
	feedback?: SourceRootDetailFeedback;
	/** Host scan controller: admission and request state stay outside this tree. */
	scan?: ReactNode;
	/** Host inventory controller: pagination and refresh stay outside this tree. */
	inventory?: ReactNode;
	overview?: Omit<SourceRootOverviewViewProps, "locale">;
	settings?: Omit<SourceRootSettingsViewProps, "locale">;
	removal?: Omit<ComponentProps<typeof SourceRootRemovalDialog>, "locale">;
};

export const sourcePublicationsNote = {
	en: "An unavailable source does not remove existing managed publications.",
	ru: "Недоступность источника не удаляет существующие управляемые публикации.",
} as const;

// One section order for the prototype and the application: heading, read
// feedback, scan, inventory, root overview, settings, then the publication
// note. Adapters supply typed models and handlers; the scan and inventory
// controllers stay host slots until their own bindings meet here.
export function SourceRootDetailView({
	locale,
	headingId,
	heading,
	feedback,
	scan,
	inventory,
	overview,
	settings,
	removal,
}: SourceRootDetailViewProps) {
	return (
		<>
			<section aria-labelledby={headingId} className="grid min-w-0 gap-[14px]">
				<SourceRootHeadingView {...heading} locale={locale} />
				{typeof feedback === "string" ? (
					<SourceNoticeView message={feedback} />
				) : (
					feedback && <SourceDetailFeedbackView {...feedback} locale={locale} />
				)}
				{scan}
				{inventory}
				{overview && <SourceRootOverviewView locale={locale} {...overview} />}
				{settings && <SourceRootSettingsView {...settings} locale={locale} />}
				{overview && <p className="m-0">{sourcePublicationsNote[locale]}</p>}
			</section>
			{removal && <SourceRootRemovalDialog {...removal} locale={locale} />}
		</>
	);
}
