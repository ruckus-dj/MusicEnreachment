import type { ComponentProps } from "react";
import { SourceDetailFeedbackView } from "./SourceDetailFeedbackView";
import {
	SourceInventoryOutlet,
	type SourceInventorySource,
} from "./SourceInventoryOutlet";
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
import { SourceScanPanel } from "./SourceScanPanel";

/** Structured read feedback, or a demo-only persistent status notice. */
export type SourceRootDetailFeedback =
	| Omit<ComponentProps<typeof SourceDetailFeedbackView>, "locale">
	| string;

/** Panel data only: this tree supplies the locale to the shared scan panel. */
export type SourceScanModel = Omit<
	ComponentProps<typeof SourceScanPanel>,
	"locale"
>;

export type SourceRootDetailViewProps<P extends object = Record<never, never>> =
	{
		locale: "en" | "ru";
		headingId: string;
		heading: Omit<ComponentProps<typeof SourceRootHeadingView>, "locale">;
		feedback?: SourceRootDetailFeedback;
		scan?: SourceScanModel;
		inventory?: SourceInventorySource<P>;
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
// note. Adapters supply typed panel data; an inventory controller mounts in its
// slot only while the root exists and keeps its own pagination lifecycle.
export function SourceRootDetailView<P extends object = Record<never, never>>({
	locale,
	headingId,
	heading,
	feedback,
	scan,
	inventory,
	overview,
	settings,
	removal,
}: SourceRootDetailViewProps<P>) {
	return (
		<>
			<section aria-labelledby={headingId} className="grid min-w-0 gap-[14px]">
				<SourceRootHeadingView {...heading} locale={locale} />
				{typeof feedback === "string" ? (
					<SourceNoticeView message={feedback} />
				) : (
					feedback && <SourceDetailFeedbackView {...feedback} locale={locale} />
				)}
				{scan && <SourceScanPanel {...scan} locale={locale} />}
				{inventory && (
					<SourceInventoryOutlet source={inventory} locale={locale} />
				)}
				{overview && <SourceRootOverviewView locale={locale} {...overview} />}
				{settings && <SourceRootSettingsView {...settings} locale={locale} />}
				{overview && <p className="m-0">{sourcePublicationsNote[locale]}</p>}
			</section>
			{removal && <SourceRootRemovalDialog {...removal} locale={locale} />}
		</>
	);
}
