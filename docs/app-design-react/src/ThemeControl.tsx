import { useEffect, useState } from "react";
import { Button, Tooltip, TooltipTrigger } from "./ui";
import { applyTheme, type Theme } from "./theme";
import { useI18n } from "./i18n";
import { shell } from "./i18n/namespaces/shell";

const storageKey = "music-design-v2-theme";
const themes: Theme[] = ["system", "light", "dark"];

function initialTheme(): Theme {
	try {
		const saved = localStorage.getItem(storageKey);
		if (saved === "light" || saved === "dark") return saved;
	} catch {
		/* Storage is optional in the original prototype. */
	}
	return "system";
}

function nextTheme(theme: Theme): Theme {
	return themes[(themes.indexOf(theme) + 1) % themes.length];
}

function ThemeIcon({ theme }: { theme: Theme }) {
	if (theme === "light") {
		return (
			<svg
				viewBox="0 0 24 24"
				fill="none"
				stroke="currentColor"
				strokeWidth="1.7"
				strokeLinecap="round"
				aria-hidden="true"
				focusable="false"
			>
				<circle cx="12" cy="12" r="4" />
				<path d="M12 2v2M12 20v2M4.93 4.93l1.42 1.42M17.65 17.65l1.42 1.42M2 12h2M20 12h2M4.93 19.07l1.42-1.42M17.65 6.35l1.42-1.42" />
			</svg>
		);
	}
	if (theme === "dark") {
		return (
			<svg
				viewBox="0 0 24 24"
				fill="none"
				stroke="currentColor"
				strokeWidth="1.7"
				strokeLinejoin="round"
				aria-hidden="true"
				focusable="false"
			>
				<path d="M20 14.5A8.5 8.5 0 0 1 9.5 4a8.5 8.5 0 1 0 10.5 10.5Z" />
			</svg>
		);
	}
	return (
		<svg
			viewBox="0 0 24 24"
			fill="none"
			stroke="currentColor"
			strokeWidth="1.7"
			aria-hidden="true"
			focusable="false"
		>
			<circle cx="12" cy="12" r="9" />
			<path d="M12 3a9 9 0 0 1 0 18Z" fill="currentColor" stroke="none" />
		</svg>
	);
}

export function ThemeControl() {
	const { locale } = useI18n();
	const copy = shell[locale];
	const labels: Record<Theme, string> = {
		system: copy.systemTheme,
		light: copy.light,
		dark: copy.dark,
	};
	const [theme, setTheme] = useState<Theme>(initialTheme);
	const [warning, setWarning] = useState(false);
	useEffect(() => {
		const media = window.matchMedia?.("(prefers-color-scheme: dark)");
		const apply = () =>
			applyTheme(
				theme === "system" ? (media?.matches ? "dark" : "light") : theme,
			);
		apply();
		media?.addEventListener("change", apply);
		return () => media?.removeEventListener("change", apply);
	}, [theme]);
	const currentLabel = labels[theme];
	const nextLabel = labels[nextTheme(theme)];
	return (
		<div className="flex items-center gap-2 text-[11px] text-[var(--muted)]">
			<TooltipTrigger>
				<Button
					className="h-[26px] w-7 !min-h-0 rounded-[4px] border border-[var(--control)] bg-[var(--surface)] !p-0 text-[var(--ink)] motion-safe:transition-[background-color,border-color] motion-safe:duration-[120ms] motion-reduce:transition-none hover:border-[var(--accent)] hover:bg-[var(--raised)] data-[hovered]:border-[var(--accent)] data-[hovered]:bg-[var(--raised)] data-[pressed]:bg-[var(--accent-soft)] data-[focus-visible]:outline-2 data-[focus-visible]:outline-[var(--focus)] data-[focus-visible]:outline-offset-2 [&_svg]:size-[15px]"
					aria-label={`${copy.theme}: ${currentLabel}. ${copy.nextTheme}: ${nextLabel}`}
					onPress={() => {
						const next = nextTheme(theme);
						setTheme(next);
						try {
							localStorage.setItem(storageKey, next);
							setWarning(false);
						} catch {
							setWarning(true);
						}
					}}
				>
					<ThemeIcon theme={theme} />
				</Button>
				<Tooltip>{`${copy.theme}: ${currentLabel}`}</Tooltip>
			</TooltipTrigger>
			{warning && (
				<span
					className="max-w-[220px] text-[10px] text-[var(--warning)]"
					role="status"
				>
					{copy.themeWarning}
				</span>
			)}
		</div>
	);
}
