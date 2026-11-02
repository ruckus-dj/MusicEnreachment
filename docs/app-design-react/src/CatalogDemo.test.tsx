import { fireEvent, render, screen, within } from "@testing-library/react";
import { expect, it } from "vitest";
import { CatalogDemo } from "./CatalogDemo";
import { catalogData, originalReleases } from "./catalogData";

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

it("renders full album completeness and keeps local publication completeness unknown", () => {
	render(<CatalogDemo />);
	fireEvent.click(screen.getByRole("button", { name: "Все альбомы" }));
	expect(
		screen.getByRole("columnheader", { name: "Есть исходники" }),
	).toBeInTheDocument();
	expect(screen.getByText(/полнота неизвестна/)).toBeInTheDocument();
	expect(
		screen.getByRole("columnheader", { name: "Провайдер" }),
	).toBeInTheDocument();
});

it("renders technical track details and links from the library track list", () => {
	render(<CatalogDemo />);
	fireEvent.click(screen.getByRole("button", { name: "Все треки" }));
	for (const heading of [
		"Длительность",
		"Источник",
		"Параметры",
		"Файл / путь",
		"Публикация",
		"Теги",
	]) {
		expect(
			screen.getByRole("columnheader", { name: heading }),
		).toBeInTheDocument();
	}
	expect(screen.getAllByRole("link", { name: "Теги" })[0]).toHaveAttribute(
		"href",
		"#tags/north-t1/track",
	);
	expect(
		screen.getAllByRole("link", { name: "First Light" })[0],
	).toHaveAttribute("href", "#track/north-t1");
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

it("returns a track metadata preview to its track route", () => {
	const previousHash = window.location.hash;
	window.location.hash = "track/north-t1";
	try {
		render(<CatalogDemo />);
		fireEvent.click(
			screen.getByRole("button", { name: "Обновить метаданные" }),
		);
		expect(window.location.hash).toBe("#publication-preview");
		fireEvent.click(screen.getByRole("button", { name: "Вернуться к треку" }));
		expect(window.location.hash).toBe("#track/north-t1");
		expect(
			screen.getByRole("heading", { name: "01 · First Light" }),
		).toBeInTheDocument();
	} finally {
		window.location.hash = previousHash;
	}
});

it("shows contextual source codec separately from metadata-only preview", () => {
	const previousHash = window.location.hash;
	window.location.hash = "release/north";
	try {
		render(<CatalogDemo />);
		fireEvent.click(
			screen.getByRole("button", { name: "Предпросмотр публикации" }),
		);
		expect(window.location.hash).toBe("#publications");
		expect(
			screen.getByText("01 - First Light.flac · FLAC"),
		).toBeInTheDocument();
	} finally {
		window.location.hash = previousHash;
	}
});

it("keeps global selected-item actions separate from the current artist controls", () => {
	const previousHash = window.location.hash;
	const mira = catalogData.artists.find(
		(artist) => artist.name === "Mira Vale",
	);
	const other = catalogData.artists.find((artist) => artist.id !== mira?.id);
	if (!mira || !other) throw new Error("Missing artist selection fixtures");
	const firstSelectedTrack = originalReleases.find(
		(release) => release.artistId === mira.id,
	)?.tracks[0];
	if (!firstSelectedTrack) throw new Error("Missing Mira Vale track fixture");
	window.location.hash = `artist/${mira.id}`;
	try {
		render(<CatalogDemo />);
		fireEvent.click(
			screen.getByRole("checkbox", {
				name: `Выбрать все позиции артиста ${mira.name}`,
			}),
		);
		window.location.hash = `artist/${other.id}`;
		fireEvent(window, new Event("hashchange"));

		expect(screen.getByRole("status")).toHaveTextContent("Выбрано позиций: 29");
		const localPreview = screen.getByRole("button", {
			name: "Предпросмотр 0 выбранных позиций",
		});
		expect(localPreview).toBeDisabled();
		fireEvent.click(
			within(screen.getByRole("status")).getByRole("button", {
				name: "Предпросмотр публикации",
			}),
		);
		expect(window.location.hash).toBe("#publication-preview");
		expect(
			screen
				.getAllByRole("link", {
					name: `${firstSelectedTrack.position}. ${firstSelectedTrack.title}`,
				})
				.find(
					(link) =>
						link.getAttribute("href") === `#track/${firstSelectedTrack.id}`,
				),
		).toHaveAttribute("href", `#track/${firstSelectedTrack.id}`);
	} finally {
		window.location.hash = previousHash;
	}
});

it("returns from a created manual release to its unchanged saved draft", async () => {
	const previousHash = window.location.hash;
	window.location.hash = "create-release";
	try {
		render(<CatalogDemo />);
		const title = screen.getByRole("textbox", { name: "Название релиза" });
		fireEvent.change(title, { target: { value: "Draft that must survive" } });
		fireEvent.click(screen.getByRole("button", { name: "Создать демо-релиз" }));

		const returnButton = screen.getByRole("button", {
			name: "Вернуться к черновику",
		});
		fireEvent.click(returnButton);
		expect(window.location.hash).toBe("#create-release");
		expect(
			await screen.findByRole("textbox", { name: "Название релиза" }),
		).toHaveValue("Draft that must survive");
	} finally {
		window.location.hash = previousHash;
	}
});
