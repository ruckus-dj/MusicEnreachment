import { fireEvent, render, screen, within } from "@testing-library/react";
import { useState } from "react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { MatchingScreen } from "./MatchingScreen";
import {
	createIncomingState,
	incomingFixtures,
	type IncomingReleases,
} from "./incomingModel";
import type { CatalogArtist } from "./catalogData";
import { I18nProvider, useI18n } from "./i18n";

const originalScrollIntoView = Object.getOwnPropertyDescriptor(
	HTMLElement.prototype,
	"scrollIntoView",
);
let previousLanguagePreference: string | null = null;

function MatchingHarness({
	artists,
	providers,
}: {
	artists?: CatalogArtist[];
	providers?: IncomingReleases;
} = {}) {
	const [state, setState] = useState(createIncomingState);
	return (
		<>
			<button
				type="button"
				onClick={() =>
					setState((current) => ({
						...current,
						inventoryVersion: current.inventoryVersion + 1,
					}))
				}
			>
				Change inventory
			</button>
			<MatchingScreen
				state={state}
				onState={setState}
				artists={artists}
				providers={providers}
			/>
		</>
	);
}

function LocaleSwitch() {
	const { setPreference } = useI18n();
	return (
		<button type="button" onClick={() => setPreference("en")}>
			Switch locale
		</button>
	);
}

afterEach(() => {
	if (previousLanguagePreference === null)
		window.localStorage.removeItem("music-design-v2-language");
	else
		window.localStorage.setItem(
			"music-design-v2-language",
			previousLanguagePreference,
		);
	if (originalScrollIntoView)
		Object.defineProperty(
			HTMLElement.prototype,
			"scrollIntoView",
			originalScrollIntoView,
		);
	else delete (HTMLElement.prototype as Partial<HTMLElement>).scrollIntoView;
	vi.restoreAllMocks();
	vi.unstubAllGlobals();
});

beforeEach(() => {
	previousLanguagePreference = window.localStorage.getItem(
		"music-design-v2-language",
	);
	window.localStorage.setItem("music-design-v2-language", "ru");
});

it("uses the compact publication inspector and scrolls its heading into focus on each open", () => {
	const scrollIntoView = vi.fn();
	Object.defineProperty(HTMLElement.prototype, "scrollIntoView", {
		configurable: true,
		value: scrollIntoView,
	});
	vi.stubGlobal(
		"matchMedia",
		vi.fn().mockReturnValue({ matches: true, addEventListener: vi.fn() }),
	);

	render(<MatchingHarness />);
	const openPreview = () =>
		fireEvent.click(
			screen.getByRole("button", { name: "Предпросмотр публикации" }),
		);
	openPreview();

	const heading = screen.getByRole("heading", { name: /Предпросмотр:/ });
	expect(heading).toHaveFocus();
	expect(scrollIntoView).toHaveBeenLastCalledWith({
		behavior: "auto",
		block: "start",
	});
	expect(
		screen.getByRole("region", { name: "Сравнить метаданные" }),
	).toBeInTheDocument();
	expect(
		screen.getAllByRole("checkbox", { name: /Включить/ }).length,
	).toBeGreaterThan(0);
	expect(
		screen.getByText(/Выбрано позиций для публикации:/),
	).toBeInTheDocument();
	expect(
		screen.queryByText(/^\d+ Выбрано позиций для публикации:/),
	).not.toBeInTheDocument();

	fireEvent.click(screen.getByRole("button", { name: "Вернуться к объекту" }));
	openPreview();
	expect(screen.getByRole("heading", { name: /Предпросмотр:/ })).toHaveFocus();
	expect(scrollIntoView).toHaveBeenCalledTimes(2);
});

