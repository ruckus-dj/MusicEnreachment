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
	const albumRow = screen
		.getAllByRole("row")
		.find((row) => row.getAttribute("data-key") === "north");
	if (!albumRow) throw new Error("Missing Northern Lines 2021 edition row");
	const albumCheckbox = within(albumRow).getByRole("checkbox", {
		name: /Выбрать позиции альбома Northern Lines/,
	});
	fireEvent.click(albumCheckbox);
	expect(screen.getByRole("status")).toHaveTextContent("19");
	const albumExpander = within(albumRow).getByRole("button", {
		name: "Northern Lines",
	});
	fireEvent.click(albumExpander);
	expect(albumExpander).toHaveAttribute("aria-expanded", "true");
	fireEvent.click(
		screen.getByRole("checkbox", { name: /Выбрать First Light/ }),
	);
	expect(screen.getByRole("status")).toHaveTextContent("20");
	expect(albumCheckbox).toBePartiallyChecked();
	fireEvent.click(screen.getByRole("tab", { name: "Артисты" }));
	expect(
		screen.getByRole("checkbox", { name: "Выбрать позиции артиста Mira Vale" }),
	).toBePartiallyChecked();
});

it("keeps publication paths out of library tables without changing track links", () => {
	const previousHash = window.location.hash;
	const release = originalReleases.find((item) => item.id === "north");
	const track = release?.tracks.find((item) => item.id === "north-t1");
	if (!release || !track?.published)
		throw new Error("Missing published track fixture");
	window.location.hash = "library";
	try {
		render(<CatalogDemo />);
		fireEvent.click(screen.getByRole("tab", { name: "Все альбомы" }));
		const albumRow = screen
			.getAllByRole("row")
			.find((row) => row.getAttribute("data-key") === release.id);
		if (!albumRow) throw new Error("Missing Northern Lines edition row");
		fireEvent.click(
			within(albumRow).getByRole("button", { name: release.title }),
		);
		expect(
			within(albumRow).getByRole("link", { name: track.title }),
		).toHaveAttribute("href", `#track/${track.id}`);
		expect(screen.queryByText(track.published.path)).not.toBeInTheDocument();

		fireEvent.click(screen.getByRole("tab", { name: "Все треки" }));
		const trackLink = screen
			.getAllByRole("link", { name: track.title })
			.find((link) => link.getAttribute("href") === `#track/${track.id}`);
		expect(trackLink).toHaveAttribute("href", `#track/${track.id}`);
		expect(screen.queryByText(track.published.path)).not.toBeInTheDocument();
	} finally {
		window.location.hash = previousHash;
		fireEvent(window, new Event("hashchange"));
	}
});

