import { fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type {
  MatchingReviewCandidate,
  MatchingReviewLayoutPosition,
} from "../../api/generated/client.schemas";
import { I18nProvider } from "../../app/i18n/host";
import { createMemoryStorage } from "../../test/memoryStorage";
import { MatchingEvidenceInspector } from "./MatchingEvidenceInspector";

const position: MatchingReviewLayoutPosition = {
  track_id: "track-1",
  medium: 0,
  position: 0,
  displayed_number: "1",
  recording_id: "recording-1",
  title: "First",
};

const candidate: MatchingReviewCandidate = {
  file_member_id: "member-1",
  release_id: "release-1",
  release_confidence: 0.55,
  positions: [
    {
      track_id: "track-1",
      medium: 0,
      position: 0,
      displayed_number: "1",
      assignment_confidence: 0.87,
    },
    {
      track_id: "track-2",
      medium: 1,
      position: 0,
      displayed_number: "2",
      assignment_confidence: 0.2,
    },
  ],
  edges: [
    {
      schema_version: 1,
      medium: 0,
      position: 0,
      release_profile: {
        Profile: "release",
        Confidence: 0.55,
        Factors: [],
      },
      assignment_profile: {
        Profile: "recording_release",
        Confidence: 0.87,
        Factors: [
          {
            Component: "release_title",
            State: "available",
            Value: 0.9,
            Weight: 3,
          },
          {
            Component: "recording_artist",
            State: "available",
            Value: 0.8,
            Weight: 2,
          },
          {
            Component: "track_number",
            State: "not_applicable",
            Value: 0,
            Weight: 1,
          },
        ],
      },
    },
    {
      schema_version: 1,
      medium: 1,
      position: 0,
      assignment_profile: {
        Confidence: 0.2,
        Factors: [
          {
            Component: "acoustid",
            State: "available",
            Value: 0.2,
            Weight: 2,
          },
        ],
      },
    },
  ],
};

const rawTags = {
  ALBUM: ["Source album"],
  ARTIST: ["Source artist"],
  TITLE: ["Source title"],
};

function renderInspector(
  overrides: Partial<Parameters<typeof MatchingEvidenceInspector>[0]> = {},
) {
  return render(
    <I18nProvider>
      <MatchingEvidenceInspector
        candidate={candidate}
        position={position}
        rawTags={rawTags}
        {...overrides}
      />
    </I18nProvider>,
  );
}

function evidenceRegion() {
  return screen.getByRole("region", {
    name: "Current evaluation evidence",
  });
}

beforeEach(() => {
  const storage = createMemoryStorage();
  storage.setItem("melotrove-language", "en");
  vi.stubGlobal("localStorage", storage);
});

afterEach(() => {
  vi.unstubAllGlobals();
});

it("shows one short percentage for the selected track", () => {
  renderInspector();
  const evidence = evidenceRegion();

  expect(within(evidence).getByText("Disc 1 · track 1 · First")).toBeVisible();
  expect(within(evidence).getAllByText("87%")).toHaveLength(2);
  expect(
    within(evidence).getByText(/2 of 3 saved scoring factors/),
  ).toBeVisible();
  expect(within(evidence).queryByText("20%")).not.toBeInTheDocument();
  expect(evidence).not.toHaveTextContent("member-1");
  expect(evidence).not.toHaveTextContent("release-1");
  expect(evidence).not.toHaveTextContent("track-1");
});

it("keeps raw tags and diagnostics collapsed until requested", () => {
  renderInspector();
  const evidence = evidenceRegion();
  const rawDetails = within(evidence)
    .getByText("Raw file tags")
    .closest("details");
  const diagnostics = within(evidence)
    .getByText("Diagnostics for this track")
    .closest("details");

  expect(rawDetails).not.toHaveAttribute("open");
  expect(diagnostics).not.toHaveAttribute("open");

  fireEvent.click(within(evidence).getByText("Raw file tags"));
  expect(rawDetails).toHaveAttribute("open");
  const rawTagsPre = within(evidence).getByText(/"ARTIST"/);
  expect(JSON.parse(rawTagsPre.textContent ?? "")).toEqual(rawTags);

  fireEvent.click(within(evidence).getByText("Diagnostics for this track"));
  expect(diagnostics).toHaveAttribute("open");
  expect(within(evidence).getByText("Release match")).toBeVisible();
  expect(within(evidence).getAllByText("87%")).toHaveLength(2);
  expect(within(evidence).getByText("55%")).toBeVisible();
  expect(within(evidence).getByText("release title")).toBeVisible();
  expect(within(evidence).getByText("recording artist")).toBeVisible();
  expect(within(evidence).getByText(/90%/)).toBeVisible();
  expect(within(evidence).getByText(/weight 3/)).toBeVisible();
  expect(within(evidence).getByText("1 factor was not scored.")).toBeVisible();
  expect(within(evidence).queryByText("acoustid")).not.toBeInTheDocument();
  expect(within(evidence).queryByText("20%")).not.toBeInTheDocument();
});

it("preserves the no-evidence behavior", () => {
  renderInspector({ candidate: undefined });
  const evidence = evidenceRegion();

  expect(
    within(evidence).getByText(
      "No candidate exists for this file and release.",
    ),
  ).toBeVisible();
  expect(
    within(evidence).queryByText("Diagnostics for this track"),
  ).not.toBeInTheDocument();
  expect(within(evidence).getByText("Raw file tags")).toBeVisible();
});

it("reports a missing comparison for an unmatched selected track", () => {
  renderInspector({
    position: { ...position, track_id: "track-3", position: 1 },
  });

  expect(
    within(evidenceRegion()).getByText(
      "No saved comparison exists for this track.",
    ),
  ).toBeVisible();
});
