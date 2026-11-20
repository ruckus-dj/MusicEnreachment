import { expect, it } from "vitest";
import { resolveManagedPublicationDeletion } from "./managedPublicationDeletionModel";

it("finishes metadata cleanup when the managed file is already absent", () => {
	expect(resolveManagedPublicationDeletion("absent", false, true)).toBe(
		"completed",
	);
	expect(resolveManagedPublicationDeletion("absent", false, false)).toBe(
		"retry",
	);
});

it("never deletes a replacement at the same path", () => {
	expect(resolveManagedPublicationDeletion("replaced", true, true)).toBe(
		"conflict",
	);
});

it("never treats an unavailable or unverified target as absent", () => {
	expect(resolveManagedPublicationDeletion("unknown", true, true)).toBe(
		"retry",
	);
});

it("requires successful unlink and metadata commit for an existing managed file", () => {
	expect(resolveManagedPublicationDeletion("present", false, true)).toBe(
		"retry",
	);
	expect(resolveManagedPublicationDeletion("present", true, false)).toBe(
		"retry",
	);
	expect(resolveManagedPublicationDeletion("present", true, true)).toBe(
		"completed",
	);
});
