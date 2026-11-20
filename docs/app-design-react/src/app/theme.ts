export type Theme = "system" | "light" | "dark";

export function applyTheme(theme: Exclude<Theme, "system">) {
	document.documentElement.dataset.theme = theme;
}
