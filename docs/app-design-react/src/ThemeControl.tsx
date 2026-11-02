import { useEffect, useState } from "react";
import { Button, Tooltip, TooltipTrigger } from "./ui";
import "./theme-button.css";

type Theme = "system" | "light" | "dark";
const storageKey = "music-design-v2-theme";
const themes: Theme[] = ["system", "light", "dark"];
const labels: Record<Theme, string> = {
	system: "как в системе",
	light: "светлая",
	dark: "тёмная",
};

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
	const [theme, setTheme] = useState<Theme>(initialTheme);
	const [warning, setWarning] = useState("");
	useEffect(() => {
		const media = window.matchMedia?.("(prefers-color-scheme: dark)");
		const apply = () => {
			document.documentElement.dataset.theme =
				theme === "system" ? (media?.matches ? "dark" : "light") : theme;
		};
		apply();
		media?.addEventListener("change", apply);
		return () => media?.removeEventListener("change", apply);
	}, [theme]);
	const currentLabel = labels[theme];
	const nextLabel = labels[nextTheme(theme)];
	return (
		<div className="theme-control">
			<TooltipTrigger>
				<Button
					className="theme-button"
					aria-label={`Тема: ${currentLabel}. Следующая: ${nextLabel}`}
					onPress={() => {
						const next = nextTheme(theme);
						setTheme(next);
						try {
							localStorage.setItem(storageKey, next);
							setWarning("");
						} catch {
							setWarning("Тема изменена; браузер не разрешил сохранить выбор.");
						}
					}}
				>
					<ThemeIcon theme={theme} />
				</Button>
				<Tooltip>{`Тема: ${currentLabel}`}</Tooltip>
			</TooltipTrigger>
			{warning && <span role="status">{warning}</span>}
		</div>
	);
}
