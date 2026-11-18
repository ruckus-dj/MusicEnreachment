import { fireEvent, render, screen, within } from "@testing-library/react";
import { expect, it } from "vitest";
import { ShellView } from "./ShellView";

const brand = { href: "#incoming", label: "MeloTrove", mark: "mt" };
const controls = { language: <span>EN</span>, theme: <span>System</span> };

function shell(incomingCount: number, withSetupLink: boolean) {
	return (
		<ShellView
			brand={brand}
			skipLabel="Skip to content"
			navigationLabel="Main navigation"
			navigation={[
				{
					id: "review",
					label: "Incoming",
					href: "#review",
					count: incomingCount,
				},
			]}
			controls={controls}
			footerText="Prototype"
			setupLink={
				withSetupLink ? { href: "#setup", label: "First run" } : undefined
			}
		>
			<p>Content</p>
		</ShellView>
	);
}

it("keeps the footer slot and skip focus while the provider count and setup link change", () => {
	const { rerender } = render(shell(0, true));

	const footer = screen.getByText("Prototype");
	const footerSlot = footer.parentElement;
	if (!footerSlot) throw new Error("Missing shell footer slot");
	expect(
		within(footerSlot).getByRole("link", { name: "First run" }),
	).toHaveAttribute("href", "#setup");
	const navigation = screen.getByRole("navigation", {
		name: "Main navigation",
	});
	const reviewLink = within(navigation).getByRole("link", {
		name: /Incoming/,
	});
	expect(within(reviewLink).getByText("0")).toBeInTheDocument();

	const content = screen.getByRole("main");
	const mobileBrand = within(screen.getByRole("banner")).getByRole("link", {
		name: "MeloTrove",
	});
	expect(mobileBrand).toHaveAttribute("href", brand.href);
	fireEvent.click(screen.getByRole("button", { name: "Skip to content" }));
	expect(content).toHaveFocus();

	rerender(shell(3, false));
	fireEvent.click(screen.getByRole("button", { name: "Skip to content" }));
	expect(content).toHaveFocus();

	const updatedLink = within(
		screen.getByRole("navigation", { name: "Main navigation" }),
	).getByRole("link", { name: /Incoming/ });
	expect(within(updatedLink).getByText("3")).toBeInTheDocument();
	expect(within(updatedLink).queryByText("0")).not.toBeInTheDocument();
	const updatedFooter = screen.getByText("Prototype");
	expect(updatedFooter.parentElement).toBe(footerSlot);
	expect(
		within(footerSlot).queryByRole("link", { name: "First run" }),
	).not.toBeInTheDocument();
});
