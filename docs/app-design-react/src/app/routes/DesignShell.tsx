import type { ReactNode } from "react";
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
	"create-release",
	"publication-preview",
	"publication-list",
];

function familyForPath(path: string): ShellRouteFamily | undefined {
	if (!path || path === "/") return "home";
	if (path === "/setup") return "setup";
	if (path === "/operations") return "operations";
	if (path === "/settings") return "settings";
	const route = path.split("/", 1)[0];
	if (route === "operations" || route === "settings") return undefined;
	if (libraryRoutes.includes(route)) return "library";
	if (route === "match") return "review";
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
						review: "#review",
						library: "#library",
						local: "#local",
						sources: "#sources",
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
