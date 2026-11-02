import { fireEvent, render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { originalReleases } from "./catalogData";
import { OperationsScreen } from "./OperationsScreen";
import { DeletionRequest } from "./DeletionRequest";

it("requires confirmation to register recovery without checking source identity", () => {
	const onRecover = vi.fn();
	render(
		<OperationsScreen
			releases={originalReleases}
			deletions={[]}
			onRecover={onRecover}
		/>,
	);
	fireEvent.click(
		screen.getByRole("button", { name: "Восстановить регистрацию" }),
	);
	expect(onRecover).not.toHaveBeenCalled();
	expect(
		screen.getByText(/Доказательство совпадения исходника: не требуется/),
	).toBeInTheDocument();
	fireEvent.click(screen.getByRole("button", { name: "Подтвердить" }));
	expect(onRecover).toHaveBeenCalledOnce();
});

it("queues a deletion request without mutating demo entities or publication files", () => {
	const release = originalReleases.find((item) => item.id === "north");
	if (!release) throw new Error("Missing baseline fixture");
	const before = structuredClone(release);
	const onQueue = vi.fn();
	render(<DeletionRequest release={release} onQueue={onQueue} />);
	fireEvent.click(screen.getByRole("button", { name: "Удалить альбом" }));
	expect(onQueue).not.toHaveBeenCalled();
	expect(screen.getByText("Композиции (сохраняются)")).toBeInTheDocument();
	fireEvent.click(screen.getByRole("button", { name: "Подтвердить" }));
	expect(onQueue).toHaveBeenCalledWith(
		expect.objectContaining({ publications: 5, paths: expect.any(Array) }),
	);
	expect(release).toEqual(before);
});
