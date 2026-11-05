import { render, screen, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import {
	originalArtists,
	originalFiles,
	originalReleases,
} from "./catalogData";
import { audioProperties } from "./audioProperties";
import { LibraryCatalogRows } from "./LibraryCatalogRows";

const release = originalReleases[0];
const rows = (
	view: "albums" | "tracks",
	files = originalFiles,
	releases = originalReleases,
) => (
	<LibraryCatalogRows
		view={view}
		artists={originalArtists}
		releases={releases.map((item) => ({
			...item,
			tracks: item.tracks.map((track) => ({
				...track,
				published: Boolean(track.published),
			})),
		}))}
		missingCounts={new Map()}
		hasAudio={() => true}
		files={files}
		groupCheckbox={(_ids, label) => <button type="button">{label}</button>}
		expanded={release.id}
		onToggleAlbum={() => undefined}
	/>
);

it("switches desktop catalog views without hook-order warnings", () => {
	const originalError = console.error;
	const errors: unknown[][] = [];
	const errorSpy = vi.spyOn(console, "error").mockImplementation((...args) => {
		errors.push(args);
		originalError(...args);
	});
	try {
		const { rerender } = render(rows("albums", originalFiles, [release]));
		expect(
			screen.getByRole("grid", { name: "Альбомы медиатеки" }),
		).toBeTruthy();
		rerender(rows("tracks", originalFiles, [release]));
		expect(screen.getByRole("grid", { name: "Треки медиатеки" })).toBeTruthy();
		rerender(rows("albums", originalFiles, [release]));
		expect(
			screen.getByRole("grid", { name: "Альбомы медиатеки" }),
		).toBeTruthy();

		const hookWarnings = errors
			.flat()
			.map(String)
			.filter((message) =>
				/^(?:Warning: )?(?:The final argument passed to .*changed size|React has detected a change in the order of Hooks)/i.test(
					message,
				),
			);
		expect(hookWarnings).toEqual([]);
	} finally {
		errorSpy.mockRestore();
	}
});

it("renders mobile albums as labeled rows with expansion and selection controls", () => {
	render(rows("albums", originalFiles, [release]));
	const mobile = screen.getByLabelText("Альбомы медиатеки", {
		selector: "div",
	});
	expect(mobile.className).toContain("max-[599px]:grid");
	// JSDOM renders both responsive surfaces; identify desktop semantically.
	expect(screen.getByRole("grid", { name: "Альбомы медиатеки" })).toBeTruthy();
	const albumRow = within(mobile)
		.getByRole("button", { name: release.title })
		.closest("article");
	expect(albumRow).not.toBeNull();
	if (!albumRow) throw new Error("expected the mobile album article");
	expect(within(albumRow).getByText("Артист")).toBeTruthy();
	expect(
		within(albumRow).getByRole("button", { name: release.title }),
	).toHaveAttribute("aria-expanded", "true");
	expect(
		within(albumRow).getByRole("button", { name: /Выбрать позиции альбома/ }),
	).toBeTruthy();
	expect(
		within(albumRow).getByRole("link", { name: /First Light/ }),
	).toBeTruthy();
});

it("renders mobile album credits, fallback artist links, and unknown artists", () => {
	const credited = {
		...release,
		title: "Credited release",
		credits: [
			{ artistId: originalArtists[0].id, credit: "Credit artist" },
			{ artistId: null, credit: "Guest credit", joinPhrase: " feat. " },
		],
	};
	const fallback = {
		...release,
		id: "fallback",
		title: "Fallback release",
		credits: [],
		artistId: originalArtists[0].id,
	};
	const unknown = {
		...release,
		id: "unknown",
		title: "Unknown release",
		credits: [],
		artistId: null,
	};
	render(rows("albums", originalFiles, [credited, fallback, unknown]));
	const mobile = screen.getByLabelText("Альбомы медиатеки", {
		selector: "div",
	});
	const creditRow = within(mobile)
		.getByRole("button", { name: credited.title })
		.closest("article");
	expect(creditRow).not.toBeNull();
	const creditField = within(creditRow as HTMLElement).getByText("Артист", {
		selector: "dt",
	}).parentElement;
	expect(
		within(creditField as HTMLElement).getByRole("link", {
			name: "Credit artist",
		}),
	).toBeTruthy();
	expect(creditField?.querySelector("dd")?.textContent).toContain(
		"Guest credit",
	);
	const fallbackRow = within(mobile)
		.getByRole("button", { name: fallback.title })
		.closest("article");
	const fallbackField = within(fallbackRow as HTMLElement).getByText("Артист", {
		selector: "dt",
	}).parentElement;
	expect(
		within(fallbackField as HTMLElement).getByRole("link", {
			name: "Mira Vale",
		}),
	).toHaveAttribute("href", `#artist/${originalArtists[0].id}`);
	const unknownRow = within(mobile)
		.getByRole("button", { name: unknown.title })
		.closest("article");
	expect(unknownRow).not.toBeNull();
	const unknownField = within(unknownRow as HTMLElement).getByText("Артист", {
		selector: "dt",
	}).parentElement;
	expect(
		within(unknownField as HTMLElement).getByText("Артист не указан", {
			selector: "dd",
		}),
	).toBeTruthy();
});

it("uses only recorded technical source properties and shows unknown otherwise", () => {
	expect(audioProperties(originalFiles["north-f1"])).toBe("24 bit · 48 kHz");
	expect(
		audioProperties({
			...originalFiles["north-f1"],
			bits: null,
			sampleRate: undefined,
		}),
	).toBe("—");
	const files = structuredClone(originalFiles);
	files["north-f1"].bits = null;
	files["north-f1"].sampleRate = undefined;
	render(rows("tracks", files, [release]));
	const mobile = screen.getByLabelText("Треки медиатеки", { selector: "div" });
	expect(mobile.className).toContain("max-[599px]:grid");
	const desktop = screen.getByRole("grid", { name: "Треки медиатеки" });
	expect(desktop).toBeTruthy();
	const trackRow = within(mobile)
		.getByRole("link", { name: /First Light/ })
		.closest("article");
	expect(trackRow).not.toBeNull();
	const propertiesLabel = within(trackRow as HTMLElement).getByText(
		"Параметры",
		{
			selector: "dt",
		},
	);
	const propertiesField = propertiesLabel.parentElement;
	expect(propertiesField).not.toBeNull();
	expect(
		within(propertiesField as HTMLElement).getByText("—", { selector: "dd" }),
	).toBeTruthy();
	expect(
		within(trackRow as HTMLElement).queryByText("24 bit · 48 kHz"),
	).toBeNull();
});
