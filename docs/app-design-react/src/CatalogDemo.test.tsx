import { fireEvent, render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import { CatalogDemo } from "./CatalogDemo";

it("keeps nested selection consistent when an album is partially deselected", () => {
	render(<CatalogDemo />);
	fireEvent.click(
		screen.getByRole("checkbox", { name: "Выбрать позиции артиста Mira Vale" }),
	);
	expect(screen.getByRole("status")).toHaveTextContent("29");
	fireEvent.click(screen.getByRole("button", { name: "Все альбомы" }));
	const albums = screen.getAllByRole("checkbox", {
		name: "Выбрать позиции альбома Northern Lines",
	});
	fireEvent.click(albums[0]);
	expect(screen.getByRole("status")).toHaveTextContent("19");
	fireEvent.click(screen.getAllByRole("button", { name: "Northern Lines" })[0]);
	fireEvent.click(
		screen.getByRole("checkbox", { name: "Выбрать First Light" }),
	);
	expect(screen.getByRole("status")).toHaveTextContent("20");
	expect(albums[0]).toBePartiallyChecked();
	fireEvent.click(screen.getByRole("button", { name: "Артисты" }));
	expect(
		screen.getByRole("checkbox", { name: "Выбрать позиции артиста Mira Vale" }),
	).toBePartiallyChecked();
});

it("includes a partially available album in both availability filters", () => {
	render(<CatalogDemo />);
	fireEvent.click(screen.getByRole("button", { name: "Все альбомы" }));
	const filter = screen.getByRole("combobox", { name: "Наличие" });
	fireEvent.change(filter, { target: { value: "available" } });
	expect(
		screen.getAllByRole("button", { name: "Northern Lines" }),
	).toHaveLength(2);
	fireEvent.change(filter, { target: { value: "missing" } });
	expect(
		screen.getAllByRole("button", { name: "Northern Lines" }),
	).toHaveLength(2);
	expect(
		screen.queryByRole("button", { name: "Collected Rooms" }),
	).not.toBeInTheDocument();
});

it("requires an explicit edition before showing an absent album's tracks", () => {
	render(<CatalogDemo />);
	fireEvent.click(screen.getByRole("button", { name: "Все альбомы" }));
	expect(screen.queryByText("After Rain")).not.toBeInTheDocument();
	fireEvent.change(screen.getByRole("combobox", { name: "Выберите издание" }), {
		target: { value: "absent-blue-b" },
	});
	expect(screen.getByText("After Rain")).toBeInTheDocument();
	expect(screen.queryByRole("status")).not.toBeInTheDocument();
});

it("renders the #local route inside a single main landmark", () => {
	const previousHash = window.location.hash;
	window.location.hash = "local";
	try {
		render(<CatalogDemo />);
		expect(screen.getAllByRole("main")).toHaveLength(1);
	} finally {
		window.location.hash = previousHash;
	}
});
