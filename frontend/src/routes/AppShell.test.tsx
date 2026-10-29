import { JSDOM } from "jsdom";
import "@testing-library/jest-dom/vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
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
import { getListSourcesQueryKey } from "../api/generated/client";
import type {
  SetupStateBody,
  SourceRootResponse,
} from "../api/generated/client.schemas";
import { server } from "../test/server";
import { AppShell } from "./AppShell";

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

function cachedSourcesClient() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  client.setQueryData(getListSourcesQueryKey(), {
    data: { sources: [sourceRoot] },
    status: 200,
    headers: new Headers(),
  });
  return client;
}

function pendingSourcesRequest() {
  let respond: (response: Response) => void = () => {};
  let startedResolve: () => void = () => {};
  const started = new Promise<void>((resolve) => {
    startedResolve = resolve;
  });
  server.use(
    http.get(
      "/api/sources",
      () =>
        new Promise<Response>((done) => {
          respond = done;
          startedResolve();
        }),
    ),
  );
  return {
    started,
    respond: (response: Response) => respond(response),
  };
}
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
  it("offers actionable source setup on an empty landing and opens the real registration form", async () => {
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
        name: "Для начала работы необходимо добавить источник",
      }),
    ).toBeVisible();
    const addSource = screen.getByRole("link", { name: "Добавить источник" });
    expect(addSource).toHaveAttribute("href", "#/sources");

    fireEvent.click(addSource);
    await waitFor(() => expect(window.location.hash).toBe("#/sources"));
    expect(
      await screen.findByRole("heading", { level: 1, name: "Источники" }),
    ).toBeVisible();
    expect(
      await screen.findByText("Ни один каталог не зарегистрирован."),
    ).toBeVisible();

    fireEvent.click(screen.getByRole("button", { name: "Добавить каталог" }));
    expect(
      screen.getByRole("heading", { name: "Новый каталог" }),
    ).toBeVisible();
    expect(screen.getByLabelText("Имя каталога")).toBeVisible();

    fireEvent.click(screen.getAllByRole("link", { name: "Настройки" })[0]);
    await waitFor(() => expect(window.location.hash).toBe("#/settings"));
    expect(await screen.findByText("Managed tools")).toBeVisible();
  });

  it("opens Incoming directly from the root when sources exist", async () => {
    server.use(
      http.get("/api/setup", () => HttpResponse.json(completedState)),
      http.get("/api/sources", () =>
        HttpResponse.json({ sources: [sourceRoot] }),
      ),
      http.get("/api/incoming-groups", () =>
        HttpResponse.json({ epoch: 1, revision: "", groups: [], files: [] }),
      ),
    );
    render(<App />);

    expect(
      await screen.findByRole("heading", {
        level: 1,
        name: "Разбор входящих групп",
      }),
    ).toBeVisible();
    await waitFor(() => expect(window.location.hash).toBe("#/incoming-groups"));
    expect(screen.queryByText("Медиатека готова к работе")).toBeNull();
    expect(screen.queryByText("Выберите, с чего продолжить.")).toBeNull();
    for (const nav of screen.getAllByRole("navigation", {
      name: "Разделы приложения",
    })) {
      expect(nav.querySelector('a[href="#/incoming-groups"]')).toHaveAttribute(
        "aria-current",
        "page",
      );
    }
  });

  it("waits for the current mount source fetch before redirecting from cached non-empty data", async () => {
    const pending = pendingSourcesRequest();
    server.use(
      http.get("/api/setup", () => HttpResponse.json(completedState)),
      http.get("/api/incoming-groups", () =>
        HttpResponse.json({ epoch: 1, revision: "", groups: [], files: [] }),
      ),
    );
    render(
      <QueryClientProvider client={cachedSourcesClient()}>
        <AppShell />
      </QueryClientProvider>,
    );

    await pending.started;
    expect(screen.getByRole("status")).toHaveTextContent(
      "Загрузка источников…",
    );
    expect(window.location.hash).toBe("");

    pending.respond(HttpResponse.json({ sources: [sourceRoot] }));
    expect(
      await screen.findByRole("heading", {
        name: "Разбор входящих групп",
      }),
    ).toBeVisible();
    await waitFor(() => expect(window.location.hash).toBe("#/incoming-groups"));
  });

  it("shows the current mount source error instead of redirecting from cached non-empty data", async () => {
    const pending = pendingSourcesRequest();
    server.use(http.get("/api/setup", () => HttpResponse.json(completedState)));
    render(
      <QueryClientProvider client={cachedSourcesClient()}>
        <AppShell />
      </QueryClientProvider>,
    );

    await pending.started;
    expect(window.location.hash).toBe("");

    pending.respond(
      HttpResponse.json({ detail: "sources unavailable" }, { status: 503 }),
    );
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "sources unavailable",
    );
    expect(window.location.hash).toBe("");
    expect(
      screen.queryByRole("heading", {
        name: "Для начала работы необходимо добавить источник",
      }),
    ).toBeNull();
  });

  it("shows the empty source action instead of redirecting from cached non-empty data", async () => {
    const pending = pendingSourcesRequest();
    server.use(http.get("/api/setup", () => HttpResponse.json(completedState)));
    render(
      <QueryClientProvider client={cachedSourcesClient()}>
        <AppShell />
      </QueryClientProvider>,
    );

    await pending.started;
    expect(window.location.hash).toBe("");

    pending.respond(HttpResponse.json({ sources: [] }));
    expect(
      await screen.findByRole("heading", {
        name: "Для начала работы необходимо добавить источник",
      }),
    ).toBeVisible();
    expect(
      screen.getByRole("link", { name: "Добавить источник" }),
    ).toHaveAttribute("href", "#/sources");
    expect(window.location.hash).toBe("");
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
    expect(window.location.hash).toBe("");
    fail = false;
    fireEvent.click(screen.getByRole("button", { name: "Повторить загрузку" }));
    expect(
      await screen.findByRole("heading", {
        level: 1,
        name: "Для начала работы необходимо добавить источник",
      }),
    ).toBeVisible();
  });
});
describe("AppShell library routing", () => {
  it("routes the library query to its real catalog screen and marks navigation active", async () => {
    window.location.hash =
      "/library?view=artists&q=blue&publication=unpublished&offset=50";
    server.use(
      http.get("/api/setup", () => HttpResponse.json(completedState)),
      http.get("/api/library/artists", ({ request }) => {
        const url = new URL(request.url);
        expect(url.searchParams.get("q")).toBe("blue");
        expect(url.searchParams.get("publication")).toBe("unpublished");
        expect(url.searchParams.get("offset")).toBe("50");
        return HttpResponse.json({
          items: [],
          total: 0,
          summary: {
            artist_count: 0,
            release_count: 0,
            position_count: 0,
            published_position_count: 0,
          },
        });
      }),
    );
    render(<App />);
    await screen.findByRole("heading", { name: "Медиатека" });
    expect(
      await screen.findByRole("heading", { name: "Ничего не найдено" }),
    ).toBeVisible();
    expect(
      screen.getAllByRole("link", { name: "Медиатека" })[0],
    ).toHaveAttribute("aria-current", "page");
  });

  it("routes artist details with preserved query and the manual-release creator", async () => {
    const artistId = "123e4567-e89b-42d3-a456-426614174000";
    window.location.hash = `/library/artists/${artistId}?view=albums&q=blue&publication=published&offset=25`;
    server.use(
      http.get("/api/setup", () => HttpResponse.json(completedState)),
      http.get(`/api/library/artists/${artistId}`, () =>
        HttpResponse.json({
          id: artistId,
          name: "Blue Artist",
          disambiguation: "",
          release_count: 0,
          position_count: 0,
          published_position_count: 0,
          genres: [],
        }),
      ),
      http.get("/api/library/releases", ({ request }) => {
        const url = new URL(request.url);
        expect(url.searchParams.get("artist_id")).toBe(artistId);
        expect(url.searchParams.get("q")).toBe("blue");
        expect(url.searchParams.get("publication")).toBe("published");
        expect(url.searchParams.get("offset")).toBe("25");
        return HttpResponse.json({ items: [], total: 0 });
      }),
    );
    render(<App />);
    await screen.findByRole("heading", { name: "Blue Artist" });
    expect(screen.getByRole("link", { name: "← Медиатека" })).toHaveAttribute(
      "href",
      "#/library?view=albums&q=blue&publication=published&offset=25",
    );

    window.location.hash = "/library/create";
    await screen.findByRole("heading", { name: "Создать локальный релиз" });
  });

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

  it("opens only the selected position from a direct library hash", async () => {
    const releaseId = "3f1c8e59-2a4b-4c7d-9e0f-1a2b3c4d5e6f";
    const positionId = "4f1c8e59-2a4b-4c7d-9e0f-1a2b3c4d5e6f";
    const otherPositionId = "5f1c8e59-2a4b-4c7d-9e0f-1a2b3c4d5e6f";
    server.use(
      http.get("/api/setup", () => HttpResponse.json(completedState)),
      http.get(`*/api/library/releases/${releaseId}`, () =>
        HttpResponse.json({
          id: releaseId,
          title: "Direct release",
          metadata: {},
          metadata_revision: 1,
          metadata_version: 1,
          primary_artist: {},
          artists: [],
          tracks: [positionId, otherPositionId].map((id, index) => ({
            id,
            title: index === 0 ? "Selected song" : "Other song",
            displayed_number: String(index + 1),
            primary_artist: {},
            artists: [],
            sources: [],
          })),
        }),
      ),
      http.get(`*/api/library/releases/${releaseId}/cover`, () =>
        HttpResponse.json({
          release_id: releaseId,
          revision: 1,
          mode: "automatic",
        }),
      ),
      http.get(
        `*/api/library/releases/${releaseId}/positions/:positionId/metadata`,
        () =>
          HttpResponse.json({
            revision: 1,
            publication_applied: false,
            envelope: { scope: "position", version: 1, desired: {} },
          }),
      ),
      http.get(
        `*/api/library/releases/${releaseId}/positions/:positionId/publication`,
        ({ params }) =>
          HttpResponse.json({
            release_id: releaseId,
            position_id: params.positionId,
            can_apply_manual_lyrics: false,
          }),
      ),
    );
    window.location.hash = `/library/releases/${releaseId}/positions/${positionId}`;
    render(<App />);

    expect(
      await screen.findByRole("heading", { name: "1. Selected song" }),
    ).toBeVisible();
    expect(screen.queryByRole("heading", { name: "Other song" })).toBeNull();
    expect(
      screen.getByRole("heading", { name: "Direct release" }),
    ).toBeVisible();
    expect(
      screen.getByRole("link", { name: "← Полный релиз" }),
    ).toHaveAttribute("href", `#/library/releases/${releaseId}`);
    expect(window.location.hash).toBe(
      `#/library/releases/${releaseId}/positions/${positionId}`,
    );
  });

  it("shows an alert instead of another position when a direct position is unknown", async () => {
    const releaseId = "3f1c8e59-2a4b-4c7d-9e0f-1a2b3c4d5e6f";
    const positionId = "4f1c8e59-2a4b-4c7d-9e0f-1a2b3c4d5e6f";
    server.use(
      http.get("/api/setup", () => HttpResponse.json(completedState)),
      http.get(`*/api/library/releases/${releaseId}`, () =>
        HttpResponse.json({
          id: releaseId,
          title: "Direct release",
          metadata: {},
          metadata_revision: 1,
          metadata_version: 1,
          primary_artist: {},
          artists: [],
          tracks: [],
        }),
      ),
      http.get(`*/api/library/releases/${releaseId}/cover`, () =>
        HttpResponse.json({
          release_id: releaseId,
          revision: 1,
          mode: "automatic",
        }),
      ),
    );
    window.location.hash = `/library/releases/${releaseId}/positions/${positionId}`;
    render(<App />);

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Позиция не найдена в этом релизе.",
    );
    expect(
      screen.getByRole("heading", { name: "Direct release" }),
    ).toBeVisible();
    expect(
      screen.queryByRole("heading", {
        name: "Для начала работы необходимо добавить источник",
      }),
    ).toBeNull();
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
      name: "Для начала работы необходимо добавить источник",
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
      expect(nav.querySelectorAll("a")).toHaveLength(6);
      expect(
        Array.from(nav.querySelectorAll("a"), (link) =>
          link.getAttribute("href"),
        ),
      ).toEqual([
        "#/incoming-groups",
        "#/library",
        "#/sources",
        "#/publications",
        "#/operations",
        "#/settings",
      ]);
      expect(nav.querySelector('a[href="#/sources"]')).toHaveAttribute(
        "aria-current",
        "page",
      );
      expect(
        nav.querySelector('a[href="#/incoming-groups"]'),
      ).toBeInTheDocument();
      expect(nav.querySelector('a[href="#/operations"]')).toBeInTheDocument();
      expect(nav.querySelector('a[href="#/settings"]')).toBeInTheDocument();
      expect(
        nav.querySelector('a[href="#/incoming-groups"]'),
      ).toHaveTextContent("Входящие");
      expect(nav.querySelector('a[href="#/library"]')).toHaveTextContent(
        "Медиатека",
      );
      expect(nav.querySelector('a[href="#/publications"]')).toHaveTextContent(
        "Публикации",
      );
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
        name: "Для начала работы необходимо добавить источник",
      }),
    ).toBeVisible();
    expect(window.location.hash).toBe("");
  });
});
