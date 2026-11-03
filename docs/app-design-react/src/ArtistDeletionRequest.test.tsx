import { fireEvent, render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import type {
	CatalogArtist,
	OriginalRelease,
	OriginalTrack,
} from "./catalogData";
import {
	ArtistDeletionRequest,
	artistDeletionPlan,
} from "./ArtistDeletionRequest";

const artist: CatalogArtist = { id: "artist-a", name: "Artist A" };

function track(
	id: string,
	artistId: string | null,
	recordingId: string,
	path?: string,
): OriginalTrack {
	return {
		id,
		title: id,
		position: 1,
		duration: 180,
		artistId,
		recordingId,
		sourceId: null,
		published: path
			? {
					tags: {},
					codec: "FLAC",
					container: "MKA",
					sourceId: null,
					path,
					readAt: "now",
					coverHash: null,
					lyrics: null,
				}
			: null,
	};
}

function release(
	id: string,
	title: string,
	artistId: string | null,
	tracks: OriginalTrack[],
	credits?: OriginalRelease["credits"],
): OriginalRelease {
	return {
		id,
		title,
		artistId,
		year: "2020",
		country: "",
		catalog: "",
		label: "",
		providerId: null,
		credits,
		tracks,
	};
}

const releases = [
	release("primary", "Primary album", artist.id, [
		track("direct-position", artist.id, "direct-recording", "/owned.mka"),
		track("other-credit-position", "artist-b", "other-recording"),
	]),
	release(
		"credited",
		"Credited album",
		"artist-b",
		[
			track(
				"credited-position",
				"artist-b",
				"credited-recording",
				"/credited.mka",
			),
		],
		[{ artistId: artist.id, credit: "Artist A feat. Artist B" }],
	),
	release("shared", "Shared recording album", "artist-c", [
		track("shared-position", "artist-c", "direct-recording", "/shared.mka"),
	]),
];

it("plans deletion across primary and credited releases and shared compositions", () => {
	const plan = artistDeletionPlan(artist.id, [artist], releases);
	expect(plan).not.toBeNull();
	expect(plan).toMatchObject({
		target: "Artist A",
		kind: "artist",
		publications: 3,
		paths: ["/owned.mka", "/credited.mka", "/shared.mka"],
	});
	expect(plan?.scope).toContain("2 связанных релизов");
	expect(plan?.scope).toContain("1 напрямую связанных композиций");
	expect(plan?.dependencies).toEqual(
		expect.arrayContaining([
			expect.objectContaining({
				title: "Релизы (будут удалены)",
				items: ["Primary album", "Credited album"],
			}),
			expect.objectContaining({
				title:
					"Композиции (напрямую связанные с артистом будут удалены; прочие сохраняются)",
				items: [
					"shared-position · direct-recording · будет удалён",
					"other-credit-position · other-recording · сохранится",
					"credited-position · credited-recording · сохранится",
				],
			}),
			expect.objectContaining({
				title:
					"Позиции удаляемых композиций в сохраняемых релизах (будут удалены)",
				items: ["Shared recording album · 01 · shared-position · опубликована"],
			}),
		]),
	);
});

it("queues only a demo deletion operation after confirmation without mutating inputs", () => {
	const before = structuredClone(releases);
	const onQueue = vi.fn();
	render(
		<ArtistDeletionRequest
			artistId={artist.id}
			artists={[artist]}
			releases={releases}
			onQueue={onQueue}
		/>,
	);

	fireEvent.click(screen.getByRole("button", { name: "Удалить артиста" }));
	expect(onQueue).not.toHaveBeenCalled();
	expect(
		screen.getByText(
			/Остальные сущности медиатеки и все записи провайдера сохраняются/,
		),
	).toBeInTheDocument();
	expect(screen.getByText("/shared.mka")).toBeInTheDocument();
	fireEvent.click(screen.getByRole("button", { name: "Подтвердить" }));

	expect(onQueue).toHaveBeenCalledWith({
		kind: "artist",
		libraryArtistId: "artist-a",
		libraryReleaseIds: ["primary", "credited"],
		libraryTrackIds: [
			"direct-position",
			"other-credit-position",
			"credited-position",
			"shared-position",
		],
		target: "Artist A",
		scope: expect.any(String),
		publications: 3,
		paths: ["/owned.mka", "/credited.mka", "/shared.mka"],
	});
	expect(releases).toEqual(before);
});

it("does not render a request for an unknown artist", () => {
	const onQueue = vi.fn();
	const { container } = render(
		<ArtistDeletionRequest
			artistId="missing"
			artists={[artist]}
			releases={releases}
			onQueue={onQueue}
		/>,
	);
	expect(container).toBeEmptyDOMElement();
	expect(onQueue).not.toHaveBeenCalled();
});
