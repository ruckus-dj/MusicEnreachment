import { type ReactNode, useRef } from "react";

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

export function ShellContent({ children }: { children: ReactNode }) {
	return (
		<div className="min-w-0 p-2.5 [&>:first-child]:mt-0 min-[600px]:p-3">
			{children}
		</div>
	);
}

export function ProductShellView({ children, ...props }: ShellViewProps) {
	return (
		<div className="font-[system-ui,sans-serif] text-[13px]/[1.4]">
			<ShellView {...props}>
				<ShellContent>{children}</ShellContent>
			</ShellView>
		</div>
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
	const activeLabel =
		sectionLabel ??
		navigation.find((item) => item.current)?.label ??
		navigation[0]?.label ??
		"";
	return (
		<>
			<button
				type="button"
				className="fixed left-3 top-[-80px] z-[200] border-0 bg-[var(--surface)] p-2 focus:top-2"
				onClick={() => content.current?.focus()}
			>
				{skipLabel}
			</button>
			<div className="grid h-dvh grid-cols-[174px_minmax(0,1fr)] grid-rows-[42px_minmax(0,1fr)] max-[599px]:flex max-[599px]:h-auto max-[599px]:min-h-dvh max-[599px]:flex-col">
				<aside className="row-span-2 flex flex-col overflow-auto border-r border-[var(--line)] bg-[var(--surface)] px-2 py-4 max-[599px]:visible max-[599px]:overflow-visible max-[599px]:border-r-0 max-[599px]:border-b max-[599px]:py-2">
					<a
						className="mb-6 flex items-center gap-2 text-[12px] font-semibold text-[var(--ink)] no-underline decoration-1 underline-offset-2 hover:underline focus-visible:outline-2 focus-visible:outline-[var(--focus)] focus-visible:outline-offset-2 max-[599px]:hidden"
						href={brand.href}
					>
						<span
							aria-hidden="true"
							className="border border-[var(--accent)] p-1 text-xl text-[var(--accent)]"
						>
							{brand.mark}
						</span>
						<span>{brand.label}</span>
					</a>
					<nav
						aria-label={navigationLabel}
						className="grid gap-[3px] max-[599px]:flex max-[599px]:flex-wrap"
					>
						{navigation.map((item) => (
							<a
								key={item.id}
								href={item.href}
								aria-current={item.current ? "page" : undefined}
								className="flex min-h-[34px] items-center gap-2 rounded px-2 py-1.5 text-[var(--ink)] no-underline decoration-1 underline-offset-2 hover:bg-[var(--raised)] hover:underline aria-[current=page]:bg-[var(--accent-soft)] aria-[current=page]:font-semibold aria-[current=page]:text-[var(--accent)] focus-visible:outline-2 focus-visible:outline-[var(--focus)] focus-visible:outline-offset-2 max-[599px]:min-h-10"
							>
								{item.label}
								{typeof item.count === "number" && (
									<span className="ml-auto text-[12px] text-[var(--muted)]">
										{item.count}
									</span>
								)}
							</a>
						))}
					</nav>
					<div className="mt-auto pt-6 max-[599px]:hidden">
						{setupLink && (
							<a
								className="block px-2 py-1.5 text-[12px] text-[var(--muted)]"
								href={setupLink.href}
							>
								{setupLink.label}
							</a>
						)}
						<p
							className={`px-2 text-xs text-[var(--muted)] ${
								setupLink ? "mt-4" : ""
							}`}
						>
							{footerText}
						</p>
					</div>
				</aside>
				<header className="flex items-center justify-between gap-3 border-b border-[var(--line)] bg-[var(--surface)] px-3 py-1 text-[12px] text-[var(--muted)] max-[599px]:min-h-[42px] max-[599px]:flex-wrap">
					<span>
						<span className="hidden max-[599px]:inline">
							<a
								href={brand.href}
								className="no-underline underline-offset-2 hover:underline focus-visible:outline-2 focus-visible:outline-[var(--focus)] focus-visible:outline-offset-2"
							>
								{brand.label}
							</a>{" "}
							·{" "}
						</span>
						{activeLabel}
					</span>
					<div className="flex items-center gap-2">
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
