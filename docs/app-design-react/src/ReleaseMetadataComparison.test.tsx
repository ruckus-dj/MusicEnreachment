import { fireEvent, render, screen, within } from "@testing-library/react";
import { expect, it } from "vitest";
import { originalFiles, originalReleases } from "./catalogData";
import { ReleaseMetadataComparison } from "./ReleaseMetadataComparison";

it("compares the selected track across all metadata sources and scopes", () => {
	const release = originalReleases.find((item) => item.id === "north");
	if (!release) throw new Error("Missing baseline fixture");
	render(<ReleaseMetadataComparison release={release} files={originalFiles} />);

	const comparison = screen.getByRole("region", {
		name: "Сравнение метаданных",
	});
	expect(
		within(comparison).getByRole("columnheader", { name: "Исходный файл" }),
	).toBeInTheDocument();
	expect(
		within(comparison).getByRole("columnheader", {
			name: "Провайдер · кэш",
		}),
	).toBeInTheDocument();

	fireEvent.click(within(comparison).getByRole("tab", { name: "Артист" }));
	expect(
		within(comparison).getByRole("rowheader", { name: "ARTISTSORT" }),
	).toBeInTheDocument();
	fireEvent.click(within(comparison).getByRole("tab", { name: "Трек" }));
	expect(
		within(comparison).getByRole("rowheader", { name: "TRACKNUMBER" }),
	).toBeInTheDocument();
	fireEvent.click(
		within(comparison).getByRole("checkbox", { name: /Только отличия/ }),
	);
	expect(
		within(comparison).getByRole("checkbox", { name: /Только отличия/ }),
	).toBeChecked();

	const trackSelect = within(comparison).getByRole("button", {
		name: /Позиция сравнения/,
	});
	const nextTrack = release.tracks[1];
	if (!nextTrack) throw new Error("Missing second baseline track");
	fireEvent.click(trackSelect);
	fireEvent.click(
		within(screen.getByRole("listbox")).getByRole("option", {
			name: nextTrack.title,
		}),
	);
	expect(
		within(comparison).getByRole("link", {
			name: "Открыть подробное сравнение альбома",
		}),
	).toHaveAttribute("href", `#tags/${nextTrack.id}/release`);
});

it("shows manual type and credits when available", () => {
	const baseline = originalReleases.find((item) => item.id === "north");
	if (!baseline) throw new Error("Missing baseline fixture");
	const release = structuredClone(baseline);
	release.manual = true;
	release.metadata = {
		date: release.year,
		country: release.country,
		label: release.label,
		catalogNumber: release.catalog,
		type: "Сингл",
	};
	release.credits = [{ credit: "Исполнитель проекта" }];
	render(<ReleaseMetadataComparison release={release} files={originalFiles} />);
	expect(
		screen.getByText(/Тип: Сингл · Исполнители: Исполнитель проекта/),
	).toBeInTheDocument();
});
