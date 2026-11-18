import { fireEvent, render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { SourceScanPanel } from "./SourceScanPanel";

it("does not start on render, disables a running scan, and retries explicitly", () => {
	const onStart = vi.fn();
	const onRetry = vi.fn();
	const props = { locale: "en" as const, onStart, onRetry };
	const { rerender } = render(
		<SourceScanPanel {...props} state={{ kind: "idle" }} />,
	);
	expect(onStart).not.toHaveBeenCalled();
	fireEvent.click(screen.getByRole("button", { name: "Scan directory" }));
	expect(onStart).toHaveBeenCalledOnce();
	rerender(<SourceScanPanel {...props} state={{ kind: "traversing" }} />);
	expect(screen.getByRole("status")).toHaveTextContent("Reading the directory");
	expect(screen.getByRole("button", { name: "Scan directory" })).toBeDisabled();
	rerender(
		<SourceScanPanel
			{...props}
			state={{ kind: "failed", message: "Directory could not be read." }}
		/>,
	);
	expect(screen.getByRole("alert")).toHaveTextContent(
		"Directory could not be read.",
	);
	expect(onRetry).not.toHaveBeenCalled();
	fireEvent.click(screen.getByRole("button", { name: "Retry scan" }));
	expect(onRetry).toHaveBeenCalledOnce();
});
