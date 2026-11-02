import { fireEvent, render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { catalogData, originalFiles, originalReleases } from "./catalogData";
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
