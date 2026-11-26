import {
	act,
	fireEvent,
	render,
	screen,
	waitFor,
	within,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "../../../app/i18n/host";
import { ApplicationRouteOutlet } from "../../../ui/app/ApplicationRouteOutlet";
import {
	DemoSettingsBinder,
	initialDemoSettings,
	type SettingsScreenProps,
} from "./SettingsScreen";
import {
	readSettingsDraft,
	resetSettingsDraftMemory,
	settingsDraftStorageKey,
} from "./settingsDraft";
import { demoToolCatalogRefreshDelayMs } from "./useToolCatalogDemoProvider";

beforeEach(() => {
	localStorage.clear();
	window.sessionStorage.clear();
	resetSettingsDraftMemory();
	window.location.hash = "";
});
afterEach(() => vi.restoreAllMocks());

function SettingsScreen(props: SettingsScreenProps) {
	return (
		<ApplicationRouteOutlet
			route="settings"
			provider={DemoSettingsBinder}
			providerProps={props}
		/>
	);
}

it("keeps a saved provider target unverified until its check succeeds", async () => {
	localStorage.setItem("music-design-v2-language", "en");
	let current: typeof initialDemoSettings = {
		...initialDemoSettings,
		musicBrainzMode: "self-hosted",
		musicBrainzURL: "https://old.example",
		musicBrainzDelay: "1",
	};
	let finishCheck: ((result: { ok: boolean }) => void) | undefined;
	const checkMusicBrainz = vi.fn(
		() =>
			new Promise<{ ok: boolean }>((resolve) => {
				finishCheck = resolve;
			}),
	);
	const element = () => (
		<I18nProvider>
			<SettingsScreen
				settings={current}
				onSettings={(next) => {
					current = next;
					view.rerender(element());
				}}
				checkMusicBrainz={checkMusicBrainz}
			/>
		</I18nProvider>
	);
	const view = render(element());
	fireEvent.click(screen.getByRole("tab", { name: "Providers" }));
	const url = document.getElementById("settings-musicBrainzURL");
	if (!url) throw new Error("Missing provider URL field");
	fireEvent.change(url, { target: { value: "https://provider.example" } });
	const check = screen.getByRole("button", {
		name: "Save and check MusicBrainz",
	});
	fireEvent.click(check);
	await waitFor(() => expect(checkMusicBrainz).toHaveBeenCalledTimes(1));
	expect(check).toBeDisabled();
	expect(current).toMatchObject({
		musicBrainzURL: "https://provider.example",
		musicBrainzVerified: false,
	});

	await act(async () => finishCheck?.({ ok: false }));
	const alert = screen.getByRole("alert");
	expect(alert).toHaveTextContent(/MusicBrainz/i);
	expect(alert).toHaveFocus();
	expect(current.musicBrainzVerified).toBe(false);

	fireEvent.click(check);
	await waitFor(() => expect(checkMusicBrainz).toHaveBeenCalledTimes(2));
	current = { ...current, concurrency: "7" };
	view.rerender(element());
	await act(async () => finishCheck?.({ ok: true }));
	expect(current).toMatchObject({
		musicBrainzURL: "https://provider.example",
		musicBrainzVerified: true,
		concurrency: "7",
	});
	expect(screen.queryByRole("alert")).toBeNull();
});

it("keeps conflict actions scoped to one setting and rechecks replacement", async () => {
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
	fireEvent.change(screen.getByLabelText(/concurrent files/i), {
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
	fireEvent.click(
		screen.getByRole("button", {
			name: "Continue editing: analysis concurrency",
		}),
	);
	expect(
		screen.queryByRole("status", {
			name: "Settings conflict: analysis concurrency",
		}),
	).toBeNull();
	expect(
		screen.getByRole("status", {
			name: "Settings conflict: MusicBrainz settings",
		}),
	).toBeInTheDocument();
	fireEvent.click(screen.getByRole("tab", { name: "Providers" }));
	const openProviderReplacement = async () => {
		fireEvent.click(
			screen.getByRole("button", { name: "Save and check MusicBrainz" }),
		);
		return screen.findByRole("dialog", { name: "Replace saved value?" });
	};
	const firstDialog = await openProviderReplacement();
	expect(onSettings).not.toHaveBeenCalled();
	current = {
		...current,
		musicBrainzURL: "https://newer.example",
		concurrency: "7",
	};
	view.rerender(element());
	fireEvent.click(
		within(firstDialog).getByRole("button", { name: "Replace saved value" }),
	);
	expect(onSettings).not.toHaveBeenCalled();
	expect(screen.queryByRole("dialog")).toBeNull();

	fireEvent.click(
		screen.getByRole("button", {
			name: "Use saved value: analysis concurrency",
		}),
	);
	const stored = window.sessionStorage.getItem(settingsDraftStorageKey);
	expect(JSON.parse(stored ?? "{}")).toMatchObject({
		value: { concurrency: "7", musicBrainzURL: "https://draft.example" },
		baseline: { concurrency: "7", musicBrainzURL: "https://old.example" },
	});
	expect(onSettings).not.toHaveBeenCalled();

	const draftDialog = await openProviderReplacement();
	const updatedURL = document.getElementById("settings-musicBrainzURL");
	if (!updatedURL) throw new Error("Missing provider URL field");
	fireEvent.change(updatedURL, {
		target: { value: "https://draft-v2.example" },
	});
	fireEvent.click(
		within(draftDialog).getByRole("button", { name: "Replace saved value" }),
	);
	expect(onSettings).not.toHaveBeenCalled();

	const nextDialog = await openProviderReplacement();
	current = { ...current, concurrency: "6" };
	view.rerender(element());
	fireEvent.click(
		within(nextDialog).getByRole("button", { name: "Replace saved value" }),
	);
	expect(onSettings).toHaveBeenCalledTimes(1);
	expect(current).toEqual(
		expect.objectContaining({
			concurrency: "6",
			musicBrainzURL: "https://draft-v2.example",
		}),
	);
});

it("keeps the GitHub token draft across Settings tabs in memory", () => {
	localStorage.setItem("music-design-v2-language", "en");
	const onSettings = vi.fn();
	render(
		<I18nProvider>
			<SettingsScreen settings={initialDemoSettings} onSettings={onSettings} />
		</I18nProvider>,
	);
	fireEvent.click(screen.getByRole("tab", { name: "Tools" }));
	fireEvent.change(screen.getByLabelText("Personal access token"), {
		target: { value: "demo draft, not a token" },
	});
	fireEvent.click(screen.getByRole("tab", { name: "General" }));
	fireEvent.click(screen.getByRole("tab", { name: "Tools" }));
	expect(screen.getByLabelText("Personal access token")).toHaveValue(
		"demo draft, not a token",
	);
	expect(onSettings).not.toHaveBeenCalled();
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
			within(section).getByText(/off by default|выключено по умолчанию/i),
		).toBeInTheDocument();
		fireEvent.click(cleanup);
		expect(cleanup).toBeChecked();
		fireEvent.click(screen.getByRole("tab", { name: /folders|папки/i }));
		expect(window.location.hash).toBe("#/settings?tab=folders");
		fireEvent.click(screen.getByRole("tab", { name: /general|общие/i }));
		expect(window.location.hash).toBe("#/settings");
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
			/concurrent files|одновременные файлы/i,
		);
		fireEvent.change(input, { target: { value: "7" } });
		expect(onSettings).not.toHaveBeenCalled();
		fireEvent.click(
			screen.getByRole("button", {
				name: /save concurrency|сохранить параллельность/i,
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
			/concurrent files|одновременные файлы/i,
		);
		fireEvent.change(input, { target: { value: "0" } });
		fireEvent.click(
			screen.getByRole("button", {
				name: /save concurrency|сохранить параллельность/i,
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
		window.location.hash = "#/settings";
		const view = render(
			<I18nProvider>
				<SettingsScreen settings={initialDemoSettings} onSettings={vi.fn()} />
			</I18nProvider>,
		);
		const input = screen.getByLabelText(
			/concurrent files|одновременные файлы/i,
		);
		fireEvent.change(input, { target: { value: "" } });
		window.history.pushState(
			null,
			"",
			"#/library?view=artists&publication=all&availability=all",
		);
		fireEvent(window, new PopStateEvent("popstate"));
		await screen.findByRole("dialog");
		expect(window.location.hash).toBe("#/settings");
		fireEvent.click(
			screen.getByRole("button", {
				name: /leave and keep draft|уйти, сохранив черновик/i,
			}),
		);
		await waitFor(() =>
			expect(window.location.hash).toBe(
				"#/library?view=artists&publication=all&availability=all",
			),
		);
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

	it("records a manual tool catalog check without changing demo installations", () => {
		localStorage.setItem("music-design-v2-language", locale);
		vi.useFakeTimers();
		try {
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
			const summary = screen.getByRole("region", {
				name: /managed tools|управляемые инструменты/i,
			});
			expect(
				within(summary).getByText(
					/catalog has not been checked yet|каталог ещё не проверялся/i,
				),
			).toBeInTheDocument();
			fireEvent.click(
				within(summary).getByRole("button", {
					name: /refresh catalog|обновить каталог/i,
				}),
			);
			expect(
				within(summary).getByRole("button", {
					name: /refreshing catalog|обновление каталога/i,
				}),
			).toBeDisabled();
			act(() => {
				vi.advanceTimersByTime(demoToolCatalogRefreshDelayMs);
			});
			expect(
				within(summary).getByText(
					/last successful catalog check|последняя успешная проверка каталога/i,
				),
			).toBeInTheDocument();
			expect(
				within(summary).queryByRole("button", {
					name: /refreshing catalog|обновление каталога/i,
				}),
			).not.toBeInTheDocument();
			expect(onSettings).not.toHaveBeenCalled();
		} finally {
			vi.useRealTimers();
		}
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
		fireEvent.change(
			screen.getByRole("combobox", {
				name: /MusicBrainz mode|режим MusicBrainz/i,
			}),
			{ target: { value: "self-hosted" } },
		);
		const input = screen.getByLabelText(
			/MusicBrainz base URL|базовый URL MusicBrainz/i,
		);
		fireEvent.change(input, { target: { value: "not-a-url" } });
		fireEvent.click(
			screen.getByRole("button", {
				name: /save and check MusicBrainz|сохранить и проверить MusicBrainz/i,
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
		const chooseMode = (value: "public" | "self-hosted") => {
			fireEvent.change(
				screen.getByRole("combobox", {
					name: /MusicBrainz mode|режим MusicBrainz/i,
				}),
				{ target: { value } },
			);
		};
		chooseMode("self-hosted");
		fireEvent.change(
			screen.getByLabelText(/MusicBrainz base URL|базовый URL MusicBrainz/i),
			{ target: { value: "https://mb.example.org" } },
		);
		fireEvent.change(
			screen.getByLabelText(/request interval|интервал запросов/i),
			{
				target: { value: "3" },
			},
		);
		chooseMode("public");
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
			screen.getByLabelText(/concurrent files|одновременные файлы/i),
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
				name: /save and check MusicBrainz|сохранить и проверить MusicBrainz/i,
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
		window.location.hash = "#/settings";
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
			screen.getByLabelText(/concurrent files|одновременные файлы/i),
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
		window.location.hash =
			"#/library?view=artists&publication=all&availability=all";
		await screen.findByRole("dialog");
		fireEvent.click(
			screen.getByRole("button", {
				name: /save and leave|сохранить и уйти/i,
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
		window.location.hash = "#/settings";
		const baseline = {
			concurrency: "4",
			matchingPairThreshold: "0.70",
			toolsPath: initialDemoSettings.toolsPath,
			toolsPathCandidate: "",
			publicationTemplate: initialDemoSettings.publicationTemplate ?? "",
			workDirectory: initialDemoSettings.workDirectory,
			outputDirectory: initialDemoSettings.currentOutput ?? "/srv/music",
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
			/concurrent files|одновременные файлы/i,
		);
		fireEvent.change(input, { target: { value: "0" } });
		await screen.findByText(
			/browser storage is unavailable|хранилище браузера недоступно/i,
		);
		window.location.hash =
			"#/library?view=artists&publication=all&availability=all";
		await screen.findByRole("dialog");
		fireEvent.click(
			screen.getByRole("button", {
				name: /leave and keep draft|уйти, сохранив черновик/i,
			}),
		);
		first.unmount();
		window.history.replaceState(window.history.state, "", "#/settings");
		const second = render(
			<I18nProvider>
				<SettingsScreen {...props} />
			</I18nProvider>,
		);
		expect(
			screen.getByLabelText(/concurrent files|одновременные файлы/i),
		).toHaveValue("0");
		expect(
			screen.getByText(
				/browser storage is unavailable|хранилище браузера недоступно/i,
			),
		).toBeInTheDocument();
		second.unmount();
	});
});
