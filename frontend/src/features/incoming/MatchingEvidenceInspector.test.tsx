import { render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { MatchingReviewCandidate } from "../../api/generated/client.schemas";
import { MatchingEvidenceInspector } from "./MatchingEvidenceInspector";

describe("MatchingEvidenceInspector", () => {
  it("shows backend-shaped nested scores and factor evidence without calculating scores", () => {
    const candidate: MatchingReviewCandidate = {
      file_member_id: "file-1",
      release_id: "release-1",
      release_confidence: 0.82,
      edges: [
        {
          release_profile: {
            Profile: "release",
            PolicyVersion: "matching-v1",
            Confidence: 0.82,
            Factors: [
              {
                Component: "release_title",
                State: "available",
                Provenance: "release metadata",
                SourceRaw: ["  Song  "],
                CandidateRaw: ["Song"],
                SourceNormalized: "song",
                CandidateNormalized: "song",
                Weight: 4,
                Contribution: 3.2,
              },
              {
                Component: "release_artist",
                State: "missing",
                Provenance: "artist metadata",
                SourceRaw: [],
                CandidateRaw: [],
                SourceNormalized: "",
                CandidateNormalized: "",
                Weight: 4,
                Contribution: 0,
              },
              {
                Component: "release_duration",
                State: "not_applicable",
                Provenance: "duration in seconds",
                SourceRaw: ["0"],
                CandidateRaw: ["0"],
                SourceNormalized: "0",
                CandidateNormalized: "0",
                Weight: 2,
                Contribution: 0,
              },
            ],
          },
          assignment_profile: {
            Profile: "recording",
            PolicyVersion: "matching-v1",
            Confidence: 0.76,
            Factors: [
              {
                Component: "recording_title",
                State: "available",
                Provenance: "MusicBrainz recording search",
                SourceRaw: ["  Song  "],
                CandidateRaw: ["Song"],
                SourceNormalized: "song",
                CandidateNormalized: "song",
                Weight: 4,
                Contribution: 3.04,
              },
            ],
          },
        },
      ],
      positions: [
        {
          track_id: "track-1",
          medium: 0,
          position: 0,
          assignment_confidence: 0.76,
        },
      ],
    };
    render(
      <MatchingEvidenceInspector
        candidate={candidate}
        rawTags={{ TITLE: ["  Song  "] }}
      />,
    );
    const inspector = screen.getByRole("region", {
      name: "Доказательства текущей оценки",
    });
    const releaseProfile =
      within(inspector).getByText("Профиль издания").parentElement;
    const assignmentProfile =
      within(inspector).getByText("Профиль назначения").parentElement;
    expect(releaseProfile).not.toBeNull();
    expect(assignmentProfile).not.toBeNull();
    for (const [profile, values] of [
      [
        releaseProfile,
        [
          "release",
          "matching-v1",
          "0.82",
          "release_title",
          "available",
          "release metadata",
          "Song",
          "Song",
          "song",
          "4",
          "3.2",
          "missing",
          "not_applicable",
        ],
      ],
      [
        assignmentProfile,
        [
          "recording",
          "matching-v1",
          "0.76",
          "recording_title",
          "MusicBrainz recording search",
          "3.04",
        ],
      ],
    ] as const) {
      for (const value of values)
        expect(
          within(profile as HTMLElement).getAllByText(value, { exact: false })
            .length,
        ).toBeGreaterThan(0);
    }
    expect(
      within(inspector).queryByText(/не предоставлено сохранённой оценкой/i),
    ).not.toBeInTheDocument();
    expect(within(inspector).getByText(/не рассчитывает/i)).toBeInTheDocument();
  });

  it("keeps support for the earlier lowercase flat evidence shape", () => {
    const candidate: MatchingReviewCandidate = {
      file_member_id: "file-1",
      release_id: "release-1",
      release_confidence: 0.5,
      edges: [
        {
          factor: "title",
          raw: "Song",
          normalized: "song",
          provenance: "legacy snapshot",
        },
      ],
      positions: [],
    };
    render(<MatchingEvidenceInspector candidate={candidate} />);
    const inspector = screen.getByRole("region", {
      name: "Доказательства текущей оценки",
    });
    expect(within(inspector).getByText("Song")).toBeInTheDocument();
    expect(within(inspector).getByText("song")).toBeInTheDocument();
    expect(within(inspector).getByText("legacy snapshot")).toBeInTheDocument();
    expect(
      within(inspector).queryByText(/не предоставлено сохранённой оценкой/i),
    ).not.toBeInTheDocument();
  });

  it("states when evidence is missing without inventing any score", () => {
    render(<MatchingEvidenceInspector rawTags={{}} />);
    expect(
      screen.getByText(/Кандидат для этого файла и издания отсутствует/),
    ).toBeInTheDocument();
    expect(
      screen.getByText(/Факторы, веса и вклад не сохранены/),
    ).toBeInTheDocument();
    expect(
      screen.getByText(
        /неприменимые факторы отображаются только если они явно указаны/,
      ),
    ).toBeInTheDocument();
  });
});
