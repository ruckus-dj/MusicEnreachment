import { render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import {
	PublicationInspector,
	type PublicationInspection,
} from "./PublicationInspector";

const item: PublicationInspection = {
	id: "track-1",
	position: "1",
	title: "First Light",
	source: "First Light.flac",
	groups: [],
};

it("does not claim an output description when matching provides none", () => {
	render(<PublicationInspector items={[item]} locale="en" />);
	expect(screen.queryByText("Will be written")).toBeNull();
	expect(screen.getByText(item.source!)).toBeTruthy();
});

it.each([
	{
		outputPath: "/music/First Light.mka",
		container: "MKA",
		expected: "/music/First Light.mka · MKA",
	},
	{ outputPath: "/music/First Light.mka", expected: "/music/First Light.mka" },
	{ container: "MKA", expected: "MKA" },
])("shows only the available output facts: $expected", ({
	expected,
	...output
}) => {
	render(<PublicationInspector items={[{ ...item, ...output }]} locale="en" />);
	expect(screen.getByText("Will be written")).toBeTruthy();
	expect(screen.getByText(expected, { selector: "dd" })).toBeTruthy();
});