it("uses the shared floating dock to enter the existing publication preview flow", async () => {
	const previousHash = window.location.hash;
	window.location.hash = "library";
	try {
		render(<CatalogDemo />);
		fireEvent.click(
			screen.getByRole("checkbox", {
				name: "Выбрать позиции артиста Mira Vale",
			}),
		);
		const docks = screen.getAllByRole("region", { name: "Выбранные позиции" });
		expect(docks).toHaveLength(1);
		fireEvent.click(
			within(docks[0]).getByRole("button", { name: "Опубликовать" }),
		);
		expect(window.location.hash).toBe("#publication-preview");
		expect(
			await screen.findByRole("heading", { name: "Предпросмотр медиатеки" }),
		).toBeInTheDocument();
	} finally {
		window.location.hash = previousHash;
		fireEvent(window, new Event("hashchange"));
	}
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

it("saves artist metadata in catalog state across library navigation", async () => {
	const previousHash = window.location.hash;
	window.location.hash = "artist/mira";
	try {
		render(<CatalogDemo />);
		await screen.findByRole("heading", { name: "Mira Vale" });
		const artistDetails = screen.getByRole("region", {
			name: "Сведения об артисте",
		});
		const name = within(artistDetails).getByLabelText("Имя");
		const genre = within(artistDetails).getByLabelText("Жанр");
		const edit = within(artistDetails).getByRole("button", {
			name: "Редактировать",
		});
		fireEvent.click(edit);
		const editableName = within(artistDetails).getByLabelText("Имя");
		const editableGenre = within(artistDetails).getByLabelText("Жанр");
		expect(editableName).toBe(name);
		expect(editableGenre).toBe(genre);
		fireEvent.change(editableName, { target: { value: "Mira Vale Updated" } });
		fireEvent.change(editableGenre, { target: { value: "Dream pop" } });
		fireEvent.click(
			within(artistDetails).getByRole("button", { name: "Сохранить" }),
		);
		expect(within(artistDetails).getByLabelText("Имя")).toHaveValue(
			"Mira Vale Updated",
		);
		expect(within(artistDetails).getByLabelText("Жанр")).toHaveValue(
			"Dream pop",
		);

		fireEvent.click(screen.getByRole("link", { name: "К медиатеке" }));
		await screen.findByRole("tab", { name: "Артисты" });
		expect(
			screen.getByRole("link", { name: /Mira Vale Updated/ }),
		).toBeInTheDocument();
		fireEvent.click(screen.getByRole("link", { name: /Mira Vale Updated/ }));
		await screen.findByRole("heading", { name: "Mira Vale Updated" });
		expect(
			within(
				screen.getByRole("region", { name: "Сведения об артисте" }),
			).getByLabelText("Имя"),
		).toHaveValue("Mira Vale Updated");
		expect(
			within(
				screen.getByRole("region", { name: "Сведения об артисте" }),
			).getByLabelText("Жанр"),
		).toHaveValue("Dream pop");
	} finally {
		window.location.hash = previousHash;
		fireEvent(window, new Event("hashchange"));
	}
});

it("saves release metadata in catalog state and restores unsaved edits on cancel and Escape", async () => {
	const previousHash = window.location.hash;
	window.location.hash = "artist/mira?release=north";
	try {
		render(<CatalogDemo />);
		await screen.findByRole("heading", { name: "Mira Vale" });
		const releaseDetails = screen.getByRole("region", {
			name: "Сведения об издании",
		});
		const title = within(releaseDetails).getByLabelText("Название");
		const year = within(releaseDetails).getByLabelText("Год");
		const editRelease = () =>
			fireEvent.click(
				within(releaseDetails).getByRole("button", {
					name: "Редактировать",
				}),
			);
		editRelease();
		expect(within(releaseDetails).getByLabelText("Название")).toBe(title);
		expect(within(releaseDetails).getByLabelText("Год")).toBe(year);
		fireEvent.change(title, { target: { value: "Northern Lines Updated" } });
		fireEvent.click(
			within(releaseDetails).getByRole("button", { name: "Отменить" }),
		);
		expect(within(releaseDetails).getByLabelText("Название")).toHaveValue(
			"Northern Lines",
		);
		expect(within(releaseDetails).getByLabelText("Название")).toBe(title);

		editRelease();
		fireEvent.change(within(releaseDetails).getByLabelText("Название"), {
			target: { value: "Northern Lines Updated" },
		});
		fireEvent.keyDown(within(releaseDetails).getByLabelText("Название"), {
			key: "Escape",
		});
		expect(within(releaseDetails).getByLabelText("Название")).toHaveValue(
			"Northern Lines",
		);

		editRelease();
		fireEvent.change(within(releaseDetails).getByLabelText("Название"), {
			target: { value: "Northern Lines Updated" },
		});
		fireEvent.change(within(releaseDetails).getByLabelText("Год"), {
			target: { value: "2022" },
		});
		fireEvent.click(
			within(releaseDetails).getByRole("button", { name: "Сохранить" }),
		);
		expect(within(releaseDetails).getByLabelText("Название")).toHaveValue(
			"Northern Lines Updated",
		);
		expect(within(releaseDetails).getByLabelText("Год")).toHaveValue("2022");

		fireEvent.click(screen.getByRole("link", { name: "К медиатеке" }));
		await screen.findByRole("tab", { name: "Артисты" });
		fireEvent.click(screen.getByRole("link", { name: /Mira Vale/ }));
		await screen.findByRole("heading", { name: "Mira Vale" });
		window.location.hash = "artist/mira?release=north";
		fireEvent(window, new Event("hashchange"));
		const savedReleaseDetails = await screen.findByRole("region", {
			name: "Сведения об издании",
		});
		expect(within(savedReleaseDetails).getByLabelText("Название")).toHaveValue(
			"Northern Lines Updated",
		);
		expect(within(savedReleaseDetails).getByLabelText("Год")).toHaveValue(
			"2022",
		);
	} finally {
		window.location.hash = previousHash;
		fireEvent(window, new Event("hashchange"));
	}
});

it("shows artist cards with a separate aggregate checkbox and real artist link", () => {
	render(<CatalogDemo />);
	const artistLink = screen.getByRole("link", { name: /Mira Vale/ });
	let artistCard = artistLink.parentElement;
	while (
		artistCard &&
		!within(artistCard).queryByRole("checkbox", {
			name: "Выбрать позиции артиста Mira Vale",
		})
	)
		artistCard = artistCard.parentElement;
	if (!artistCard) throw new Error("Missing Mira Vale artist card");

	expect(artistLink).toHaveAttribute("href", "#artist/mira");
	expect(within(artistCard).getAllByRole("link")).toContain(artistLink);
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
	window.location.hash = "track/north-t1";
	try {
		render(<CatalogDemo />);
		const publicationPanel = screen
			.getByRole("heading", { name: "Публикация" })
			.closest("section");
		if (!publicationPanel) throw new Error("Missing track publication panel");
		fireEvent.click(
			within(publicationPanel).getByRole("button", {
				name: "Предпросмотр публикации",
			}),
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
	window.location.hash = "track/north-t1";
	try {
		render(<CatalogDemo />);
		const publicationPanel = screen
			.getByRole("heading", { name: "Публикация" })
			.closest("section");
		if (!publicationPanel) throw new Error("Missing track publication panel");
		fireEvent.click(
			within(publicationPanel).getByRole("button", {
				name: "Предпросмотр публикации",
			}),
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
	window.location.hash = "track/north-t1";
	try {
		render(<CatalogDemo />);
		const publicationPanel = screen
			.getByRole("heading", { name: "Публикация" })
			.closest("section");
		if (!publicationPanel) throw new Error("Missing track publication panel");
		fireEvent.click(
			within(publicationPanel).getByRole("button", {
				name: "Предпросмотр публикации",
			}),
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
		const localPreview = screen.queryByRole("button", {
			name: "Предпросмотр 0 выбранных позиций",
		});
		expect(localPreview).not.toBeInTheDocument();
		fireEvent.click(
			within(
				screen.getByRole("region", { name: "Выбранные позиции" }),
			).getByRole("button", {
				name: "Опубликовать",
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

it("migrates a legacy release route to its artist with the album expanded", async () => {
	const previousHash = window.location.hash;
	window.location.hash = "release/north";
	try {
		render(<CatalogDemo />);
		expect(
			await screen.findByRole("heading", { name: "Mira Vale" }),
		).toBeInTheDocument();
		expect(window.location.hash).toBe("#artist/mira?release=north");
		const album = screen
			.getAllByRole("button", {
				name: (name) =>
					name.includes("Northern Lines") &&
					name.includes("2021") &&
					name.includes("FR-021"),
			})
			.find((button) => button.getAttribute("slot") === "trigger");
		if (!album) throw new Error("Missing Northern Lines disclosure trigger");
		expect(album).toHaveAttribute("aria-expanded", "true");
		expect(screen.getByRole("link", { name: /First Light/ })).toHaveAttribute(
			"href",
			"#track/north-t1",
		);
	} finally {
		window.location.hash = previousHash;
		fireEvent(window, new Event("hashchange"));
	}
});

it.each([
	["artist", "Сведения об артисте"],
	["release", "Сведения об издании"],
])("opens %s details inline from the legacy tag route", async (scope, region) => {
	const previousHash = window.location.hash;
	window.location.hash = `tags/north-t1/${scope}`;
	try {
		render(<CatalogDemo />);
		expect(
			await screen.findByRole("heading", { name: "Mira Vale" }),
		).toBeInTheDocument();
		expect(window.location.hash).toBe(
			`#artist/mira?tags=${scope}&release=north`,
		);
		const details = screen.getByRole("region", { name: region });
		for (const field of within(details).getAllByRole("textbox")) {
			expect(field).toHaveAttribute("readonly");
		}
		expect(
			screen.queryByRole("region", { name: /^Сравнение тегов:/ }),
		).not.toBeInTheDocument();
		expect(
			screen.queryByRole("columnheader", {
				name: "Исходный файл",
			}),
		).not.toBeInTheDocument();
		expect(
			screen.queryByRole("columnheader", {
				name: "Сейчас в публикации",
			}),
		).not.toBeInTheDocument();
		expect(screen.getByRole("link", { name: /First Light/ })).toHaveAttribute(
			"href",
			"#track/north-t1",
		);
	} finally {
		window.location.hash = previousHash;
		fireEvent(window, new Event("hashchange"));
	}
});

it("keeps the legacy track-tag route on the standalone tags screen", async () => {
	const previousHash = window.location.hash;
	window.location.hash = "tags/north-t1/track";
	try {
		render(<CatalogDemo />);
		expect(
			await screen.findByRole("heading", { name: "Сравнение тегов" }),
		).toBeInTheDocument();
		expect(window.location.hash).toBe("#tags/north-t1/track");
	} finally {
		window.location.hash = previousHash;
		fireEvent(window, new Event("hashchange"));
	}
});

it("keeps track-tag links available independently of artist and release metadata", () => {
	render(<CatalogDemo />);
	fireEvent.click(screen.getByRole("tab", { name: "Все треки" }));
	expect(screen.getAllByRole("link", { name: "Теги" })[0]).toHaveAttribute(
		"href",
		"#tags/north-t1/track",
	);
});
