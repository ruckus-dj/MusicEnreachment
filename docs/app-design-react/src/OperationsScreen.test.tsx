import { fireEvent, render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import {
	originalFiles,
	originalReleases,
	recoveryFixture,
} from "./catalogData";
import {
	initialOperationsDemoState,
	OperationsScreen,
} from "./OperationsScreen";
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
	fireEvent.click(screen.getByRole("button", { name: "Проверить результат" }));
	expect(onRecover).not.toHaveBeenCalled();
	expect(
		screen.getByText(/доказательство совпадения с исходником не/),
	).toBeInTheDocument();
	fireEvent.click(
		screen.getByRole("button", { name: "Зарегистрировать результат" }),
	);
	expect(onRecover).toHaveBeenCalledOnce();
});

it("requires an explicit full retry when the recovery target validation fails", () => {
	const onRecover = vi.fn();
	render(
		<OperationsScreen
			releases={originalReleases}
			deletions={[]}
			onRecover={onRecover}
		/>,
	);
	fireEvent.click(screen.getByRole("button", { name: "Проверить результат" }));
	fireEvent.click(
		screen.getByRole("checkbox", {
			name: "Смоделировать непрохождение проверки",
		}),
	);
	expect(screen.getByRole("alert")).toHaveTextContent(
		"Требуется явный полный повтор",
	);
	fireEvent.click(screen.getByRole("button", { name: "Полный повтор · демо" }));
	expect(onRecover).not.toHaveBeenCalled();
	expect(screen.getByRole("status")).toHaveTextContent(
		"запрошен полный повтор",
	);
});

it("passes the frozen deletion object to retry and dismiss callbacks", () => {
	const operation = {
		target: "Release",
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
	fireEvent.click(screen.getByRole("button", { name: "Смоделировать отказ" }));
	fireEvent.click(
		screen.getByRole("button", { name: "Повторить выполнение · демо" }),
	);
	expect(onCompleteDeletion).toHaveBeenCalledWith(operation);
	expect(screen.getByRole("status")).toHaveTextContent("область устарела");
	fireEvent.click(screen.getByRole("button", { name: "Скрыть · демо" }));
	expect(onDismissDeletion).toHaveBeenCalledWith(operation);
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

it("recovers from the frozen original publication snapshot, not current release input", () => {
	const onRecover = vi.fn();
	const changedReleases = structuredClone(originalReleases);
	const track = changedReleases
		.flatMap((release) => release.tracks)
		.find((item) => item.id === recoveryFixture.trackId);
	if (!track) throw new Error("Missing recovery track fixture");
	track.published = null;
	render(
		<OperationsScreen
			releases={changedReleases}
			deletions={[]}
			onRecover={onRecover}
		/>,
	);
	fireEvent.click(screen.getByRole("button", { name: "Проверить результат" }));
	fireEvent.click(
		screen.getByRole("button", { name: "Зарегистрировать результат" }),
	);
	expect(onRecover).toHaveBeenCalledWith(
		expect.objectContaining({ path: recoveryFixture.targetPath }),
	);
	const baselinePublished = originalReleases
		.flatMap((release) => release.tracks)
		.find((item) => item.id === recoveryFixture.trackId)?.published;
	if (!baselinePublished) {
		throw new Error("Missing recovery publication fixture");
	}
	expect(onRecover.mock.calls[0][0]).toMatchObject(baselinePublished);
	expect(
		screen.queryByText("Northern Lines · позиция 01"),
	).not.toBeInTheDocument();
});

it("dismisses current operations independently and a successful retry disappears", () => {
	const onState = vi.fn();
	const firstRender = render(
		<OperationsScreen
			releases={originalReleases}
			deletions={[]}
			onRecover={vi.fn()}
			onState={onState}
		/>,
	);
	fireEvent.click(screen.getByRole("button", { name: "Повторить" }));
	expect(screen.getByText("Повтор в очереди · демо")).toBeInTheDocument();
	fireEvent.click(
		screen.getByRole("button", { name: "Завершить повтор · демо" }),
	);
	expect(screen.queryByText("Signals · позиция 06")).not.toBeInTheDocument();
	expect(screen.getByText("Northern Lines · позиция 01")).toBeInTheDocument();

	const fresh = vi.fn();
	const state = onState.mock.lastCall?.[0];
	expect(state.retryState).toBe("succeeded");
	firstRender.unmount();
	const secondRender = render(
		<OperationsScreen
			releases={originalReleases}
			deletions={[]}
			onRecover={vi.fn()}
			initialState={state}
			onState={fresh}
		/>,
	);
	expect(screen.queryByText("Signals · позиция 06")).not.toBeInTheDocument();
	fireEvent.click(screen.getByRole("button", { name: "Скрыть ошибку · демо" }));
	expect(
		screen.queryByText("Northern Lines · позиция 01"),
	).not.toBeInTheDocument();
	const parentState = fresh.mock.lastCall?.[0];
	expect(parentState.dismissedOperations).toContain("recovery");
	secondRender.unmount();
	render(
		<OperationsScreen
			releases={originalReleases}
			deletions={[]}
			onRecover={vi.fn()}
			initialState={parentState ?? initialOperationsDemoState}
		/>,
	);
	expect(
		screen.queryByText("Northern Lines · позиция 01"),
	).not.toBeInTheDocument();
});

it("deletion completes directly on success and retains stale rejection for explicit re-confirmation", () => {
	const success = {
		target: "Album",
		kind: "publication" as const,
		scope: "Only selected album",
		publications: 2,
		paths: ["/owned/one.flac"],
		libraryReleaseIds: ["release-1"],
	};
	const stale = { ...success, target: "Stale album" };
	const onCompleteDeletion = vi.fn((operation) => operation === success);
	render(
		<OperationsScreen
			releases={originalReleases}
			deletions={[success, stale]}
			onRecover={vi.fn()}
			onCompleteDeletion={onCompleteDeletion}
		/>,
	);
	fireEvent.click(
		screen.getAllByRole("button", { name: "Выполнить удаление · демо" })[0],
	);
	expect(onCompleteDeletion).toHaveBeenCalledWith(success);
	expect(
		screen.queryByText("Album", { selector: "td" }),
	).not.toBeInTheDocument();
	expect(
		screen.getByText("Stale album", { selector: "td" }),
	).toBeInTheDocument();
	fireEvent.click(
		screen.getByRole("button", { name: "Выполнить удаление · демо" }),
	);
	expect(screen.getByText("Область устарела")).toBeInTheDocument();
	expect(
		screen.getByRole("link", {
			name: "Вернуться в медиатеку и подтвердить удаление заново",
		}),
	).toHaveAttribute("href", "#library");
});
