import { render, screen, within } from "@testing-library/react";
import { expect, it } from "vitest";
import { originalProviders, originalReleases } from "./catalogData";
import { TagsComparison } from "./TagsComparison";

it("renders an empty provider tag when the provider link exists, not the absent-link marker", () => {
	const providerTrack = originalProviders[0].tracks[0];
	const release = structuredClone(originalReleases[0]);
	const track = release.tracks[0];
	track.providerTrackId = providerTrack.id;
	const { rerender } = render(
		<TagsComparison
			releases={[release]}
			files={{}}
			scope="track"
			showAllFields
		/>,
	);
	const comment = screen.getByRole("row", { name: /COMMENT/ });
	const linkedProviderValue = within(comment).getAllByRole("cell")[1];
	expect(linkedProviderValue).toHaveTextContent("—");
	expect(linkedProviderValue).not.toHaveTextContent("Нет связи с провайдером");

	track.providerTrackId = null;
	rerender(
		<TagsComparison
			releases={[release]}
			files={{}}
			scope="track"
			showAllFields
		/>,
	);
	const unlinkedProviderValue = within(
		screen.getByRole("row", { name: /COMMENT/ }),
	).getAllByRole("cell")[1];
	expect(unlinkedProviderValue).toHaveTextContent("Нет данных связанного провайдера");
});
