import {
	fireEvent,
	render,
	screen,
	within,
	waitFor,
} from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { MetadataEditor } from "./MetadataEditor";
import { fieldsForScope, type MetadataScope } from "./registry";
import type { MetadataValues } from "./projection";

const scopes: MetadataScope[] = ["release", "recording", "track", "audio"];

function markedKeys(container: HTMLElement) {
	return [...container.querySelectorAll<HTMLElement>("[data-metadata-keys]")];
}

it.each(
	scopes,
)("renders and enables every editable registered %s field", (scope) => {
	const { container } = render(
		<MetadataEditor
			label="Coverage"
			scope={scope}
			values={{}}
			onSave={vi.fn()}
		/>,
	);
	fireEvent.click(screen.getByRole("button", { name: "Редактировать" }));
	const rendered = markedKeys(container);
	const keys = rendered.flatMap((node) =>
		node.getAttribute("data-metadata-keys")!.split(","),
	);
	for (const field of fieldsForScope(scope)) {
		expect(keys, `${scope}.${field.key} is rendered`).toContain(field.key);
		if (
			(field.kind === "identity" &&
				!["asin", "musicbrainz_discid"].includes(field.key)) ||
			field.derived
		)
			continue;
		const controls = rendered.filter((node) =>
			node.getAttribute("data-metadata-keys")!.split(",").includes(field.key),
		);
		const editable =
			"input:not([readonly]), textarea:not([readonly]), select:not(:disabled), button:not(:disabled)";
		expect(
			controls.some(
				(control) =>
					control.matches(editable) || control.querySelector(editable),
			),
			`${scope}.${field.key} is editable`,
		).toBeTruthy();
	}
});

it("keeps derived artist sort values and entity identities readonly in edit mode", () => {
	const { container } = render(
		<MetadataEditor
			label="Recording"
			scope="recording"
			values={{
				artist: ["Demo Artist"],
				artistsort: ["Artist, Demo"],
				musicbrainz_recordingid: ["12345678-1234-4234-8234-123456789abc"],
			}}
			onSave={vi.fn()}
		/>,
	);
	fireEvent.click(screen.getByRole("button", { name: "Редактировать" }));
	for (const key of ["artistsort", "musicbrainz_recordingid"]) {
		const marker = markedKeys(container).find(
			(node) =>
				node.matches("input, textarea") &&
				node.getAttribute("data-metadata-keys")!.split(",").includes(key),
		);
		expect(marker, key).toBeTruthy();
		expect(marker!).toHaveProperty("readOnly", true);
	}
});

it("keeps ASIN and MusicBrainz Disc ID editable as descriptive identifiers", () => {
	const { container } = render(
		<MetadataEditor
			label="Release"
			scope="release"
			values={{ asin: ["old-asin"], musicbrainz_discid: ["old-disc"] }}
			onSave={vi.fn()}
		/>,
	);
	fireEvent.click(screen.getByRole("button", { name: "Редактировать" }));
	for (const key of ["asin", "musicbrainz_discid"]) {
		const marker = markedKeys(container).find(
			(node) =>
				node.matches("input") &&
				node.getAttribute("data-metadata-keys")!.split(",").includes(key),
		);
		expect(marker, key).toBeTruthy();
		expect(
			marker!.matches("input:not([readonly])"),
			`${key} remains editable`,
		).toBeTruthy();
	}
});

