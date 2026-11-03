import { fireEvent, render, screen, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { originalReleases, recoveryFixture } from "./catalogData";
import { OperationsScreen } from "./OperationsScreen";
import { DeletionRequest } from "./DeletionRequest";

it("shows the failure diagnostic and removes it when retry succeeds", () => {
	const onState = vi.fn();
	render(
		<OperationsScreen
			releases={originalReleases}
			deletions={[]}
			onRecover={vi.fn()}
			onState={onState}
		/>,
	);
	expect(screen.getByText(/EACCES: нет записи/)).toBeInTheDocument();
	const failedRow = screen.getByText(/EACCES: нет записи/).closest("tr");
	if (!failedRow) throw new Error("Missing failed operation row");
	expect(
		within(failedRow).getByRole("button", { name: "Повторить" }),
	).toBeInTheDocument();
	expect(
		within(failedRow).getByRole("button", { name: "Скрыть" }),
	).toBeInTheDocument();
	fireEvent.click(within(failedRow).getByRole("button", { name: "Повторить" }));
	expect(screen.queryByText("Signals · позиция 06")).not.toBeInTheDocument();
	expect(screen.getByRole("status")).toHaveTextContent("Повтор завершён");
	expect(onState.mock.lastCall?.[0].retryState).toBe("succeeded");
});

it("checks the frozen target and confirms recovery without requiring a registered publication", () => {
	const releases = structuredClone(originalReleases);
	const track = releases
		.flatMap((release) => release.tracks)
		.find((item) => item.id === recoveryFixture.trackId);
	if (!track) throw new Error("Missing recovery fixture track");
	track.published = null;
	const onRecover = vi.fn();
	render(
		<OperationsScreen
			releases={releases}
			deletions={[]}
			onRecover={onRecover}
		/>,
	);
	fireEvent.click(screen.getByRole("button", { name: "Проверить результат" }));
	expect(onRecover).not.toHaveBeenCalled();
	expect(
		screen.getByText(new RegExp(recoveryFixture.targetPath)),
	).toBeInTheDocument();
	fireEvent.click(
		screen.getByRole("button", { name: "Зарегистрировать результат" }),
	);
	expect(onRecover).toHaveBeenCalledOnce();
	expect(onRecover.mock.calls[0][0]).toMatchObject({
		path: recoveryFixture.targetPath,
	});
	expect(
		screen.queryByText("Northern Lines · позиция 01"),
	).not.toBeInTheDocument();
});

it("retains recovery guards and leaves the operation current when registration rejects", () => {
	const onRecover = vi.fn(() => false);
	render(
		<OperationsScreen
			releases={originalReleases}
			deletions={[]}
			onRecover={onRecover}
		/>,
	);
	fireEvent.click(screen.getByRole("button", { name: "Проверить результат" }));
	fireEvent.click(
		screen.getByRole("button", { name: "Зарегистрировать результат" }),
	);
	expect(onRecover).toHaveBeenCalledOnce();
	expect(screen.getByText("Northern Lines · позиция 01")).toBeInTheDocument();
	expect(screen.getByRole("status")).toHaveTextContent(
		"Восстановление отклонено",
	);
});

it("passes the current deletion request to its handler and reports stale scope", () => {
	const operation = {
		target: "Release",
		kind: "publication" as const,
		scope: "Only this release",
		publications: 1,
		paths: ["/srv/music/release.flac"],
		libraryReleaseIds: ["release-1"],
	};
	const onCompleteDeletion = vi.fn(() => false);
	render(
		<OperationsScreen
			releases={originalReleases}
			deletions={[operation]}
			onRecover={vi.fn()}
			onCompleteDeletion={onCompleteDeletion}
		/>,
	);
	fireEvent.click(screen.getByRole("button", { name: "Выполнить удаление" }));
	expect(onCompleteDeletion).toHaveBeenCalledWith(operation);
	expect(screen.getByText("Область устарела")).toBeInTheDocument();
	expect(
		screen.getByRole("link", {
			name: "Вернуться в медиатеку и подтвердить область удаления заново",
		}),
	).toHaveAttribute("href", "#library");
});

it("preserves the frozen deletion request and dismissal callback", () => {
	const operation = {
		target: "Release",
		kind: "publication" as const,
		scope: "Only this release",
		publications: 1,
		paths: ["/srv/music/release.flac"],
		libraryReleaseIds: ["release-1"],
	};
	const onCompleteDeletion = vi.fn(() => false);
	const onDismissDeletion = vi.fn();
	render(
		<OperationsScreen
			releases={originalReleases}
			deletions={[operation]}
			onRecover={vi.fn()}
			onCompleteDeletion={onCompleteDeletion}
			onDismissDeletion={onDismissDeletion}
		/>,
	);
	fireEvent.click(screen.getByRole("button", { name: "Выполнить удаление" }));
	const deletionRow = screen.getByText("Only this release").closest("tr");
	if (!deletionRow) throw new Error("Missing deletion request row");
	within(deletionRow).getByRole("button", { name: "Скрыть" }).click();
	expect(onCompleteDeletion).toHaveBeenCalledWith(operation);
	expect(onDismissDeletion).toHaveBeenCalledWith(operation);
});

it("queues a deletion request without changing the release", () => {
	const release = originalReleases.find((item) => item.id === "north");
	if (!release) throw new Error("Missing baseline fixture");
	const before = structuredClone(release);
	const onQueue = vi.fn();
	render(<DeletionRequest release={release} onQueue={onQueue} />);
	fireEvent.click(screen.getByRole("button", { name: "Удалить альбом" }));
	fireEvent.click(screen.getByRole("button", { name: "Подтвердить" }));
	expect(onQueue).toHaveBeenCalledWith(
		expect.objectContaining({ publications: 5, paths: expect.any(Array) }),
	);
	expect(release).toEqual(before);
});
