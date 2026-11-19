import { fireEvent, render, screen, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import {
	LocalReleaseEditor,
	type LocalReleaseEditorProps,
} from "./LocalReleaseEditor";

it("requires a successful search and saves a missing recording separately from the release", async () => {
	const onCancel = vi.fn();
	const onSave = vi.fn();
	const saveRecording = vi.fn<LocalReleaseEditorProps["saveRecording"]>(
		(data) => ({
			...data,
			id: "new-recording",
			key: "recording-key",
			provider: "Local",
		}),
	);
	const checkRelease = vi
		.fn<LocalReleaseEditorProps["checkRelease"]>()
		.mockRejectedValueOnce(new Error("offline"))
		.mockResolvedValue([]);
	render(
		<LocalReleaseEditor
			locale="en"
			incomingLabel="Northern Lines"
			suggestedArtist="Mira Vale"
			suggestedTitle="Northern Lines"
			artists={[]}
			recordings={[]}
			checkRelease={checkRelease}
			findRecordings={async () => []}
			findArtists={async () => []}
			saveArtist={(data) => ({
				id: "new-artist",
				name: data.name,
				provider: "Local",
			})}
			saveRecording={saveRecording}
			onUseRelease={vi.fn()}
			onSave={onSave}
			onCancel={onCancel}
		/>,
	);
	const check = screen.getByRole("button", { name: "Check editions" });
	const next = "No suitable edition — continue";
	fireEvent.click(check);
	expect(await screen.findByRole("alert")).toHaveTextContent(
		"Search is unavailable",
	);
	expect(screen.queryByRole("button", { name: next })).not.toBeInTheDocument();
	fireEvent.click(check);
	fireEvent.click(await screen.findByRole("button", { name: next }));

	fireEvent.click(screen.getByRole("button", { name: "Add a recording" }));
	const picker = screen.getByRole("region", { name: "Add a recording" });
	fireEvent.change(
		within(picker).getByRole("textbox", { name: "Recording title" }),
		{
			target: { value: "Still Water" },
		},
	);
	fireEvent.click(within(picker).getByRole("button", { name: "Search" }));
	fireEvent.click(
		await within(picker).findByRole("button", {
			name: "No suitable recording — create one",
		}),
	);
	fireEvent.click(
		within(picker).getByRole("button", { name: "Save recording" }),
	);
	expect(saveRecording).toHaveBeenCalledExactlyOnceWith({
		title: "Still Water",
		duration_ms: undefined,
	});
	expect(
		screen.queryByRole("list", { name: "Tracklist" }),
	).not.toBeInTheDocument();
	expect(onSave).not.toHaveBeenCalled();

	fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
	const dialog = await screen.findByRole("dialog", {
		name: "Cancel this release?",
	});
	expect(dialog).toHaveTextContent("These recordings persist");
	expect(dialog).toHaveTextContent("Still Water");
	fireEvent.click(within(dialog).getByRole("button", { name: "Keep editing" }));
	fireEvent.click(
		await screen.findByRole("button", {
			name: "Add to tracklist: Still Water",
		}),
	);
	fireEvent.click(
		screen.getByRole("button", { name: "Save release and return" }),
	);
	expect(onSave).toHaveBeenCalledExactlyOnceWith(
		expect.objectContaining({
			title: "Northern Lines",
			tracks: [expect.objectContaining({ recordingId: "new-recording" })],
		}),
	);
	expect(onCancel).not.toHaveBeenCalled();
});
