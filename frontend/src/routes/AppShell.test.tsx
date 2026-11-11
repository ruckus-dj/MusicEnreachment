import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { App } from "../App";
import type {
  SetupStateBody,
  SourceRootResponse,
} from "../api/generated/client.schemas";
import { createMemoryStorage } from "../test/memoryStorage";
import { server } from "../test/server";

const state: SetupStateBody = {
  completed: false,
  platform: {
    diagnostic: false,
    supported: true,
    goos: "linux",
    goarch: "amd64",
  },
  settings: {
    tools_directory: "",
    output_directory: "",
    work_directory: "",
    publication_format: "",
    publication_path_template:
      '{ALBUM_PRIMARY_ARTIST} [{ALBUM_PRIMARY_ARTIST_MBID_PART | ALBUM_PRIMARY_ARTIST_ID}]/{ALBUM_TYPE | "Unclassified"}/{ALBUM_YEAR | "Unknown year"} - {ALBUM_NAME} [{ALBUM_MBID_PART | ALBUM_ID}]/{DISC_NUMBER:02}-{TRACK_NUMBER:02} - {TRACK_NAME}.{EXT}',
    publication_remove_sources: false,
    musicbrainz_mode: "public",
    musicbrainz_base_url: "",
    musicbrainz_self_hosted_throttle: false,
    musicbrainz_self_hosted_delay_seconds: 0.5,
    lrclib_enabled: true,
    sha256_enabled: true,
    log_level: "info",
    source_file_concurrency: 4,
    has_acoustid_application_key: false,
    has_github_token: false,
  },
  configuration_health: { healthy: false, problems: ["setup incomplete"] },
};

const completedState: SetupStateBody = {
  ...state,
  completed: true,
  configuration_health: { healthy: true, problems: [] },
};

const sourceRoot: SourceRootResponse = {
  id: "root-1",
  display_name: "Входящие",
  configured_path: "/srv/inbox",
  processing_mode: "in_place",
  enabled: true,
  status: "available",
  stale: false,
  scan_generation: 4,
  location_count: 12,
  inventory_path: "/srv/inbox",
  last_successful_scan_at: "2026-09-26T10:20:00Z",
  created_at: "2026-09-01T00:00:00Z",
  updated_at: "2026-09-26T10:20:00Z",
};
beforeEach(() => {
  window.location.hash = "";
  const localStorage = createMemoryStorage();
  localStorage.setItem("melotrove-language", "ru");
  vi.stubGlobal("localStorage", localStorage);
  server.use(
    http.get("/api/sources", () => HttpResponse.json({ sources: [] })),
    http.get("/api/operations", () => HttpResponse.json({ operations: [] })),
    http.get("/api/sources/:sourceId/locations", () =>
      HttpResponse.json({ locations: [] }),
    ),
  );
});
afterEach(() => {
  cleanup();
  window.location.hash = "";
  vi.unstubAllGlobals();
});
describe("AppShell sources routing", () => {
  it("provides a working skip link and one accessible current-section navigation", async () => {
    const setupRequests = vi.fn();
    server.use(
      http.get("/api/setup", () => {
        setupRequests();
        return HttpResponse.json(completedState);
      }),
    );
    window.location.hash = "/operations";
    render(<App />);

    const navigation = await screen.findByRole("navigation", {
      name: "Разделы приложения",
    });
    expect(navigation.querySelectorAll("a")).toHaveLength(6);
    expect(
      screen.queryByRole("link", { name: "Публикации" }),
    ).not.toBeInTheDocument();
    expect(
      screen.getByRole("link", { name: "Операции", current: "page" }),
    ).toBeInTheDocument();
    expect(
      screen.getByText("Операции", { selector: ".app-section-label" }),
    ).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: /Язык интерфейса/ }));
    fireEvent.click(await screen.findByRole("option", { name: "English" }));
    expect(
      await screen.findByRole("navigation", { name: "Application sections" }),
    ).toHaveTextContent("IncomingLibraryOwn catalogSourcesOperationsSettings");
    expect(
      screen.getByRole("link", { name: "Operations", current: "page" }),
    ).toBeInTheDocument();
    expect(
      screen.getByText("Operations", { selector: ".app-section-label" }),
    ).toBeInTheDocument();
    expect(setupRequests).toHaveBeenCalledTimes(1);

    const content = document.querySelector("#app-content");
    expect(content).toHaveAttribute("tabindex", "-1");
    fireEvent.click(screen.getByRole("button", { name: "Skip to content" }));
    expect(document.activeElement).toBe(content);
    expect(window.location.hash).toBe("#/operations");
  });

  it("loads source roots through the production App root without a supplied query context", async () => {
    server.use(
      http.get("/api/setup", () => HttpResponse.json(completedState)),
      http.get("/api/sources", () =>
        HttpResponse.json({ sources: [sourceRoot] }),
      ),
    );
    window.location.hash = "/sources";
    render(<App />);
    expect(
      await screen.findByRole("heading", { level: 1, name: "Источники" }),
    ).toBeVisible();
    expect(await screen.findByText("/srv/inbox")).toBeVisible();
  });

  it("keeps the legacy publications URL inside the library without standalone navigation", async () => {
    server.use(
      http.get("/api/setup", () => HttpResponse.json(completedState)),
      http.get("*/api/library/artists", () =>
        HttpResponse.json({
          items: [],
          total: 0,
          limit: 50,
          offset: 0,
          summary: {
            artist_count: 0,
            release_count: 0,
            position_count: 0,
            published_position_count: 0,
          },
        }),
      ),
    );
    window.location.hash = "/publications";
    render(<App />);

    expect(
      await screen.findByRole("heading", { level: 1, name: "Медиатека" }),
    ).toBeVisible();
    expect(
      screen.getByRole("navigation", { name: "Разделы приложения" }),
    ).not.toHaveTextContent("Публикации");
  });
});
