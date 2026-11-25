import { act, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import { CatalogDemo } from "../demo/CatalogDemo";
import { firstPublicationPreviewMessages } from "../ui/features/library/firstPublicationPreviewMessages";

const northernReleaseHref = "#/library/releases/north";
const northernPositionHref = "#/library/releases/north/positions/north-t1";

afterEach(() => {
	window.history.replaceState(
		null,
		"",
		"#/library?view=artists&publication=all&availability=all",
	);
});

it("renders one keyboard-operable four-option filter radio group", () => {
	window.history.replaceState(
		null,
		"",
		"#/library?view=artists&publication=all&availability=all",
	);
	render(<CatalogDemo />);
	const group = screen.getByRole("radiogroup", { name: "Состояние" });
	const labels = ["Все", "Не хватает", "В наличии", "Опубликовано"];
	const radios = labels.map((name) =>
		within(group).getByRole("radio", { name }),
	);
	expect(screen.getAllByRole("radiogroup")).toHaveLength(1);
	expect(radios[0]).toBeChecked();
	radios[0].focus();
	fireEvent.keyDown(radios[0], { key: "ArrowRight" });
	expect(radios[1]).toBeChecked();
	fireEvent.keyDown(radios[1], { key: "ArrowRight" });
	expect(radios[2]).toBeChecked();
	fireEvent.keyDown(radios[2], { key: "ArrowRight" });
	expect(radios[3]).toBeChecked();
});

it("opens Incoming from the root when the fixture has registered sources", () => {
	window.history.replaceState(null, "", "#/");
	render(<CatalogDemo />);
	expect(window.location.hash).toBe("#/incoming-groups");
});

it("opens the exact release destination from an album row", () => {
	window.history.replaceState(
		null,
		"",
		"#/library?view=albums&publication=all&availability=all",
	);
	render(<CatalogDemo />);
	const grid = screen.getByRole("grid", { name: "Альбомы медиатеки" });
	const links = within(grid).getAllByRole("link", {
		name: "Открыть релиз: Northern Lines",
	});
	expect(
		links.some((link) => link.getAttribute("href") === northernReleaseHref),
	).toBe(true);
});

it("restores the artist query and filter from the canonical address", () => {
	window.history.replaceState(
		null,
		"",
		"#/library/artists/mira?view=artists&q=Mira&publication=published&availability=all",
	);
	render(<CatalogDemo />);
	const search = screen.getByDisplayValue("Mira");
	expect(search).toBeInTheDocument();
	search.focus();
	expect(screen.getByRole("radio", { name: "Опубликовано" })).toBeChecked();

	act(() => {
		window.history.replaceState(
			null,
			"",
			"#/library/artists/mira?view=artists&q=Arden&publication=all&availability=missing",
		);
		window.dispatchEvent(new HashChangeEvent("hashchange"));
	});
	expect(screen.getByDisplayValue("Arden")).toHaveFocus();
	expect(screen.getByRole("radio", { name: "Не хватает" })).toBeChecked();
	expect(window.location.hash).toBe(
		"#/library/artists/mira?view=artists&q=Arden&publication=all&availability=missing",
	);
});

it("keeps a copied exact offset instead of snapping to the page start", () => {
	const hash =
		"#/library?view=artists&publication=all&availability=all&offset=1";
	window.history.replaceState(null, "", hash);
	render(<CatalogDemo />);
	expect(
		screen.queryByRole("link", { name: /Mira Vale/ }),
	).not.toBeInTheDocument();
	expect(screen.getByRole("link", { name: /Arden Trio/ })).toBeInTheDocument();
	expect(window.location.hash).toBe(hash);
});

it("renders the focused release destination without artist management", () => {
	window.history.replaceState(null, "", northernReleaseHref);
	const view = render(<CatalogDemo />);
	expect(
		screen.getByRole("heading", { name: "Northern Lines" }),
	).toBeInTheDocument();
	expect(
		screen.getByRole("heading", { name: "Носители и позиции" }),
	).toBeInTheDocument();
	expect(
		screen.queryByRole("heading", { name: "Сведения об артисте" }),
	).not.toBeInTheDocument();
	view.unmount();

	window.history.replaceState(
		null,
		"",
		"#/library/releases/hours/positions/north-t1",
	);
	render(<CatalogDemo />);
	expect(
		screen.getByRole("heading", { name: "Small Hours" }),
	).toBeInTheDocument();
	expect(screen.getByRole("alert")).toHaveTextContent(
		"Позиция не найдена в этом релизе.",
	);
	expect(screen.getByRole("link", { name: "← Полный релиз" })).toHaveAttribute(
		"href",
		"#/library/releases/hours",
	);
});

it("retains an unsaved release title draft across a position visit", async () => {
	window.history.replaceState(null, "", northernReleaseHref);
	render(<CatalogDemo />);
	const editor = screen.getByRole("region", { name: "Сведения об издании" });
	fireEvent.click(within(editor).getByRole("button", { name: "Изменить" }));
	const title = editor.querySelector<HTMLInputElement>(
		'input[data-metadata-keys~="album"]',
	);
	if (!title) throw new Error("Missing release title field");
	fireEvent.change(title, { target: { value: "Northern Lines draft" } });
	const trackLink = screen
		.getAllByRole("link", { name: /First Light/ })
		.find((link) => link.getAttribute("href") === northernPositionHref);
	if (!trackLink) throw new Error("Missing Northern Lines position link");
	fireEvent.click(trackLink);
	await screen.findByRole("heading", { level: 1, name: /First Light$/ });
	expect(window.location.hash).toBe(northernPositionHref);

	window.history.back();
	await screen.findByRole("heading", { name: "Northern Lines" });
	expect(
		await screen.findByDisplayValue("Northern Lines draft"),
	).toBeInTheDocument();
});

it("shows a read-only preview of the selected library position", async () => {
	window.history.replaceState(
		null,
		"",
		"#/library?view=tracks&q=First%20Light&publication=all&availability=all",
	);
	render(<CatalogDemo />);
	const grid = screen.getByRole("grid", { name: "Треки медиатеки" });
	const track = within(grid)
		.getAllByRole("link", { name: "First Light" })
		.find((link) => link.getAttribute("href") === northernPositionHref)
		?.closest("tr");
	if (!track) throw new Error("Missing Northern Lines position");
	const checkbox = within(track).getByRole("checkbox", {
		name: "Выбрать трек First Light",
	});
	fireEvent.click(checkbox);
	fireEvent.click(
		screen.getByRole("button", { name: "Предпросмотр публикации" }),
	);
	const dialog = await screen.findByRole("dialog", {
		name: "Предпросмотр публикации",
	});
	expect(within(dialog).getByRole("listitem")).toHaveTextContent(
		"First Light · Northern Lines",
	);
	const close = within(dialog).getByRole("button", { name: "Закрыть" });
	expect(within(dialog).getAllByRole("button")).toEqual([close]);
	fireEvent.click(close);
	expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
	expect(checkbox).toBeChecked();
});

it("keeps a CURRENT comparison inside the position publication panel", () => {
	window.history.replaceState(null, "", northernPositionHref);
	render(<CatalogDemo />);
	const publication = within(
		screen.getByRole("region", { name: "Публикация" }),
	);
	const copy = firstPublicationPreviewMessages.ru;
	fireEvent.click(publication.getByRole("button", { name: copy.preview }));
	expect(window.location.hash).toBe(northernPositionHref);
	expect(publication.getByText(copy.comparisonReadOnly)).toBeInTheDocument();
	expect(publication.queryByRole("button", { name: copy.publish })).toBeNull();
	expect(
		publication.queryByRole("button", { name: copy.retainedRepublish }),
	).toBeNull();
	fireEvent.click(publication.getByRole("radio", { name: copy.forced }));
	fireEvent.click(publication.getByRole("button", { name: copy.preview }));
	expect(
		publication.queryByRole("button", { name: copy.retainedRepublish }),
	).toBeNull();

	fireEvent.click(
		publication.getByRole("radio", { name: copy.retainedRepublish }),
	);
	expect(
		publication.getByRole("button", { name: copy.retainedRepublish }),
	).toBeDisabled();
	fireEvent.click(publication.getByRole("button", { name: copy.preview }));
	expect(
		publication.getByRole("button", { name: copy.retainedRepublish }),
	).toBeEnabled();
	expect(screen.queryByRole("dialog")).toBeNull();
});

it("opens the publication preview inline without changing the route", async () => {
	const hash = northernPositionHref;
	window.history.replaceState(null, "", hash);
	render(<CatalogDemo />);
	const publication = within(
		screen.getByRole("region", { name: "Публикация" }),
	);
	fireEvent.click(
		publication.getByRole("button", { name: "Обновить метаданные" }),
	);
	expect(window.location.hash).toBe(hash);
	const preview = await screen.findByRole("heading", {
		name: "Предпросмотр публикации",
	});
	expect(preview.tagName).toBe("H3");
	fireEvent.click(screen.getByRole("button", { name: "Закрыть предпросмотр" }));
	expect(
		screen.queryByRole("heading", { name: "Предпросмотр публикации" }),
	).not.toBeInTheDocument();
	expect(window.location.hash).toBe(hash);
});
