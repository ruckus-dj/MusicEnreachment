import { describe, expect, it } from "vitest";
import { isValidMusicBrainzBaseURL } from "../../infrastructure/providerValidation";
import {
	processSourceQueue,
	resetSourceProcessingQueue,
	retrySourceStep,
	type SourceProcessingState,
} from "./sourceProcessingModel";

describe("workflow recovery", () => {
	it("cancels queued steps on output reset and keeps recovery manual and per-step", () => {
		const interrupted: SourceProcessingState = {
			files: [
				{
					id: "root:file",
					rootId: "root",
					fileId: "file",
					path: "/music/song.flac",
					mode: "staged",
					status: "queued",
					copyId: "retained-copy",
					steps: [
						{ id: "audio_inspection", status: "succeeded", attempts: 1 },
						{ id: "fingerprint", status: "queued", attempts: 1 },
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
		const reset = resetSourceProcessingQueue(interrupted);

		expect(reset.files[0]).toMatchObject({
			status: "failed",
			blockedReason: "output_reset",
			copyId: "retained-copy",
			steps: [
				{ id: "audio_inspection", status: "succeeded", attempts: 1 },
				{
					id: "fingerprint",
					status: "failed",
					error: "outputResetCancelled",
					attempts: 1,
				},
			],
		});
		expect(processSourceQueue(reset)).toEqual(reset);

		const retried = retrySourceStep(reset, "root:file", "fingerprint");
		expect(retried.files[0]).toMatchObject({
			status: "queued",
			steps: [
				{ id: "audio_inspection", status: "succeeded", attempts: 1 },
				{ id: "fingerprint", status: "queued", attempts: 1 },
			],
		});
		expect(retried.files[0].blockedReason).toBeUndefined();

		const completed = processSourceQueue(retried);
		expect(completed.files[0]).toMatchObject({
			status: "completed",
			steps: [
				{ id: "audio_inspection", status: "succeeded", attempts: 1 },
				{ id: "fingerprint", status: "succeeded", attempts: 2 },
			],
		});
		expect(completed.copies).toEqual(interrupted.copies);
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
