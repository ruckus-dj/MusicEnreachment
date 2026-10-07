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
    musicbrainz_mode: "public",
    musicbrainz_base_url: "",
    lrclib_enabled: true,
    sha256_enabled: true,
    log_level: "info",
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
    render(<AppShell />);
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
    render(<AppShell />);
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
    render(<AppShell />);
    await waitFor(() => expect(window.location.hash).toBe("#/"));
    fireEvent.click(screen.getByRole("button", { name: "Настройки" }));
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
    render(<AppShell />);
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
    render(<AppShell />);
    expect(await screen.findByText("direct.flac")).toBeVisible();
    expect(
      screen.getByRole("heading", { name: "Инспектор файла" }),
    ).toBeVisible();
    expect(window.location.hash).toBe("#/sources/root-1/locations/file-1");
  });
  it("keeps an incomplete Setup redirecting away from #/sources", async () => {
    server.use(http.get("/api/setup", () => HttpResponse.json(state)));
    window.location.hash = "/sources";
    render(<AppShell />);
    await waitFor(() =>
      expect(
        screen.getByRole("heading", { name: "Первый запуск" }),
      ).toBeVisible(),
    );
    await waitFor(() => expect(window.location.hash).toBe("#/setup"));
    expect(screen.queryByText(/Входящие/)).not.toBeInTheDocument();
  });
  it("lists source roots at #/sources once Setup is complete", async () => {
    server.use(
      http.get("/api/setup", () => HttpResponse.json(completedState)),
      http.get("/api/sources", () =>
        HttpResponse.json({ sources: [sourceRoot] }),
      ),
    );
    window.location.hash = "/sources";
    render(<AppShell />);
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
    render(<AppShell />);
    expect(
      await screen.findByRole("heading", { level: 1, name: "Входящие" }),
    ).toBeVisible();
    expect(requested).toEqual(["root-1"]);
    expect(screen.queryByRole("table")).not.toBeInTheDocument();
    expect(window.location.hash).toBe("#/sources/root-1");
  });
  it("keeps the default landing and navigates the header to Sources and Settings", async () => {
    server.use(
      http.get("/api/setup", () => HttpResponse.json(completedState)),
      http.get("/api/sources", () => HttpResponse.json({ sources: [] })),
      http.get("/api/settings", () => HttpResponse.json(completedState)),
      http.get("/api/tools/installations", () =>
        HttpResponse.json({ installations: [] }),
      ),
      http.get("/api/operations", () => HttpResponse.json({ operations: [] })),
    );
    render(<AppShell />);
    expect(
      await screen.findByText("Приложение готово к настройке."),
    ).toBeVisible();

    fireEvent.click(screen.getByRole("button", { name: "Источники" }));
    await waitFor(() => expect(window.location.hash).toBe("#/sources"));
    expect(
      await screen.findByRole("heading", { level: 1, name: "Источники" }),
    ).toBeVisible();
    expect(
      screen.getByText("Ни один каталог не зарегистрирован."),
    ).toBeVisible();

    fireEvent.click(screen.getByRole("button", { name: "Настройки" }));
    await waitFor(() => expect(window.location.hash).toBe("#/settings"));
    expect(await screen.findByText("Managed tools")).toBeVisible();
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
    render(<AppShell />);
    await screen.findByText("Managed tools");
    const main = screen.getByRole("main");
    expect(main).toHaveClass("settings-screen", "settings-shell");
    expect(main).not.toHaveClass("bg-stone-100");
  });
  it("uses the themed Settings shell for the completed landing route", async () => {
    server.use(http.get("/api/setup", () => HttpResponse.json(completedState)));
    render(<AppShell />);
    await screen.findByText("Приложение готово к настройке.");
    expect(screen.getByRole("main")).toHaveClass("settings-shell");
  });
  it("keeps the setup shell for an incomplete instance", async () => {
    server.use(http.get("/api/setup", () => HttpResponse.json(state)));
    render(<AppShell />);
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
    render(<AppShell />);
    await screen.findByRole("heading", { level: 1, name: "Источники" });
    const main = screen.getByRole("main");
    expect(main).toHaveClass("sources-screen", "sources-shell");
    expect(main).not.toHaveClass("settings-shell");
  });
});
