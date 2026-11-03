import { type ReactNode, useSyncExternalStore } from "react";
import { ThemeControl } from "./ThemeControl";
import { Link } from "./ui";
import { LanguageControl } from "./LanguageControl";
import { useI18n } from "./i18n";
import { shell } from "./i18n/namespaces/shell";

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
	incomingCount = 3,
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
		<>
			<a
				className="fixed left-3 top-[-80px] z-[200] bg-[var(--surface)] p-2 focus:top-2"
				href="#content"
				onClick={(event) => {
					event.preventDefault();
					document.querySelector<HTMLElement>("#content main")?.focus();
				}}
			>
				{copy.skip}
			</a>
			<div className="grid h-dvh grid-cols-[174px_minmax(0,1fr)] grid-rows-[42px_minmax(0,1fr)] max-[599px]:flex max-[599px]:h-auto max-[599px]:min-h-dvh max-[599px]:flex-col">
				<aside className="row-span-2 flex flex-col overflow-auto border-r border-[var(--line)] bg-[var(--surface)] px-2 py-4 max-[599px]:visible max-[599px]:overflow-visible max-[599px]:border-r-0 max-[599px]:border-b max-[599px]:py-2">
					<Link
						className="mb-6 flex items-center gap-2 !text-[var(--ink)] text-[12px] font-semibold max-[599px]:hidden"
						href="#review"
					>
						<span
							aria-hidden="true"
							className="border border-[var(--accent)] p-1 text-xl text-[var(--accent)]"
						>
							mt
						</span>
						<span>MeloTrove</span>
					</Link>
					<nav
						aria-label={copy.navigation}
						className="grid gap-[3px] max-[599px]:flex max-[599px]:flex-wrap"
					>
						{sections.map(([id, label]) => (
							<Link
								key={id}
								href={`#${id}`}
								aria-current={section === id ? "page" : undefined}
								className="flex min-h-[34px] items-center gap-2 rounded px-2 py-1.5 !text-[var(--ink)] hover:bg-[var(--raised)] aria-[current=page]:bg-[var(--accent-soft)] aria-[current=page]:!text-[var(--accent)] aria-[current=page]:font-semibold max-[599px]:min-h-10"
							>
								{copy[label]}
								{id === "review" && (
									<span className="ml-auto text-[12px] text-[var(--muted)]">
										{incomingCount}
									</span>
								)}
							</Link>
						))}
					</nav>
					<div className="mt-auto pt-6 max-[599px]:hidden">
						<a
							className="block px-2 py-1.5 text-[12px] text-[var(--muted)]"
							href="#setup"
						>
							{copy.setup}
						</a>
						<p className="mt-4 px-2 text-xs text-[var(--muted)]">
							{copy.prototypeDisclosure}
						</p>
					</div>
				</aside>
				<header className="flex items-center justify-between gap-3 border-b border-[var(--line)] bg-[var(--surface)] px-3 py-1 text-[12px] text-[var(--muted)] max-[599px]:min-h-[42px] max-[599px]:flex-wrap">
					<span>
						<span className="hidden max-[599px]:inline">MeloTrove · </span>
						{copy[sections.find(([id]) => id === section)?.[1] ?? "library"]}
					</span>
					<div className="flex items-center gap-2">
						<LanguageControl />
						<ThemeControl />
					</div>
				</header>
				<div
					id="content"
					className="min-h-0 min-w-0 overflow-auto max-[599px]:overflow-visible"
				>
					{children}
				</div>
			</div>
		</>
	);
}
