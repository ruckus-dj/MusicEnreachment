import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { afterEach, describe, expect, it } from "vitest";
import type { SetupStateBody } from "../../api/generated/client.schemas";
import { server } from "../../test/server";
import { SetupSummary } from "./SetupSummary";

function summary(
  overrides: Partial<SetupStateBody["settings"]> = {},
  healthy = true,
): SetupStateBody {
  return {
    completed: false,
    platform: {
      diagnostic: false,
      supported: true,
      goos: "linux",
      goarch: "arm64",
    },
    settings: {
      tools_directory: "/srv/tools",
      output_directory: "/srv/music",
      publication_format: "m4a",
      publication_path_template: "{TRACK_NAME}.{EXT}",
      publication_remove_sources: false,
      musicbrainz_mode: "public",
      musicbrainz_base_url: "",
      musicbrainz_self_hosted_throttle: false,
      musicbrainz_self_hosted_delay_seconds: 0.5,
      lrclib_enabled: true,
      sha256_enabled: true,
      log_level: "info",
      source_file_concurrency: 4,
      has_acoustid_application_key: true,
      has_github_token: false,
      active_ffmpeg_installation_id: "ff-id",
      active_fpcalc_installation_id: "fp-id",
      musicbrainz_verified_at: "2026-10-01T00:00:00Z",
      ...overrides,
    },
    configuration_health: {
      healthy,
      problems: healthy ? [] : ["MusicBrainz не проверен"],
    },
  };
}

afterEach(cleanup);

describe("SetupSummary", () => {
  it("shows human publication, cleanup, metadata and verified active tool versions without IDs", async () => {
    server.use(
      http.get("/api/tools/installations", () =>
        HttpResponse.json({
          installations: [
            {
              id: "ff-id",
              package_kind: "ffmpeg",
              release_identity: "8.0",
              source_name: "trusted",
              state: "ready",
              active: true,
              created_at: "2026-10-01T00:00:00Z",
              verified_at: "2026-10-01T00:00:00Z",
              version: "ffmpeg n8.0.1-build+X + ffprobe n8.0.2-other+Y",
            },
            {
              id: "fp-id",
              package_kind: "fpcalc",
              release_identity: "1.5",
              source_name: "trusted",
              state: "ready",
              active: true,
              created_at: "2026-10-01T00:00:00Z",
              verified_at: "2026-10-01T00:00:00Z",
              version: "1.5.1",
            },
          ],
        }),
      ),
    );
    const { container } = render(<SetupSummary state={summary()} />);
    expect(
      screen.getByRole("heading", { name: "Готово к работе" }),
    ).toBeVisible();
    expect(
      screen.getByText(
        "AAC и ALAC копируются без перекодирования; другие lossless-кодеки преобразуются в ALAC, остальные — в AAC с потерями. Возможность обработки зависит от поддерживаемых FFmpeg кодеков и параметров исходного аудио.",
      ),
    ).toBeVisible();
    expect(screen.getByText("Сохранять")).toBeVisible();
    expect(screen.getByText("Ключ задан")).toBeVisible();
    await waitFor(() =>
      expect(
        screen.getByText(
          "Готов · ffmpeg n8.0.1-build+X + ffprobe n8.0.2-other+Y",
        ),
      ).toBeVisible(),
    );
    expect(screen.getByText("Готов · 1.5.1")).toBeVisible();
    expect(container).not.toHaveTextContent("configuration:");
    expect(container).not.toHaveTextContent("built with gcc");
    expect(container).not.toHaveTextContent("ff-id");
    expect(container).not.toHaveTextContent("fp-id");
  });

  it("shows unhealthy reasons, actual source handling and unknown inventory state", async () => {
    server.use(
      http.get("/api/tools/installations", () =>
        HttpResponse.json({ installations: [] }),
      ),
    );
    render(
      <SetupSummary
        state={summary(
          { publication_format: "source", publication_remove_sources: true },
          false,
        )}
      />,
    );
    expect(
      screen.getByRole("heading", { name: "Требует внимания" }),
    ).toBeVisible();
    expect(screen.getByText("MusicBrainz не проверен")).toBeVisible();
    expect(screen.getByText("Удалять")).toBeVisible();
    await waitFor(() =>
      expect(
        screen.getAllByText("Состояние неизвестно — обновите проверку"),
      ).toHaveLength(2),
    );
  });

  it("reports an installation inventory request failure without blocking the summary", async () => {
    server.use(
      http.get(
        "/api/tools/installations",
        () => new HttpResponse(null, { status: 503 }),
      ),
    );
    render(<SetupSummary state={summary()} />);
    expect(
      screen.getByRole("heading", { name: "Готово к работе" }),
    ).toBeVisible();
    await waitFor(() =>
      expect(
        screen.getByText(/Состояние инструментов недоступно/),
      ).toBeVisible(),
    );
  });
});
