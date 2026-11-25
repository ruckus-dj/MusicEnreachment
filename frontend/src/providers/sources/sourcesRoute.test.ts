import { describe, expect, it } from "vitest";
import { parseSourcesRoute, sourcesListRoute } from "./sourcesRoute";

describe("parseSourcesRoute", () => {
  it("parses list, detail, and location addresses", () => {
    expect(parseSourcesRoute("#/sources")).toBe(sourcesListRoute);
    expect(parseSourcesRoute("#/sources/root")).toEqual({
      view: "detail",
      id: "root",
    });
    expect(parseSourcesRoute("#/sources/root/locations/file")).toEqual({
      view: "location",
      id: "root",
      locationId: "file",
    });
  });

  it("decodes each identifier once", () => {
    expect(parseSourcesRoute("#/sources/a%20b/locations/c%252Fd")).toEqual({
      view: "location",
      id: "a b",
      locationId: "c%2Fd",
    });
  });

  it.each([
    "#/settings",
    "#/sources/",
    "#/sources/a/extra",
    "#/sources/a/locations/b/extra",
    "#/sources/%broken",
  ])("rejects invalid address %s without throwing", (hash) => {
    expect(parseSourcesRoute(hash)).toBeUndefined();
  });
});
