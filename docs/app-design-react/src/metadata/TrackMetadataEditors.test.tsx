import { fireEvent, render, screen, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import {
	originalFiles,
	originalReleases,
	desiredTags,
	type OriginalTrack,
} from "../catalogData";
import { TrackMetadataEditors } from "./TrackMetadataEditors";

function fixture() {
	const release = originalReleases.find((item) => item.id === "north");
	const track = release?.tracks.find((item) => item.id === "north-t1");
	if (!release || !track) throw new Error("Missing metadata editor fixture");
	return { release, track };
}

it("saves recording data without changing the track-owned publication or source", () => {
	const { release, track } = fixture();
	const before = structuredClone(track);
	let updated = track;
	const onMutate = vi.fn(
		(update: (current: OriginalTrack) => OriginalTrack) => {
			updated = update(updated);
		},
	);
	render(
		<TrackMetadataEditors
			track={track}
			artists={[]}
			files={originalFiles}
			genresLoaded
			onMutate={onMutate}
		/>,
	);
	const recording = screen.getByRole("region", {
		name: "Сведения о композиции",
	});
	fireEvent.click(
		within(recording).getByRole("button", { name: "Редактировать" }),
	);
	fireEvent.change(within(recording).getByLabelText("Название"), {
		target: { value: "Renamed recording" },
	});
	fireEvent.click(within(recording).getByRole("button", { name: "Сохранить" }));
	expect(updated.recordingMetadata?.title).toEqual(["Renamed recording"]);
	expect(updated.recordingTitle).toBe("Renamed recording");
	expect(updated.title).toBe("Renamed recording");
	expect(updated.sourceId).toBe(before.sourceId);
	expect(updated.published).toEqual(before.published);
	expect(updated.positionMetadata).toEqual(before.positionMetadata);
	expect(release.tracks[0]).toBe(track);
});

it("edits audio metadata only for the selected source and preserves other variant data", () => {
	const track = structuredClone(fixture().track);
	const variants = track.sourceIds ?? [];
	if (variants.length < 2) throw new Error("Expected two source variants");
	const otherSourceId = variants.find((id) => id !== track.sourceId) ?? "";
	const selectedMeasurements = {
		replaygain_track_gain: ["-6.0 dB"],
		replaygain_track_peak: ["0.95"],
		replaygain_album_gain: ["-5.5 dB"],
		replaygain_album_peak: ["0.97"],
		r128_track_gain: ["-400"],
		r128_album_gain: ["-350"],
	};
	track.audioMetadata = {
		[track.sourceId ?? ""]: selectedMeasurements,
		[otherSourceId]: {
			encodedby: ["Existing"],
			encodersettings: ["Existing settings"],
		},
	};
	let updated = track;
	render(
		<TrackMetadataEditors
			track={track}
			artists={[]}
			files={originalFiles}
			genresLoaded
			onMutate={(update) => {
				updated = update(updated);
			}}
		/>,
	);
	const audio = screen.getByRole("region", {
		name: "Метаданные аудио для публикации",
	});
	fireEvent.click(within(audio).getByText(/Громкость и кодирование/));
	const selectedEditor = within(audio).getByRole("region", {
		name: `${originalFiles[track.sourceId ?? ""]?.name} · выбран для публикации`,
	});
	expect(within(audio).queryByText(/не выбран/)).not.toBeInTheDocument();
	fireEvent.click(
		within(selectedEditor).getByRole("button", { name: "Редактировать" }),
	);
	fireEvent.change(within(selectedEditor).getByLabelText("Закодировано"), {
		target: { value: "Test encoder" },
	});
	fireEvent.change(
		within(selectedEditor).getByLabelText("Настройки кодирования"),
		{ target: { value: "Test settings" } },
	);
	fireEvent.click(
		within(selectedEditor).getByRole("button", { name: "Сохранить" }),
	);
	expect(updated.audioMetadata?.[track.sourceId ?? ""]?.encodedby).toEqual([
		"Test encoder",
	]);
	expect(
		updated.audioMetadata?.[track.sourceId ?? ""]?.encodersettings,
	).toEqual(["Test settings"]);
	expect(updated.audioMetadata?.[track.sourceId ?? ""]).toMatchObject(
		selectedMeasurements,
	);
	expect(updated.audioMetadata?.[otherSourceId]).toEqual({
		encodedby: ["Existing"],
		encodersettings: ["Existing settings"],
	});
	expect(updated.sourceId).toBe(track.sourceId);
});

it("shows measured loudness values independently for each concrete audio source", () => {
	const track = structuredClone(fixture().track);
	const selectedSource = track.sourceId;
	const otherSource = track.sourceIds?.find((id) => id !== selectedSource);
	if (!selectedSource || !otherSource)
		throw new Error("Expected two source variants");
	const selectedSourceMeasurements = {
		replaygain_track_gain: ["-7.2 dB"],
		replaygain_track_peak: ["0.98"],
		replaygain_album_gain: ["-6.8 dB"],
		replaygain_album_peak: ["0.99"],
		r128_track_gain: ["-512"],
		r128_album_gain: ["-480"],
	};
	const otherMeasurements = { replaygain_track_gain: ["-3.4 dB"] };
	const onMutate = vi.fn();
	render(
		<TrackMetadataEditors
			track={{
				...track,
				audioMetadata: {
					[selectedSource]: selectedSourceMeasurements,
					[otherSource]: otherMeasurements,
				},
			}}
			artists={[]}
			files={originalFiles}
			genresLoaded
			onMutate={onMutate}
		/>,
	);
	fireEvent.click(screen.getByText(/Громкость и кодирование ·/));
	fireEvent.click(screen.getByText(/Другие аудиоварианты ·/));
	const selectedEditor = screen.getByRole("region", {
		name: `${originalFiles[selectedSource]?.name} · выбран для публикации`,
	});
	const otherEditor = screen.getByRole("region", {
		name: `${originalFiles[otherSource]?.name} · только просмотр`,
	});
	const selectedMeasurements = within(selectedEditor).getByRole("region", {
		name: "Измерения аудио",
	});
	const otherMeasurementsRegion = within(otherEditor).getByRole("region", {
		name: "Измерения аудио",
	});
	for (const value of Object.values(selectedSourceMeasurements).flat())
		expect(within(selectedMeasurements).getByText(value)).toBeInTheDocument();
	expect(
		within(otherMeasurementsRegion).getByText("-3.4 dB"),
	).toBeInTheDocument();
	expect(
		within(otherMeasurementsRegion).getAllByText("Не рассчитано"),
	).toHaveLength(5);
	expect(
		within(selectedEditor).queryByRole("textbox", { name: "ReplayGain трека" }),
	).not.toBeInTheDocument();
	expect(
		within(selectedEditor).getByRole("textbox", { name: "Закодировано" }),
	).toBeInTheDocument();
	expect(onMutate).not.toHaveBeenCalled();
});

it("allows an explicitly empty library recording title", () => {
	const { track } = fixture();
	let updated = track;
	render(
		<TrackMetadataEditors
			track={track}
			artists={[]}
			files={originalFiles}
			genresLoaded
			onMutate={(update) => {
				updated = update(updated);
			}}
		/>,
	);
	const recording = screen.getByRole("region", {
		name: "Сведения о композиции",
	});
	fireEvent.click(
		within(recording).getByRole("button", { name: "Редактировать" }),
	);
	fireEvent.change(within(recording).getByLabelText("Название"), {
		target: { value: "" },
	});
	fireEvent.click(within(recording).getByRole("button", { name: "Сохранить" }));
	expect(updated.recordingMetadata?.title).toEqual([]);
	expect(updated.recordingTitle).toBe("");
});

it("updates positional fields without mutating the shared recording source", () => {
	const { track } = fixture();
	let updated = track;
	render(
		<TrackMetadataEditors
			track={track}
			artists={[]}
			files={originalFiles}
			genresLoaded
			onMutate={(update) => {
				updated = update(updated);
			}}
		/>,
	);
	fireEvent.click(
		screen.getByText(/Позиция в издании ·.*№/, { selector: "summary" }),
	);
	const position = screen.getByRole("region", { name: "Позиция в издании" });
	fireEvent.click(
		within(position).getByRole("button", { name: "Редактировать" }),
	);
	fireEvent.change(within(position).getByLabelText("Номер позиции"), {
		target: { value: "07" },
	});
	fireEvent.click(within(position).getByRole("button", { name: "Сохранить" }));
	expect(updated.displayedNumber).toBe("07");
	expect(updated.mediumPosition).toBe(7);
	expect(updated.recordingMetadata).toEqual(track.recordingMetadata);
	expect(updated.sourceId).toBe(track.sourceId);
});

it.each([
	"north-t2",
	"north-japan-local-t2",
])("preserves legacy position titles when editing shared recording from %s", (trackId) => {
	const positions = originalReleases
		.flatMap((release) => release.tracks)
		.filter((track) => track.recordingId === "demo-rec-north-2");
	const track = positions.find((item) => item.id === trackId);
	if (!track) throw new Error("Missing shared recording fixture");
	let updated = positions;
	render(
		<TrackMetadataEditors
			track={track}
			artists={[]}
			files={originalFiles}
			genresLoaded
			onMutate={() => {
				throw new Error("Expected shared update");
			}}
			onRecordingChange={(update) => {
				updated = updated.map(update);
			}}
		/>,
	);
	const region = screen.getByRole("region", { name: "Сведения о композиции" });
	fireEvent.click(
		within(region).getByRole("button", { name: "Редактировать" }),
	);
	fireEvent.change(within(region).getByLabelText("Название"), {
		target: { value: "Renamed shared recording" },
	});
	fireEvent.click(within(region).getByRole("button", { name: "Сохранить" }));
	expect(updated.find((item) => item.id === "north-t2")?.title).toBe(
		"Still Water (local edit)",
	);
	expect(
		updated.find((item) => item.id === "north-japan-local-t2")?.title,
	).toBe("Renamed shared recording");
	for (const [index, item] of updated.entries()) {
		expect(item.sourceId).toBe(positions[index].sourceId);
		expect(item.published).toEqual(positions[index].published);
		expect(item.positionMetadata).toEqual(positions[index].positionMetadata);
		const release = originalReleases.find((candidate) =>
			candidate.tracks.some((t) => t.id === item.id),
		);
		if (!release) throw new Error("Missing release");
		expect(desiredTags(release, item).TITLE).toEqual([item.title]);
	}
});
it("inspects another audio slot without changing the publication source", () => {
	const { track } = fixture();
	const other = track.sourceIds?.find((id) => id !== track.sourceId);
	if (!other) throw new Error("Missing alternate audio fixture");
	const onMutate = vi.fn();
	render(
		<TrackMetadataEditors
			track={{
				...track,
				audioMetadata: { [other]: { encodedby: ["Alternate encoder"] } },
			}}
			artists={[]}
			files={originalFiles}
			genresLoaded
			onMutate={onMutate}
		/>,
	);
	fireEvent.click(screen.getByText(/Громкость и кодирование ·/));
	fireEvent.click(screen.getByText(/Другие аудиоварианты ·/));
	const region = screen.getByRole("region", {
		name: `${originalFiles[other].name} · только просмотр`,
	});
	expect(within(region).getByLabelText("Закодировано")).toHaveValue(
		"Alternate encoder",
	);
	expect(
		within(region).getByRole("button", { name: "Редактировать" }),
	).toBeDisabled();
	expect(onMutate).not.toHaveBeenCalled();
});
