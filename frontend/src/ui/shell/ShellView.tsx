import {
	type ReactNode,
	useCallback,
	useEffect,
	useRef,
	useState,
} from "react";
import { AppIcon, type AppIconName } from "../components/AppIcon";
import { LanguageControl } from "../components/LanguageControl";
import { ThemeControl } from "../components/ThemeControl";
import type { ThemePreferenceStore } from "../components/themePreference";
import { shell } from "../i18n/namespaces/shell";
import type { Locale } from "../i18n/types";
import { shellStartupMessages } from "./shellStartupMessages";

export interface ShellNavigationItem {
	id: string;
	label: string;
	href: string;
	current?: boolean;
	count?: number;
}

export interface ShellBrand {
	href: string;
	label: string;
	mark: string;
}

export interface ShellControls {
	language: ReactNode;
	theme: ReactNode;
}

export const shellSections = [
	"review",
	"library",
	"local",
	"sources",
	"operations",
	"settings",
] as const;

export type ShellSectionId = (typeof shellSections)[number];

export type ShellSectionLinks = Record<ShellSectionId, string>;

export interface ProductShellProps {
	locale: Locale;
	brandHref: string;
	activeSection?: ShellSectionId;
	sectionLinks: ShellSectionLinks;
	incomingCount?: number;
	footer:
		| { kind: "label"; label: string }
		| {
				kind: "version";
				version: string;
		  };
	setupHref?: string;
	themeStore: ThemePreferenceStore;
	fallbackSection?: "home" | "library";
	children: ReactNode;
}

export interface ShellViewProps {
	brand: ShellBrand;
	skipLabel: string;
	navigationLabel: string;
	sectionLabel?: string;
	navigation: readonly ShellNavigationItem[];
	controls: ShellControls;
	footerText: string;
	setupLink?: { href: string; label: string };
	children: ReactNode;
}

const navigationIcons: Record<string, AppIconName> = {
	review: "inbox",
	library: "library",
	local: "catalog",
	sources: "sources",
	operations: "operations",
	settings: "settings",
};

export function shellControls(themeStore: ThemePreferenceStore): ShellControls {
	return {
		language: <LanguageControl />,
		theme: <ThemeControl store={themeStore} />,
	};
}

export function ShellContent({ children }: { children: ReactNode }) {
	return (
		<div className="min-w-0 px-6 py-6 max-[700px]:px-3 max-[700px]:py-4">
			{children}
		</div>
	);
}

export function ProductShellView({ children, ...props }: ShellViewProps) {
	return (
		<div className="text-[14px]/[1.5]">
			<ShellView {...props}>
				<ShellContent>{children}</ShellContent>
			</ShellView>
		</div>
	);
}

/**
 * Portable product shell: canonical section order, default labels and
 * theme/locale controls. Links, active section, counts, footer and setup
 * capability are caller data; host routing stays outside this tree.
 */
export function ProductShell({
	locale,
	brandHref,
	activeSection,
	sectionLinks,
	incomingCount,
	footer,
	setupHref,
	themeStore,
	fallbackSection = "library",
	children,
}: ProductShellProps) {
	const copy = shell[locale];
	const navigation: ShellNavigationItem[] = shellSections.map((id) => ({
		id,
		label: copy[id],
		href: sectionLinks[id],
		current: activeSection === id,
		count: id === "review" ? incomingCount : undefined,
	}));
	return (
		<ProductShellView
			brand={{ href: brandHref, label: "MeloTrove", mark: "mt" }}
			skipLabel={copy.skip}
			navigationLabel={copy.navigation}
			sectionLabel={
				activeSection
					? copy[activeSection]
					: fallbackSection === "home"
						? shellStartupMessages[locale].home
						: copy.library
			}
			navigation={navigation}
			controls={shellControls(themeStore)}
			footerText={footer.kind === "label" ? footer.label : `v${footer.version}`}
			setupLink={setupHref ? { href: setupHref, label: copy.setup } : undefined}
		>
			{children}
		</ProductShellView>
	);
}

