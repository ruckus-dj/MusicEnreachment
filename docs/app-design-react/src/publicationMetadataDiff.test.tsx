import { expect, it, vi } from "vitest";
import { effectivePublicationTags, originalReleases } from "./catalogData";
import {
	metadataSnapshotDiff,
	metadataTagDiff,
	publicationMetadataDiff,
} from "./publicationMetadataDiff";
import {
	fireEvent,
	render,
	screen,
	within,
	waitFor,
} from "@testing-library/react";
import { PublicationInspector } from "./PublicationInspector";
import { publicationMessage } from "./publicationMessages";

it("shows thematic old-to-new values, including cleared tags, artwork, and lyrics", () => {
	const release = structuredClone(
		originalReleases.find((item) => item.id === "north"),
	);
	if (!release) throw new Error("Missing north release fixture");
	const track = release.tracks.find((item) => item.id === "north-t2");
	if (!track?.published) throw new Error("Missing published track fixture");
	track.metadataBaseline = {
		...track.published.tags,
		COMMENT: ["Old comment"],
	};
	track.recordingMetadata = { comment: [] };
	track.metadataBaselineExtras = {
		coverHash: "cover-hash",
		lyrics: "Old lyrics",
	};
	track.coverHash = null;
	track.lyrics = null;

	const groups = publicationMetadataDiff(release, track);
	expect(groups.map((group) => group.key)).toContain("artwork");
	expect(groups.map((group) => group.key)).toContain("lyrics");
	render(
		<PublicationInspector
			locale="ru"
			items={[
				{
					id: track.id,
					position: String(track.position),
					title: track.title,
					groups,
				},
			]}
		/>,
	);
	expect(screen.getByText("Комментарий")).toBeInTheDocument();
	expect(screen.getByText("Old comment")).toBeInTheDocument();
	const artwork = screen.getByRole("region", { name: "Обложка" });
	const lyrics = screen.getByRole("region", { name: "Текст песни" });
	expect(
		within(artwork).getByText(publicationMessage("ru", "artworkPresent")),
	).toBeInTheDocument();
	expect(screen.getByText("Old lyrics")).toBeInTheDocument();
	expect(within(artwork).getByText("Нет значения")).toBeInTheDocument();
	expect(within(lyrics).getByText("Нет значения")).toBeInTheDocument();
	expect(screen.getByText("Композиция")).toBeInTheDocument();
	expect(within(lyrics).getByRole("table")).toBeInTheDocument();
});

it("includes additional tag keys and shows actual changed lyrics and cover state", () => {
	const groups = metadataTagDiff(
		{ CUSTOM_AUDIO_TAG: ["before"] },
		{ CUSTOM_AUDIO_TAG: ["after"] },
	);
	expect(groups.flatMap((group) => group.changes)).toEqual([
		expect.objectContaining({
			key: "CUSTOM_AUDIO_TAG",
			before: ["before"],
			after: ["after"],
		}),
	]);
	const snapshotGroups = metadataSnapshotDiff(
		{},
		{},
		{ coverHash: "old-cover", lyrics: "Before\nsecond line" },
		{ coverHash: "new-cover", lyrics: "After\nnew line" },
	);
	render(
		<PublicationInspector
			locale="en"
			items={[
				{ id: "t1", position: "1", title: "Track", groups: snapshotGroups },
			]}
		/>,
	);
	const artwork = screen.getByRole("region", { name: "Artwork" });
	const lyrics = screen.getByRole("region", { name: "Lyrics" });
	const table = within(artwork).getByRole("table");
	expect(
		within(table).getByRole("columnheader", { name: "Current" }),
	).toBeInTheDocument();
	expect(
		within(table).getByRole("columnheader", { name: "After" }),
	).toBeInTheDocument();
	const coverRow = within(table).getByRole("row", {
		name: /Artwork.*Present.*current.*Present.*new/,
	});
	expect(within(coverRow).getAllByText(/Present/)).toHaveLength(2);
	expect(
		within(artwork).getByText(publicationMessage("en", "artworkCurrent")),
	).toBeInTheDocument();
	expect(
		within(artwork).getByText(publicationMessage("en", "artworkNew")),
	).toBeInTheDocument();
	expect(within(lyrics).getByRole("table")).toBeInTheDocument();
	const lyricsTable = within(lyrics).getByRole("table");
	expect(
		within(lyricsTable).getByText("Before\nsecond line", {
			normalizer: (text) => text,
		}),
	).toBeInTheDocument();
	expect(
		within(lyricsTable).getByText("After\nnew line", {
			normalizer: (text) => text,
		}),
	).toBeInTheDocument();
});

