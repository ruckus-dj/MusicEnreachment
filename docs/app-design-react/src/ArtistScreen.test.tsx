import { fireEvent, render, screen, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { ArtistScreen } from "./ArtistScreen";
import {
	catalogData,
	desiredTags,
	type OriginalRelease,
	originalFiles,
	originalReleases,
} from "./catalogData";

function expandAlbum(release: OriginalRelease) {
	const trigger = screen
		.getAllByRole("button", {
			name: (name) =>
				name.includes(release.title) &&
				name.includes(String(release.year)) &&
				name.includes(release.catalog ?? ""),
		})
		.find((button) => button.getAttribute("slot") === "trigger");
	if (!trigger)
		throw new Error(`Missing disclosure trigger for ${release.title}`);
	fireEvent.click(trigger);
}

function commonAncestor(first: Element, second: Element): HTMLElement | null {
	let ancestor: HTMLElement | null = first.parentElement;
	while (ancestor && !ancestor.contains(second))
		ancestor = ancestor.parentElement;
	return ancestor;
}

it("keeps publication summaries compact in the artist track table", () => {
	const release = originalReleases.find((item) => item.id === "north");
	const track = release?.tracks.find((item) => item.id === "north-t1");
	if (!release || !track?.published)
		throw new Error("Missing published track fixture");
	render(
		<ArtistScreen
			artistId="mira"
			artists={catalogData.artists}
			releases={[release]}
			files={originalFiles}
		/>,
	);
	expandAlbum(release);
	expect(screen.getByRole("link", { name: track.title })).toHaveAttribute(
		"href",
		`#track/${track.id}`,
	);
	expect(
		screen.getAllByText(new RegExp(track.published.codec)).length,
	).toBeGreaterThan(0);
	expect(screen.queryByText(track.published.path)).not.toBeInTheDocument();
	expect(
		screen.queryByText("Путь и снимок публикации"),
	).not.toBeInTheDocument();
});

it("renders artist releases and tracks with aggregate selection", () => {
	const release = originalReleases.find((item) => item.id === "north");
	if (!release) throw new Error("Missing baseline fixture");
	const onSelect = vi.fn();
	render(
		<ArtistScreen
			artistId={release.artistId ?? ""}
			artists={catalogData.artists}
			releases={originalReleases}
			files={originalFiles}
			selected={new Set(["north-t1"])}
			onSelect={onSelect}
		/>,
	);
	expandAlbum(release);
	expect(
		screen.getByRole("heading", {
			name: catalogData.artists.find((artist) => artist.id === release.artistId)
				?.name,
		}),
	).toBeInTheDocument();
	expect(
		screen.getAllByRole("link", { name: /First Light/ })[0],
	).toHaveAttribute("href", "#track/north-t1");
	expect(
		screen.queryByRole("button", { name: /Предпросмотр/ }),
	).not.toBeInTheDocument();
	fireEvent.click(
		screen.getByRole("checkbox", { name: "Выбрать Still Water (local edit)" }),
	);
	expect(onSelect).toHaveBeenCalledWith(["north-t2"], true);
});

it("shows always-visible artist and release metadata forms", () => {
	const release = originalReleases.find((item) => item.id === "north");
	if (!release) throw new Error("Missing baseline fixture");
	render(
		<ArtistScreen
			artistId={release.artistId ?? ""}
			artists={catalogData.artists}
			releases={originalReleases}
			files={originalFiles}
		/>,
	);
	const artistDetails = screen.getByRole("region", {
		name: "Сведения об артисте",
	});
	const releaseTrigger = screen
		.getAllByRole("button", {
			name: (name) =>
				name.includes(release.title) && name.includes(String(release.year)),
		})
		.find((button) => button.getAttribute("slot") === "trigger");
	if (!releaseTrigger)
		throw new Error(`Missing disclosure trigger for ${release.title}`);
	expect(releaseTrigger).toHaveAttribute("aria-expanded", "false");
	expandAlbum(release);
	const releaseDetails = screen.getByRole("region", {
		name: "Сведения об издании",
	});
	expect(within(artistDetails).getByLabelText("Имя")).toHaveValue(
		catalogData.artists.find((artist) => artist.id === release.artistId)?.name,
	);
	expect(within(artistDetails).getByText("Жанры")).toBeInTheDocument();
	expect(
		within(artistDetails).getByLabelText("Имя для сортировки"),
	).toHaveAttribute("readonly");
	expect(within(artistDetails).getByLabelText("Уточнение")).toHaveAttribute(
		"readonly",
	);
	expect(within(releaseDetails).getByLabelText("Название")).toHaveValue(
		release.title,
	);
	expect(within(releaseDetails).getByLabelText("Год")).toHaveValue(
		release.year,
	);
	expect(within(releaseDetails).getByLabelText("Страна")).toHaveValue(
		release.country,
	);
	expect(within(releaseDetails).getByLabelText("Лейбл")).toHaveValue(
		release.label,
	);
	expect(within(releaseDetails).getByLabelText("Каталожный номер")).toHaveValue(
		release.catalog,
	);
	for (const input of [
		...within(artistDetails).getAllByRole("textbox"),
		...within(releaseDetails).getAllByRole("textbox"),
	])
		expect(input).toHaveAttribute("readonly");
	expect(
		screen.getByRole("button", {
			name: (name) =>
				name.includes(release.title) && name.includes(String(release.year)),
		}),
	).toHaveAttribute("aria-expanded", "true");
	expect(
		screen.queryByRole("button", { name: "Предпросмотр публикации" }),
	).not.toBeInTheDocument();

	expect(
		screen.queryByRole("region", { name: "Сравнение тегов: artist" }),
	).not.toBeInTheDocument();
	expect(
		screen.queryByRole("region", { name: "Сравнение тегов: release" }),
	).not.toBeInTheDocument();
});

it("saves artist-owned sort metadata and canonical genres without a sort-name fallback", () => {
	const artist = {
		id: "artist-test",
		name: "Artist Name",
		sortName: "Name, Artist",
		disambiguation: "Live project",
		genres: [
			{ id: "demo-genre-rock", name: "rock" },
			{ id: "legacy-genre-id", name: "Unavailable genre" },
		],
		genre: "Legacy genre",
		tags: ["ambient", "instrumental"],
	};
	const onUpdateArtist = vi.fn();
	const release = {
		...originalReleases[0],
		artistId: artist.id,
		tracks: originalReleases[0].tracks.map((track) => ({
			...track,
			artistId: artist.id,
		})),
	};
	render(
		<ArtistScreen
			artistId={artist.id}
			artists={[artist]}
			releases={[release]}
			files={originalFiles}
			genresLoaded
			onUpdateArtist={onUpdateArtist}
		/>,
	);
	const details = screen.getByRole("region", { name: "Сведения об артисте" });
	expect(desiredTags(release, release.tracks[0], [artist]).ARTISTSORT).toEqual([
		"Name, Artist",
	]);
	const noSortName = { ...artist, sortName: undefined };
	expect(
		desiredTags(release, release.tracks[0], [noSortName]).ARTISTSORT,
	).toEqual([]);

	fireEvent.click(
		within(details).getByRole("button", { name: "Редактировать" }),
	);
	fireEvent.change(within(details).getByLabelText("Имя для сортировки"), {
		target: { value: "Artist, Name" },
	});
	fireEvent.change(within(details).getByLabelText("Теги"), {
		target: { value: "ambient, live" },
	});
	fireEvent.click(within(details).getByRole("button", { name: "Жанры" }));
	fireEvent.click(within(details).getByLabelText("electronic"));
	fireEvent.click(within(details).getByRole("button", { name: "Сохранить" }));
	expect(onUpdateArtist).toHaveBeenCalledWith(
		expect.objectContaining({
			sortName: "Artist, Name",
			disambiguation: "Live project",
			tags: ["ambient", "live"],
			genre: "Legacy genre",
			genres: [
				{ id: "demo-genre-rock", name: "rock" },
				{ id: "legacy-genre-id", name: "Unavailable genre" },
				{ id: "demo-genre-electronic", name: "electronic" },
			],
		}),
	);
});

it("preserves legacy and unavailable genre references when editing without a loaded picker", () => {
	const artist = {
		id: "artist-unloaded-genres",
		name: "Artist Name",
		genre: "Legacy genre",
		genres: [{ id: "unavailable-id", name: "Unavailable genre" }],
	};
	const onUpdateArtist = vi.fn();
	const release = {
		...originalReleases[0],
		artistId: artist.id,
		tracks: originalReleases[0].tracks.map((track) => ({
			...track,
			artistId: artist.id,
		})),
	};
	render(
		<ArtistScreen
			artistId={artist.id}
			artists={[artist]}
			releases={[release]}
			files={originalFiles}
			onUpdateArtist={onUpdateArtist}
		/>,
	);
	const details = screen.getByRole("region", { name: "Сведения об артисте" });
	fireEvent.click(
		within(details).getByRole("button", { name: "Редактировать" }),
	);
	expect(within(details).getByRole("button", { name: "Жанры" })).toBeDisabled();
	fireEvent.change(within(details).getByLabelText("Имя"), {
		target: { value: "Renamed Artist" },
	});
	fireEvent.click(within(details).getByRole("button", { name: "Сохранить" }));
	expect(onUpdateArtist).toHaveBeenCalledWith(
		expect.objectContaining({
			name: "Renamed Artist",
			genre: "Legacy genre",
			genres: [{ id: "unavailable-id", name: "Unavailable genre" }],
		}),
	);
});

it("keeps empty album disclosures available without preview actions", () => {
	const base = originalReleases.find((item) => item.id === "north");
	if (!base) throw new Error("Missing baseline fixture");
	const noTracksRelease = { ...base, id: "empty-album", tracks: [] };
	render(
		<ArtistScreen
			artistId={base.artistId ?? ""}
			artists={catalogData.artists}
			releases={[noTracksRelease]}
			files={originalFiles}
		/>,
	);
	expandAlbum(noTracksRelease);
	expect(
		screen.getByRole("button", {
			name: (name) =>
				name.includes(noTracksRelease.title) &&
				name.includes(String(noTracksRelease.year)),
		}),
	).toHaveAttribute("aria-expanded", "true");
	expect(
		screen.queryByRole("button", { name: "Теги" }),
	).not.toBeInTheDocument();
});

it("filters track rows and searches absent editions without adding them to the library", () => {
	const artist = catalogData.artists.find((item) => item.id === "mira");
	if (!artist) throw new Error("Missing artist fixture");
	render(
		<ArtistScreen
			artistId={artist.id}
			artists={catalogData.artists}
			releases={originalReleases}
			files={originalFiles}
		/>,
	);
	expect(
		screen.getByRole("heading", { name: "Отсутствующие альбомы" }),
	).toBeInTheDocument();
	fireEvent.change(
		screen.getByRole("searchbox", { name: "Поиск в медиатеке" }),
		{ target: { value: "Blue Atlas" } },
	);
	expect(screen.getByText("Blue Atlas")).toBeInTheDocument();
	fireEvent.click(screen.getByRole("radio", { name: "В наличии" }));
	expect(
		screen.queryByRole("heading", { name: "Отсутствующие альбомы" }),
	).not.toBeInTheDocument();
});

it("emphasizes rows for active availability and publication filters together", () => {
	const release = originalReleases.find((item) => item.id === "north");
	if (!release) throw new Error("Missing baseline fixture");
	const availablePublishedID = release.tracks.find(
		(track) =>
			Boolean(track.published) &&
			track.sourceId &&
			originalFiles[track.sourceId]?.available &&
			originalFiles[track.sourceId]?.locations?.length,
	)?.id;
	const availableUnpublishedID = release.tracks.find(
		(track) =>
			!track.published &&
			track.sourceId &&
			originalFiles[track.sourceId]?.available &&
			originalFiles[track.sourceId]?.locations?.length,
	)?.id;
	const missingUnpublishedID = release.tracks.find(
		(track) =>
			!track.published &&
			!(
				track.sourceId &&
				originalFiles[track.sourceId]?.available &&
				originalFiles[track.sourceId]?.locations?.length
			),
	)?.id;
	if (!availablePublishedID || !availableUnpublishedID || !missingUnpublishedID)
		throw new Error("Missing availability fixture");
	const { rerender } = render(
		<ArtistScreen
			artistId={release.artistId ?? ""}
			artists={catalogData.artists}
			releases={originalReleases}
			files={originalFiles}
			filter="available"
		/>,
	);
	expandAlbum(release);
	const rowFor = (id: string) =>
		screen
			.getAllByRole("table")
			.flatMap((table) => [...table.querySelectorAll("tbody tr")])
			.find((row) => row.querySelector(`a[href="#track/${id}"]`));
	const rowForPublished = rowFor(availablePublishedID);
	const rowForUnpublished = rowFor(availableUnpublishedID);
	const rowForMissing = rowFor(missingUnpublishedID);
	expect(rowForPublished).toHaveClass("filter-match");
	expect(rowForUnpublished).toHaveClass("filter-match");
	expect(rowForMissing).toHaveClass("filter-muted");
	expect(
		within(screen.getByRole("radiogroup", { name: "Состояние" })).getByRole(
			"radio",
			{ name: "В наличии" },
		),
	).toBeChecked();
	rerender(
		<ArtistScreen
			artistId={release.artistId ?? ""}
			artists={catalogData.artists}
			releases={originalReleases}
			files={originalFiles}
			filter="published"
		/>,
	);
	expect(rowFor(availablePublishedID)).toHaveClass("filter-match");
	expect(rowFor(availableUnpublishedID)).toHaveClass("filter-muted");
	expect(rowForMissing).toHaveClass("filter-muted");
	rerender(
		<ArtistScreen
			artistId={release.artistId ?? ""}
			artists={catalogData.artists}
			releases={originalReleases}
			files={originalFiles}
			filter="published"
		/>,
	);
	expect(
		within(screen.getByRole("radiogroup", { name: "Состояние" })).getByRole(
			"radio",
			{ name: "Опубликовано" },
		),
	).toBeChecked();
	expect(rowFor(availablePublishedID)).toHaveClass("filter-match");
	expect(rowFor(availableUnpublishedID)).toHaveClass("filter-muted");
	expect(rowFor(missingUnpublishedID)).toHaveClass("filter-muted");
	expect(screen.queryByText(/По фильтру|Вне фильтра/)).not.toBeInTheDocument();
});

it("reports changes through the unified artist filter and retains collapsed albums", () => {
	const artist = catalogData.artists.find((item) => item.id === "mira");
	if (!artist) throw new Error("Missing artist fixture");
	const artistRelease = originalReleases.find(
		(release) =>
			release.artistId === artist.id ||
			release.credits?.some((credit) => credit.artistId === artist.id),
	);
	if (!artistRelease || artistRelease.tracks.length < 2)
		throw new Error("Missing multi-track artist release fixture");
	const onFilterChange = vi.fn();
	render(
		<ArtistScreen
			artistId={artist.id}
			artists={catalogData.artists}
			releases={originalReleases}
			files={originalFiles}
			selected={new Set([artistRelease.tracks[0].id])}
			onFilterChange={onFilterChange}
		/>,
	);
	const filterGroup = screen.getByRole("radiogroup", { name: "Состояние" });
	fireEvent.click(
		within(filterGroup).getByRole("radio", { name: "Не хватает" }),
	);
	fireEvent.click(
		within(filterGroup).getByRole("radio", { name: "Опубликовано" }),
	);
	expect(onFilterChange).toHaveBeenNthCalledWith(1, "missing");
	expect(onFilterChange).toHaveBeenNthCalledWith(2, "published");
	const albumCheckbox = screen.getAllByRole("checkbox", {
		name: `Выбрать все позиции альбома ${artistRelease.title} · ${artistRelease.year} · ${artistRelease.catalog || "—"}`,
	})[0];
	expect(albumCheckbox).toBePartiallyChecked();
	const albumTrigger = screen.getByRole("button", {
		name: `${artistRelease.title} · ${artistRelease.year} · ${artistRelease.catalog || "—"}`,
	});
	const albumGroup = commonAncestor(albumCheckbox, albumTrigger);
	expect(albumGroup).not.toBeNull();
	expect(albumGroup).toContainElement(albumCheckbox);
	expect(albumGroup).toContainElement(albumTrigger);
	expect(albumTrigger).toHaveAttribute("aria-expanded", "false");
	if (!albumGroup) throw new Error("Missing album group");
	expect(within(albumGroup).queryByRole("table")).toBeNull();
	fireEvent.click(albumTrigger);
	expect(albumTrigger).toHaveAttribute("aria-expanded", "true");
	const heroCheckbox = screen.getByRole("checkbox", {
		name: `Выбрать все позиции артиста ${artist.name}`,
	});
	expect(heroCheckbox).toBeVisible();
	expect(heroCheckbox.closest('[aria-hidden="true"]')).toBeNull();
	expect(screen.queryByText(/Аудио по изданиям:/)).not.toBeInTheDocument();
	expect(
		albumTrigger.querySelector('[aria-hidden="true"]'),
	).toBeInTheDocument();
});

it("selects only the album's tracks and keeps the artist aggregate partial", () => {
	const release = originalReleases.find((item) => item.id === "north");
	if (!release || release.tracks.length !== 10)
		throw new Error("Expected the ten-track Northern Lines fixture");
	const firstTrack = release.tracks[0];
	if (!firstTrack) throw new Error("Missing baseline track");
	const onSelect = vi.fn();
	render(
		<ArtistScreen
			artistId="mira"
			artists={catalogData.artists}
			releases={originalReleases}
			files={originalFiles}
			selected={new Set([firstTrack.id])}
			onSelect={onSelect}
		/>,
	);
	const album = screen.getByRole("checkbox", {
		name: `Выбрать все позиции альбома ${release.title} · ${release.year} · ${release.catalog || "—"}`,
	});
	expect(album).toBePartiallyChecked();
	const artist = screen.getByRole("checkbox", {
		name: "Выбрать все позиции артиста Mira Vale",
	});
	expect(artist).toBePartiallyChecked();
	expect(
		screen.queryByText("Выбрать", { selector: "th" }),
	).not.toBeInTheDocument();
	fireEvent.click(album);
	expect(onSelect).toHaveBeenCalledWith(
		release.tracks.map((track) => track.id),
		true,
	);
	expect(onSelect.mock.calls[0][0]).toHaveLength(10);
	expect(onSelect.mock.calls[0][0]).not.toHaveLength(29);
});

it("numbers tracks by medium for a cloned multi-medium release without displayed numbers", () => {
	const base = originalReleases.find((item) => item.id === "north");
	if (!base) throw new Error("Missing baseline fixture");
	const release: OriginalRelease = structuredClone(base);
	release.id = "numbering-test";
	release.media = [
		{ title: "One", format: "CD", positions: [] },
		{ title: "Two", format: "CD", positions: [] },
	];
	release.tracks = release.tracks.slice(0, 2).map((track, index) => ({
		...track,
		id: `numbering-${index}`,
		displayedNumber: undefined,
		medium: index + 1,
		mediumPosition: 1,
	}));
	render(
		<ArtistScreen
			artistId={release.artistId ?? ""}
			artists={catalogData.artists}
			releases={[release]}
			files={originalFiles}
		/>,
	);
	expandAlbum(release);
	const table = screen.getAllByRole("table")[0];
	const rows = [...table.querySelectorAll("tbody tr")];
	expect(
		rows.find((row) => row.textContent?.includes("First Light")),
	).toHaveTextContent("1.01");
	expect(
		rows.find((row) => row.textContent?.includes("Still Water")),
	).toHaveTextContent("2.01");
});

it("selects a collapsed album without opening it", () => {
	const onSelect = vi.fn();
	render(
		<ArtistScreen
			artistId="mira"
			artists={catalogData.artists}
			releases={originalReleases}
			files={originalFiles}
			onSelect={onSelect}
		/>,
	);
	const release = originalReleases.filter(
		(item) => item.artistId === "mira",
	)[1];
	if (!release) throw new Error("Missing collapsed album fixture");
	const trigger = screen.getByRole("button", {
		name: `${release.title} · ${release.year} · ${release.catalog || "—"}`,
	});
	expect(trigger).toHaveAttribute("aria-expanded", "false");
	const checkbox = screen.getByRole("checkbox", {
		name: `Выбрать все позиции альбома ${release.title} · ${release.year} · ${release.catalog || "—"}`,
	});
	const card = commonAncestor(checkbox, trigger);
	if (!card) throw new Error("Album controls do not share a card");
	expect(checkbox).toBeVisible();
	fireEvent.click(checkbox);
	expect(onSelect).toHaveBeenCalled();
	expect(trigger).toHaveAttribute("aria-expanded", "false");
});

it("opens album deletion from a collapsed card without expanding or selecting it", () => {
	const release = originalReleases.find((item) => item.id === "north");
	if (!release) throw new Error("Missing baseline fixture");
	const onQueueDeletion = vi.fn();
	const onSelect = vi.fn();
	render(
		<ArtistScreen
			artistId={release.artistId ?? ""}
			artists={catalogData.artists}
			releases={[release]}
			files={originalFiles}
			onQueueDeletion={onQueueDeletion}
			onSelect={onSelect}
		/>,
	);
	const deleteButton = screen.getByRole("button", {
		name: `Удалить альбом ${release.title} · ${release.year} · ${release.catalog || "—"}`,
	});
	const expandTrigger = screen.getByRole("button", {
		name: `${release.title} · ${release.year} · ${release.catalog || "—"}`,
	});
	const card = commonAncestor(deleteButton, expandTrigger);
	if (!(card instanceof HTMLElement)) throw new Error("Missing album card");
	expect(expandTrigger).toHaveAttribute("aria-expanded", "false");
	fireEvent.click(
		within(card).getByRole("button", {
			name: `Удалить альбом ${release.title} · ${release.year} · ${release.catalog || "—"}`,
		}),
	);
	expect(expandTrigger).toHaveAttribute("aria-expanded", "false");
	expect(onSelect).not.toHaveBeenCalled();
	expect(screen.getByRole("dialog")).toBeInTheDocument();
	fireEvent.click(screen.getByRole("button", { name: "Отмена" }));
	expect(onQueueDeletion).not.toHaveBeenCalled();
});

it("keeps album-level duration, observed-file evidence, and deletion scope on the artist route", () => {
	const base = originalReleases.find((item) => item.id === "north");
	if (!base) throw new Error("Missing baseline fixture");
	const release: OriginalRelease = structuredClone(base);
	release.tracks[0] = {
		...release.tracks[0],
		observedFile: { state: "modified" },
	};
	const onQueueDeletion = vi.fn();
	render(
		<ArtistScreen
			artistId={release.artistId ?? ""}
			artists={catalogData.artists}
			releases={[release]}
			files={originalFiles}
			onQueueDeletion={onQueueDeletion}
		/>,
	);
	expandAlbum(release);
	expect(screen.getByText(/всего/)).toBeInTheDocument();
	const firstTrackRow = screen
		.getAllByRole("table")
		.flatMap((table) => [...table.querySelectorAll("tbody tr")])
		.find((row) => row.querySelector('a[href="#track/north-t1"]'));
	if (!(firstTrackRow instanceof HTMLElement))
		throw new Error("Missing first track row");
	expect(
		within(firstTrackRow).getByText("Файл изменён вне приложения"),
	).toBeInTheDocument();
	fireEvent.click(screen.getByRole("button", { name: /Удалить альбом/ }));
	expect(screen.getByText(/Позиции релиза/)).toBeInTheDocument();
	fireEvent.click(screen.getByRole("button", { name: "Подтвердить" }));
	expect(onQueueDeletion).toHaveBeenCalledWith(
		expect.objectContaining({ kind: "library", target: release.title }),
	);
});

it("starts collapsed and resets expansion when changing artists", () => {
	const props = {
		artists: catalogData.artists,
		releases: originalReleases,
		files: originalFiles,
	};
	const { container, rerender } = render(
		<ArtistScreen {...props} artistId="mira" />,
	);
	const triggers = () => [
		...container.querySelectorAll('button[slot="trigger"]'),
	];
	expect(triggers().length).toBeGreaterThan(0);
	expect(
		triggers().every((item) => item.getAttribute("aria-expanded") === "false"),
	).toBe(true);
	expandAlbum(originalReleases.find((item) => item.id === "north")!);
	expect(
		triggers().some((item) => item.getAttribute("aria-expanded") === "true"),
	).toBe(true);
	const other = catalogData.artists.find(
		(item) =>
			item.id !== "mira" &&
			originalReleases.some((release) => release.artistId === item.id),
	);
	if (!other) throw new Error("Missing second artist fixture");
	rerender(<ArtistScreen {...props} artistId={other.id} />);
	expect(triggers().length).toBeGreaterThan(0);
	expect(
		triggers().every((item) => item.getAttribute("aria-expanded") === "false"),
	).toBe(true);
	rerender(<ArtistScreen {...props} artistId="mira" />);
	expect(
		triggers().every((item) => item.getAttribute("aria-expanded") === "false"),
	).toBe(true);
});
