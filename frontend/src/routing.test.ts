import { describe, expect, it } from "vitest";
import { parseRoute, routePath } from "./routing";

describe("parseRoute", () => {
  it("ignores accidental trailing whitespace in track identifiers", () => {
    expect(
      parseRoute(
        "/library/artist/%D0%9A%D0%B8%D1%81-%D0%9A%D0%B8%D1%81%20%26%20Turbosh/album/%D0%9B%D0%91%D0%A2%D0%9B%20(Dance%20Remix)/track/record-c937fa995f8a4658bc4cbf3dfd46fb5e/70af67eeb02b90b045a39341f6cd8daa013c81cebcebd6b7a827319b137983b0%20",
      ),
    ).toMatchObject({
      screen: "track",
      recordId: "record-c937fa995f8a4658bc4cbf3dfd46fb5e",
      sourceId: "70af67eeb02b90b045a39341f6cd8daa013c81cebcebd6b7a827319b137983b0",
    });
  });

  it("parses canonical collection queries", () => {
    expect(
      parseRoute("/library/tracks", "?artist_name=Noize%20MC&album_id=release-123&published=false"),
    ).toEqual({
      screen: "tracks",
      artist: "Noize MC",
      album: "id:release-123",
      publicationFilter: "unpublished",
    });
    expect(parseRoute("/library/tracks", "?artist_name=Artist&album_name=Greatest%20Hits")).toEqual(
      {
        screen: "tracks",
        artist: "Artist",
        album: "album:Greatest Hits",
        publicationFilter: "all",
      },
    );
  });

  it("builds canonical album and track URLs", () => {
    expect(
      routePath({
        screen: "tracks",
        artist: "Noize MC",
        album: "release-123",
        publicationFilter: "published",
      }),
    ).toBe("/library/tracks?artist_name=Noize%20MC&album_id=release-123&published=true");
    expect(routePath({ screen: "track", recordId: "record-123" })).toBe(
      "/library/track/record-123",
    );
    expect(routePath({ screen: "track", recordId: "record-123", sourceId: "source-456" })).toBe(
      "/library/track/record-123?source_id=source-456",
    );
  });

  it("persists the manual-action tab in the URL", () => {
    expect(parseRoute("/manual-actions", "?action=needs-review")).toEqual({
      screen: "manual-actions",
      manualActionFilter: "needs-review",
    });
    expect(parseRoute("/manual-actions", "?action=unknown")).toEqual({
      screen: "manual-actions",
      manualActionFilter: "analysis-error",
    });
    expect(routePath({ screen: "manual-actions", manualActionFilter: "needs-review" })).toBe(
      "/manual-actions?action=needs-review",
    );
  });

  it("keeps legacy album keys intact while parsing old track lists", () => {
    expect(parseRoute("/library/artist/Artist/album/release-123/tracks")).toMatchObject({
      screen: "tracks",
      artist: "Artist",
      album: "release-123",
    });
    expect(parseRoute("/library/artist/Artist/album/record%3Arecord-123/tracks")).toMatchObject({
      screen: "tracks",
      album: "record:record-123",
    });
  });
});
