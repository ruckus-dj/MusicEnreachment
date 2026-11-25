import {
	type ComponentProps,
	type ComponentType,
	createContext,
	type ReactNode,
	useContext,
} from "react";
import { SourceDetailFeedbackView } from "./SourceDetailFeedbackView";
import { SourceInventoryPanel } from "./SourceInventoryPanel";
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

export type SourceInventoryModel = Omit<
	ComponentProps<typeof SourceInventoryPanel>,
	"locale"
>;

/** Host-owned inventory controller props, opaque to this portable tree. */
type SourceInventoryHost<P extends object> = {
	Component: ComponentType<P & { children: ReactNode }>;
	props: P;
};

/** Inventory data, or a host component that binds its own controller here. */
type SourceInventorySource<P extends object> =
	| SourceInventoryModel
	| SourceInventoryHost<P>;

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

const InventoryModelContext = createContext<SourceInventoryModel | null>(null);

/** Host adapter: binds inventory panel data without rendering the portable view. */
export function SourceInventoryDataBinder({
	model,
	children,
}: {
	model: SourceInventoryModel;
	children: ReactNode;
}) {
	return (
		<InventoryModelContext.Provider value={model}>
			{children}
		</InventoryModelContext.Provider>
	);
}

function BoundInventoryView({ locale }: { locale: "en" | "ru" }) {
	const model = useContext(InventoryModelContext);
	if (!model) throw new Error("missing source inventory view model");
	return <SourceInventoryPanel {...model} locale={locale} />;
}

// A host controller keeps its own hook mounted and rebindable: changing the
// opaque props updates this component in place instead of remounting it.
function InventoryOutlet<P extends object>({
	source,
	locale,
}: {
	source: SourceInventorySource<P>;
	locale: "en" | "ru";
}) {
	if ("Component" in source) {
		const { Component, props } = source;
		return (
			<Component {...props}>
				<BoundInventoryView locale={locale} />
			</Component>
		);
	}
	return (
		<SourceInventoryDataBinder model={source}>
			<BoundInventoryView locale={locale} />
		</SourceInventoryDataBinder>
	);
}

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
				{inventory && <InventoryOutlet source={inventory} locale={locale} />}
				{overview && <SourceRootOverviewView locale={locale} {...overview} />}
				{settings && <SourceRootSettingsView {...settings} locale={locale} />}
				{overview && <p className="m-0">{sourcePublicationsNote[locale]}</p>}
			</section>
			{removal && <SourceRootRemovalDialog {...removal} locale={locale} />}
		</>
	);
}
