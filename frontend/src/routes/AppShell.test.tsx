import { JSDOM } from "jsdom";
import "@testing-library/jest-dom/vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { App } from "../App";
import type {
  SetupStateBody,
  SourceRootResponse,
} from "../api/generated/client.schemas";
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
  vi.stubGlobal(
    "localStorage",
    new JSDOM("", { url: "http://localhost" }).window.localStorage,
  );
});
afterEach(() => {
  cleanup();
  window.location.hash = "";
});
describe("AppShell gates", () => {
  it("waits for backend before routing, then gates product routes into Setup", async () => {
    let respond: (value: Response) => void = () => {};
    const request = new Promise<void>((resolve) => {
      server.use(
        http.get(
          "/api/setup",
          () =>
            new Promise<Response>((done) => {
              respond = done;
              resolve();
            }),
        ),
      );
    });
    window.location.hash = "/settings";
    render(<App />);
    expect(screen.getByRole("status")).toHaveTextContent("Загрузка");
    expect(screen.queryByText("Managed tools")).not.toBeInTheDocument();
    await request;
    const redirected = new Promise<void>((resolve, reject) => {
      const timeout = setTimeout(() => {
        window.removeEventListener("hashchange", onChange);
        reject(new Error("Setup route did not replace the settings route"));
      }, 4000);
      function onChange() {
        if (window.location.hash !== "#/setup") return;
        clearTimeout(timeout);
        window.removeEventListener("hashchange", onChange);
        resolve();
      }
      window.addEventListener("hashchange", onChange);
    });
    respond(HttpResponse.json(state));
    await redirected;
    expect(
      screen.getByRole("heading", { name: "Первый запуск" }),
    ).toBeVisible();
    expect(window.location.hash).toBe("#/setup");
  });
  it("provides error and retry before selecting route", async () => {
    let fail = true;
    server.use(
      http.get("/api/setup", () =>
        fail
          ? HttpResponse.json(
              { detail: "database unavailable" },
              { status: 503 },
            )
          : HttpResponse.json(state),
      ),
    );
    render(<App />);
    await waitFor(() =>
      expect(screen.getByRole("alert")).toHaveTextContent(
        "database unavailable",
      ),
    );
    fail = false;
    fireEvent.click(screen.getByRole("button", { name: "Повторить загрузку" }));
    await waitFor(() =>
      expect(
        screen.getByRole("heading", { name: "Первый запуск" }),
      ).toBeVisible(),
    );
  });
  it("redirects Setup after completion and opens settings only after backend completion", async () => {
    window.location.hash = "/setup";
    server.use(
      http.get("/api/setup", () =>
        HttpResponse.json({ ...state, completed: true }),
      ),
      http.get("/api/settings", () =>
        HttpResponse.json({ ...state, completed: true }),
      ),
      http.get("/api/tools/installations", () =>
        HttpResponse.json({ installations: [] }),
      ),
      http.get("/api/operations", () => HttpResponse.json({ operations: [] })),
    );
    render(<App />);
    await waitFor(() => expect(window.location.hash).toBe("#/"));
    fireEvent.click(screen.getAllByRole("link", { name: "Настройки" })[0]);
    await waitFor(() =>
      expect(screen.getByText("Managed tools")).toBeVisible(),
    );
  });
  it("renders isolated platform diagnostic without Setup/product", async () => {
    window.location.hash = "/settings";
    server.use(
      http.get("/api/setup", () =>
        HttpResponse.json({
          ...state,
          platform: {
            ...state.platform,
            diagnostic: true,
            reason: "platform mismatch",
          },
        }),
      ),
    );
    render(<App />);
    await waitFor(() =>
      expect(
        screen.getByRole("heading", { name: "Диагностика платформы" }),
      ).toBeVisible(),
    );
    expect(screen.getByRole("alert")).toHaveTextContent("platform mismatch");
    expect(screen.queryByText("Managed tools")).not.toBeInTheDocument();
    expect(window.location.hash).toBe("#/settings");
  });
});
describe("AppShell sources routing", () => {
  it("renders a direct nested file URL instead of the landing when Setup is complete", async () => {
    const subscriptions = vi.spyOn(window, "addEventListener");
    server.use(
      http.get("/api/setup", () => HttpResponse.json(completedState)),
      http.get("/api/sources/root-1/locations/file-1", () =>
        HttpResponse.json({
          root_id: "root-1",
          location_id: "file-1",
          relative_path: "direct.flac",
          size_bytes: 1024,
          mtime: "2026-10-01T10:20:00Z",
          probe_status: "audio",
          analysis_state: "not_analyzed",
          matching_eligible: false,
          steps: [],
          root: { enabled: true, stale: false, status: "available" },
        }),
      ),
    );
    window.location.hash = "/sources/root-1/locations/file-1";
    render(<App />);
    expect(await screen.findByText("direct.flac")).toBeVisible();
    expect(
      screen.getByRole("heading", { name: "Инспектор файла" }),
    ).toBeVisible();
    expect(window.location.hash).toBe("#/sources/root-1/locations/file-1");
    expect(
      subscriptions.mock.calls.filter(([type]) => type === "hashchange"),
    ).toHaveLength(1);
    subscriptions.mockRestore();
  });
  it("keeps an incomplete Setup redirecting away from #/sources", async () => {
    server.use(http.get("/api/setup", () => HttpResponse.json(state)));
    window.location.hash = "/sources";
    render(<App />);
    await waitFor(() =>
      expect(
        screen.getByRole("heading", { name: "Первый запуск" }),
      ).toBeVisible(),
    );
    await waitFor(() => expect(window.location.hash).toBe("#/setup"));
    expect(screen.queryByText(/Входящие/)).not.toBeInTheDocument();
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
  it("opens one root detail at #/sources/{id}", async () => {
    const requested: unknown[] = [];
    server.use(
      http.get("/api/setup", () => HttpResponse.json(completedState)),
      http.get("/api/sources/:sourceId", ({ params }) => {
        requested.push(params.sourceId);
        return HttpResponse.json(sourceRoot);
      }),
    );
    window.location.hash = "/sources/root-1";
    render(<App />);
    expect(
      await screen.findByRole("heading", { level: 1, name: "Входящие" }),
    ).toBeVisible();
    expect(requested).toEqual(["root-1"]);
    expect(screen.queryByRole("table")).not.toBeInTheDocument();
    expect(window.location.hash).toBe("#/sources/root-1");
  });
  it("offers source setup on an empty landing and navigates to Sources and Settings", async () => {
    server.use(
      http.get("/api/setup", () => HttpResponse.json(completedState)),
      http.get("/api/sources", () => HttpResponse.json({ sources: [] })),
      http.get("/api/settings", () => HttpResponse.json(completedState)),
      http.get("/api/tools/installations", () =>
        HttpResponse.json({ installations: [] }),
      ),
      http.get("/api/operations", () => HttpResponse.json({ operations: [] })),
    );
    render(<App />);
    expect(
      await screen.findByRole("heading", {
        level: 1,
        name: "Добавьте музыкальный каталог",
      }),
    ).toBeVisible();
    expect(
      screen.getByRole("link", { name: "Перейти к источникам" }),
    ).toHaveAttribute("href", "#/sources");

    fireEvent.click(screen.getAllByRole("link", { name: "Источники" })[0]);
    await waitFor(() => expect(window.location.hash).toBe("#/sources"));
    expect(
      await screen.findByRole("heading", { level: 1, name: "Источники" }),
    ).toBeVisible();
    expect(
      await screen.findByText("Ни один каталог не зарегистрирован."),
    ).toBeVisible();

    fireEvent.click(screen.getAllByRole("link", { name: "Настройки" })[0]);
    await waitFor(() => expect(window.location.hash).toBe("#/settings"));
    expect(await screen.findByText("Managed tools")).toBeVisible();
  });

  it("offers incoming work and source configuration when sources exist", async () => {
    server.use(
      http.get("/api/setup", () => HttpResponse.json(completedState)),
      http.get("/api/sources", () =>
        HttpResponse.json({ sources: [sourceRoot] }),
      ),
    );
    render(<App />);

    expect(
      await screen.findByRole("heading", {
        level: 1,
        name: "Медиатека готова к работе",
      }),
    ).toBeVisible();
    expect(
      screen.getByRole("link", { name: "Открыть входящие" }),
    ).toHaveAttribute("href", "#/incoming-groups");
    expect(
      screen.getByRole("link", { name: "Настроить источники" }),
    ).toHaveAttribute("href", "#/sources");
  });

  it("shows source loading while the root list is pending", async () => {
    let respond: (response: Response) => void = () => {};
    const request = new Promise<void>((resolve) => {
      server.use(
        http.get("/api/setup", () => HttpResponse.json(completedState)),
        http.get(
          "/api/sources",
          () =>
            new Promise<Response>((done) => {
              respond = done;
              resolve();
            }),
        ),
      );
    });
    render(<App />);

    expect(await screen.findByText("Загрузка источников…")).toHaveAttribute(
      "role",
      "status",
    );
    await request;
    respond(HttpResponse.json({ sources: [] }));
  });

  it("retries a failed source list request on the landing", async () => {
    let fail = true;
    server.use(
      http.get("/api/setup", () => HttpResponse.json(completedState)),
      http.get("/api/sources", () =>
        fail
          ? HttpResponse.json(
              { detail: "sources unavailable" },
              { status: 503 },
            )
          : HttpResponse.json({ sources: [] }),
      ),
    );
    render(<App />);

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "sources unavailable",
    );
    fail = false;
    fireEvent.click(screen.getByRole("button", { name: "Повторить загрузку" }));
    expect(
      await screen.findByRole("heading", {
        level: 1,
        name: "Добавьте музыкальный каталог",
      }),
    ).toBeVisible();
  });
});
describe("AppShell library routing", () => {
  it("opens the local release editor from a #/library/releases/{uuid} link once Setup is complete", async () => {
    const releaseId = "3f1c8e59-2a4b-4c7d-9e0f-1a2b3c4d5e6f";
    server.use(
      http.get("/api/setup", () => HttpResponse.json(completedState)),
      http.get(`*/api/library/releases/${releaseId}`, () =>
        HttpResponse.json({
          id: releaseId,
          title: "Local release",
          metadata: {},
          metadata_revision: 1,
          metadata_version: 1,
          primary_artist: {},
          artists: [],
          tracks: [],
        }),
      ),
    );
    window.location.hash = `/library/releases/${releaseId}`;
    render(<App />);
    expect(
      await screen.findByRole("heading", { level: 1, name: "Local release" }),
    ).toBeVisible();
    expect(window.location.hash).toBe(`#/library/releases/${releaseId}`);
  });
});
describe("AppShell operations routing", () => {
  it("renders the empty and list operations states at #/operations from real requests", async () => {
    let operations: unknown[] = [];
    server.use(
      http.get("/api/setup", () => HttpResponse.json(completedState)),
      http.get("/api/operations", () => HttpResponse.json({ operations })),
    );
    window.location.hash = "/operations";
    render(<App />);

    expect(
      await screen.findByRole("heading", {
        level: 1,
        name: "Текущие операции",
      }),
    ).toBeVisible();
    expect(await screen.findByText("Нет текущих операций")).toBeVisible();
    const navs = screen.getAllByRole("navigation", {
      name: "Разделы приложения",
    });
    for (const nav of navs) {
      expect(nav.querySelector('a[href="#/operations"]')).toHaveAttribute(
        "aria-current",
        "page",
      );
    }

    operations = [
      {
        id: "scan-id",
        kind: "scan_source",
        state: "failed",
        stage: "scan_files",
        bytes_completed: 12,
        bytes_total: 30,
        target_identity: "/music",
        safe_error: "Permission denied",
        created_at: "2026-01-01T00:00:00Z",
        updated_at: "2026-01-01T00:01:00Z",
      },
    ];
    fireEvent.click(screen.getByRole("button", { name: "Обновить" }));

    expect(await screen.findByText("Permission denied")).toBeVisible();
    expect(screen.getByText("12 / 30 байт")).toBeVisible();
    expect(screen.queryByText("Нет текущих операций")).not.toBeInTheDocument();
    expect(window.location.hash).toBe("#/operations");
  });
});
describe("AppShell themed shell", () => {
  it("uses the themed Settings shell for the settings route", async () => {
    window.location.hash = "/settings";
    server.use(
      http.get("/api/setup", () => HttpResponse.json(completedState)),
      http.get("/api/settings", () => HttpResponse.json(completedState)),
      http.get("/api/tools/installations", () =>
        HttpResponse.json({ installations: [] }),
      ),
      http.get("/api/operations", () => HttpResponse.json({ operations: [] })),
    );
    render(<App />);
    await screen.findByText("Managed tools");
    const main = screen.getByRole("main");
    expect(main).toHaveClass("settings-screen", "settings-shell");
    expect(main).not.toHaveClass("bg-stone-100");
  });
  it("uses the themed Settings shell for the completed landing route", async () => {
    server.use(
      http.get("/api/setup", () => HttpResponse.json(completedState)),
      http.get("/api/sources", () => HttpResponse.json({ sources: [] })),
    );
    render(<App />);
    await screen.findByRole("heading", {
      name: "Добавьте музыкальный каталог",
    });
    expect(screen.getByRole("main")).toHaveClass("settings-shell");
  });
  it("keeps the setup shell for an incomplete instance", async () => {
    server.use(http.get("/api/setup", () => HttpResponse.json(state)));
    render(<App />);
    await screen.findByRole("heading", { name: "Первый запуск" });
    const main = screen.getByRole("main");
    expect(main).toHaveClass("setup-shell");
    expect(main).not.toHaveClass("settings-shell");
  });
  it("keeps the sources shell for the sources route", async () => {
    window.location.hash = "/sources";
    server.use(
      http.get("/api/setup", () => HttpResponse.json(completedState)),
      http.get("/api/sources", () => HttpResponse.json({ sources: [] })),
    );
    render(<App />);
    await screen.findByRole("heading", { level: 1, name: "Источники" });
    const main = screen.getByRole("main");
    expect(main).toHaveClass("sources-screen", "sources-shell");
    expect(main).not.toHaveClass("settings-shell");
  });

  it("renders accessible navigation only for working destinations and marks the active route", async () => {
    window.location.hash = "/sources";
    server.use(
      http.get("/api/setup", () => HttpResponse.json(completedState)),
      http.get("/api/sources", () => HttpResponse.json({ sources: [] })),
    );
    render(<App />);

    const navs = await screen.findAllByRole("navigation", {
      name: "Разделы приложения",
    });
    expect(navs).toHaveLength(2);
    for (const nav of navs) {
      expect(nav.querySelectorAll("a")).toHaveLength(4);
      expect(nav.querySelector('a[href="#/sources"]')).toHaveAttribute(
        "aria-current",
        "page",
      );
      expect(
        nav.querySelector('a[href="#/incoming-groups"]'),
      ).toBeInTheDocument();
      expect(nav.querySelector('a[href="#/operations"]')).toBeInTheDocument();
      expect(nav.querySelector('a[href="#/settings"]')).toBeInTheDocument();
      expect(nav.querySelectorAll('a[href="#/library"]')).toHaveLength(0);
      expect(nav.querySelectorAll('a[href="#/publications"]')).toHaveLength(0);
    }
    expect(
      screen.getByRole("combobox", { name: "Тема интерфейса" }),
    ).toBeVisible();
    expect(window.location.hash).toBe("#/sources");
  });

  it("keeps the current root route as the source-first landing without redirecting", async () => {
    server.use(
      http.get("/api/setup", () => HttpResponse.json(completedState)),
      http.get("/api/sources", () => HttpResponse.json({ sources: [] })),
    );
    render(<App />);

    expect(
      await screen.findByRole("heading", {
        name: "Добавьте музыкальный каталог",
      }),
    ).toBeVisible();
    expect(window.location.hash).toBe("");
  });
});
