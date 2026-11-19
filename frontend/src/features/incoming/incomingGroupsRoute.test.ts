import { describe, expect, it } from "vitest";
import {
  incomingGroupMatchingHref,
  incomingGroupsListHref,
  parseIncomingGroupsRoute,
} from "./incomingGroupsRoute";

describe("incoming groups route", () => {
  it("parses the list route and its expanded groups", () => {
    expect(parseIncomingGroupsRoute("#/incoming-groups")).toEqual({
      view: "list",
      expandedGroupIds: [],
    });
    expect(
      parseIncomingGroupsRoute(
        "#/incoming-groups?group=first&group=second&group=first",
      ),
    ).toEqual({ view: "list", expandedGroupIds: ["first", "second"] });
    expect(parseIncomingGroupsRoute("/incoming-groups?group=")).toEqual({
      view: "list",
      expandedGroupIds: [],
    });
  });

  it("parses a concrete group matching route with an explicit release", () => {
    expect(
      parseIncomingGroupsRoute("#/incoming-groups/group%2Fone/matching"),
    ).toEqual({ view: "matching", groupId: "group/one" });
    expect(
      parseIncomingGroupsRoute(
        "#/incoming-groups/group-one/matching?release=release%2Fone",
      ),
    ).toEqual({
      view: "matching",
      groupId: "group-one",
      releaseId: "release/one",
    });
  });

  it("parses a local release creation route and preferred local release", () => {
    expect(
      parseIncomingGroupsRoute(
        "#/incoming-groups/group%2Fone/matching/local-release",
      ),
    ).toEqual({
      view: "matching",
      groupId: "group/one",
      creatingLocalRelease: true,
    });
    expect(
      parseIncomingGroupsRoute(
        "#/incoming-groups/group-one/matching?local=local%2Frelease",
      ),
    ).toEqual({
      view: "matching",
      groupId: "group-one",
      localReleaseId: "local/release",
    });
    expect(
      parseIncomingGroupsRoute(
        "#/incoming-groups/group-one/matching/local-release?release=release%2Fone&local=local%2Frelease&prepare=prepare%2Frelease",
      ),
    ).toEqual({
      view: "matching",
      groupId: "group-one",
      releaseId: "release/one",
      localReleaseId: "local/release",
      prepareReleaseId: "prepare/release",
      creatingLocalRelease: true,
    });
    expect(
      parseIncomingGroupsRoute(
        "#/incoming-groups/group-one/matching/local-release/extra",
      ),
    ).toBe(undefined);
  });

  it("ignores unrelated and malformed locations", () => {
    expect(parseIncomingGroupsRoute("#/incoming-groups/group-one")).toBe(
      undefined,
    );
    expect(
      parseIncomingGroupsRoute("#/incoming-groups/group-one/matching/extra"),
    ).toBe(undefined);
    expect(
      parseIncomingGroupsRoute("#/incoming-groups/%E0%A4%A/matching"),
    ).toBe(undefined);
    expect(parseIncomingGroupsRoute("#/library")).toBe(undefined);
  });

  it("builds routes that parse back to the same identity", () => {
    const matching = incomingGroupMatchingHref("group/one", "release/one");
    expect(matching).toBe(
      "#/incoming-groups/group%2Fone/matching?release=release%2Fone",
    );
    expect(parseIncomingGroupsRoute(matching)).toEqual({
      view: "matching",
      groupId: "group/one",
      releaseId: "release/one",
    });

    const list = incomingGroupsListHref(["group/one"]);
    expect(list).toBe("#/incoming-groups?group=group%2Fone");
    expect(parseIncomingGroupsRoute(list)).toEqual({
      view: "list",
      expandedGroupIds: ["group/one"],
    });
    expect(incomingGroupsListHref()).toBe("#/incoming-groups");

    const localCreation = incomingGroupMatchingHref(
      "group/one",
      "release/one",
      {
        localReleaseId: "local/release",
        prepareReleaseId: "prepare/release",
        creatingLocalRelease: true,
      },
    );
    expect(localCreation).toBe(
      "#/incoming-groups/group%2Fone/matching/local-release?release=release%2Fone&local=local%2Frelease&prepare=prepare%2Frelease",
    );
    expect(parseIncomingGroupsRoute(localCreation)).toEqual({
      view: "matching",
      groupId: "group/one",
      releaseId: "release/one",
      localReleaseId: "local/release",
      prepareReleaseId: "prepare/release",
      creatingLocalRelease: true,
    });

    const preferred = incomingGroupMatchingHref("group-one", undefined, {
      localReleaseId: "local/release",
    });
    expect(preferred).toBe(
      "#/incoming-groups/group-one/matching?local=local%2Frelease",
    );
    expect(parseIncomingGroupsRoute(preferred)).toEqual({
      view: "matching",
      groupId: "group-one",
      localReleaseId: "local/release",
    });
  });
});