/**
 * Portable shell presentation: navigation, chrome and keyboard skip target.
 * Copy, controls, counts, links and footer identity come from the caller.
 */
export function ShellView({
	brand,
	skipLabel,
	navigationLabel,
	sectionLabel,
	navigation,
	controls,
	footerText,
	setupLink,
	children,
}: ShellViewProps) {
	const content = useRef<HTMLElement>(null);
	const navigationScroller = useRef<HTMLElement>(null);
	const [scrollEdges, setScrollEdges] = useState({ start: false, end: false });
	const syncScrollEdges = useCallback(() => {
		const node = navigationScroller.current;
		if (!node) return;
		const maxScroll = node.scrollWidth - node.clientWidth;
		const start = node.scrollLeft > 2;
		const end = maxScroll - node.scrollLeft > 2;
		setScrollEdges((current) =>
			current.start === start && current.end === end ? current : { start, end },
		);
	}, []);
	useEffect(() => {
		const node = navigationScroller.current;
		if (!node) return;
		syncScrollEdges();
		const observer =
			typeof ResizeObserver === "undefined"
				? undefined
				: new ResizeObserver(syncScrollEdges);
		observer?.observe(node);
		window.addEventListener("resize", syncScrollEdges);
		return () => {
			observer?.disconnect();
			window.removeEventListener("resize", syncScrollEdges);
		};
	}, [syncScrollEdges]);
	const activeLabel =
		sectionLabel ??
		navigation.find((item) => item.current)?.label ??
		navigation[0]?.label ??
		"";
	return (
		<>
			<button
				type="button"
				className="fixed left-3 top-[-80px] z-[200] rounded-lg border border-[var(--line)] bg-[var(--surface)] px-3 py-2 text-sm shadow-sm focus:top-2"
				onClick={() => content.current?.focus()}
			>
				{skipLabel}
			</button>
			<div className="grid h-dvh grid-cols-[240px_minmax(0,1fr)] grid-rows-[56px_minmax(0,1fr)] max-[599px]:flex max-[599px]:h-auto max-[599px]:min-h-dvh max-[599px]:flex-col">
				<aside className="row-span-2 flex min-h-0 flex-col overflow-auto border-r border-[var(--line)] bg-[var(--surface)] px-4 py-5 max-[599px]:overflow-visible max-[599px]:border-r-0 max-[599px]:border-b max-[599px]:px-3 max-[599px]:py-3">
					<a
						className="mb-7 flex items-center gap-2.5 rounded-lg text-[15px] font-semibold tracking-[-.01em] text-[var(--ink)] no-underline focus-visible:outline-2 focus-visible:outline-[var(--accent)] focus-visible:outline-offset-2 max-[599px]:hidden"
						href={brand.href}
					>
						<span
							aria-hidden="true"
							className="grid size-9 place-items-center rounded-[var(--control-radius)] bg-[var(--button)] text-[var(--button-text)]"
						>
							<AppIcon name="music" className="size-5" />
						</span>
						<span>{brand.label}</span>
					</a>
					<div className="relative min-w-0">
						<nav
							ref={navigationScroller}
							onScroll={syncScrollEdges}
							aria-label={navigationLabel}
							className="grid gap-1 max-[599px]:flex max-[599px]:flex-nowrap max-[599px]:overflow-x-auto max-[599px]:pb-2 max-[599px]:[scrollbar-color:var(--control)_transparent] max-[599px]:[scrollbar-width:thin] [&::-webkit-scrollbar]:h-1.5 [&::-webkit-scrollbar-thumb]:rounded-full [&::-webkit-scrollbar-thumb]:bg-[var(--control)] [&::-webkit-scrollbar-track]:bg-transparent"
						>
							{navigation.map((item) => (
								<a
									key={item.id}
									href={item.href}
									aria-current={item.current ? "page" : undefined}
									className="group flex min-h-10 items-center gap-3 rounded-[var(--control-radius)] px-3 py-2 text-sm font-medium text-[var(--muted)] no-underline transition-colors hover:bg-[var(--raised)] hover:text-[var(--ink)] aria-[current=page]:bg-[var(--accent-soft)] aria-[current=page]:text-[var(--accent-strong)] focus-visible:outline-2 focus-visible:outline-[var(--accent)] focus-visible:outline-offset-2 max-[599px]:min-h-9 max-[599px]:shrink-0"
								>
									<AppIcon
										name={navigationIcons[item.id] ?? "library"}
										className="size-[18px] shrink-0"
									/>
									<span className="min-w-0 truncate">{item.label}</span>
									{typeof item.count === "number" && (
										<span className="ml-auto inline-grid h-5 min-w-5 place-items-center rounded-full bg-[var(--surface)] px-1.5 text-xs font-medium tabular-nums text-[var(--muted)] ring-1 ring-[var(--line)] aria-[current=page]:bg-[var(--surface)]">
											{item.count}
										</span>
									)}
								</a>
							))}
						</nav>
						<span
							aria-hidden="true"
							className={`pointer-events-none absolute top-0 bottom-2 left-0 hidden w-7 items-center bg-gradient-to-r from-[var(--surface)] to-transparent pl-0.5 text-[var(--muted)] transition-opacity motion-reduce:transition-none max-[599px]:flex ${
								scrollEdges.start ? "opacity-100" : "opacity-0"
							}`}
						>
							<AppIcon name="chevronRight" className="size-4 rotate-180" />
						</span>
						<span
							aria-hidden="true"
							className={`pointer-events-none absolute top-0 right-0 bottom-2 hidden w-7 items-center justify-end bg-gradient-to-l from-[var(--surface)] to-transparent pr-0.5 text-[var(--muted)] transition-opacity motion-reduce:transition-none max-[599px]:flex ${
								scrollEdges.end ? "opacity-100" : "opacity-0"
							}`}
						>
							<AppIcon name="chevronRight" className="size-4" />
						</span>
					</div>
					<div className="mt-auto grid gap-3 pt-8 max-[599px]:hidden">
						{setupLink && (
							<a
								className="flex items-center gap-2 rounded-lg px-3 py-2 text-sm text-[var(--muted)] hover:bg-[var(--raised)] hover:text-[var(--ink)] focus-visible:outline-2 focus-visible:outline-[var(--accent)] focus-visible:outline-offset-2"
								href={setupLink.href}
							>
								<AppIcon name="chevronRight" className="size-4" />
								{setupLink.label}
							</a>
						)}
						<p className="m-0 px-3 text-xs text-[var(--muted)]">{footerText}</p>
					</div>
				</aside>
				<header className="flex items-center justify-between gap-4 border-b border-[var(--line)] bg-[var(--surface)] px-6 max-[599px]:min-h-[52px] max-[599px]:flex-wrap max-[599px]:gap-2 max-[599px]:px-3 max-[599px]:py-2">
					<div className="flex min-w-0 items-center gap-2 text-sm">
						<span className="hidden max-[599px]:inline">
							<a
								href={brand.href}
								className="font-semibold text-[var(--ink)] no-underline underline-offset-2 hover:underline focus-visible:outline-2 focus-visible:outline-[var(--accent)] focus-visible:outline-offset-2"
							>
								{brand.label}
							</a>
							<span className="mx-2 text-[var(--control)]" aria-hidden="true">
								/
							</span>
						</span>
						<span className="truncate font-medium text-[var(--ink)]">
							{activeLabel}
						</span>
					</div>
					<div className="flex shrink-0 items-center gap-2">
						{controls.language}
						{controls.theme}
					</div>
				</header>
				<main
					id="content"
					ref={content}
					tabIndex={-1}
					className="min-h-0 min-w-0 overflow-auto max-[599px]:overflow-visible"
				>
					{children}
				</main>
			</div>
		</>
	);
}
