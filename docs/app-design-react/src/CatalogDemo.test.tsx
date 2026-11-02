import { fireEvent, render, screen, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { CatalogDemo } from "./CatalogDemo";
import { ContextPublicationPreview } from "./ContextPublicationPreview";
import {
	catalogData,
	originalFiles,
	originalProviders,
	originalReleases,
} from "./catalogData";
import { makeReleasePlan } from "./incomingModel";

function selectOption(label: RegExp, option: string) {
	const group = screen.getByRole("radiogroup", { name: label });
	fireEvent.click(within(group).getByRole("radio", { name: option }));
}

it("keeps nested selection consistent when an album is partially deselected", () => {
	render(<CatalogDemo />);
	fireEvent.click(
		screen.getByRole("checkbox", { name: "Выбрать позиции артиста Mira Vale" }),
	);
	expect(screen.getByRole("status")).toHaveTextContent("29");
	fireEvent.click(screen.getByRole("tab", { name: "Все альбомы" }));
	const albums = screen.getAllByRole("checkbox", {
		name: "Выбрать позиции альбома Northern Lines",
	});
	fireEvent.click(albums[0]);
	expect(screen.getByRole("status")).toHaveTextContent("19");
	const albumExpander = screen.getAllByRole("button", {
		name: "Northern Lines",
	})[0];
	fireEvent.click(albumExpander);
	expect(albumExpander).toHaveAttribute("aria-expanded", "true");
	fireEvent.click(
		screen.getByRole("checkbox", { name: /Выбрать First Light/ }),
	);
	expect(screen.getByRole("status")).toHaveTextContent("20");
	expect(albums[0]).toBePartiallyChecked();
	fireEvent.click(screen.getByRole("tab", { name: "Артисты" }));
	expect(
		screen.getByRole("checkbox", { name: "Выбрать позиции артиста Mira Vale" }),
	).toBePartiallyChecked();
});

it("uses the shared filter to include partially matching albums", () => {
	render(<CatalogDemo />);
	fireEvent.click(screen.getByRole("tab", { name: "Все альбомы" }));
	selectOption(/Состояние/, "В наличии");
	expect(
		screen.getAllByRole("button", { name: "Northern Lines" }),
	).toHaveLength(2);
	selectOption(/Состояние/, "Не хватает");
	expect(
		screen.getAllByRole("button", { name: "Northern Lines" }),
	).toHaveLength(2);
	expect(
		screen.queryByRole("button", { name: "Collected Rooms" }),
	).not.toBeInTheDocument();
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

it("retains the selected filter when navigating between library and artist", async () => {
	const previousHash = window.location.hash;
	window.location.hash = "library";
	try {
		render(<CatalogDemo />);
		selectOption(/Состояние/, "Опубликовано");
		fireEvent.click(screen.getByRole("link", { name: /Mira Vale/ }));
		await screen.findByRole("heading", { name: "Mira Vale" });
		const group = screen.getByRole("radiogroup", { name: "Состояние" });
		expect(
			within(group).getByRole("radio", { name: "Опубликовано" }),
		).toBeChecked();
		fireEvent.click(screen.getByRole("link", { name: "К медиатеке" }));
		await screen.findByRole("tab", { name: "Артисты" });
		const libraryGroup = screen.getByRole("radiogroup", { name: "Состояние" });
		expect(
			within(libraryGroup).getByRole("radio", { name: "Опубликовано" }),
		).toBeChecked();
	} finally {
		window.location.hash = previousHash;
		fireEvent(window, new Event("hashchange"));
	}
});

it("shows artist cards with a separate aggregate checkbox and real artist link", () => {
	render(<CatalogDemo />);
	const artistLink = screen.getByRole("link", { name: /Mira Vale/ });
	const artistCard = artistLink.closest<HTMLElement>(".artist-card");
	if (!artistCard) throw new Error("Missing Mira Vale artist card");

	expect(artistLink).toHaveAttribute("href", "#artist/mira");
	expect(artistCard).toHaveAttribute("class", "artist-card");
	expect(artistCard).toHaveTextContent("изданий");
	expect(artistCard).toHaveTextContent("позиций");
	expect(artistCard).toHaveTextContent("аудио");
	expect(artistCard).toHaveTextContent("опубликовано");
	expect(
		within(artistCard).getByRole("checkbox", {
			name: "Выбрать позиции артиста Mira Vale",
		}),
	).toBeInTheDocument();
});

it("renders full album completeness and keeps local publication completeness unknown", () => {
	render(<CatalogDemo />);
	fireEvent.click(screen.getByRole("tab", { name: "Все альбомы" }));
	expect(
		screen.getByRole("columnheader", { name: "Исходники" }),
	).toBeInTheDocument();
	expect(screen.getByText(/Полнота неизвестна/)).toBeInTheDocument();
	expect(
		screen.getByRole("columnheader", { name: "Провайдер" }),
	).toBeInTheDocument();
});

it("renders technical track details and links from the library track list", () => {
	render(<CatalogDemo />);
	fireEvent.click(screen.getByRole("tab", { name: "Все треки" }));
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
	fireEvent.click(screen.getByRole("tab", { name: "Все альбомы" }));
	expect(screen.queryByText("After Rain")).not.toBeInTheDocument();
	const absentAlbum = screen.getByText("Blue Atlas").closest("section");
	if (!absentAlbum) throw new Error("Missing absent album section");
	fireEvent.click(
		within(absentAlbum).getByRole("button", { name: /Blue Atlas/ }),
	);
	fireEvent.click(screen.getByRole("option", { name: /2001 · JP/ }));
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

it("does not publish when the final contextual confirmation is cancelled", () => {
	const previousHash = window.location.hash;
	window.location.hash = "release/north";
	try {
		render(<CatalogDemo />);
		fireEvent.click(
			screen.getByRole("button", { name: "Предпросмотр публикации" }),
		);
		fireEvent.click(
			screen.getByRole("button", { name: "Подтвердить публикацию" }),
		);
		expect(screen.getByRole("dialog")).toBeInTheDocument();
		fireEvent.click(
			within(screen.getByRole("dialog")).getByRole("button", {
				name: "Отмена",
			}),
		);
		expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
		expect(window.location.hash).toBe("#publications");
		expect(
			screen.getByRole("button", { name: "Подтвердить публикацию" }),
		).toBeInTheDocument();
	} finally {
		window.location.hash = previousHash;
	}
});

it("announces a confirmed contextual publication after returning to incoming", () => {
	const previousHash = window.location.hash;
	window.location.hash = "release/north";
	try {
		render(<CatalogDemo />);
		fireEvent.click(
			screen.getByRole("button", { name: "Предпросмотр публикации" }),
		);
		fireEvent.click(
			screen.getByRole("button", { name: "Подтвердить публикацию" }),
		);
		fireEvent.click(screen.getByRole("button", { name: "Подтвердить" }));
		expect(window.location.hash).toBe("#review");
		expect(screen.getByRole("status")).toHaveTextContent("Демо:");
	} finally {
		window.location.hash = previousHash;
	}
});

it("refuses final confirmation when inventory becomes stale while the dialog is open", () => {
	const releases = structuredClone(originalReleases);
	const release = releases.find((item) =>
		item.tracks.some(
			(track) => track.sourceId && originalFiles[track.sourceId],
		),
	);
	if (!release) throw new Error("Missing release with an available source");
	const files = structuredClone(originalFiles);
	const plan = makeReleasePlan(
		{
			inventoryVersion: 0,
		} as Parameters<typeof makeReleasePlan>[0],
		release,
		files,
	);
	const track = release.tracks.find((item) => plan.assignments[item.id]);
	if (!track) throw new Error("Missing plan assignment");
	const source = files[plan.assignments[track.id].selectedSourceId ?? ""];
	if (!source) throw new Error("Missing selected source");
	const replacementId = "confirmation-test-replacement";
	files[replacementId] = { ...source, id: replacementId };
	plan.assignments[track.id] = {
		...plan.assignments[track.id],
		sourceIds: [...plan.assignments[track.id].sourceIds, replacementId],
		selectedSourceId: replacementId,
	};
	const onReleases = vi.fn();
	const props = {
		plan,
		files,
		releases,
		providers: originalProviders,
		artists: catalogData.artists,
		confirmDowngrade: false,
		onPlan: vi.fn(),
		onReleases,
		onSuccess: vi.fn(),
		onBack: vi.fn(),
	};
	const view = render(
		<ContextPublicationPreview {...props} inventoryVersion={0} />,
	);
	fireEvent.click(
		screen.getByRole("button", { name: "Подтвердить публикацию" }),
	);
	view.rerender(<ContextPublicationPreview {...props} inventoryVersion={1} />);
	expect(screen.getByRole("button", { name: "Подтвердить" })).toBeDisabled();
	expect(screen.getAllByRole("alert").length).toBeGreaterThan(0);
	expect(onReleases).not.toHaveBeenCalled();
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

it("redirects the obsolete manual-release route to the Local provider", async () => {
	const previousHash = window.location.hash;
	window.location.hash = "create-release";
	try {
		render(<CatalogDemo />);
		expect(
			await screen.findByRole("heading", { name: "Собственный каталог" }),
		).toBeInTheDocument();
		expect(window.location.hash).toBe("#local");
	} finally {
		window.location.hash = previousHash;
	}
});
