import { expect, it } from "vitest";
import type {
  IncomingFileResponse,
  IncomingGroupResponse,
  IncomingLocationResponse,
} from "../../api/generated/client.schemas";
import { incomingOverviewOrder } from "./incomingOverviewOrder";

function location(
  configured_path: string,
  relative_path: string,
): IncomingLocationResponse {
  return {
    configured_path,
    inventory_path: configured_path,
    location_id: `${configured_path}:${relative_path}`,
    mtime: "2026-01-01T00:00:00Z",
    relative_path,
    root_id: configured_path,
    size_bytes: 1,
    technical_identity: "",
    work_id: "",
  };
}

function file(
  variant_id: string,
  ...locations: IncomingLocationResponse[]
): IncomingFileResponse {
  return {
    capture_analysis_id: "",
    capture_identity: "",
    locations,
    tags: {},
    variant_id,
  };
}

function group(id: string, ...members: string[]): IncomingGroupResponse {
  return {
    diagnostics: null,
    id,
    manual: false,
    members,
    revision: id,
    unready_members: null,
  };
}

it("orders by source and exact folder naturally, keeps ties stable, and puts missing paths last", () => {
  const filesById = new Map<string, IncomingFileResponse>([
    ["inbox-c", file("inbox-c", location("/mnt/inbox", "Set 10/c.flac"))],
    ["missing", file("missing")],
    ["backup-b", file("backup-b", location("/mnt/backup", "Set 10/b.flac"))],
    ["inbox-a", file("inbox-a", location("/mnt/inbox", "Set 10/a.flac"))],
    ["inbox-d", file("inbox-d", location("/mnt/inbox", "Set 2/d.flac"))],
  ]);
  const groups = [
    group("inbox-c", "inbox-c"),
    group("missing", "missing"),
    group("backup-b", "backup-b"),
    group("inbox-a", "inbox-a"),
    group("inbox-d", "inbox-d"),
    group("missing-2"),
  ];

  const result = incomingOverviewOrder(groups, filesById);

  expect(result.groups.map(({ id }) => id)).toEqual([
    "backup-b",
    "inbox-d",
    "inbox-c",
    "inbox-a",
    "missing",
    "missing-2",
  ]);
});

it("uses the folder with the most distinct members as the group's position", () => {
  const filesById = new Map<string, IncomingFileResponse>([
    ["two-a", file("two-a", location("/mnt/inbox", "album 1/01.flac"))],
    ["two-b", file("two-b", location("/mnt/inbox", "album 2/01.flac"))],
    ["two-c", file("two-c", location("/mnt/inbox", "album 2/02.flac"))],
    ["early", file("early", location("/mnt/inbox", "album 1/05.flac"))],
    ["tie-a", file("tie-a", location("/mnt/inbox", "zzz/01.flac"))],
    ["tie-b", file("tie-b", location("/mnt/inbox", "aaa/01.flac"))],
  ]);
  const groups = [
    group("two", "two-a", "two-b", "two-c"),
    group("early", "early"),
    group("tie", "tie-a", "tie-b"),
  ];

  const result = incomingOverviewOrder(groups, filesById);

  expect(result.groups.map(({ id }) => id)).toEqual(["early", "two", "tie"]);
});

it("reports the canonical folder paths of every member location for search", () => {
  const filesById = new Map<string, IncomingFileResponse>([
    [
      "paths-a",
      file(
        "paths-a",
        location("/mnt/inbox", "Set 1/a.flac"),
        location("/mnt/inbox", "Set 1/a-copy.flac"),
      ),
    ],
    ["paths-b", file("paths-b", location("/mnt/inbox", "Set 2/b.flac"))],
    ["paths-c", file("paths-c", location("/mnt/inbox", "c.flac"))],
  ]);
  const groups = [
    group("paths", "paths-a", "paths-b", "paths-c"),
    group("none"),
  ];

  const { folderPaths } = incomingOverviewOrder(groups, filesById);

  expect(folderPaths.get("paths")).toEqual([
    "/mnt/inbox/Set 1",
    "/mnt/inbox/Set 2",
    "/mnt/inbox",
  ]);
  expect(folderPaths.get("none")).toEqual([]);
});
