import { fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import { firstPublicationPreviewMessages } from "../features/library/firstPublicationPreviewMessages";
import { CatalogDemo } from "./CatalogDemo";

afterEach(() => {
	window.history.replaceState(null, "", "#library");
});

it("renders one keyboard-operable four-option filter radio group", () => {
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

it("opens the exact release destination from an album row", () => {
	window.history.replaceState(null, "", "#library?tab=albums");
	render(<CatalogDemo />);
	const grid = screen.getByRole("grid", { name: "Альбомы медиатеки" });
	const links = within(grid).getAllByRole("link", {
		name: "Открыть релиз: Northern Lines",
	});
	expect(
		links.some((link) => link.getAttribute("href") === "#release/north"),
	).toBe(true);
});

it("renders the focused release destination without artist management", () => {
	window.history.replaceState(null, "", "#release/north");
	render(<CatalogDemo />);
	expect(
		screen.getByRole("heading", { name: "Northern Lines" }),
	).toBeInTheDocument();
	expect(
		screen.getByRole("heading", { name: "Носители и позиции" }),
	).toBeInTheDocument();
	expect(
		screen.queryByRole("heading", { name: "Сведения об артисте" }),
	).not.toBeInTheDocument();
});

it("shows a read-only preview of the selected library position", async () => {
	window.history.replaceState(
		null,
		"",
		"#library?tab=tracks&q=First%20Light&filter=all",
	);
	render(<CatalogDemo />);
	const grid = screen.getByRole("grid", { name: "Треки медиатеки" });
	const track = within(grid)
		.getAllByRole("link", { name: "First Light" })
		.find((link) => link.getAttribute("href") === "#track/north-t1")
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
	window.history.replaceState(null, "", "#track/north-t1");
	render(<CatalogDemo />);
	const publication = within(
		screen.getByRole("region", { name: "Публикация" }),
	);
	const copy = firstPublicationPreviewMessages.ru;
	fireEvent.click(publication.getByRole("button", { name: copy.preview }));
	expect(window.location.hash).toBe("#track/north-t1");
	expect(publication.getByText(copy.comparisonReadOnly)).toBeInTheDocument();
	expect(publication.queryByRole("button", { name: copy.publish })).toBeNull();
	expect(
		publication.queryByRole("button", { name: copy.retainedRepublish }),
	).toBeNull();
	expect(screen.queryByRole("dialog")).toBeNull();
});
