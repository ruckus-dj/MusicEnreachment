import { fireEvent, render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { originalReleases } from "./catalogData";
import { PublicationPreview } from "./PublicationPreview";

it("matches original Mira preview counts without selecting alternative sources", () => {
	const ids = originalReleases
		.filter((release) => release.artistId === "mira")
		.flatMap((release) => release.tracks.map((track) => track.id));
	const onBack = vi.fn();
	render(<PublicationPreview selected={new Set(ids)} onBack={onBack} />);
	expect(screen.getByText("Создать: 4")).toBeInTheDocument();
	expect(screen.getByText("Обновить: 13")).toBeInTheDocument();
	expect(screen.getByText("Без изменений: 1")).toBeInTheDocument();
	expect(screen.getByText("Пропустить: 11")).toBeInTheDocument();
	expect(screen.getAllByRole("row")).toHaveLength(30);
	fireEvent.click(
		screen.getByRole("button", { name: "Вернуться в медиатеку" }),
	);
	expect(onBack).toHaveBeenCalledOnce();
});
