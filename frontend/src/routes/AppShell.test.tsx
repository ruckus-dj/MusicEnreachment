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
import type { SetupStateBody } from "../api/generated/client.schemas";
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
    log_level: "info",
  },
  configuration_health: { healthy: false, problems: ["setup incomplete"] },
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
    respond(HttpResponse.json(state));
    await waitFor(() =>
      expect(
        screen.getByRole("heading", { name: "Первый запуск" }),
      ).toBeVisible(),
    );
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
