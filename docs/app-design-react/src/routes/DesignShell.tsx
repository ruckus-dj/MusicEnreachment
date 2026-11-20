import { type ReactNode, useSyncExternalStore } from "react";
import { LanguageControl } from "../components/LanguageControl";
import { ThemeControl } from "../components/ThemeControl";
import { useI18n } from "../i18n";
import { shell } from "../i18n/namespaces/shell";
import { ShellView } from "./ShellView";

const sections = [
	["review", "review"],
	["library", "library"],
	["local", "local"],
	["sources", "sources"],
	["operations", "operations"],
	["settings", "settings"],
] as const;
const subscribe = (listener: () => void) => {
	window.addEventListener("hashchange", listener);
	return () => window.removeEventListener("hashchange", listener);
};

export function DesignShell({
	children,
	incomingCount,
}: {
	children: ReactNode;
	incomingCount?: number;
}) {
	const { locale } = useI18n();
	const copy = shell[locale];
	const route = useSyncExternalStore(
		subscribe,
		() => window.location.hash.slice(1).split("/")[0] || "library",
		() => "library",
	);
	const section = [
		"artist",
		"release",
		"track",
		"tags",
		"create-release",
		"publication-preview",
		"publications",
		"publication-list",
	].includes(route)
		? "library"
		: route === "match"
			? "review"
			: route;
	return (
		<ShellView
			brand={{ href: "#review", label: "MeloTrove", mark: "mt" }}
			skipLabel={copy.skip}
			navigationLabel={copy.navigation}
			sectionLabel={
				copy[sections.find(([id]) => id === section)?.[1] ?? "library"]
			}
			navigation={sections.map(([id, label]) => ({
				id,
				label: copy[label],
				href: `#${id}`,
				current: section === id,
				count: id === "review" ? incomingCount : undefined,
			}))}
			controls={{
				language: <LanguageControl />,
				theme: <ThemeControl />,
			}}
			footerText={copy.prototypeFooter}
			setupLink={{ href: "#setup", label: copy.setup }}
		>
			{children}
		</ShellView>
	);
}
