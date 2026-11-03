import { fireEvent, render, screen, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { GenreLibrary } from "./GenreLibrary";
import { genreCatalog } from "./genres/catalogue";

it("searches the full offline catalogue by name and MusicBrainz ID", () => {
	const onLoadedChange = vi.fn();
	render(<GenreLibrary loaded={false} onLoadedChange={onLoadedChange} />);

	expect(screen.getByText("Доступен офлайн")).toBeInTheDocument();
	expect(screen.getByRole("status")).toHaveTextContent(
		/Найдено жанров: 2\s?220/,
	);

	const search = screen.getByRole("searchbox", { name: "Поиск жанров" });
	fireEvent.change(search, { target: { value: genreCatalog[0].id } });
	expect(screen.getByRole("status")).toHaveTextContent("Найдено жанров: 1");
	const results = screen.getByRole("list", { name: "Результаты" });
	expect(within(results).getByText("2 Tone")).toBeInTheDocument();
	expect(within(results).getByText("2 Tone").closest("li")).toHaveAttribute(
		"title",
		expect.stringContaining(genreCatalog[0].id),
	);

	fireEvent.change(search, { target: { value: "not-a-genre" } });
	expect(
		screen.getByText("По этому запросу жанры не найдены."),
	).toBeInTheDocument();

	fireEvent.click(screen.getByRole("button", { name: "Обновить каталог" }));
	expect(onLoadedChange).toHaveBeenCalledWith(true);
});
