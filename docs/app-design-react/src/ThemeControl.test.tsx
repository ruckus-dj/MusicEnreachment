import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { ThemeControl } from "./ThemeControl";

const storageKey = "music-design-v2-theme";
const storage = new Map<string, string>();
const localStorageStub: Storage = {
	get length() {
		return storage.size;
	},
	clear: () => storage.clear(),
	getItem: (key) => storage.get(key) ?? null,
	key: (index) => [...storage.keys()][index] ?? null,
	removeItem: (key) => storage.delete(key),
	setItem: (key, value) => storage.set(key, String(value)),
};

function setupMedia(matches = false) {
	const listeners = new Set<(event: MediaQueryListEvent) => void>();
	const media = {
		matches,
		media: "(prefers-color-scheme: dark)",
		addEventListener: (
			_type: string,
			listener: (event: MediaQueryListEvent) => void,
		) => listeners.add(listener),
		removeEventListener: (
			_type: string,
			listener: (event: MediaQueryListEvent) => void,
		) => listeners.delete(listener),
	};
	Object.defineProperty(window, "matchMedia", {
		configurable: true,
		value: vi.fn(() => media),
	});
	return {
		set(matches: boolean) {
			media.matches = matches;
			for (const listener of listeners)
				listener({ matches } as MediaQueryListEvent);
		},
	};
}

beforeEach(() => {
	storage.clear();
	vi.stubGlobal("localStorage", localStorageStub);
	delete document.documentElement.dataset.theme;
});

afterEach(() => {
	vi.restoreAllMocks();
	vi.unstubAllGlobals();
	window.location.hash = "";
});

it("cycles system → light → dark → system and persists each selection", () => {
	setupMedia(true);
	render(<ThemeControl />);
	const control = screen.getByRole("button");
	expect(control).toHaveAccessibleName(
		"Тема: как в системе. Следующая: светлая",
	);
	expect(document.documentElement).toHaveAttribute("data-theme", "dark");

	fireEvent.click(control);
	expect(control).toHaveAccessibleName("Тема: светлая. Следующая: тёмная");
	expect(document.documentElement).toHaveAttribute("data-theme", "light");
	expect(localStorage.getItem(storageKey)).toBe("light");

	fireEvent.click(control);
	expect(control).toHaveAccessibleName(
		"Тема: тёмная. Следующая: как в системе",
	);
	expect(document.documentElement).toHaveAttribute("data-theme", "dark");
	expect(localStorage.getItem(storageKey)).toBe("dark");

	fireEvent.click(control);
	expect(control).toHaveAccessibleName(
		"Тема: как в системе. Следующая: светлая",
	);
	expect(document.documentElement).toHaveAttribute("data-theme", "dark");
	expect(localStorage.getItem(storageKey)).toBe("system");
});

it.each([
	"light",
	"dark",
] as const)("restores a valid saved %s preference", (theme) => {
	localStorage.setItem(storageKey, theme);
	setupMedia(false);
	render(<ThemeControl />);
	expect(screen.getByRole("button")).toHaveAccessibleName(
		theme === "light"
			? "Тема: светлая. Следующая: тёмная"
			: "Тема: тёмная. Следующая: как в системе",
	);
	expect(document.documentElement).toHaveAttribute("data-theme", theme);
});

it("ignores an invalid stored value and reacts to system changes only in system mode", () => {
	localStorage.setItem(storageKey, "sepia");
	const media = setupMedia(false);
	render(<ThemeControl />);
	const control = screen.getByRole("button");
	expect(document.documentElement).toHaveAttribute("data-theme", "light");

	media.set(true);
	expect(document.documentElement).toHaveAttribute("data-theme", "dark");
	fireEvent.click(control);
	media.set(false);
	expect(document.documentElement).toHaveAttribute("data-theme", "light");
	fireEvent.click(control);
	media.set(true);
	expect(document.documentElement).toHaveAttribute("data-theme", "dark");
});

it("keeps the changed theme and announces denied storage writes", () => {
	setupMedia(false);
	vi.spyOn(localStorageStub, "setItem").mockImplementation(() => {
		throw new DOMException("blocked", "QuotaExceededError");
	});
	render(<ThemeControl />);
	fireEvent.click(screen.getByRole("button"));
	expect(document.documentElement).toHaveAttribute("data-theme", "light");
	expect(screen.getByRole("status")).toHaveTextContent(
		"Тема изменена; браузер не разрешил сохранить выбор.",
	);
});

it("falls back to system when reading storage is denied", () => {
	setupMedia(false);
	vi.spyOn(localStorageStub, "getItem").mockImplementation(() => {
		throw new DOMException("blocked", "SecurityError");
	});
	render(<ThemeControl />);
	expect(screen.getByRole("button")).toHaveAccessibleName(
		"Тема: как в системе. Следующая: светлая",
	);
	expect(document.documentElement).toHaveAttribute("data-theme", "light");
});

it("cycles with keyboard activation", () => {
	setupMedia(false);
	render(<ThemeControl />);
	const control = screen.getByRole("button");
	control.focus();
	fireEvent.keyDown(control, { key: "Enter" });
	fireEvent.keyUp(control, { key: "Enter" });
	expect(document.documentElement).toHaveAttribute("data-theme", "light");
	fireEvent.keyDown(control, { key: " " });
	fireEvent.keyUp(control, { key: " " });
	expect(document.documentElement).toHaveAttribute("data-theme", "dark");
});
