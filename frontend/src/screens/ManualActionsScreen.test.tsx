// @vitest-environment jsdom
import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ManualActionsScreen } from "./ManualActionsScreen";

describe("ManualActionsScreen", () => {
  it("shows only the selected manual-action category", () => {
    const onNavigate = vi.fn();

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
        reprocessing={false}
        onNavigate={onNavigate}
        onRetry={vi.fn()}
      />,
    );

    expect(screen.getByText("Ошибка анализа")).toBeTruthy();
    expect(screen.queryByText("Нужна проверка")).toBeNull();

    fireEvent.click(screen.getByRole("button", { name: "Нужна проверка 1" }));

    expect(screen.queryByText("Ошибка анализа")).toBeNull();
    expect(screen.getByText("Нужна проверка")).toBeTruthy();
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
        reprocessing={false}
        onNavigate={onNavigate}
        onRetry={onRetry}
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: "Повторить" }));

    expect(onRetry).toHaveBeenCalledWith("analysis-error", "source-error");
    expect(onNavigate).not.toHaveBeenCalled();
  });
});
