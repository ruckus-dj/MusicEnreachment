import { fireEvent, render, screen, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { SourceRootList } from "./SourceRootList";

it("shows saved inventory independently from directory availability", () => {
	render(
		<SourceRootList
			locale="en"
			rows={[
				{
					id: "archive",
					name: "Archive",
					path: "/mnt/archive",
					mode: "staged",
					availability: "unavailable",
					lastSuccessfulScan: {
						label: "10:20",
						dateTime: "2026-09-26T10:20:00Z",
					},
					inventory: {
						label: "12 files",
						notes: ["From the last successful scan"],
					},
					href: "#sources/archive",
				},
			]}
		/>,
	);
	const grid = within(
		screen.getByRole("grid", { name: "Connected directories" }),
	);
	expect(grid.getByRole("link", { name: "Archive" })).toHaveAttribute(
		"href",
		"#sources/archive",
	);
	expect(grid.getByText("Unavailable")).toBeInTheDocument();
	expect(grid.getByText("12 files")).toBeInTheDocument();
	expect(grid.getByText("10:20")).toHaveAttribute(
		"datetime",
		"2026-09-26T10:20:00Z",
	);
	expect(grid.queryByRole("button", { name: /scan/i })).not.toBeInTheDocument();
});

it("distinguishes loading, empty, and retryable errors", () => {
	const retry = vi.fn();
	const { rerender } = render(
		<SourceRootList locale="en" rows={[]} state={{ kind: "loading" }} />,
	);
	expect(screen.getByRole("status")).toHaveTextContent("Loading directories");
	rerender(
		<SourceRootList
			locale="en"
			rows={[]}
			state={{ kind: "error", message: "Unavailable", onRetry: retry }}
		/>,
	);
	const alert = within(screen.getByRole("alert"));
	expect(alert.getByText("Unavailable")).toBeInTheDocument();
	fireEvent.click(alert.getByRole("button", { name: "Retry" }));
	expect(retry).toHaveBeenCalledOnce();
	rerender(<SourceRootList locale="en" rows={[]} />);
	expect(screen.getByText("No directories registered.")).toBeInTheDocument();
});
