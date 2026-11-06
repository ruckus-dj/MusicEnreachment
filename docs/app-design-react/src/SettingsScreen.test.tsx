import {
	fireEvent,
	render,
	screen,
	waitFor,
	within,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "./i18n/I18nProvider";
import { initialDemoSettings, SettingsScreen } from "./SettingsScreen";
import { readSettingsDraft, resetSettingsDraftMemory } from "./settingsDraft";

beforeEach(() => {
	localStorage.clear();
	window.sessionStorage.clear();
	resetSettingsDraftMemory();
	window.location.hash = "";
});
afterEach(() => vi.restoreAllMocks());

it("consumes replacement approval before saving a different conflicting field group", async () => {
	localStorage.setItem("music-design-v2-language", "en");
	let current: typeof initialDemoSettings = {
		...initialDemoSettings,
		concurrency: "4",
		musicBrainzMode: "self-hosted",
		musicBrainzURL: "https://old.example",
		musicBrainzDelay: "1",
	};
	let view: ReturnType<typeof render>;
	const onSettings = vi.fn((next: typeof initialDemoSettings) => {
		current = next;
		view.rerender(element());
	});
	const element = () => (
		<I18nProvider>
			<SettingsScreen settings={current} onSettings={onSettings} />
		</I18nProvider>
	);
	view = render(element());
	fireEvent.change(screen.getByLabelText(/file processing concurrency/i), {
		target: { value: "9" },
	});
	fireEvent.click(screen.getByRole("tab", { name: "Providers" }));
	const url = document.getElementById("settings-musicBrainzURL");
	if (!url) throw new Error("Missing provider URL field");
	fireEvent.change(url, { target: { value: "https://draft.example" } });
	fireEvent.click(screen.getByRole("tab", { name: "General" }));
	current = {
		...current,
		concurrency: "8",
		musicBrainzURL: "https://remote.example",
	};
	view.rerender(element());
	fireEvent.click(screen.getByRole("button", { name: "Continue editing" }));
	fireEvent.click(screen.getByRole("button", { name: "Save settings" }));
	await screen.findByRole("dialog");
	fireEvent.click(screen.getByRole("button", { name: "Replace saved values" }));
	await waitFor(() =>
		expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
	);
	expect(onSettings).toHaveBeenCalledTimes(1);
	expect(current).toEqual(
		expect.objectContaining({
			concurrency: "9",
			musicBrainzURL: "https://remote.example",
		}),
	);
	fireEvent.click(screen.getByRole("tab", { name: "Providers" }));
	const saveProviders = screen.getByRole("button", {
		name: "Save provider settings",
	});
	expect(saveProviders).toBeDisabled();
	fireEvent.click(screen.getByRole("button", { name: "Continue editing" }));
	fireEvent.click(saveProviders);
	await screen.findByRole("dialog");
	expect(onSettings).toHaveBeenCalledTimes(1);
	fireEvent.click(screen.getByRole("button", { name: "Replace saved values" }));
	expect(onSettings).toHaveBeenCalledTimes(2);
	expect(current).toEqual(
		expect.objectContaining({
			concurrency: "9",
			musicBrainzURL: "https://draft.example",
		}),
	);
});

describe.each(["ru", "en"] as const)("SettingsScreen (%s)", (locale) => {
	it("keeps cleanup off by default and autosaves its changed value across tabs", () => {
		localStorage.setItem("music-design-v2-language", locale);
		let current = { ...initialDemoSettings };
		const onSettings = vi.fn((next) => {
			current = next;
			renderScreen.rerender(
				<I18nProvider>
					<SettingsScreen settings={current} onSettings={onSettings} />
				</I18nProvider>,
			);
		});
		const renderScreen = render(
			<I18nProvider>
				<SettingsScreen settings={current} onSettings={onSettings} />
			</I18nProvider>,
		);
		const section = screen.getByRole("region", {
			name: /source deletion|удаление исходников/i,
		});
		const cleanup = within(section).getByRole("checkbox");
		expect(cleanup).not.toBeChecked();
		expect(
			within(section).getByText(/filesystem permission|разрешени/i),
		).toBeInTheDocument();
		fireEvent.click(cleanup);
		expect(cleanup).toBeChecked();
		fireEvent.click(screen.getByRole("tab", { name: /folders|папки/i }));
		fireEvent.click(screen.getByRole("tab", { name: /general|общие/i }));
		expect(
			within(
				screen.getByRole("region", {
					name: /source deletion|удаление исходников/i,
				}),
			).getByRole("checkbox"),
		).toBeChecked();
		expect(onSettings).toHaveBeenCalledWith(
			expect.objectContaining({
				sourceCleanupEnabled: true,
			}),
		);
	});

	it("keeps manual text edits out of effective settings until explicit save", () => {
		localStorage.setItem("music-design-v2-language", locale);
		const onSettings = vi.fn();
		render(
			<I18nProvider>
				<SettingsScreen
					settings={initialDemoSettings}
					onSettings={onSettings}
				/>
			</I18nProvider>,
		);
		const input = screen.getByLabelText(
			/file processing concurrency|параллельность обработки файлов/i,
		);
		fireEvent.change(input, { target: { value: "7" } });
		expect(onSettings).not.toHaveBeenCalled();
		fireEvent.click(
			screen.getByRole("button", {
				name: /save settings|сохранить настройки/i,
			}),
		);
		expect(onSettings).toHaveBeenCalledWith(
			expect.objectContaining({ concurrency: "7" }),
		);
	});

	it("rejects invalid concurrency, exposes the error and focuses the field", () => {
		localStorage.setItem("music-design-v2-language", locale);
		const onSettings = vi.fn();
		render(
			<I18nProvider>
				<SettingsScreen
					settings={initialDemoSettings}
					onSettings={onSettings}
				/>
			</I18nProvider>,
		);
		const input = screen.getByLabelText(
			/file processing concurrency|параллельность обработки файлов/i,
		);
		fireEvent.change(input, { target: { value: "0" } });
		fireEvent.click(
			screen.getByRole("button", {
				name: /save settings|сохранить настройки/i,
			}),
		);
		expect(input).toHaveFocus();
		expect(screen.getByRole("alert")).toHaveTextContent(
			/whole number|целое число/i,
		);
		expect(onSettings).not.toHaveBeenCalled();
	});

	it("guards browser hash/back navigation and keeps an invalid draft when leaving", async () => {
		localStorage.setItem("music-design-v2-language", locale);
		window.location.hash = "#settings";
		const view = render(
			<I18nProvider>
				<SettingsScreen settings={initialDemoSettings} onSettings={vi.fn()} />
			</I18nProvider>,
		);
		const input = screen.getByLabelText(
			/file processing concurrency|параллельность обработки файлов/i,
		);
		fireEvent.change(input, { target: { value: "" } });
		window.history.pushState(null, "", "#library");
		fireEvent(window, new PopStateEvent("popstate"));
		await screen.findByRole("dialog");
		expect(window.location.hash).toBe("#settings");
		fireEvent.click(
			screen.getByRole("button", {
				name: /leave and keep draft|выйти, сохранив черновик/i,
			}),
		);
		await waitFor(() => expect(window.location.hash).toBe("#library"));
		expect(readSettingsDraft(window.sessionStorage)?.value.concurrency).toBe(
			"",
		);
		view.unmount();
	});

	it("retains a manual tools-directory candidate without applying it", () => {
		localStorage.setItem("music-design-v2-language", locale);
		const onSettings = vi.fn();
		render(
			<I18nProvider>
				<SettingsScreen
					settings={initialDemoSettings}
					onSettings={onSettings}
				/>
			</I18nProvider>,
		);
		fireEvent.click(screen.getByRole("tab", { name: /tools|инструменты/i }));
		fireEvent.change(
			screen.getByLabelText(/new tools directory|новый каталог инструментов/i),
			{
				target: { value: "/var/lib/melotrove/tools-next" },
			},
		);
		expect(onSettings).not.toHaveBeenCalled();
		expect(
			readSettingsDraft(window.sessionStorage)?.value.toolsPathCandidate,
		).toBe("/var/lib/melotrove/tools-next");
	});

	it("shows a field-level MusicBrainz error and focuses the invalid URL", async () => {
		localStorage.setItem("music-design-v2-language", locale);
		const onSettings = vi.fn();
		render(
			<I18nProvider>
				<SettingsScreen
					settings={initialDemoSettings}
					onSettings={onSettings}
				/>
			</I18nProvider>,
		);
		fireEvent.click(screen.getByRole("tab", { name: /providers|провайдеры/i }));
		fireEvent.click(screen.getByRole("button", { name: /MusicBrainz/i }));
		fireEvent.click(
			await screen.findByRole("option", {
				name: /self-hosted|собственный сервер/i,
			}),
		);
		const input = screen.getByLabelText(
			/self-hosted server base URL|базовый URL собственного сервера/i,
		);
		fireEvent.change(input, { target: { value: "not-a-url" } });
		fireEvent.click(
			screen.getByRole("button", {
				name: /save provider settings|сохранить настройки провайдера/i,
			}),
		);
		expect(input).toHaveFocus();
		expect(
			screen.getByText(/valid HTTP\(S\) URL|корректный URL HTTP\(S\)/i),
		).toBeInTheDocument();
		expect(onSettings).not.toHaveBeenCalled();
	});

	it("does not submit unsaved MusicBrainz URL or delay when a mode selector autosaves", async () => {
		localStorage.setItem("music-design-v2-language", locale);
		const onSettings = vi.fn();
		render(
			<I18nProvider>
				<SettingsScreen
					settings={initialDemoSettings}
					onSettings={onSettings}
				/>
			</I18nProvider>,
		);
		fireEvent.click(screen.getByRole("tab", { name: /providers|провайдеры/i }));
		const chooseMode = async (name: RegExp) => {
			fireEvent.click(screen.getByRole("button", { name: /MusicBrainz/i }));
			fireEvent.click(await screen.findByRole("option", { name }));
		};
		await chooseMode(/self-hosted|собственный сервер/i);
		fireEvent.change(
			screen.getByLabelText(
				/self-hosted server base URL|базовый URL собственного сервера/i,
			),
			{ target: { value: "https://mb.example.org" } },
		);
		fireEvent.change(
			screen.getByLabelText(/request delay|задержка запросов/i),
			{
				target: { value: "3" },
			},
		);
		await chooseMode(/public service|публичный сервис/i);
		expect(onSettings).toHaveBeenLastCalledWith(
			expect.objectContaining({
				musicBrainzMode: "public",
				musicBrainzURL: "",
				musicBrainzDelay: "",
			}),
		);
		expect(readSettingsDraft(window.sessionStorage)?.value).toEqual(
			expect.objectContaining({
				musicBrainzURL: "https://mb.example.org",
				musicBrainzDelay: "3",
			}),
		);
	});

	it("rebases untouched provider props before saving while concurrency is dirty", () => {
		localStorage.setItem("music-design-v2-language", locale);
		const onSettings = vi.fn();
		const view = render(
			<I18nProvider>
				<SettingsScreen
					settings={initialDemoSettings}
					onSettings={onSettings}
				/>
			</I18nProvider>,
		);
		fireEvent.change(
			screen.getByLabelText(
				/file processing concurrency|параллельность обработки файлов/i,
			),
			{ target: { value: "9" } },
		);
		const current = {
			...initialDemoSettings,
			musicBrainzMode: "self-hosted" as const,
			musicBrainzURL: "https://saved.example.org",
			musicBrainzDelay: "2",
			musicBrainzThrottle: true,
		};
		view.rerender(
			<I18nProvider>
				<SettingsScreen settings={current} onSettings={onSettings} />
			</I18nProvider>,
		);
		fireEvent.click(screen.getByRole("tab", { name: /providers|провайдеры/i }));
		fireEvent.click(
			screen.getByRole("button", {
				name: /save provider settings|сохранить настройки провайдера/i,
			}),
		);
		expect(onSettings).toHaveBeenLastCalledWith(
			expect.objectContaining({
				concurrency: "4",
				musicBrainzMode: "self-hosted",
				musicBrainzURL: "https://saved.example.org",
				musicBrainzDelay: "2",
				musicBrainzThrottle: true,
			}),
		);
	});

	it("saves a concurrency edit on leave without overwriting newer provider values", async () => {
		localStorage.setItem("music-design-v2-language", locale);
		window.location.hash = "#settings";
		const onSettings = vi.fn();
		const view = render(
			<I18nProvider>
				<SettingsScreen
					settings={initialDemoSettings}
					onSettings={onSettings}
				/>
			</I18nProvider>,
		);
		fireEvent.change(
			screen.getByLabelText(
				/file processing concurrency|параллельность обработки файлов/i,
			),
			{ target: { value: "9" } },
		);
		const latest = {
			...initialDemoSettings,
			musicBrainzMode: "self-hosted" as const,
			musicBrainzURL: "https://latest.example.org",
			musicBrainzDelay: "6",
			musicBrainzThrottle: true,
		};
		view.rerender(
			<I18nProvider>
				<SettingsScreen settings={latest} onSettings={onSettings} />
			</I18nProvider>,
		);
		window.location.hash = "#library";
		await screen.findByRole("dialog");
		fireEvent.click(
			screen.getByRole("button", {
				name: /save valid changes and leave|сохранить корректные изменения и выйти/i,
			}),
		);
		expect(onSettings).toHaveBeenLastCalledWith(
			expect.objectContaining({
				concurrency: "9",
				musicBrainzMode: "self-hosted",
				musicBrainzURL: "https://latest.example.org",
				musicBrainzDelay: "6",
				musicBrainzThrottle: true,
			}),
		);
	});

	it("rebases toolsPath before a clean manual candidate starts", () => {
		localStorage.setItem("music-design-v2-language", locale);
		const view = render(
			<I18nProvider>
				<SettingsScreen settings={initialDemoSettings} onSettings={vi.fn()} />
			</I18nProvider>,
		);
		const current = { ...initialDemoSettings, toolsPath: "/var/lib/new-tools" };
		view.rerender(
			<I18nProvider>
				<SettingsScreen settings={current} onSettings={vi.fn()} />
			</I18nProvider>,
		);
		fireEvent.click(screen.getByRole("tab", { name: /tools|инструменты/i }));
		fireEvent.change(
			screen.getByLabelText(/new tools directory|новый каталог инструментов/i),
			{ target: { value: "/var/lib/candidate" } },
		);
		expect(screen.queryByRole("alert")).not.toBeInTheDocument();
	});

	it("keeps an invalid draft across route unmount when session storage fails", async () => {
		localStorage.setItem("music-design-v2-language", locale);
		window.location.hash = "#settings";
		const baseline = {
			concurrency: "4",
			toolsPath: initialDemoSettings.toolsPath,
			toolsPathCandidate: "",
			musicBrainzMode: "" as const,
			musicBrainzURL: "",
			musicBrainzDelay: "",
		};
		const stored = JSON.stringify({
			baseline,
			value: { ...baseline, concurrency: "5" },
		});
		const failingStorage = {
			getItem: () => stored,
			setItem: () => {
				throw new DOMException("full", "QuotaExceededError");
			},
			removeItem: () => {
				throw new DOMException("denied", "SecurityError");
			},
		} as unknown as Storage;
		vi.spyOn(window, "sessionStorage", "get").mockReturnValue(failingStorage);
		const props = { settings: initialDemoSettings, onSettings: vi.fn() };
		const first = render(
			<I18nProvider>
				<SettingsScreen {...props} />
			</I18nProvider>,
		);
		const input = screen.getByLabelText(
			/file processing concurrency|параллельность обработки файлов/i,
		);
		fireEvent.change(input, { target: { value: "0" } });
		await screen.findByText(
			/browser storage is unavailable|хранилище браузера недоступно/i,
		);
		window.location.hash = "#library";
		await screen.findByRole("dialog");
		fireEvent.click(
			screen.getByRole("button", {
				name: /leave and keep draft|выйти, сохранив черновик/i,
			}),
		);
		first.unmount();
		window.history.replaceState(window.history.state, "", "#settings");
		const second = render(
			<I18nProvider>
				<SettingsScreen {...props} />
			</I18nProvider>,
		);
		expect(
			screen.getByLabelText(
				/file processing concurrency|параллельность обработки файлов/i,
			),
		).toHaveValue(0);
		expect(
			screen.getByText(
				/browser storage is unavailable|хранилище браузера недоступно/i,
			),
		).toBeInTheDocument();
		second.unmount();
	});
});
