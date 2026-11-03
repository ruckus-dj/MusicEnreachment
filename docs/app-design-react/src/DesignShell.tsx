import { type ReactNode, useSyncExternalStore } from "react";
import { ThemeControl } from "./ThemeControl";
import { Link } from "./ui";

const sections = [
	["review", "Входящие"],
	["library", "Медиатека"],
	["local", "Собственный каталог"],
	["sources", "Источники"],
	["operations", "Операции"],
	["settings", "Настройки"],
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
				К содержимому
			</a>
			<div className="grid h-dvh grid-cols-[174px_minmax(0,1fr)] grid-rows-[42px_minmax(0,1fr)] max-[599px]:flex max-[599px]:h-auto max-[599px]:min-h-dvh max-[599px]:flex-col">
				<aside className="row-span-2 flex flex-col overflow-auto border-r border-[var(--line)] bg-[var(--surface)] px-2 py-4 max-[599px]:visible max-[599px]:overflow-visible max-[599px]:border-r-0 max-[599px]:border-b max-[599px]:py-2">
					<Link
						className="mb-6 flex items-center gap-2 !text-[var(--ink)] text-[11px] font-semibold max-[599px]:mb-2"
						href="#review"
					>
						<span
							aria-hidden="true"
							className="border border-[var(--accent)] p-1 text-xl text-[var(--accent)]"
						>
							mt
						</span>
						<span>
							MeloTrove
							<small className="mt-[3px] block text-[7px] tracking-[.08em] text-[var(--muted)]">
								АРХИВНАЯ МАСТЕРСКАЯ
							</small>
						</span>
					</Link>
					<p className="mb-2 px-2 text-[9px] tracking-[.06em] text-[var(--muted)] max-[599px]:hidden">
						РАБОЧЕЕ ПРОСТРАНСТВО
					</p>
					<nav
						aria-label="Основная навигация"
						className="grid gap-[3px] max-[599px]:flex max-[599px]:flex-wrap"
					>
						{sections.map(([id, label], index) => (
							<Link
								key={id}
								href={`#${id}`}
								aria-current={section === id ? "page" : undefined}
								className="flex min-h-[34px] items-center gap-2 rounded px-2 py-1.5 !text-[var(--ink)] hover:bg-[var(--raised)] aria-[current=page]:bg-[var(--accent-soft)] aria-[current=page]:!text-[var(--accent)] aria-[current=page]:font-semibold max-[599px]:min-h-10"
							>
								<span
									aria-hidden="true"
									className="font-mono text-[10px] text-[var(--muted)] max-[599px]:hidden"
								>
									{String(index + 1).padStart(2, "0")}
								</span>
								{label}
								{id === "review" && (
									<span className="ml-auto font-mono text-[10px] text-[var(--muted)]">
										{incomingCount}
									</span>
								)}
							</Link>
						))}
					</nav>
					<div className="mt-auto pt-6 max-[599px]:hidden">
						<p className="mb-2 px-2 text-[9px] tracking-[.06em] text-[var(--muted)]">
							ОБ ЭТОЙ ВЕРСИИ
						</p>
						<a
							className="block px-2 py-1.5 text-[11px] text-[var(--muted)]"
							href="#setup"
						>
							Первый запуск
						</a>
						<a
							className="block px-2 py-1.5 text-[11px] text-[var(--muted)]"
							href="#components"
						>
							Визуальная система
						</a>
						<p className="mt-4 border-t border-[var(--line)] px-2 pt-3 text-[10px] text-[var(--muted)]">
							Независимая концепция
							<br />
							Плотный каталог · v2
						</p>
					</div>
				</aside>
				<header className="flex items-center justify-between gap-3 border-b border-[var(--line)] bg-[var(--surface)] px-3 py-1 text-[11px] text-[var(--muted)] max-[599px]:min-h-[42px] max-[599px]:flex-wrap">
					<span>
						{sections.find(([id]) => id === section)?.[1] ?? "MeloTrove"}
					</span>
					<div className="flex items-center gap-2">
						<span className="text-[9px] tracking-[.04em] max-[599px]:hidden">
							МАКЕТ · ТЕСТОВЫЕ ДАННЫЕ
						</span>
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
