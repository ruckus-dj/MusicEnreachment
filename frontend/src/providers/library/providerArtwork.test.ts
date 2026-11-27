import { expect, it } from "vitest";
import {
  exactMusicBrainzReleaseArtworkURL,
  providerReleaseFrontArtworkURL,
} from "./providerArtwork";

const mbid = "123e4567-e89b-42d3-a456-426614174000";
const otherMBID = "223e4567-e89b-42d3-a456-426614174001";

it("builds the same-origin cached front URL for an exact edition", () => {
  expect(providerReleaseFrontArtworkURL(mbid)).toBe(
    `/api/library/provider-artwork/releases/${mbid}/front`,
  );
  expect(providerReleaseFrontArtworkURL(mbid.toUpperCase())).toBe(
    `/api/library/provider-artwork/releases/${mbid}/front`,
  );
});

it("uses only a single exact MusicBrainz release link", () => {
  expect(
    exactMusicBrainzReleaseArtworkURL([
      { provider_code: "musicbrainz", provider_key: mbid },
    ]),
  ).toBe(`/api/library/provider-artwork/releases/${mbid}/front`);
  expect(
    exactMusicBrainzReleaseArtworkURL([
      { provider_code: "musicbrainz", provider_key: mbid },
      { provider_code: "musicbrainz", provider_key: otherMBID },
    ]),
  ).toBeNull();
  expect(
    exactMusicBrainzReleaseArtworkURL([
      { provider_code: "musicbrainz", provider_key: "not-an-mbid" },
    ]),
  ).toBeNull();
  expect(
    exactMusicBrainzReleaseArtworkURL([
      { provider_code: "local", provider_key: mbid },
    ]),
  ).toBeNull();
  expect(exactMusicBrainzReleaseArtworkURL([])).toBeNull();
});
