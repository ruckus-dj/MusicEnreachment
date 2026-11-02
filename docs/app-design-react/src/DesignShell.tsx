import { type ReactNode, useSyncExternalStore } from "react";
import { ThemeControl } from "./ThemeControl";

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

export function DesignShell({ children }: { children: ReactNode }) {
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
	].includes(route)
		? "library"
		: route === "match"
			? "review"
			: route;
	return (
		<>
			<a
				className="demo-skip"
				href="#content"
				onClick={(event) => {
					event.preventDefault();
					document.querySelector<HTMLElement>(".demo-workspace main")?.focus();
				}}
			>
				К содержимому
			</a>
			<div className="design-shell">
				<aside className="design-rail">
					<a className="design-brand" href="#review">
						<span aria-hidden="true" className="design-mark">
							mt
						</span>
						<span>
							MeloTrove<small>АРХИВНАЯ МАСТЕРСКАЯ</small>
						</span>
					</a>
					<p className="design-rail-label">РАБОЧЕЕ ПРОСТРАНСТВО</p>
					<nav aria-label="Основная навигация">
						{sections.map(([id, label], index) => (
							<a
								key={id}
								href={`#${id}`}
								aria-current={section === id ? "page" : undefined}
							>
								<span aria-hidden="true" className="design-nav-number">
									{String(index + 1).padStart(2, "0")}
								</span>
								{label}
								{id === "review" && <span className="design-nav-count">3</span>}
							</a>
						))}
					</nav>
					<div className="design-rail-bottom">
						<p className="design-rail-label">ОБ ЭТОЙ ВЕРСИИ</p>
						<a href="#setup">Первый запуск</a>
						<a href="#components">Визуальная система</a>
						<a href="/compare">Сверка переноса</a>
						<p>
							Независимая концепция
							<br />
							Плотный каталог · v2
						</p>
					</div>
				</aside>
				<header className="design-topbar">
					<span>
						{sections.find(([id]) => id === section)?.[1] ?? "MeloTrove"}
					</span>
					<div>
						<span className="design-demo-label">МАКЕТ · ТЕСТОВЫЕ ДАННЫЕ</span>
						<ThemeControl />
					</div>
				</header>
				<div className="demo-workspace">{children}</div>
			</div>
		</>
	);
}
