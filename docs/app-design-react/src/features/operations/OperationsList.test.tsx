import { fireEvent, render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import {
	OperationsList,
	type OperationsListCopy,
	type OperationsListRow,
} from "./OperationsList";

const copy: OperationsListCopy = {
	title: "Current operations",
	description: "Work in progress and errors that need attention.",
	refresh: "Refresh",
	refreshing: "Refreshing…",
	loading: "Loading operations…",
	loadError: "Could not load operations.",
	retryLoad: "Retry loading",
	emptyTitle: "No current operations",
	emptyDescription: "Work in progress will appear here.",
	target: "Target",
	operation: "Operation",
	stateStage: "Status / stage",
	progress: "Progress",
	created: "Created",
	error: "Error",
	actions: "Actions",
	footnote: "Completed operations are hidden.",
	search: "Search by target",
	statusFilter: "Status",
	kindFilter: "Operation type",
	details: "Details",
	close: "Close",
	previous: "Previous",
	next: "Next",
};

const row: OperationsListRow = {
	id: "publication-1",
	target: "Northern Lines · position 01",
	targetHref: "#track/north-t1",
	operation: "Publish position",
	state: "Running",
	stateTone: "info",
	stage: "Preparing result",
	progress: "42%",
	created: "12:04",
	error: "EACCES: no write access to the temporary directory",
	actions: [{ id: "dismiss", label: "Dismiss", onPress: vi.fn() }],
};

it("renders an operation row and forwards row actions and refresh", () => {
	const onRefresh = vi.fn();
	render(<OperationsList copy={copy} rows={[row]} onRefresh={onRefresh} />);
	expect(
		screen.getByRole("link", { name: "Northern Lines · position 01" }),
	).toHaveAttribute("href", "#track/north-t1");
	expect(screen.getByText("Preparing result")).toBeVisible();
	expect(screen.getByText("42%")).toBeVisible();
	expect(screen.getByText("12:04")).toBeVisible();
	fireEvent.click(screen.getByRole("button", { name: /Details/ }));
	const details = screen.getByRole("dialog");
	expect(details).toHaveTextContent(
		"EACCES: no write access to the temporary directory",
	);
	fireEvent.click(screen.getByRole("button", { name: "Close" }));
	fireEvent.click(screen.getByRole("button", { name: "Dismiss" }));
	expect(row.actions?.[0]?.onPress).toHaveBeenCalledTimes(1);
	fireEvent.click(screen.getByRole("button", { name: "Refresh" }));
	expect(onRefresh).toHaveBeenCalledTimes(1);
});

it("shows loading, error and empty states instead of rows", () => {
	const onRefresh = vi.fn();
	const { rerender } = render(
		<OperationsList copy={copy} rows={[]} status="loading" />,
	);
	expect(screen.getByText("Loading operations…")).toBeVisible();
	rerender(
		<OperationsList
			copy={copy}
			rows={[]}
			status="error"
			error="Operations API is unavailable."
			onRefresh={onRefresh}
		/>,
	);
	expect(screen.getByRole("alert")).toHaveTextContent(
		"Operations API is unavailable.",
	);
	fireEvent.click(screen.getByRole("button", { name: "Retry loading" }));
	expect(onRefresh).toHaveBeenCalledTimes(1);
	rerender(
		<OperationsList
			copy={copy}
			rows={[row]}
			status="error"
			isRefreshing
			onRefresh={onRefresh}
			actionError="Could not dismiss the operation."
		/>,
	);
	expect(screen.getByText("Northern Lines · position 01")).toBeVisible();
	expect(
		screen.getByText(copy.loadError).closest('[role="alert"]'),
	).not.toBeNull();
	expect(screen.getByText("Could not dismiss the operation.")).toHaveRole(
		"alert",
	);
	expect(screen.getByRole("button", { name: "Retry loading" })).toBeDisabled();
	rerender(<OperationsList copy={copy} rows={[]} />);
	expect(screen.getByText("No current operations")).toBeVisible();
	expect(screen.queryByRole("button", { name: "Retry loading" })).toBeNull();
});
