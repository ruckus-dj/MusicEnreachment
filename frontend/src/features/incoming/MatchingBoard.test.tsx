import { fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { I18nProvider } from "../../i18n/I18nProvider";
import { createMemoryStorage } from "../../test/memoryStorage";
import {
  MatchingBoard,
  type MatchingBoardAutomaticAssignments,
  type MatchingBoardSavedScores,
  matchingBoardAutomaticForRelease,
  matchingBoardSavedScore,
  matchingBoardSavedTrack,
  matchingBoardScoresFromCandidates,
} from "./MatchingBoard";
import { decodeMatchingFilePayload } from "./MatchingDnD";

const release = {
  provider_release_id: "release-1",
  release_id: "provider:release-1",
  title: "Album",
  tracklist_complete: true,
  positions: [
    {
      track_id: "track-1",
      medium: 0,
      position: 0,
      displayed_number: "1",
      recording_id: "recording-1",
      title: "First",
    },
    {
      track_id: "track-2",
      medium: 1,
      position: 0,
      displayed_number: "2",
      recording_id: "recording-2",
      title: "Second",
    },
  ],
};

const sources = [
  {
    id: "member-1",
    label: "member-1.flac",
    detail: "flac · 3:20",
    path: "album/01.flac",
  },
  {
    id: "member-2",
    label: "member-2.flac",
    detail: "flac · 3:40",
    path: "album/02.flac",
  },
  { id: "member-3", label: "member-3.flac" },
];

const automaticAssignments: MatchingBoardAutomaticAssignments = {
  "release-1": {
    "member-1": "track-1",
    "member-2": "track-2",
  },
};

const savedScores: MatchingBoardSavedScores = {
  "release-1": {
    "member-1": { "track-1": 0.87, "track-9": 0.88 },
    "member-2": { "track-2": 0.42 },
  },
  "release-other": {
    "member-1": { "track-2": 0.99 },
  },
};

beforeEach(() => {
  const storage = createMemoryStorage();
  storage.setItem("melotrove-language", "en");
  vi.stubGlobal("localStorage", storage);
});

afterEach(() => {
  vi.unstubAllGlobals();
});

function positionRow(title: string) {
  const row = screen.getByText(title).closest("li");
  if (!row) throw new Error(`Missing position row for ${title}`);
  return row;
}

it("shows manual assignments, saved automatic facts, and release-scoped scores", () => {
  const onInspectPosition = vi.fn();
  const onInspectSource = vi.fn();
  render(
    <I18nProvider>
      <MatchingBoard
        groupId="group-1"
        release={release}
        members={["member-1", "member-2", "member-3"]}
        sources={sources}
        assignments={{ "member-1": "track-2" }}
        automaticAssignments={automaticAssignments}
        savedScores={savedScores}
        onAssign={vi.fn()}
        onInspectPosition={onInspectPosition}
        onInspectSource={onInspectSource}
      />
    </I18nProvider>,
  );

  const first = positionRow("First");
  expect(within(first).getByText("Saved automatic")).toBeVisible();
  expect(within(first).getByText("score 87%")).toBeVisible();

  const second = positionRow("Second");
  expect(within(second).getByText("member-1.flac")).toBeVisible();
  expect(within(second).getByText("score 42%")).toBeVisible();
  expect(within(second).queryByText("score 99%")).toBeNull();
  expect(within(second).queryByText("score 88%")).toBeNull();
  expect(
    screen.queryByRole("button", { name: "Select member-1.flac" }),
  ).toBeNull();

  fireEvent.click(
    within(first).getByRole("button", {
      name: "Inspect position Disc 1 · track 1 · First",
    }),
  );
  expect(onInspectPosition).toHaveBeenCalledWith("track-1");
  fireEvent.click(
    within(first).getByRole("button", { name: /Saved automatic/ }),
  );
  expect(onInspectSource).toHaveBeenCalledWith("member-1");
  expect(onInspectPosition).toHaveBeenLastCalledWith("track-1");
});

it("prefers paired inspection over the separate source and position handlers", () => {
  const onInspectPair = vi.fn();
  const onInspectPosition = vi.fn();
  const onInspectSource = vi.fn();
  render(
    <I18nProvider>
      <MatchingBoard
        groupId="group-1"
        release={release}
        members={["member-1", "member-2", "member-3"]}
        sources={sources}
        assignments={{}}
        automaticAssignments={automaticAssignments}
        onAssign={vi.fn()}
        onInspectPosition={onInspectPosition}
        onInspectSource={onInspectSource}
        onInspectPair={onInspectPair}
      />
    </I18nProvider>,
  );

  const first = positionRow("First");
  fireEvent.click(
    within(first).getByRole("button", { name: /Saved automatic/ }),
  );
  expect(onInspectPair).toHaveBeenCalledWith("member-1", "track-1");
  expect(onInspectSource).not.toHaveBeenCalled();
  expect(onInspectPosition).not.toHaveBeenCalled();
});

it("assigns from the free source rail without dragging", () => {
  const onAssign = vi.fn();
  const onUnassign = vi.fn();
  render(
    <I18nProvider>
      <MatchingBoard
        groupId="group-1"
        release={release}
        members={["member-1", "member-2", "member-3"]}
        sources={sources}
        assignments={{ "member-1": "track-2" }}
        onAssign={onAssign}
        onUnassign={onUnassign}
      />
    </I18nProvider>,
  );

  const select = screen.getByRole("button", { name: "Select member-2.flac" });
  fireEvent.click(select);
  expect(select).toHaveAttribute("aria-pressed", "true");

  const second = positionRow("Second");
  fireEvent.click(
    within(second).getByRole("button", {
      name: "Add member-2.flac to Disc 2 · track 2 · Second",
    }),
  );
  expect(onAssign).toHaveBeenCalledWith("member-2", "track-2");
  expect(select).toHaveAttribute("aria-pressed", "false");

  fireEvent.click(
    within(second).getByRole("button", {
      name: "Remove member-1.flac from Disc 2 · track 2 · Second",
    }),
  );
  expect(onUnassign).toHaveBeenCalledWith("member-1");
});

it("keys saved scores by release, member, and track", () => {
  expect(
    matchingBoardSavedScore(savedScores, release, "member-1", "track-1"),
  ).toBe(0.87);
  expect(
    matchingBoardSavedScore(savedScores, release, "member-1", "track-2"),
  ).toBeUndefined();
  expect(
    matchingBoardSavedScore(savedScores, release, "member-1", "track-9"),
  ).toBe(0.88);
  expect(
    matchingBoardSavedScore(
      savedScores,
      { ...release, provider_release_id: "release-other" },
      "member-1",
      "track-2",
    ),
  ).toBe(0.99);

  // The parent's savedAutomaticAssignments helper returns release-scoped maps.
  const scopedAutomatic = { "member-1": "track-1" };
  const scopedScores = { "member-1": { "track-1": 0.87 } };
  expect(matchingBoardAutomaticForRelease(scopedAutomatic, release)).toEqual(
    scopedAutomatic,
  );
  expect(matchingBoardSavedTrack(scopedAutomatic, release, "member-1")).toBe(
    "track-1",
  );
  expect(
    matchingBoardSavedScore(scopedScores, release, "member-1", "track-1"),
  ).toBe(0.87);
});

it("rejects drag payloads from another group", () => {
  const payload = JSON.stringify({ groupId: "group-1", fileId: "member-2" });
  expect(decodeMatchingFilePayload(payload, "group-1")).toBe("member-2");
  expect(decodeMatchingFilePayload(payload, "group-2")).toBeNull();
  expect(decodeMatchingFilePayload("not json", "group-1")).toBeNull();
});

it("projects only selected-release candidate scores by member and track", () => {
  const scores = matchingBoardScoresFromCandidates(
    [
      {
        file_member_id: "member-1",
        release_id: "release-1",
        release_confidence: 0.8,
        edges: [],
        positions: [
          {
            track_id: "track-1",
            medium: 0,
            position: 0,
            assignment_confidence: 0.87,
          },
          {
            track_id: "track-9",
            medium: 0,
            position: 1,
            assignment_confidence: 0.88,
          },
        ],
      },
      {
        file_member_id: "member-1",
        release_id: "release-other",
        release_confidence: 0.9,
        edges: [],
        positions: [
          {
            track_id: "track-2",
            medium: 1,
            position: 0,
            assignment_confidence: 0.99,
          },
        ],
      },
      {
        file_member_id: "member-2",
        release_id: "release-1",
        release_confidence: 0.5,
        edges: [],
        positions: [{ track_id: "track-2", medium: 1, position: 0 }],
      },
    ],
    release,
  );
  expect(scores).toEqual({ "member-1": { "track-1": 0.87 } });
});