it("opens the narrow inspector as a dialog and returns focus after Escape", async () => {
	const originalMatchMedia = window.matchMedia;
	Object.defineProperty(window, "matchMedia", {
		configurable: true,
		value: vi.fn().mockReturnValue({ matches: true }),
	});
	const { unmount } = render(
		<PublicationInspector
			locale="en"
			items={[
				{ id: "position-1", position: "1", title: "Track one", groups: [] },
				{ id: "position-2", position: "2", title: "Track two", groups: [] },
			]}
		/>,
	);
	const trigger = screen.getByRole("button", {
		name: /1\. Track one/,
	});
	try {
		trigger.focus();
		fireEvent.click(trigger);
		const dialog = screen.getByRole("dialog");
		expect(dialog).toBeInTheDocument();
		fireEvent.keyDown(dialog, { key: "Escape", code: "Escape" });
		await waitFor(() =>
			expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
		);
		await waitFor(() => expect(trigger).toHaveFocus());
	} finally {
		unmount();
		Object.defineProperty(window, "matchMedia", {
			configurable: true,
			value: originalMatchMedia,
		});
	}
});

it("projects audio-scoped tags from the audio that metadata-only publication preserves", () => {
	const release = structuredClone(
		originalReleases.find((item) => item.id === "north"),
	);
	if (!release) throw new Error("Missing north release fixture");
	const track = release.tracks.find((item) => item.id === "north-t1");
	if (!track?.published) throw new Error("Missing published track fixture");
	const publishedSource = "published-audio";
	track.published.sourceId = publishedSource;
	track.sourceId = "newly-selected-audio";
	track.audioMetadata = {
		[publishedSource]: { replaygain_track_gain: ["-4.00 dB"] },
		"newly-selected-audio": { replaygain_track_gain: ["-10.00 dB"] },
	};
	track.published.tags.REPLAYGAIN_TRACK_GAIN = ["-4.00 dB"];
	track.metadataBaseline = {
		...track.published.tags,
		REPLAYGAIN_TRACK_GAIN: ["-4.00 dB"],
	};

	expect(
		effectivePublicationTags(release, track).REPLAYGAIN_TRACK_GAIN,
	).toEqual(["-4.00 dB"]);
	expect(
		publicationMetadataDiff(release, track)
			.flatMap((group) => group.changes)
			.some((change) => change.key === "REPLAYGAIN_TRACK_GAIN"),
	).toBe(false);
});

it("compares tag values exactly, including case, whitespace, and Unicode form", () => {
	const groups = metadataTagDiff(
		{ TITLE: ["Cafe\u0301"] },
		{ TITLE: ["Café "] },
	);
	expect(groups.flatMap((group) => group.changes)).toMatchObject([
		{ key: "TITLE", before: ["Cafe\u0301"], after: ["Café "] },
	]);
	expect(
		metadataTagDiff({ TITLE: ["Song"] }, { TITLE: ["song"] }),
	).toHaveLength(1);
});

it("keeps metadata value boundaries and significant whitespace visible", () => {
	const groups = metadataTagDiff(
		{ TITLE: ["A · B", "  spaced  value  "] },
		{ TITLE: ["A", "B", "  spaced  value  "] },
	);
	render(
		<PublicationInspector
			locale="en"
			items={[
				{
					id: "values",
					position: "1",
					title: "Values",
					groups,
				},
			]}
		/>,
	);
	const row = within(screen.getByRole("table")).getByRole("row", {
		name: /Title/i,
	});
	const lists = within(row).getAllByRole("list");
	expect(
		within(lists[0])
			.getAllByRole("listitem")
			.map((item) => item.querySelector("pre")?.textContent),
	).toEqual(["A · B", "  spaced  value  "]);
	expect(
		within(lists[1])
			.getAllByRole("listitem")
			.map((item) => item.querySelector("pre")?.textContent),
	).toEqual(["A", "B", "  spaced  value  "]);
});
