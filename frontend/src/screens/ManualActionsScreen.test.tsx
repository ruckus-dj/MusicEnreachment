// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ManualActionsScreen } from "./ManualActionsScreen";

afterEach(cleanup);

describe("ManualActionsScreen", () => {
  it("shows only the selected manual-action category", () => {
    const onNavigate = vi.fn();
    const onFilterChange = vi.fn();

    render(
      <ManualActionsScreen
        tracks={[
          {
            item: {
              record_id: "analysis-error",
              source_state: "present",
              processing_state: "blocked_infrastructure",
              match_state: "unmatched",
              publication_state: "current",
              metadata_state: "final",
              sources: [
                {
                  source_id: "source-error",
                  path: "/library/Error Track.flac",
                  sha256: "a",
                  state: "present",
                  tag_observations: [{ name: "TITLE", value: "Ошибка анализа", format: "FLAC" }],
                },
              ],
              publications: [],
            },
            source: {
              source_id: "source-error",
              path: "/library/Error Track.flac",
              sha256: "a",
              state: "present",
              tag_observations: [{ name: "TITLE", value: "Ошибка анализа", format: "FLAC" }],
            },
          },
          {
            item: {
              record_id: "needs-review",
              source_state: "present",
              processing_state: "needs_review",
              match_state: "needs_review",
              publication_state: "current",
              metadata_state: "final",
              sources: [
                {
                  source_id: "source-review",
                  path: "/library/Review Track.flac",
                  sha256: "b",
                  state: "present",
                  tag_observations: [{ name: "TITLE", value: "Нужна проверка", format: "FLAC" }],
                },
              ],
              publications: [],
            },
            source: {
              source_id: "source-review",
              path: "/library/Review Track.flac",
              sha256: "b",
              state: "present",
              tag_observations: [{ name: "TITLE", value: "Нужна проверка", format: "FLAC" }],
            },
          },
        ]}
        filter="analysis-error"
        counts={{ "analysis-error": 1, "needs-review": 1 }}
        reprocessing={false}
        onNavigate={onNavigate}
        onFilterChange={onFilterChange}
        onRetry={vi.fn()}
      />,
    );

    expect(screen.getByText("Ошибка анализа")).toBeTruthy();
    expect(screen.queryByText("Нужна проверка")).toBeNull();

    fireEvent.click(screen.getByRole("button", { name: "Нужна проверка 1" }));

    expect(onFilterChange).toHaveBeenCalledWith("needs-review");
  });

  it("retries an analysis error without opening its inspector", () => {
    const onNavigate = vi.fn();
    const onRetry = vi.fn();

    render(
      <ManualActionsScreen
        tracks={[
          {
            item: {
              record_id: "analysis-error",
              source_state: "present",
              processing_state: "blocked_infrastructure",
              match_state: "unmatched",
              publication_state: "current",
              metadata_state: "final",
              sources: [],
              publications: [],
            },
            source: {
              source_id: "source-error",
              path: "/library/Error Track.flac",
              sha256: "a",
              state: "present",
              tag_observations: [{ name: "TITLE", value: "Ошибка анализа", format: "FLAC" }],
            },
          },
        ]}
        filter="analysis-error"
        counts={{ "analysis-error": 1, "needs-review": 0 }}
        reprocessing={false}
        onNavigate={onNavigate}
        onFilterChange={vi.fn()}
        onRetry={onRetry}
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: "Повторить" }));

    expect(onRetry).toHaveBeenCalledWith("analysis-error", "source-error");
    expect(onNavigate).not.toHaveBeenCalled();
  });

  it("hides a replaced source even when its former record is blocked", () => {
    render(
      <ManualActionsScreen
        tracks={[
          {
            item: {
              record_id: "replaced-record",
              source_state: "present",
              processing_state: "blocked_infrastructure",
              match_state: "unmatched",
              publication_state: "absent",
              metadata_state: "original",
              sources: [],
              publications: [],
            },
            source: {
              source_id: "replaced-source",
              path: "/library/Replaced Track.flac",
              sha256: "a",
              state: "replaced",
              tag_observations: [{ name: "TITLE", value: "Устаревший трек", format: "FLAC" }],
            },
          },
        ]}
        filter="analysis-error"
        counts={{ "analysis-error": 0, "needs-review": 0 }}
        reprocessing={false}
        onNavigate={vi.fn()}
        onFilterChange={vi.fn()}
        onRetry={vi.fn()}
      />,
    );

    expect(screen.queryByText("/library/Replaced Track.flac")).toBeNull();
  });
});
