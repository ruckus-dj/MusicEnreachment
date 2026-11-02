import { useEffect, useState } from "react";

type Theme = "system" | "light" | "dark";
const storageKey = "music-design-v2-theme";
function initialTheme(): Theme {
	try {
		const saved = localStorage.getItem(storageKey);
		if (saved === "light" || saved === "dark") return saved;
	} catch {
		/* Storage is optional in the original prototype. */
	}
	return "system";
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
	return (
		<label className="demo-theme" title={warning}>
			Тема
			<select
				value={theme}
				onChange={(event) => {
					const next = event.currentTarget.value as Theme;
					setTheme(next);
					try {
						localStorage.setItem(storageKey, next);
						setWarning("");
					} catch {
						setWarning("Тема изменена; браузер не разрешил сохранить выбор.");
					}
				}}
			>
				<option value="system">Как в системе</option>
				<option value="light">Светлая</option>
				<option value="dark">Тёмная</option>
			</select>
		</label>
	);
}
