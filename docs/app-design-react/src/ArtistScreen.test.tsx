import { fireEvent, render, screen } from "@testing-library/react";
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
		screen.getAllByRole("link", { name: "First Light" })[0],
	).toHaveAttribute("href", "#track/north-t1");
	expect(screen.getByText(/Аудио по изданиям:/)).toBeInTheDocument();
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
	expect(screen.getByRole("row", { name: /Blue Atlas/ })).toBeInTheDocument();
	fireEvent.change(screen.getByRole("combobox", { name: "Наличие" }), {
		target: { value: "available" },
	});
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
			.getAllByRole("row")
			.find((row) => row.querySelector(`a[href="#track/${id}"]`));
	const availableRow = rowFor(availableID);
	const missingRow = rowFor(missingID);
	expect(availableRow).toBeTruthy();
	expect(missingRow).toBeTruthy();
	expect(availableRow).toHaveClass("filter-match");
	expect(missingRow).toHaveClass("filter-muted");
	expect(screen.getByRole("combobox", { name: "Наличие" })).toHaveValue(
		"available",
	);
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
	expect(screen.getByRole("combobox", { name: "Публикация" })).toHaveValue(
		"published",
	);
	expect(rowFor(availableID)).not.toHaveClass("filter-match", "filter-muted");
	expect(rowFor(missingID)).not.toHaveClass("filter-match", "filter-muted");
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
	expect(screen.getByRole("row", { name: /First Light/ })).toHaveTextContent(
		"1.01",
	);
	expect(screen.getByRole("row", { name: /Still Water/ })).toHaveTextContent(
		"2.01",
	);
});
