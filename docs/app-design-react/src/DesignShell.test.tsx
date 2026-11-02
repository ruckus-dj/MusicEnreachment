import { fireEvent, render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import { DesignShell } from "./DesignShell";

it("preserves original navigation sections without a separate publication tab", () => {
	window.location.hash = "library";
	render(
		<DesignShell>
			<main tabIndex={-1}>
				<h1>Медиатека</h1>
			</main>
		</DesignShell>,
	);
	const navigation = screen.getByRole("navigation", {
		name: "Основная навигация",
	});
	expect(navigation.querySelectorAll("a")).toHaveLength(6);
	expect(screen.getByRole("link", { name: "Медиатека" })).toHaveAttribute(
		"aria-current",
		"page",
	);
	expect(
		screen.queryByRole("link", { name: "Публикации" }),
	).not.toBeInTheDocument();
	fireEvent.click(screen.getByRole("link", { name: "К содержимому" }));
	expect(screen.getByRole("main")).toHaveFocus();
	window.location.hash = "";
});

it("renders the supplied incoming count in navigation", () => {
	window.location.hash = "review";
	render(
		<DesignShell incomingCount={7}>
			<main>
				<h1>Входящие</h1>
			</main>
		</DesignShell>,
	);
	const review = screen.getByRole("link", { name: "Входящие7" });
	expect(review).toHaveTextContent("7");
	window.location.hash = "";
});
