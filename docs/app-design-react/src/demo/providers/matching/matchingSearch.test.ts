import { expect, it } from "vitest";
import { incomingFixtures, providerReleases } from "../incoming/incomingModel";
import { matchingPage, searchMatchingReleases } from "./matchingSearch";

it("requires an explicit query and never matches internal identities", () => {
  expect(
    searchMatchingReleases(
      "Northern Lines",
      providerReleases,
      incomingFixtures.artists,
    ),
  ).toEqual(
    expect.arrayContaining([
      expect.objectContaining({ id: "p-north" }),
      expect.objectContaining({ id: "p-japan" }),
    ]),
  );
  expect(
    searchMatchingReleases(
      "release not in fixtures",
      providerReleases,
      incomingFixtures.artists,
    ),
  ).toEqual([]);
  expect(
    searchMatchingReleases(
      "p-north",
      providerReleases,
      incomingFixtures.artists,
    ),
  ).toEqual([]);
});

it("matches every token across credits, title and available edition details", () => {
  const ids = (query: string) =>
    searchMatchingReleases(
      query,
      providerReleases,
      incomingFixtures.artists,
    ).map((release) => release.id);
  expect(ids("Mira Vale Northern")).toEqual(
    expect.arrayContaining(["p-north", "p-japan"]),
  );
  expect(ids("Field Records FR-021-JP")).toEqual(["p-japan"]);
  expect(ids("Mira JP")).toEqual(["p-japan"]);

  const credited = {
    ...providerReleases[0],
    credits: [
      { artistId: "mira", credit: "Mira Vale", joinPhrase: " & " },
      { artistId: "arden", credit: "Arden Trio", joinPhrase: "" },
    ],
  };
  expect(
    searchMatchingReleases(
      "Mira Vale & Arden Trio",
      [credited],
      incomingFixtures.artists,
    ),
  ).toEqual([credited]);
});

it("pages search results with a default size of ten", () => {
  expect(
    matchingPage(
      Array.from({ length: 21 }, (_, index) => index),
      1,
    ),
  ).toEqual({
    items: [10, 11, 12, 13, 14, 15, 16, 17, 18, 19],
    page: 1,
    pageCount: 3,
  });
});
