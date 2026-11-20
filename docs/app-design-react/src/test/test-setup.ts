import "@testing-library/jest-dom/vitest";
import { cleanup } from "@testing-library/react";
import { afterEach, beforeEach } from "vitest";

// Node exposes its own optional localStorage. Give browser tests an isolated
// Storage implementation instead of depending on Node's --localstorage-file.
beforeEach(() => {
	const values = new Map<string, string>();
	const storage: Storage = {
		get length() {
			return values.size;
		},
		clear: () => values.clear(),
		getItem: (key) => values.get(String(key)) ?? null,
		key: (index) => [...values.keys()][index] ?? null,
		removeItem: (key) => {
			values.delete(String(key));
		},
		setItem: (key, value) => {
			values.set(String(key), String(value));
		},
	};
	Object.defineProperty(window, "localStorage", {
		configurable: true,
		value: storage,
	});
	Object.defineProperty(globalThis, "localStorage", {
		configurable: true,
		value: storage,
	});
});

afterEach(cleanup);

// JSDOM does not implement the native dialog lifecycle used by both frontends.
if (!HTMLDialogElement.prototype.showModal) {
	HTMLDialogElement.prototype.showModal = function () {
		this.setAttribute("open", "");
	};
	HTMLDialogElement.prototype.close = function () {
		this.removeAttribute("open");
	};
}
