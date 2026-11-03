import { expect, it } from "vitest";
import { effectivePublicationTags, originalReleases } from "./catalogData";
import {
	MetadataDiff,
	metadataSnapshotDiff,
	metadataTagDiff,
	publicationMetadataDiff,
} from "./publicationMetadataDiff";
import { render, screen } from "@testing-library/react";

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
	render(<MetadataDiff groups={groups} />);
	expect(
		screen.getByText(/Комментарий: Old comment → Нет значения/),
	).toBeInTheDocument();
	expect(
		screen.getByText(/Встроенная обложка: Есть → Нет значения/),
	).toBeInTheDocument();
	expect(
		screen.getByText(/Текст песни: Old lyrics → Нет значения/),
	).toBeInTheDocument();
	expect(screen.getByText("Композиция")).toBeInTheDocument();
	expect(screen.getByText("Вложенное содержимое")).toBeInTheDocument();
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
	const { container } = render(
		<MetadataDiff
			groups={metadataSnapshotDiff(
				{},
				{},
				{ coverHash: "old-cover", lyrics: "Before\nsecond line" },
				{ coverHash: "new-cover", lyrics: "After\nnew line" },
			)}
		/>,
	);
	expect(screen.getByText(/Есть · текущая → Есть · новая/)).toBeInTheDocument();
	expect(container.textContent).toContain(
		"Before\nsecond line → After\nnew line",
	);
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
