import { fireEvent, render, screen, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { useState } from "react";
import { LocalProviderScreen } from "./LocalProviderScreen";
import { I18nProvider } from "./i18n/I18nProvider";
import { parseLocalDuration } from "./localProviderModel";
import {
	createLocalProviderState,
	localProviderFixtures,
} from "./localProviderModel";

it("accepts empty and valid local durations but rejects unsafe magnitudes", () => {
	expect(parseLocalDuration("")).toBeNull();
	expect(parseLocalDuration("2:00")).toBe(120_000);
	expect(parseLocalDuration("9007199254740992")).toBeNaN();
	expect(
		parseLocalDuration("999999999999999999999999999999999999999999"),
	).toBeNaN();
});

it("retains original provider identities and search never creates records", () => {
	const state = createLocalProviderState();
	render(<LocalProviderScreen />);
	expect(
		screen.getByRole("heading", { name: "Mira Vale" }),
	).toBeInTheDocument();
	expect(state.items.artists.map(({ id }) => id)).toEqual(
		localProviderFixtures.artists.map(({ id }) => id),
	);
	fireEvent.change(
		screen.getByRole("searchbox", { name: /Поиск в артистах/ }),
		{ target: { value: "not present" } },
	);
	expect(
		screen.getByText(
			"Записей не найдено. Поиск ничего не создаёт автоматически.",
		),
	).toBeInTheDocument();
	expect(state.items.artists).toHaveLength(4);
});

it("creates and edits only by explicit action, emitting controlled current state", () => {
	const state = createLocalProviderState();
	const onState = vi.fn();
	function Harness() {
		const [current, setCurrent] = useState(state);
		return (
			<LocalProviderScreen
				state={current}
				onState={(next) => {
					onState(next);
					setCurrent(next);
				}}
			/>
		);
	}
	render(<Harness />);
	fireEvent.click(screen.getByRole("button", { name: "Создать артиста" }));
	fireEvent.change(screen.getByRole("textbox", { name: /^Имя Обязательно$/ }), {
		target: { value: "New Local Artist" },
	});
	fireEvent.click(
		screen.getAllByRole("button", { name: "Создать артиста" })[1],
	);
	const created = onState.mock.calls.at(-1)?.[0];
	expect(created.items.artists[0]).toMatchObject({
		name: "New Local Artist",
		provider: "Local",
	});
	expect(created.items.artists[0].id).not.toBe("");
	expect(created.notice).toEqual({ key: "recordCreated" });
	expect(screen.getByRole("status")).toHaveTextContent(
		"Демо: запись создана в памяти страницы. API и серверная часть не вызываются.",
	);
});

it("localizes Local-provider UI to English without translating fixture values", () => {
	window.localStorage.setItem("music-design-v2-language", "en");
	render(
		<I18nProvider>
			<LocalProviderScreen />
		</I18nProvider>,
	);
	window.localStorage.removeItem("music-design-v2-language");
	expect(
		screen.getByRole("heading", { name: "Local catalog" }),
	).toBeInTheDocument();
	expect(screen.getByRole("tab", { name: /Artists/ })).toBeInTheDocument();
	expect(screen.getByLabelText("Search artists")).toBeInTheDocument();
	expect(
		screen.getByRole("heading", { name: "Mira Vale" }),
	).toBeInTheDocument();
});

it("preserves external artist references as readonly identities and gates genre selection", () => {
	const state = createLocalProviderState();
	state.type = "recordings";
	state.selectedId = "local-recording-green";
	function Harness() {
		const [current, setCurrent] = useState(state);
		return (
			<LocalProviderScreen state={current} onState={setCurrent} genresLoaded />
		);
	}
	render(<Harness />);
	fireEvent.click(screen.getAllByRole("button", { name: "Редактировать" })[0]);
	const forma = screen.getByText("mb-artist-forma").closest("div");
	if (!forma) throw new Error("Missing Forma artist option");
	expect(forma).toHaveTextContent("mb-artist-forma");
	fireEvent.click(screen.getByRole("button", { name: "Жанры" }));
	fireEvent.change(screen.getByRole("searchbox", { name: "Поиск жанров" }), {
		target: { value: "electronic" },
	});
	expect(
		screen.getByRole("option", { name: "Electronic" }),
	).toBeInTheDocument();
	fireEvent.click(screen.getByRole("button", { name: "Готово" }));
	// The external artist option is only a reference checkbox; its canonical key is not editable.
	expect(
		within(forma).getByRole("checkbox", { name: "Forma" }),
	).toBeInTheDocument();
});

it("keeps unknown genre references when saving a Local release draft", () => {
	const state = createLocalProviderState();
	state.type = "releases";
	state.selectedId = "local-release-north";
	state.items.releases[0].genres = ["retired-catalog-genre"];
	state.items.releases[0].genreNamesById = {
		"retired-catalog-genre": "Archived genre",
	};
	let updated = state;
	function Harness() {
		const [current, setCurrent] = useState(state);
		return (
			<LocalProviderScreen
				state={current}
				onState={(next) => {
					updated = next;
					setCurrent(next);
				}}
			/>
		);
	}
	render(<Harness />);
	fireEvent.click(screen.getAllByRole("button", { name: "Редактировать" })[0]);
	fireEvent.click(screen.getByRole("button", { name: "Сохранить" }));
	const saved = updated.items.releases.find(
		(release) => release.id === "local-release-north",
	);
	expect(saved?.genres).toEqual(["retired-catalog-genre"]);
	// The stored name remains available even when its former catalog entry is gone.
	expect(saved?.genreNamesById?.["retired-catalog-genre"]).toBe(
		"Archived genre",
	);
});

it("offers the Local artist sort name only in its artist edit card", () => {
	const state = createLocalProviderState();
	render(<LocalProviderScreen state={state} onState={() => {}} />);
	fireEvent.click(screen.getAllByRole("button", { name: "Редактировать" })[0]);
	expect(
		screen.getByRole("textbox", { name: /Имя для сортировки/ }),
	).toBeInTheDocument();
});

it("saves metadata-editor drafts with the outer Local save and reopens read-only", () => {
	const state = createLocalProviderState();
	state.type = "releases";
	state.selectedId = "local-release-north";
	let updated = state;
	function Harness() {
		const [current, setCurrent] = useState(state);
		return (
			<LocalProviderScreen
				state={current}
				onState={(next) => {
					updated = next;
					setCurrent(next);
				}}
			/>
		);
	}
	render(<Harness />);
	const readonlyEditor = screen.getByRole("region", {
		name: "Метаданные издания",
	});
	within(readonlyEditor).getByRole("textbox", { name: "Издание" });
	fireEvent.click(screen.getByRole("button", { name: "Редактировать" }));
	fireEvent.change(screen.getByRole("textbox", { name: "Издание" }), {
		target: { value: "Typed release metadata" },
	});
	fireEvent.click(screen.getByRole("button", { name: "Сохранить" }));
	expect(updated.draft).toBeNull();
	const saved = updated.items.releases.find(
		(release) => release.id === "local-release-north",
	);
	expect(saved?.metadataValues?.album).toEqual(["Typed release metadata"]);
	expect(
		within(
			screen.getByRole("region", { name: "Метаданные издания" }),
		).getByRole("textbox", { name: "Издание" }),
	).toHaveValue("Typed release metadata");
});

it("keeps Local release fields in one readonly-to-edit form without changing its grid", () => {
	const state = createLocalProviderState();
	state.type = "releases";
	state.selectedId = "local-release-north";
	function Harness() {
		const [current, setCurrent] = useState(state);
		return <LocalProviderScreen state={current} onState={setCurrent} />;
	}
	render(<Harness />);

	const form = screen.getByLabelText("Дата необязательно").closest("form");
	expect(form).not.toBeNull();
	expect(form).toBeInTheDocument();
	const date = within(form as HTMLFormElement).getByLabelText(
		"Дата необязательно",
	);
	const label = within(form as HTMLFormElement).getByLabelText("Лейбл");
	expect(date).toHaveAttribute("readonly");
	expect(label).toHaveAttribute("readonly");
	within(form as HTMLFormElement).getByLabelText("Штрихкод");

	fireEvent.click(screen.getByRole("button", { name: "Редактировать" }));
	const editingForm = screen
		.getByLabelText("Дата необязательно")
		.closest("form");
	expect(editingForm).toBe(form);
	expect(
		within(editingForm as HTMLFormElement).getByLabelText("Дата необязательно"),
	).not.toHaveAttribute("readonly");
});
