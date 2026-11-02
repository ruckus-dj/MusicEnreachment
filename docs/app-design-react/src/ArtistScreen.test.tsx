import { fireEvent, render, screen, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import {
	catalogData,
	originalFiles,
	originalReleases,
	type OriginalRelease,
} from "./catalogData";
import { ArtistScreen } from "./ArtistScreen";

it("renders artist releases and tracks, selection and publication preview actions", () => {
	const release = originalReleases.find((item) => item.id === "north");
	if (!release) throw new Error("Missing baseline fixture");
	const onSelect = vi.fn();
	const onPreview = vi.fn();
	render(
		<ArtistScreen
			artistId={release.artistId ?? ""}
			artists={catalogData.artists}
			releases={originalReleases}
			files={originalFiles}
			selected={new Set(["north-t1"])}
			onSelect={onSelect}
			onPreview={onPreview}
		/>,
	);
	expect(
		screen.getByRole("heading", {
			name: catalogData.artists.find((artist) => artist.id === release.artistId)
				?.name,
		}),
	).toBeInTheDocument();
	expect(
		screen.getAllByRole("link", { name: /First Light/ })[0],
	).toHaveAttribute("href", "#track/north-t1");
	fireEvent.click(
		screen.getByRole("button", {
			name: "Предпросмотр 1 выбранных позиций",
		}),
	);
	expect(onPreview).toHaveBeenCalledWith(["north-t1"]);
	fireEvent.click(
		screen.getByRole("checkbox", { name: "Выбрать Still Water (local edit)" }),
	);
	expect(onSelect).toHaveBeenCalledWith(["north-t2"], true);
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

it("uses shared availability and publication props while only availability emphasizes rows", () => {
	const release = originalReleases.find((item) => item.id === "north");
	if (!release) throw new Error("Missing baseline fixture");
	const availableID = release.tracks.find(
		(track) =>
			track.sourceId &&
			originalFiles[track.sourceId]?.available &&
			originalFiles[track.sourceId]?.locations?.length,
	)?.id;
	const missingID = release.tracks.find(
		(track) =>
			!track.published &&
			!(
				track.sourceId &&
				originalFiles[track.sourceId]?.available &&
				originalFiles[track.sourceId]?.locations?.length
			),
	)?.id;
	if (!availableID || !missingID)
		throw new Error("Missing availability fixture");
	const { rerender } = render(
		<ArtistScreen
			artistId={release.artistId ?? ""}
			artists={catalogData.artists}
			releases={originalReleases}
			files={originalFiles}
			availability="available"
			publication="all"
		/>,
	);
	const rowFor = (id: string) =>
		screen
			.getAllByRole("table")
			.flatMap((table) => [...table.querySelectorAll("tbody tr")])
			.find((row) => row.querySelector(`a[href="#track/${id}"]`));
	const availableRow = rowFor(availableID);
	const missingRow = rowFor(missingID);
	expect(availableRow).toBeTruthy();
	expect(missingRow).toBeTruthy();
	expect(availableRow).toHaveClass("filter-match");
	expect(missingRow).toHaveClass("filter-muted");
	expect(screen.getByRole("radio", { name: "В наличии" })).toBeChecked();
	rerender(
		<ArtistScreen
			artistId={release.artistId ?? ""}
			artists={catalogData.artists}
			releases={originalReleases}
			files={originalFiles}
			availability="all"
			publication="published"
		/>,
	);
	expect(
		within(screen.getByRole("radiogroup", { name: "Публикация" })).getByRole(
			"radio",
			{ name: "Есть" },
		),
	).toBeChecked();
	expect(rowFor(availableID)).not.toHaveClass("filter-match", "filter-muted");
	expect(rowFor(missingID)).not.toHaveClass("filter-match", "filter-muted");
});

it("keeps artist availability and publication filters independently selectable", () => {
	const artist = catalogData.artists.find((item) => item.id === "mira");
	if (!artist) throw new Error("Missing artist fixture");
	const artistRelease = originalReleases.find(
		(release) =>
			release.artistId === artist.id ||
			release.credits?.some((credit) => credit.artistId === artist.id),
	);
	if (!artistRelease || artistRelease.tracks.length < 2)
		throw new Error("Missing multi-track artist release fixture");
	const onAvailabilityChange = vi.fn();
	const onPublicationChange = vi.fn();
	const { container } = render(
		<ArtistScreen
			artistId={artist.id}
			artists={catalogData.artists}
			releases={originalReleases}
			files={originalFiles}
			selected={new Set([artistRelease.tracks[0].id])}
			onAvailabilityChange={onAvailabilityChange}
			onPublicationChange={onPublicationChange}
		/>,
	);
	fireEvent.click(screen.getByRole("radio", { name: "Не хватает" }));
	fireEvent.click(
		within(screen.getByRole("radiogroup", { name: "Публикация" })).getByRole(
			"radio",
			{ name: "Есть" },
		),
	);
	expect(onAvailabilityChange).toHaveBeenCalledWith("missing");
	expect(onPublicationChange).toHaveBeenCalledWith("published");
	const albumCheckbox = screen.getAllByRole("checkbox", {
		name: `Выбрать все позиции альбома ${artistRelease.title}`,
	})[0];
	expect(albumCheckbox).toBePartiallyChecked();
	expect(albumCheckbox.closest(".artist-album-card")).not.toBeNull();
	expect(albumCheckbox.closest(".catalogue-edition-trigger")).toBeNull();
	expect(albumCheckbox.closest(".ui-disclosure-panel")).toBeNull();
	const heroCheckbox = screen.getByRole("checkbox", {
		name: `Выбрать все позиции артиста ${artist.name}`,
	});
	expect(heroCheckbox.closest(".artist-hero-art")).not.toBeNull();
	expect(heroCheckbox.closest('[aria-hidden="true"]')).toBeNull();
	expect(screen.queryByText(/Аудио по изданиям:/)).not.toBeInTheDocument();
	expect(container.querySelector(".artist-album-cover")).toBeInTheDocument();
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
	const { container } = render(
		<ArtistScreen
			artistId="mira"
			artists={catalogData.artists}
			releases={originalReleases}
			files={originalFiles}
			onSelect={onSelect}
		/>,
	);
	const card = container.querySelectorAll(".artist-album-card")[1];
	if (!(card instanceof HTMLElement))
		throw new Error("Missing collapsed album fixture");
	const trigger = card.querySelector('button[slot="trigger"]');
	expect(trigger).toHaveAttribute("aria-expanded", "false");
	const checkbox = within(card).getByRole("checkbox");
	expect(checkbox).toBeVisible();
	fireEvent.click(checkbox);
	expect(onSelect).toHaveBeenCalled();
	expect(trigger).toHaveAttribute("aria-expanded", "false");
});