it("does not move focus back to the preview after a locale change", () => {
	const scrollIntoView = vi.fn();
	Object.defineProperty(HTMLElement.prototype, "scrollIntoView", {
		configurable: true,
		value: scrollIntoView,
	});
	vi.stubGlobal(
		"matchMedia",
		vi.fn().mockReturnValue({ matches: false, addEventListener: vi.fn() }),
	);

	render(
		<I18nProvider>
			<LocaleSwitch />
			<MatchingHarness />
		</I18nProvider>,
	);
	fireEvent.click(
		screen.getByRole("button", { name: "Предпросмотр публикации" }),
	);
	const heading = screen.getByRole("heading", { name: /Предпросмотр:/ });
	const switchLocale = screen.getByRole("button", { name: "Switch locale" });
	switchLocale.focus();
	fireEvent.click(switchLocale);

	expect(switchLocale).toHaveFocus();
	expect(heading).not.toHaveFocus();
	expect(scrollIntoView).toHaveBeenCalledTimes(1);
});

it("keeps stale plans blocked and retains the final confirmation step", () => {
	render(<MatchingHarness />);
	const openPreview = () =>
		fireEvent.click(
			screen.getByRole("button", { name: "Предпросмотр публикации" }),
		);
	openPreview();

	const previewConfirm = screen.getByRole("button", {
		name: "Подтвердить публикацию",
	});
	expect(previewConfirm).toBeEnabled();
	fireEvent.click(previewConfirm);
	const dialog = screen.getByRole("dialog");
	fireEvent.click(screen.getByRole("button", { name: "Отмена" }));
	expect(dialog).not.toBeInTheDocument();

	fireEvent.click(screen.getByRole("button", { name: "Change inventory" }));
	expect(screen.getByRole("alert")).toHaveTextContent("Предпросмотр устарел");
	expect(
		screen.getByRole("button", { name: "Подтвердить публикацию" }),
	).toBeDisabled();

	fireEvent.click(screen.getByRole("button", { name: "Вернуться к объекту" }));
	openPreview();
	fireEvent.click(
		screen.getByRole("button", { name: "Подтвердить публикацию" }),
	);
	const finalDialog = screen.getByRole("dialog");
	fireEvent.click(
		within(finalDialog).getByRole("button", { name: "Подтвердить" }),
	);
	expect(
		screen.queryByRole("heading", { name: /Предпросмотр:/ }),
	).not.toBeInTheDocument();
});

it("shows reactive, nonblocking localized sort-collision notices in the matching preview", () => {
	const providers = structuredClone(incomingFixtures.providerReleases);
	const release = providers.find((item) => item.id === "p-north");
	if (!release) throw new Error("Missing provider release fixture");
	release.credits = [
		{ artistId: "mira", credit: "Mira credit", joinPhrase: " & " },
		{ artistId: "arden", credit: "Arden credit", joinPhrase: "" },
	];
	release.tracks = release.tracks.map((track) => ({
		...track,
		recordingCredits: [
			{ artistId: "mira", creditName: "Mira credit", joinPhrase: " & " },
			{ artistId: "arden", creditName: "Arden credit", joinPhrase: "" },
		],
	}));
	const artists: CatalogArtist[] = [
		{ id: "mira", name: "Shared sort" },
		{ id: "arden", name: "Shared sort" },
	];

	render(
		<I18nProvider>
			<LocaleSwitch />
			<MatchingHarness artists={artists} providers={providers} />
		</I18nProvider>,
	);
	fireEvent.click(
		screen.getByRole("button", { name: "Предпросмотр публикации" }),
	);
	expect(
		screen.getByText(
			"Сортировочные теги исполнителей композиции пропущены: у разных артистов совпало имя сортировки.",
			{ exact: false },
		),
	).toBeInTheDocument();
	expect(
		screen.getByText(
			"Сортировочные теги исполнителей издания пропущены: у разных артистов совпало имя сортировки.",
			{ exact: false },
		),
	).toBeInTheDocument();
	const confirm = screen.getByRole("button", {
		name: "Подтвердить публикацию",
	});
	expect(confirm).toBeEnabled();

	fireEvent.click(screen.getByRole("button", { name: "Switch locale" }));
	expect(
		screen.getByText(
			"Recording artist sort tags are omitted because different artists resolve to the same sort name.",
			{ exact: false },
		),
	).toBeInTheDocument();
	expect(
		screen.getByText(
			"Release artist sort tags are omitted because different artists resolve to the same sort name.",
			{ exact: false },
		),
	).toBeInTheDocument();
	expect(confirm).toBeEnabled();
});
