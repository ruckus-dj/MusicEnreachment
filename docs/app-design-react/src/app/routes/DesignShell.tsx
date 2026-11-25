import { type ReactNode, useSyncExternalStore } from "react";
import { ApplicationFrame } from "../../ui/shell/ShellStartupView";
import { type ShellSectionId, shellSections } from "../../ui/shell/ShellView";
import { useI18n } from "../i18n";
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
const subscribe = (listener: () => void) => {
	window.addEventListener("hashchange", listener);
	return () => window.removeEventListener("hashchange", listener);
};

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
	const route = useSyncExternalStore(
		subscribe,
		() => window.location.hash.slice(1).split("/")[0] || "library",
		() => "library",
	);
	const section = libraryRoutes.includes(route)
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
								operations: "#operations",
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