it("saves and reopens typed scalar values, explicit clears, contributors, works, and movement", async () => {
	let saved: MetadataValues = {
		title: ["Old title"],
		originaldate: ["1999"],
		isrc: ["OLD"],
		explicitstatus: ["1"],
		composer: ["Old Composer"],
		work: ["Old Work"],
		movementname: ["Old movement"],
		movement: ["1"],
	};
	const onSave = vi.fn((next: MetadataValues) => {
		saved = next;
	});
	const view = render(
		<MetadataEditor
			label="Recording"
			scope="recording"
			values={saved}
			onSave={onSave}
		/>,
	);
	fireEvent.click(screen.getByRole("button", { name: "Редактировать" }));
	fireEvent.change(screen.getByRole("textbox", { name: "Название" }), {
		target: { value: "New title" },
	});
	fireEvent.change(
		screen.getByRole("textbox", { name: "Дата первоначального выпуска" }),
		{
			target: { value: "2001-02" },
		},
	);
	const isrc = screen.getByRole("textbox", { name: "ISRC" });
	fireEvent.change(isrc, { target: { value: "" } });
	fireEvent.change(screen.getByRole("combobox", { name: "Explicit / clean" }), {
		target: { value: "2" },
	});
	const composer = screen.getByRole("heading", {
		name: "Композитор",
	}).parentElement!;
	fireEvent.click(
		within(composer).getByRole("button", { name: "Добавить участника" }),
	);
	fireEvent.change(
		within(composer).getAllByRole("textbox", { name: "Имя участника" })[1],
		{
			target: { value: "New Composer" },
		},
	);
	fireEvent.click(
		screen.getByRole("button", { name: "Добавить произведение" }),
	);
	fireEvent.change(
		screen.getAllByRole("textbox", { name: "Название произведения" })[1],
		{
			target: { value: "New Work" },
		},
	);
	fireEvent.change(screen.getByRole("textbox", { name: "Название части" }), {
		target: { value: "New movement" },
	});
	fireEvent.click(screen.getByRole("button", { name: "Сохранить" }));
	expect(onSave).toHaveBeenCalledOnce();
	expect(saved).toMatchObject({
		title: ["New title"],
		originaldate: ["2001-02"],
		isrc: [],
		explicitstatus: ["2"],
		composer: ["Old Composer", "New Composer"],
		work: ["Old Work", "New Work"],
		movementname: ["New movement"],
	});
	await waitFor(() =>
		expect(screen.getByRole("button", { name: "Редактировать" })).toBeEnabled(),
	);
	view.rerender(
		<MetadataEditor
			label="Recording"
			scope="recording"
			values={saved}
			onSave={onSave}
		/>,
	);
	fireEvent.click(screen.getByRole("button", { name: "Редактировать" }));
	expect(screen.getByRole("textbox", { name: "Название" })).toHaveValue(
		"New title",
	);
	expect(screen.getByRole("textbox", { name: "ISRC" })).toHaveValue("");
	expect(
		screen.getAllByRole("textbox", { name: "Название произведения" })[1],
	).toHaveValue("New Work");
});

it("edits the release-scoped script field", () => {
	const onSave = vi.fn();
	render(
		<MetadataEditor
			label="Release"
			scope="release"
			values={{ script: ["Latn"] }}
			onSave={onSave}
		/>,
	);
	fireEvent.click(screen.getByRole("button", { name: "Редактировать" }));
	fireEvent.change(screen.getByRole("textbox", { name: "Система письма" }), {
		target: { value: "Cyrl" },
	});
	fireEvent.click(screen.getByRole("button", { name: "Сохранить" }));
	expect(onSave).toHaveBeenCalledWith({ script: ["Cyrl"] });
});

it("edits audio-only ReplayGain and R128 values without scope leakage", () => {
	const onSave = vi.fn();
	const values = { replaygain_track_gain: ["-7.5"], r128_album_gain: ["-256"] };
	const { container } = render(
		<MetadataEditor
			label="Audio"
			scope="audio"
			values={values}
			onSave={onSave}
		/>,
	);
	fireEvent.click(screen.getByRole("button", { name: "Редактировать" }));
	fireEvent.change(
		screen.getByRole("spinbutton", { name: "ReplayGain трека" }),
		{
			target: { value: "-6.25" },
		},
	);
	fireEvent.change(screen.getByRole("spinbutton", { name: "R128 альбома" }), {
		target: { value: "-240" },
	});
	fireEvent.click(screen.getByRole("button", { name: "Сохранить" }));
	expect(onSave).toHaveBeenCalledWith({
		replaygain_track_gain: ["-6.25"],
		r128_album_gain: ["-240"],
	});
	expect(
		container.querySelector('[data-metadata-keys="r128_track_gain"]'),
	).toBeTruthy();
});
