import { describe, expect, it } from "vitest";
import {
	processSourceQueue,
	retryCancelledSourceFile,
	resetSourceProcessingQueue,
	type SourceProcessingState,
} from "./sourceProcessingModel";
import { isValidMusicBrainzBaseURL } from "./providerValidation";

describe("workflow recovery", () => {
	it("cancels queued work on output reset until an explicit retry", () => {
		const queued: SourceProcessingState = {
			files: [
				{
					id: "root:file",
					rootId: "root",
					fileId: "file",
					path: "/music/song.flac",
					mode: "staged",
					status: "queued",
					blockedReason: "work_directory_required",
					copyId: "retained-copy",
					steps: [
						{ id: "audio_inspection", status: "queued" },
						{ id: "fingerprint", status: "queued" },
					],
				},
			],
			copies: [
				{
					id: "retained-copy",
					rootId: "root",
					fileId: "file",
					ownerFileId: "root:file",
					status: "retained",
				},
			],
		};
		const reset = resetSourceProcessingQueue(queued);

		expect(reset.files[0]).toMatchObject({
			status: "failed",
			blockedReason: "output_reset",
			copyId: "retained-copy",
			steps: queued.files[0].steps,
		});
		expect(reset.files[0].blockedReason).not.toBe("work_directory_required");
		expect(processSourceQueue(reset).files[0]).toEqual(reset.files[0]);

		const retried = retryCancelledSourceFile(reset, "root:file");
		expect(retried.files[0].blockedReason).toBeUndefined();
		expect(retried.files[0].status).toBe("queued");
	});

	it("accepts only HTTP(S) base URLs with a hostname", () => {
		expect(isValidMusicBrainzBaseURL("https://music.example.test/api")).toBe(
			true,
		);
		expect(isValidMusicBrainzBaseURL("http://localhost:5000")).toBe(true);
		expect(isValidMusicBrainzBaseURL("ftp://music.example.test")).toBe(false);
		expect(isValidMusicBrainzBaseURL("http://")).toBe(false);
		expect(isValidMusicBrainzBaseURL("garbage")).toBe(false);
	});
});
