import type { ReactNode } from "react";
import {
	incomingGroupsListHref,
	parseIncomingGroupsRoute,
} from "../../demo/providers/incoming/incomingGroupsRoute";
import { libraryRootHref } from "../../demo/providers/library/libraryRoute";
import {
	demoSourcesHref,
	parseDemoSourceRoute,
} from "../../demo/providers/sources/sourceDemoRoute";
import { ApplicationFrame } from "../../ui/shell/ShellStartupView";
import {
	type ShellRouteFamily,
	shellApplicationFrameState,
	shellSectionId,
} from "../../ui/shell/shellRoute";
import { useI18n } from "../i18n";
import { useHashLocation } from "../navigation/hashLocation";
import { themePreferenceStore } from "../theme/themePreferenceHost";

const libraryRoutes = [
	"artist",
	"release",
	"track",
	"tags",
	"publication-list",
];

function familyForPath(path: string): ShellRouteFamily | undefined {
	if (!path || path === "/") return "home";
	if (path === "/library") return "library";
	if (path === "/setup") return "setup";
	if (path === "/operations") return "operations";
	if (path === "/settings") return "settings";
	if (path === "/own-catalog") return "local";
	if (parseIncomingGroupsRoute(path)) return "review";
	if (parseDemoSourceRoute(path)) return "sources";
	const route = path.split("/", 1)[0];
	if (
		route === "operations" ||
		route === "settings" ||
		route === "sources" ||
		route === "review" ||
		route === "local"
	)
		return undefined;
	if (libraryRoutes.includes(route)) return "library";
	return shellSectionId(route);
}

export function DesignShell({
	children,
	incomingCount,
}: {
	children: ReactNode;
	incomingCount?: number;
}) {
	const { locale } = useI18n();
	const path = useHashLocation().slice(1).split("?", 1)[0];
	const family = familyForPath(path);
	return (
		<ApplicationFrame
			locale={locale}
			themeStore={themePreferenceStore}
			state={shellApplicationFrameState(
				family,
				family === "setup" ? "startup" : "product",
				{
					brandHref: "#/",
					sectionLinks: {
						review: incomingGroupsListHref(),
						library: libraryRootHref(),
						local: "#/own-catalog",
						sources: demoSourcesHref,
						operations: "#/operations",
						settings: "#/settings",
					},
					incomingCount,
					footer: { kind: "prototype" },
					setupHref: "#/setup",
				},
			)}
		>
			{children}
		</ApplicationFrame>
	);
}
