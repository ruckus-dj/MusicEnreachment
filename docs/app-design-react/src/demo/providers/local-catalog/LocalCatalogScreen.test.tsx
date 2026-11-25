import { fireEvent, render, screen, within } from "@testing-library/react";
import { expect, it } from "vitest";
import { LocalCatalogScreen } from "./LocalCatalogScreen";
import type {
	LocalProviderRecord,
	LocalProviderState,
} from "./localProviderModel";

const localRelease = (index: number): LocalProviderRecord => ({
	id: `synthetic-release-${index}`,
	key: `synthetic-release-key-${index}`,
	provider: "Local",
	title: `Synthetic Release ${index}`,
	release_group_title: `Synthetic Release ${index}`,
	artistIds: [],
	tracks: [],
});

const externalRelease: LocalProviderRecord = {
	id: "external-release",
	key: "mb-external-release",
	provider: "MusicBrainz",
	title: "External Session",
	release_group_title: "External Session",
	artistIds: [],
	tracks: [],
};

const externalArtist: LocalProviderRecord = {
	id: "external-artist",
	key: "mb-external-artist",
	provider: "MusicBrainz",
	name: "External Artist",
};

const state: LocalProviderState = {
	type: "releases",
	selectedId: null,
	query: "",
	editing: false,
	creating: false,
	draft: null,
	items: {
		releases: [
			...Array.from({ length: 7 }, (_, index) => localRelease(index + 1)),
			externalRelease,
		],
		recordings: [],
		artists: [externalArtist],
	},
	notice: "",
};

const filter = () => screen.getByLabelText("Фильтр по всем записям");

const pager = () =>
	screen.getByRole("navigation", { name: "Страницы собственного каталога" });

const expectPage = (page: number, pages: number) =>
	expect(within(pager()).getByRole("status")).toHaveTextContent(
		`Страница ${page} из ${pages}`,
	);

const row = (title: string) =>
	screen.getByRole("button", { name: new RegExp(`^${title}\\b`) });

it("filters Local records before paging and restores the tab, filter and page after a record", () => {
	render(<LocalCatalogScreen state={state} onState={() => undefined} />);

	const releasesTab = screen.getByRole("tab", { name: "Релизы: 7" });
	expect(within(releasesTab).getByText("7")).toBeInTheDocument();
	expectPage(1, 3);
	expect(row("Synthetic Release 1")).toBeInTheDocument();
	expect(
		screen.queryByRole("button", { name: /^Synthetic Release 7\b/ }),
	).not.toBeInTheDocument();

	fireEvent.click(
		within(pager()).getByRole("button", { name: "Следующая страница" }),
	);
	fireEvent.click(
		within(pager()).getByRole("button", { name: "Следующая страница" }),
	);
	expectPage(3, 3);
	expect(row("Synthetic Release 7")).toBeInTheDocument();

	// A filter match that lives outside the current page returns to the first page.
	fireEvent.change(filter(), { target: { value: "Synthetic Release 1" } });
	expectPage(1, 1);
	expect(row("Synthetic Release 1")).toBeInTheDocument();
	expect(
		screen.queryByRole("button", { name: /^Synthetic Release 7\b/ }),
	).not.toBeInTheDocument();

	fireEvent.change(filter(), { target: { value: "Synthetic" } });
	fireEvent.click(
		within(pager()).getByRole("button", { name: "Следующая страница" }),
	);
	expectPage(2, 3);
	fireEvent.click(row("Synthetic Release 4"));
	expect(
		screen.getByRole("heading", { name: "Synthetic Release 4" }),
	).toBeInTheDocument();
	fireEvent.click(screen.getByRole("button", { name: "Назад" }));
	expect(filter()).toHaveValue("Synthetic");
	expectPage(2, 3);
	expect(row("Synthetic Release 4")).toBeInTheDocument();
	expect(screen.getByRole("tab", { name: /^Релизы/ })).toHaveAttribute(
		"aria-selected",
		"true",
	);

	// External records never become rows and an external-only match is a filter miss.
	fireEvent.change(filter(), { target: { value: "External Session" } });
	expect(screen.getByText("По фильтру ничего не найдено.")).toBeInTheDocument();
	expect(
		screen.queryByRole("button", { name: /External Session/ }),
	).not.toBeInTheDocument();
	fireEvent.click(screen.getByRole("button", { name: "Сбросить фильтр" }));
	expectPage(1, 3);
	expect(row("Synthetic Release 1")).toBeInTheDocument();

	// An empty tab is distinct from a filter miss, and external artists stay out of the rows.
	fireEvent.click(screen.getByRole("tab", { name: /^Артисты/ }));
	expect(screen.getByText(/пока нет артистов/)).toBeInTheDocument();
	expect(
		screen.queryByText("По фильтру ничего не найдено."),
	).not.toBeInTheDocument();
	expect(screen.queryByText("External Artist")).not.toBeInTheDocument();
});
