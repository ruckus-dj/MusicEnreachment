import { describe, expect, it } from "vitest";
import { parseRoute } from "./routing";

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
});
