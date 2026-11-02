import { useEffect, useState } from "react";
import {
	Button,
	Label,
	ListBox,
	ListBoxItem,
	Popover,
	Select,
	SelectValue,
} from "./ui";

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
		<>
			<Select
				aria-label="Тема оформления"
				selectedKey={theme}
				onSelectionChange={(key) => {
					const next = key as Theme;
					setTheme(next);
					try {
						localStorage.setItem(storageKey, next);
						setWarning("");
					} catch {
						setWarning("Тема изменена; браузер не разрешил сохранить выбор.");
					}
				}}
				className="demo-theme"
			>
				<Label>Тема</Label>
				<Button>
					<SelectValue />
					<span aria-hidden="true">⌄</span>
				</Button>
				<Popover>
					<ListBox>
						<ListBoxItem id="system">Как в системе</ListBoxItem>
						<ListBoxItem id="light">Светлая</ListBoxItem>
						<ListBoxItem id="dark">Тёмная</ListBoxItem>
					</ListBox>
				</Popover>
			</Select>
			{warning && <span role="status">{warning}</span>}
		</>
	);
}
