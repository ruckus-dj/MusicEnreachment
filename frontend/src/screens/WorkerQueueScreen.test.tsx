// @vitest-environment jsdom
import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { WorkerQueueScreen } from "./WorkerQueueScreen";

describe("WorkerQueueScreen", () => {
  it("links a source-backed job to its track inspector", () => {
    render(
      <WorkerQueueScreen
        queue={{
          observed_at: "2026-08-14T10:00:00+00:00",
          worker: {
            configured_concurrency: 1,
            liveness: "available",
            slots: [
              {
                slot: 0,
                state: "processing",
                observed_at: "2026-08-14T10:00:00+00:00",
                error: null,
                job_id: "job-1",
                job_kind: "filesystem_scan",
              },
              {
                slot: 4,
                state: "disabled",
                observed_at: "2026-08-14T10:00:00+00:00",
                error: null,
                job_id: null,
                job_kind: null,
              },
            ],
          },
          summary: { running: 1, ready: 0, retry_wait: 0 },
          total_jobs: 1,
          jobs: [
            {
              job_id: "job-1",
              kind: "filesystem_scan",
              state: "running",
              queue_state: "ready",
              source_id: "source-1",
              library_record_id: "record-1",
              release_mbid: null,
              created_at: "2026-08-14T10:00:00+00:00",
              next_attempt_at: null,
              attempt_count: 1,
              target: {
                record_id: "record-1",
                source_id: "source-1",
                title: "Очередной трек",
                artist: "Исполнитель",
                album: "Альбом",
                path: "/incoming/track.flac",
              },
            },
          ],
        }}
        loading={false}
        error=""
        onRefresh={vi.fn()}
      />,
    );

    const link = screen.getByRole("link", { name: "Открыть инспектор трека: Очередной трек" });
    expect(link.getAttribute("href")).toBe("/library/track/record-1?source_id=source-1");
    expect(screen.getByText("Исполнитель · Альбом")).toBeTruthy();
    expect(screen.getByText("Обрабатывает: Проверка источника")).toBeTruthy();
    expect(screen.queryByText("Отключён")).toBeNull();
  });
});
