import { expect, it } from "vitest";
import { originalArtists, originalFiles } from "./catalogData";
import type { AbsentCatalogGroup } from "./catalogViewModel";
import { libraryCatalogAlbumResults } from "./LibraryCatalogRows";
import { missingEditionChooserProps } from "./LibraryMissingEditionDemo";

const firstEdition = {
  id: "edition-one",
  year: "1999",
  country: "US",
  format: "CD",
  disambiguation: "",
  titles: ["One"],
};

const groupWith = (
  editions: AbsentCatalogGroup["editions"],
): AbsentCatalogGroup => ({
  id: "group-one",
  title: "Only Album",
  artistId: originalArtists[0]?.id ?? "",
  groupId: "mb-group-one",
  editions,
});

it("projects a sole edition without the edition picker", () => {
  const props = missingEditionChooserProps({
    locale: "en",
    groupTitle: "Only Album",
    editions: [firstEdition],
    selectedEditionKey: "",
    onSelectEdition: () => undefined,
  });

  expect(props.soleEdition).toBe(true);
  expect(props.selectedEditionKey).toBe(firstEdition.id);
  expect(props.tracks?.tracks.map((track) => track.title)).toEqual(["One"]);
});

it("keeps the edition picker when several editions are known", () => {
  const props = missingEditionChooserProps({
    locale: "en",
    groupTitle: "Only Album",
    editions: [
      firstEdition,
      { ...firstEdition, id: "edition-two", format: "Vinyl" },
    ],
    selectedEditionKey: "",
    onSelectEdition: () => undefined,
  });

  expect(props.soleEdition).toBe(false);
});

it("passes the sole-edition state into the expanded album detail", () => {
  const group = groupWith([firstEdition]);
  const model = libraryCatalogAlbumResults({
    locale: "en",
    artists: originalArtists,
    releases: [],
    hasAudio: () => true,
    files: originalFiles,
    albumEntries: [{ kind: "missing", group }],
    selectedEditions: {},
    onSelectEdition: () => undefined,
    expanded: `provider:${group.groupId}`,
    onToggleAlbum: () => undefined,
    pageSelection: { selectedIds: new Set(), onChange: () => undefined },
  });

  const detail = model.detail;
  if (detail?.kind !== "missing-edition")
    throw new Error("missing edition detail");
  expect(detail.soleEdition).toBe(true);
  expect(detail.selectedEditionKey).toBe(firstEdition.id);
  expect(detail.tracks?.tracks.map((track) => track.title)).toEqual(["One"]);
});
