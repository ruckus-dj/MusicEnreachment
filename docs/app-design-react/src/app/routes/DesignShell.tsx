import type { ReactNode } from "react";
import { ApplicationFrame } from "../../ui/shell/ShellStartupView";
import { type ShellSectionId, shellSections } from "../../ui/shell/ShellView";
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
function sectionForRoute(section: string): ShellSectionId | undefined {
	return shellSections.some((id) => id === section)
		? (section as ShellSectionId)
		: undefined;
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
	const route = path.split("/", 1)[0] || "library";
	const section =
		path === "/operations"
			? "operations"
			: route === "operations"
				? ""
				: libraryRoutes.includes(route)
					? "library"
					: route === "match"
						? "review"
						: route;
	return (
		<ApplicationFrame
			locale={locale}
			themeStore={themePreferenceStore}
			state={
				route === "setup"
					? { kind: "startup" }
					: {
							kind: "product",
							brandHref: "#review",
							activeSection: sectionForRoute(section),
							sectionLinks: {
								review: "#review",
								library: "#library",
								local: "#local",
								sources: "#sources",
								operations: "#/operations",
								settings: "#settings",
							},
							incomingCount,
							footer: { kind: "prototype" },
							setupHref: "#setup",
						}
			}
		>
			{children}
		</ApplicationFrame>
	);
}
