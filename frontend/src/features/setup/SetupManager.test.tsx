import { JSDOM } from "jsdom";
import "@testing-library/jest-dom/vitest";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { HttpResponse, http } from "msw";
import {
  afterEach,
  beforeEach,
  describe,
  expect,
  it,
  onTestFinished,
  vi,
} from "vitest";
import type {
  OperationResponse,
  SetupStateBody,
} from "../../api/generated/client.schemas";
import { server } from "../../test/server";
import { SetupManager } from "./SetupManager";

const settings: SetupStateBody["settings"] = {
  tools_directory: "",
  output_directory: "",
  publication_format: "",
  musicbrainz_mode: "public",
  musicbrainz_base_url: "",
  lrclib_enabled: true,
  sha256_enabled: true,
  log_level: "info",
  source_file_concurrency: 4,
  has_acoustid_application_key: false,
};
function state(
  overrides: Partial<SetupStateBody["settings"]> = {},
  healthy = false,
): SetupStateBody {
  return {
    completed: false,
    platform: {
      diagnostic: false,
      supported: true,
      goos: "linux",
      goarch: "amd64",
    },
    settings: { ...settings, ...overrides },
    configuration_health: {
      healthy,
      problems: healthy ? [] : ["setup incomplete"],
    },
  };
}
const paths = {
  tools_directory: "/srv/tools",
  output_directory: "/srv/output",
};
const installed = {
  ...paths,
  active_ffmpeg_installation_id: "ff-id",
  active_fpcalc_installation_id: "fp-id",
};
const published = { ...installed, publication_format: "source" };
const verified = {
  ...published,
  musicbrainz_verified_at: "2026-09-28T00:00:00Z",
};
const operation: OperationResponse = {
  id: "76092c63-0e0f-4dc1-af39-674dc1ce037c",
  kind: "install",
  state: "running",
  stage: "download",
  bytes_completed: 12,
  created_at: "2026-09-28T00:00:00Z",
  updated_at: "2026-09-28T00:00:01Z",
};
class TestEventSource extends EventTarget {
  static instances: TestEventSource[] = [];
  static onCreated: (() => void) | undefined;
  close = vi.fn();
  constructor(public url: string) {
    super();
    TestEventSource.instances.push(this);
    TestEventSource.onCreated?.();
  }
}
beforeEach(() => {
  vi.stubGlobal(
    "localStorage",
    new JSDOM("", { url: "http://localhost" }).window.localStorage,
  );
  TestEventSource.instances = [];
  TestEventSource.onCreated = undefined;
  vi.stubGlobal("EventSource", TestEventSource);
  server.use(
    http.get("/api/operations", () => HttpResponse.json({ operations: [] })),
  );
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});
function renderSetup(value: SetupStateBody, onCompleted = vi.fn()) {
  return render(
    <SetupManager initialState={value} onCompleted={onCompleted} />,
  );
}
async function click(name: string) {
  fireEvent.click(screen.getByRole("button", { name }));
}
function heading(name: string) {
  return screen.getByRole("heading", { level: 2, name });
}

async function selectVersion(
  kind: "ffmpeg" | "fpcalc",
  version: string,
  source = "trusted",
) {
  const trigger = screen.getByRole("button", {
    name: new RegExp(`Версия ${kind}`),
  });
  await waitFor(() => expect(trigger).toBeEnabled());
  fireEvent.click(trigger);
  const option = await screen.findByRole("option", {
    name: `${version} (${source})`,
  });
  fireEvent.click(option);
  await waitFor(() =>
    expect(trigger).toHaveTextContent(`${version} (${source})`),
  );
}

describe("SetupManager", () => {
  it("starts with platform, recovers stored step against server progress and moves focus", async () => {
    localStorage.setItem("melotrove.setup.step", "5");
    server.use(
      http.post("/api/setup/paths/check", () => HttpResponse.json(paths)),
      http.put(
        "/api/setup/runtime",
        () => new HttpResponse(null, { status: 204 }),
      ),
      http.get("/api/setup", () => HttpResponse.json(state(paths))),
    );
    renderSetup(state({ tools_directory: paths.tools_directory }));
    expect(heading("Каталоги")).toHaveFocus();
    fireEvent.change(screen.getByLabelText("Tools directory"), {
      target: { value: paths.tools_directory },
    });
    fireEvent.change(screen.getByLabelText("Output directory"), {
      target: { value: paths.output_directory },
    });
    await click("Продолжить");
    await waitFor(() => expect(heading("Инструменты")).toHaveFocus());
    expect(localStorage.getItem("melotrove.setup.step")).toBe("2");
  });

  it.each([
    1, 2, 3, 4, 5,
  ])("restores step %i from server values", async (step) => {
    const values = [
      state({ tools_directory: paths.tools_directory }),
      state(paths),
      state(installed),
      state(published),
      state(verified, true),
    ];
    localStorage.setItem("melotrove.setup.step", "5");
    const { unmount } = renderSetup(values[step - 1]);
    expect(
      heading(
        [
          "Каталоги",
          "Инструменты",
          "Публикация",
          "Провайдеры метаданных",
          "Итог",
        ][step - 1],
      ),
    ).toBeVisible();
    unmount();
    renderSetup(values[step - 1]);
    expect(
      heading(
        [
          "Каталоги",
          "Инструменты",
          "Публикация",
          "Провайдеры метаданных",
          "Итог",
        ][step - 1],
      ),
    ).toBeVisible();
  });

  it("requires an explicit publication choice and submits only the chosen format", async () => {
    let current = state(installed);
    const saved = vi.fn((_body: unknown) => {
      current = state(published);
      return new HttpResponse(null, { status: 204 });
    });
    server.use(
      http.get("/api/setup", () => HttpResponse.json(current)),
      http.put("/api/setup/runtime", async ({ request }) =>
        saved(await request.json()),
      ),
    );
    renderSetup(current);
    await click("Продолжить");
    await waitFor(() =>
      expect(screen.getByRole("alert")).toHaveTextContent("Выберите формат"),
    );
    expect(saved).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("radio", { name: "Исходный формат" }));
    await click("Продолжить");
    await waitFor(() => expect(heading("Провайдеры метаданных")).toBeVisible());
    expect(saved).toHaveBeenCalledWith({ publication_format: "source" });
  });

  it("checks MusicBrainz before completion and does not invalidate a verified config on advance", async () => {
    let current = state(published);
    const save = vi.fn(() => {
      current = state(published);
      return new HttpResponse(null, { status: 204 });
    });
    const check = vi.fn(() => {
      current = state(verified, true);
      return HttpResponse.json({ success: true });
    });
    server.use(
      http.put("/api/setup/musicbrainz", save),
      http.post("/api/setup/check-musicbrainz", check),
      http.put(
        "/api/setup/lrclib",
        () => new HttpResponse(null, { status: 204 }),
      ),
      http.get("/api/setup", () => HttpResponse.json(current)),
      http.post(
        "/api/setup/complete",
        () => new HttpResponse(null, { status: 204 }),
      ),
    );
    const done = vi.fn();
    renderSetup(current, done);
    await click("Продолжить");
    await waitFor(() =>
      expect(screen.getByRole("alert")).toHaveTextContent(
        "Сохраните и проверьте MusicBrainz",
      ),
    );
    await click("Проверить MusicBrainz");
    await waitFor(() => expect(screen.getByText(/Проверено:/)).toBeVisible());
    await click("Продолжить");
    await waitFor(() => expect(heading("Итог")).toHaveFocus());
    expect(save).toHaveBeenCalledTimes(1);
    await click("Завершить Setup");
    await waitFor(() => expect(done).toHaveBeenCalledOnce());
  });

  it("shows per-action server errors, focus, and retry on directory validation", async () => {
    let fails = true;
    server.use(
      http.post("/api/setup/paths/check", () =>
        fails
          ? HttpResponse.json({ detail: "invalid paths" }, { status: 400 })
          : HttpResponse.json(paths),
      ),
      http.put(
        "/api/setup/runtime",
        () => new HttpResponse(null, { status: 204 }),
      ),
      http.get("/api/setup", () => HttpResponse.json(state(paths))),
    );
    renderSetup(state({ tools_directory: paths.tools_directory }));
    fireEvent.change(screen.getByLabelText("Tools directory"), {
      target: { value: paths.tools_directory },
    });
    fireEvent.change(screen.getByLabelText("Output directory"), {
      target: { value: paths.output_directory },
    });
    await click("Продолжить");
    await waitFor(() => expect(screen.getByRole("alert")).toHaveFocus());
    expect(screen.getByRole("alert")).toHaveTextContent("invalid paths");
    fails = false;
    await click("Продолжить");
    await waitFor(() => expect(heading("Инструменты")).toBeVisible());
  });

  it("selects a catalog version with keyboard and sends its identity to preflight", async () => {
    const preflightBodies: unknown[] = [];
    server.use(
      http.get("/api/tools/installations", () =>
        HttpResponse.json({ installations: [] }),
      ),
      http.get("/api/tools/catalog", ({ request }) =>
        HttpResponse.json({
          package_kind: new URL(request.url).searchParams.get("package_kind"),
          releases: [
            { identity: "8.0", source: "trusted", artifacts: [] },
            { identity: "8.1", source: "trusted", artifacts: [] },
          ],
          platform: state().platform,
        }),
      ),
      http.post("/api/tools/installations/preflight", async ({ request }) => {
        preflightBodies.push(await request.json());
        return HttpResponse.json({
          preflight_token: "8.1",
          conflicts: ["/srv/tools/ffmpeg"],
          targets: ["/srv/tools/ffmpeg"],
        });
      }),
    );
    renderSetup(state(paths));
    const trigger = screen.getByRole("button", { name: /Версия ffmpeg/ });
    await waitFor(() => expect(trigger).toBeEnabled());
    expect(
      screen.getByRole("button", { name: "Проверить установку ffmpeg" }),
    ).toBeDisabled();
    trigger.focus();
    fireEvent.keyDown(trigger, { key: "ArrowDown", code: "ArrowDown" });
    fireEvent.keyUp(trigger, { key: "ArrowDown", code: "ArrowDown" });
    await screen.findByRole("option", { name: "8.0 (trusted)" });
    fireEvent.keyDown(document.activeElement || trigger, {
      key: "ArrowDown",
      code: "ArrowDown",
    });
    fireEvent.keyDown(document.activeElement || trigger, {
      key: "Enter",
      code: "Enter",
    });
    fireEvent.keyUp(document.activeElement || trigger, {
      key: "Enter",
      code: "Enter",
    });
    await waitFor(() => expect(trigger).toHaveTextContent("8.1 (trusted)"));
    expect(
      screen.getByRole("button", { name: "Проверить установку ffmpeg" }),
    ).toBeEnabled();
    await click("Проверить установку ffmpeg");
    await waitFor(() =>
      expect(preflightBodies).toEqual([
        { package_kind: "ffmpeg", release_identity: "8.1" },
      ]),
    );
  });

  it("shows scoped conflicts and starts install only after confirmation", async () => {
    let startBody: unknown;
    const start = vi.fn(async ({ request }: { request: Request }) => {
      startBody = await request.json();
      return HttpResponse.json(operation);
    });
    server.use(
      http.get("/api/tools/installations", () =>
        HttpResponse.json({ installations: [] }),
      ),
      http.get("/api/tools/catalog", ({ request }) =>
        HttpResponse.json({
          package_kind: new URL(request.url).searchParams.get("package_kind"),
          releases: [{ identity: "8.0", source: "trusted", artifacts: [] }],
          platform: state().platform,
        }),
      ),
      http.post("/api/tools/installations/preflight", () =>
        HttpResponse.json({
          preflight_token: "token",
          targets: ["/srv/tools/ffmpeg/8.0/ffmpeg"],
          conflicts: ["/srv/tools/ffmpeg/8.0/ffmpeg"],
        }),
      ),
      http.post("/api/tools/installations", start),
    );
    renderSetup(state(paths));
    await selectVersion("ffmpeg", "8.0");
    await click("Проверить установку ffmpeg");
    await waitFor(() =>
      expect(screen.getByText("/srv/tools/ffmpeg/8.0/ffmpeg")).toBeVisible(),
    );
    expect(start).not.toHaveBeenCalled();
    await click("Подтвердить перезапись");
    await waitFor(() => expect(start).toHaveBeenCalledOnce());
    expect(startBody).toEqual({
      preflight_token: "token",
      confirmed_conflicts: ["/srv/tools/ffmpeg/8.0/ffmpeg"],
    });
  });

  it("recovers operation progress via SSE reconnect and REST snapshot", async () => {
    localStorage.setItem("melotrove.setup.operation", operation.id);
    let current: OperationResponse = operation;
    server.use(
      http.get("/api/tools/installations", () =>
        HttpResponse.json({ installations: [] }),
      ),
      http.get("/api/tools/catalog", ({ request }) =>
        HttpResponse.json({
          package_kind: new URL(request.url).searchParams.get("package_kind"),
          releases: [],
          platform: state().platform,
        }),
      ),
      http.get(`/api/operations/${operation.id}`, () =>
        HttpResponse.json(current),
      ),
    );
    renderSetup(state(paths));
    await waitFor(() => expect(TestEventSource.instances).toHaveLength(1));
    const source = TestEventSource.instances[0];
    expect(source.url).toContain(`${operation.id}/events`);
    await act(async () => {
      source.dispatchEvent(new Event("open"));
    });
    await waitFor(() => expect(screen.getByText(/download, 12/)).toBeVisible());
    await act(async () => {
      source.dispatchEvent(new Event("error"));
    });
    await waitFor(() =>
      expect(screen.getByRole("alert")).toHaveTextContent("disconnected"),
    );
    current = {
      ...operation,
      state: "succeeded",
      stage: "verified",
      bytes_completed: 100,
    };
    await act(async () => {
      source.dispatchEvent(new Event("open"));
    });
    await waitFor(() =>
      expect(screen.getByText(/verified, 100/)).toBeVisible(),
    );
    expect(localStorage.getItem("melotrove.setup.operation")).toBeNull();
  });

  it("runs all six steps with the first verified tool package versions autoactivated", async () => {
    let current = state();
    const packages: Record<
      string,
      {
        id: string;
        package_kind: string;
        release_identity: string;
        state: string;
        active: boolean;
        created_at: string;
        source_name: string;
        executable_versions: Record<string, string>;
      }
    > = {};
    let installing = "";
    let completedKind = "";
    const completed = vi.fn(() => new HttpResponse(null, { status: 204 }));
    server.use(
      http.get("/api/setup", () => HttpResponse.json(current)),
      http.post("/api/setup/paths/check", () => HttpResponse.json(paths)),
      http.put("/api/setup/runtime", async ({ request }) => {
        const body: unknown = await request.json();
        if (typeof body !== "object" || body === null)
          return HttpResponse.json(
            { detail: "invalid runtime" },
            { status: 400 },
          );
        current = state({ ...current.settings, ...body });
        return new HttpResponse(null, { status: 204 });
      }),
      http.get("/api/tools/catalog", ({ request }) =>
        HttpResponse.json({
          package_kind: new URL(request.url).searchParams.get("package_kind"),
          releases: [{ identity: "1.0", source: "trusted", artifacts: [] }],
          platform: current.platform,
        }),
      ),
      http.get("/api/tools/installations", () =>
        HttpResponse.json({ installations: Object.values(packages) }),
      ),
      http.post("/api/tools/installations/preflight", async ({ request }) => {
        const body: unknown = await request.json();
        if (
          typeof body !== "object" ||
          body === null ||
          !("package_kind" in body) ||
          typeof body.package_kind !== "string"
        )
          return HttpResponse.json({ detail: "invalid kind" }, { status: 400 });
        installing = body.package_kind;
        return HttpResponse.json({
          preflight_token: installing,
          conflicts: [],
          targets: [`/srv/tools/${installing}/1.0`],
        });
      }),
      http.post("/api/tools/installations", () =>
        HttpResponse.json({ ...operation, id: installing }),
      ),
      http.get("/api/operations/:id", () =>
        HttpResponse.json({
          ...operation,
          id: installing,
          state: completedKind === installing ? "succeeded" : "running",
          stage: completedKind === installing ? "verified" : "download",
        }),
      ),
      http.put(
        "/api/setup/musicbrainz",
        () => new HttpResponse(null, { status: 204 }),
      ),
      http.post("/api/setup/check-musicbrainz", () => {
        current = state(
          {
            ...current.settings,
            musicbrainz_verified_at: "2026-09-28T00:00:00Z",
          },
          true,
        );
        return HttpResponse.json({ success: true });
      }),
      http.put(
        "/api/setup/lrclib",
        () => new HttpResponse(null, { status: 204 }),
      ),
      http.post("/api/setup/complete", completed),
    );
    const done = vi.fn();
    renderSetup(current, done);
    expect(heading("Платформа")).toHaveFocus();
    await click("Продолжить");
    expect(heading("Каталоги")).toHaveFocus();
    fireEvent.change(screen.getByLabelText("Tools directory"), {
      target: { value: paths.tools_directory },
    });
    fireEvent.change(screen.getByLabelText("Output directory"), {
      target: { value: paths.output_directory },
    });
    await click("Продолжить");
    await waitFor(() => expect(heading("Инструменты")).toBeVisible());
    for (const kind of ["ffmpeg", "fpcalc"]) {
      await selectVersion(kind === "ffmpeg" ? "ffmpeg" : "fpcalc", "1.0");
      await click(`Проверить установку ${kind}`);
      await waitFor(() =>
        expect(TestEventSource.instances.length).toBe(
          kind === "ffmpeg" ? 1 : 2,
        ),
      );
      packages[kind] = {
        id: kind,
        package_kind: kind,
        release_identity: "1.0",
        state: "ready",
        active: true,
        created_at: "2026-09-28T00:00:00Z",
        source_name: "trusted",
        executable_versions: { [kind]: "1.0" },
      };
      current = state({
        ...current.settings,
        [kind === "ffmpeg"
          ? "active_ffmpeg_installation_id"
          : "active_fpcalc_installation_id"]: kind,
      });
      completedKind = kind;
      await act(async () => {
        TestEventSource.instances.at(-1)?.dispatchEvent(new Event("open"));
      });
      await waitFor(() =>
        expect(screen.getAllByText("1.0: Активна")).toHaveLength(
          kind === "ffmpeg" ? 1 : 2,
        ),
      );
      expect(
        screen.queryByRole("button", { name: /Активировать/ }),
      ).not.toBeInTheDocument();
    }
    await click("Продолжить");
    await waitFor(() => expect(heading("Публикация")).toBeVisible());
    fireEvent.click(screen.getByRole("radio", { name: "MKA remux" }));
    await click("Продолжить");
    await waitFor(() => expect(heading("Провайдеры метаданных")).toBeVisible());
    fireEvent.click(screen.getByRole("checkbox", { name: "LRCLIB включён" }));
    await click("Проверить MusicBrainz");
    await waitFor(() => expect(screen.getByText(/Проверено:/)).toBeVisible());
    await click("Продолжить");
    await waitFor(() => expect(heading("Итог")).toBeVisible());
    expect(screen.getByText("Серверная проверка: Готово")).toBeVisible();
    await click("Завершить Setup");
    await waitFor(() => expect(done).toHaveBeenCalledOnce());
    expect(completed).toHaveBeenCalledOnce();
    expect(current.settings.publication_format).toBe("mka");
    // This scenario drives all six wizard steps; React Aria rendering in jsdom
    // can exceed Vitest's default per-test budget on CI. Scope the longer bound
    // to this full-wizard test only.
  }, 10_000);

  it("keeps a failed operation available across reload and retries via REST", async () => {
    localStorage.setItem("melotrove.setup.operation", operation.id);
    const retry = vi.fn(() =>
      HttpResponse.json({ ...operation, state: "queued", stage: "queued" }),
    );
    server.use(
      http.get("/api/tools/installations", () =>
        HttpResponse.json({ installations: [] }),
      ),
      http.get("/api/tools/catalog", ({ request }) =>
        HttpResponse.json({
          package_kind: new URL(request.url).searchParams.get("package_kind"),
          releases: [],
          platform: state().platform,
        }),
      ),
      http.get(`/api/operations/${operation.id}`, () =>
        HttpResponse.json({
          ...operation,
          state: "failed",
          safe_error: "download unavailable",
        }),
      ),
      http.post(`/api/operations/${operation.id}/retry`, retry),
    );
    renderSetup(state(paths));
    await waitFor(() => expect(TestEventSource.instances).toHaveLength(1));
    await act(async () => {
      TestEventSource.instances[0].dispatchEvent(new Event("open"));
    });
    await waitFor(() =>
      expect(screen.getByText(/download unavailable/)).toBeVisible(),
    );
    expect(localStorage.getItem("melotrove.setup.operation")).toBe(
      operation.id,
    );
    await click(`Повторить операцию ${operation.id}`);
    await waitFor(() => expect(retry).toHaveBeenCalledOnce());
    expect(localStorage.getItem("melotrove.setup.operation")).toBe(
      operation.id,
    );
  });

  it("allows keyboard activation of the platform step and focuses next heading", async () => {
    renderSetup(state());
    const next = screen.getByRole("button", { name: "Продолжить" });
    next.focus();
    fireEvent.keyDown(next, { key: "Enter", code: "Enter" });
    fireEvent.keyUp(next, { key: "Enter", code: "Enter" });
    await waitFor(() => expect(heading("Каталоги")).toHaveFocus());
  });

  it("shows catalog HTTP failures with an actionable refresh", async () => {
    let unavailable = true;
    server.use(
      http.get("/api/tools/installations", () =>
        HttpResponse.json({ installations: [] }),
      ),
      http.get("/api/tools/catalog", () =>
        unavailable
          ? HttpResponse.json(
              { detail: "upstream unavailable" },
              { status: 502 },
            )
          : HttpResponse.json({
              package_kind: "ffmpeg",
              releases: [],
              platform: state().platform,
            }),
      ),
    );
    renderSetup(state(paths));
    await waitFor(() =>
      expect(screen.getByRole("alert")).toHaveTextContent(
        "upstream unavailable",
      ),
    );
    unavailable = false;
    await click("Обновить каталог");
    await waitFor(() => expect(screen.queryByRole("alert")).toBeNull());
  });

  it("blocks summary completion on server validation or completion conflict", async () => {
    let healthy = false;
    server.use(
      http.get("/api/setup", () => HttpResponse.json(state(verified, healthy))),
      http.post("/api/setup/complete", () =>
        HttpResponse.json({ detail: "requirements changed" }, { status: 409 }),
      ),
    );
    const done = vi.fn();
    renderSetup(state(verified, true), done);
    await click("Завершить Setup");
    await waitFor(() =>
      expect(screen.getByRole("alert")).toHaveTextContent("setup incomplete"),
    );
    healthy = true;
    await click("Завершить Setup");
    await waitFor(() =>
      expect(screen.getByRole("alert")).toHaveTextContent(
        "requirements changed",
      ),
    );
    expect(done).not.toHaveBeenCalled();
  });

  it("keeps metadata check retryable when the provider returns failure", async () => {
    let available = false;
    server.use(
      http.put(
        "/api/setup/musicbrainz",
        () => new HttpResponse(null, { status: 204 }),
      ),
      http.post("/api/setup/check-musicbrainz", () =>
        HttpResponse.json(
          available
            ? { success: true }
            : { success: false, error: "MusicBrainz unavailable" },
        ),
      ),
      http.get("/api/setup", () =>
        HttpResponse.json(
          state(
            available
              ? {
                  ...verified,
                  musicbrainz_mode: "self-hosted",
                  musicbrainz_base_url: "https://mb.example.com",
                }
              : published,
            available,
          ),
        ),
      ),
    );
    renderSetup(state(published));
    fireEvent.click(screen.getByRole("radio", { name: "Self-hosted" }));
    fireEvent.change(screen.getByLabelText("MusicBrainz base URL"), {
      target: { value: "https://mb.example.com" },
    });
    await click("Проверить MusicBrainz");
    await waitFor(() =>
      expect(screen.getByRole("alert")).toHaveTextContent(
        "MusicBrainz unavailable",
      ),
    );
    available = true;
    await click("Проверить MusicBrainz");
    await waitFor(() => expect(screen.getByText(/Проверено:/)).toBeVisible());
  });

  it("follows a failed operation through retry, running and success without reload", async () => {
    localStorage.setItem("melotrove.setup.operations", operation.id);
    let current: OperationResponse = {
      ...operation,
      state: "failed",
      safe_error: "download unavailable",
    };
    let ready = false;
    const retry = vi.fn(() => {
      current = { ...operation, state: "queued", stage: "queued" };
      return HttpResponse.json(current);
    });
    server.use(
      http.get("/api/tools/installations", () =>
        HttpResponse.json({
          installations: ready
            ? [
                {
                  id: "ff-ready",
                  package_kind: "ffmpeg",
                  state: "ready",
                  active: true,
                  release_identity: "8.0",
                  source_name: "trusted",
                  created_at: operation.created_at,
                  executable_versions: { ffmpeg: "8.0" },
                },
              ]
            : [],
        }),
      ),
      http.get("/api/tools/catalog", ({ request }) =>
        HttpResponse.json({
          package_kind: new URL(request.url).searchParams.get("package_kind"),
          releases: [],
          platform: state().platform,
        }),
      ),
      http.get(`/api/operations/${operation.id}`, () =>
        HttpResponse.json(current),
      ),
      http.post(`/api/operations/${operation.id}/retry`, retry),
    );
    renderSetup(state(paths));
    await waitFor(() => expect(TestEventSource.instances).toHaveLength(1));
    const stream = TestEventSource.instances[0];
    await act(async () => {
      stream.dispatchEvent(new Event("open"));
    });
    await waitFor(() =>
      expect(screen.getByText(/download unavailable/)).toBeVisible(),
    );
    const button = screen.getByRole("button", {
      name: `Повторить операцию ${operation.id}`,
    });
    button.focus();
    fireEvent.keyDown(button, { key: "Enter", code: "Enter" });
    fireEvent.keyUp(button, { key: "Enter", code: "Enter" });
    await waitFor(() => expect(retry).toHaveBeenCalledOnce());
    expect(screen.getByText(/queued, queued/)).toBeVisible();
    expect(localStorage.getItem("melotrove.setup.operations")).toBe(
      operation.id,
    );
    current = { ...operation, state: "running", bytes_completed: 70 };
    await act(async () => {
      stream.dispatchEvent(new Event("operation-changed"));
    });
    await waitFor(() =>
      expect(screen.getByText(/running, download, 70/)).toBeVisible(),
    );
    ready = true;
    current = {
      ...operation,
      state: "succeeded",
      stage: "verified",
      bytes_completed: 100,
    };
    await act(async () => {
      stream.dispatchEvent(new Event("operation-changed"));
    });
    expect(await screen.findByText("8.0: Активна")).toBeVisible();
    expect(localStorage.getItem("melotrove.setup.operations")).toBe("");
    expect(stream.close).toHaveBeenCalled();
  });

  it("tracks overlapping FFmpeg and fpcalc installs independently across reload and failure", async () => {
    const ids = {
      ffmpeg: "26092c63-0e0f-4dc1-af39-674dc1ce037c",
      fpcalc: "36092c63-0e0f-4dc1-af39-674dc1ce037c",
    };
    const snapshots: Record<string, OperationResponse> = {};
    const retry = vi.fn(({ params }: { params: { id: string } }) => {
      const id = String(params.id);
      snapshots[id] = { ...operation, id, state: "queued", stage: "queued" };
      return HttpResponse.json(snapshots[id]);
    });
    server.use(
      http.get("/api/tools/installations", () =>
        HttpResponse.json({ installations: [] }),
      ),
      http.get("/api/tools/catalog", ({ request }) =>
        HttpResponse.json({
          package_kind: new URL(request.url).searchParams.get("package_kind"),
          releases: [{ identity: "1.0", source: "trusted", artifacts: [] }],
          platform: state().platform,
        }),
      ),
      http.post("/api/tools/installations/preflight", async ({ request }) => {
        const body: unknown = await request.json();
        if (
          typeof body !== "object" ||
          body === null ||
          !("package_kind" in body) ||
          (body.package_kind !== "ffmpeg" && body.package_kind !== "fpcalc")
        )
          return HttpResponse.json({ detail: "invalid kind" }, { status: 400 });
        return HttpResponse.json({
          preflight_token: body.package_kind,
          conflicts: [],
          targets: [],
        });
      }),
      http.post("/api/tools/installations", async ({ request }) => {
        const body: unknown = await request.json();
        if (
          typeof body !== "object" ||
          body === null ||
          !("preflight_token" in body) ||
          (body.preflight_token !== "ffmpeg" &&
            body.preflight_token !== "fpcalc")
        )
          return HttpResponse.json(
            { detail: "invalid token" },
            { status: 400 },
          );
        const snapshot = { ...operation, id: ids[body.preflight_token] };
        snapshots[snapshot.id] = snapshot;
        return HttpResponse.json(snapshot);
      }),
      http.get("/api/operations/:id", ({ params }) =>
        HttpResponse.json(snapshots[String(params.id)]),
      ),
      http.post("/api/operations/:id/retry", retry),
    );
    const mounted = renderSetup(state(paths));
    for (const kind of ["ffmpeg", "fpcalc"] as const) {
      await selectVersion(kind, "1.0");
      await click(`Проверить установку ${kind}`);
      await waitFor(() =>
        expect(
          screen.getByText(new RegExp(`Операция ${ids[kind]}`)),
        ).toBeVisible(),
      );
    }
    expect(
      localStorage.getItem("melotrove.setup.operations")?.split(","),
    ).toEqual([ids.ffmpeg, ids.fpcalc]);
    mounted.unmount();
    expect(
      TestEventSource.instances
        .slice(0, 2)
        .every((source) => source.close.mock.calls.length === 1),
    ).toBe(true);
    renderSetup(state(paths));
    await waitFor(() => expect(TestEventSource.instances).toHaveLength(4));
    const ff = [...TestEventSource.instances]
      .reverse()
      .find((source) => source.url.includes(ids.ffmpeg));
    const fp = [...TestEventSource.instances]
      .reverse()
      .find((source) => source.url.includes(ids.fpcalc));
    if (!ff || !fp) throw new Error("Both operation streams must reconnect");
    snapshots[ids.ffmpeg] = {
      ...operation,
      id: ids.ffmpeg,
      state: "failed",
      safe_error: "ffmpeg failure",
    };
    await act(async () => {
      ff.dispatchEvent(new Event("open"));
      fp.dispatchEvent(new Event("open"));
    });
    await waitFor(() =>
      expect(screen.getByText(/ffmpeg failure/)).toBeVisible(),
    );
    expect(screen.getByText(/Операция 36092c63.*running/)).toBeVisible();
    await click(`Повторить операцию ${ids.ffmpeg}`);
    await waitFor(() => expect(retry).toHaveBeenCalledOnce());
    expect(
      localStorage.getItem("melotrove.setup.operations")?.split(","),
    ).toEqual([ids.ffmpeg, ids.fpcalc]);
    snapshots[ids.ffmpeg] = {
      ...operation,
      id: ids.ffmpeg,
      state: "succeeded",
      stage: "verified",
    };
    await act(async () => {
      ff.dispatchEvent(new Event("operation-changed"));
    });
    await waitFor(() =>
      expect(screen.getByText(/Операция 26092c63.*succeeded/)).toBeVisible(),
    );
    expect(localStorage.getItem("melotrove.setup.operations")).toBe(ids.fpcalc);
    snapshots[ids.fpcalc] = {
      ...operation,
      id: ids.fpcalc,
      state: "failed",
      safe_error: "fpcalc failure",
    };
    await act(async () => {
      fp.dispatchEvent(new Event("operation-changed"));
    });
    await waitFor(() =>
      expect(screen.getByText(/fpcalc failure/)).toBeVisible(),
    );
    expect(localStorage.getItem("melotrove.setup.operations")).toBe(ids.fpcalc);
  });

  it("discovers two server-only installs and retries the failed one without creating another stream", async () => {
    const ffmpeg = {
      ...operation,
      id: "26092c63-0e0f-4dc1-af39-674dc1ce037c",
      state: "failed",
      safe_error: "ffmpeg download failed",
    };
    let fpcalc: OperationResponse = {
      ...operation,
      id: "36092c63-0e0f-4dc1-af39-674dc1ce037c",
      state: "running",
    };
    const listed = vi.fn();
    const ffmpegRetry = vi.fn(() =>
      HttpResponse.json({ ...ffmpeg, state: "queued", stage: "queued" }),
    );
    const fpcalcRetry = vi.fn(() =>
      HttpResponse.json({ ...fpcalc, state: "queued", stage: "queued" }),
    );
    server.use(
      http.get("/api/operations", ({ request }) => {
        listed(new URL(request.url).search);
        return HttpResponse.json({
          operations: [
            ffmpeg,
            fpcalc,
            { ...operation, id: "ignored-move", kind: "move" },
            { ...operation, id: "ignored-success", state: "succeeded" },
          ],
        });
      }),
      http.get("/api/tools/installations", () =>
        HttpResponse.json({ installations: [] }),
      ),
      http.get("/api/tools/catalog", ({ request }) =>
        HttpResponse.json({
          package_kind: new URL(request.url).searchParams.get("package_kind"),
          releases: [],
          platform: state().platform,
        }),
      ),
      http.get("/api/operations/:id", ({ params }) =>
        HttpResponse.json(String(params.id) === ffmpeg.id ? ffmpeg : fpcalc),
      ),
      http.post(`/api/operations/${ffmpeg.id}/retry`, ffmpegRetry),
      http.post(`/api/operations/${fpcalc.id}/retry`, fpcalcRetry),
    );
    const subscribed = new Promise<void>((resolve) => {
      TestEventSource.onCreated = () => {
        if (TestEventSource.instances.length === 2) {
          TestEventSource.onCreated = undefined;
          resolve();
        }
      };
    });
    renderSetup(state(paths));
    await screen.findByText(/Операция 26092c63.*failed/);
    expect(screen.getByText(/Операция 36092c63.*running/)).toBeVisible();
    expect(listed).toHaveBeenCalledExactlyOnceWith("");
    await subscribed;
    expect(TestEventSource.instances).toHaveLength(2);
    expect(
      localStorage.getItem("melotrove.setup.operations")?.split(","),
    ).toEqual([ffmpeg.id, fpcalc.id]);
    await click(`Повторить операцию ${ffmpeg.id}`);
    await screen.findByText(/Операция 26092c63.*queued/);
    expect(ffmpegRetry).toHaveBeenCalledOnce();
    expect(screen.getByText(/Операция 36092c63.*running/)).toBeVisible();
    fpcalc = {
      ...fpcalc,
      state: "failed",
      safe_error: "fpcalc download failed",
    };
    const fpcalcStream = TestEventSource.instances.find((source) =>
      source.url.includes(fpcalc.id),
    );
    if (!fpcalcStream) throw new Error("fpcalc stream must remain subscribed");
    await act(async () => {
      fpcalcStream.dispatchEvent(new Event("operation-changed"));
    });
    await screen.findByText(/Операция 36092c63.*failed/);
    await click(`Повторить операцию ${fpcalc.id}`);
    await screen.findByText(/Операция 36092c63.*queued/);
    expect(fpcalcRetry).toHaveBeenCalledOnce();
    expect(screen.getByText(/Операция 26092c63.*queued/)).toBeVisible();
    expect(TestEventSource.instances).toHaveLength(2);
  });

  it("keeps saved operations and the catalog usable when discovery fails", async () => {
    const otherId = "36092c63-0e0f-4dc1-af39-674dc1ce037c";
    localStorage.setItem(
      "melotrove.setup.operations",
      `${operation.id},${operation.id},${otherId}`,
    );
    localStorage.setItem("melotrove.setup.operation", otherId);
    const listed = vi.fn(() =>
      HttpResponse.json(
        { detail: "operation list unavailable" },
        { status: 503 },
      ),
    );
    server.use(
      http.get("/api/operations", listed),
      http.get("/api/tools/installations", () =>
        HttpResponse.json({ installations: [] }),
      ),
      http.get("/api/tools/catalog", ({ request }) =>
        HttpResponse.json({
          package_kind: new URL(request.url).searchParams.get("package_kind"),
          releases: [{ identity: "8.0", source: "trusted", artifacts: [] }],
          platform: state().platform,
        }),
      ),
      http.get("/api/operations/:id", ({ params }) =>
        HttpResponse.json({ ...operation, id: String(params.id) }),
      ),
    );
    renderSetup(state(paths));
    await screen.findByRole("alert");
    expect(screen.getByRole("alert")).toHaveTextContent(
      "operation list unavailable",
    );
    expect(listed).toHaveBeenCalledOnce();
    await selectVersion("ffmpeg", "8.0");
    expect(TestEventSource.instances).toHaveLength(2);
    expect(
      localStorage.getItem("melotrove.setup.operations")?.split(","),
    ).toEqual([operation.id, otherId]);
  });

  it("retries only the installation refresh after a succeeded operation without reload", async () => {
    localStorage.setItem("melotrove.setup.operations", operation.id);
    let installationRequests = 0;
    let initialLoaded: () => void = () => {};
    const initialInstallations = new Promise<void>((resolve) => {
      initialLoaded = resolve;
    });
    const ready = {
      id: "ff-ready",
      package_kind: "ffmpeg",
      state: "ready",
      active: true,
      release_identity: "8.0",
      source_name: "trusted",
      created_at: operation.created_at,
      executable_versions: { ffmpeg: "8.0" },
    };
    server.use(
      http.get("/api/tools/installations", () => {
        installationRequests += 1;
        if (installationRequests === 1) initialLoaded();
        if (installationRequests === 2)
          return HttpResponse.json(
            { detail: "installation refresh unavailable" },
            { status: 503 },
          );
        return HttpResponse.json({
          installations: installationRequests === 1 ? [] : [ready],
        });
      }),
      http.get("/api/tools/catalog", ({ request }) =>
        HttpResponse.json({
          package_kind: new URL(request.url).searchParams.get("package_kind"),
          releases: [{ identity: "8.0", source: "trusted", artifacts: [] }],
          platform: state().platform,
        }),
      ),
      http.get(`/api/operations/${operation.id}`, async () => {
        await initialInstallations;
        return HttpResponse.json({
          ...operation,
          state: "succeeded",
          stage: "verified",
        });
      }),
    );
    renderSetup(state(paths));
    await selectVersion("ffmpeg", "8.0");
    expect(TestEventSource.instances).toHaveLength(1);
    const retryRefresh = await screen.findByRole("button", {
      name: `Повторить загрузку установок ${operation.id}`,
    });
    expect(screen.getByRole("alert")).toHaveTextContent(
      "installation refresh unavailable",
    );
    expect(localStorage.getItem("melotrove.setup.operations")).toBe(
      operation.id,
    );
    fireEvent.click(retryRefresh);
    await screen.findByText("8.0: Активна");
    expect(installationRequests).toBe(3);
    expect(localStorage.getItem("melotrove.setup.operations")).toBe("");
    expect(TestEventSource.instances[0].close).toHaveBeenCalledOnce();
  });

  it("rechecks a persisted succeeded operation after remount when discovery excludes it", async () => {
    localStorage.setItem("melotrove.setup.operations", operation.id);
    let installationRequests = 0;
    let initialLoaded: () => void = () => {};
    const initialInstallations = new Promise<void>((resolve) => {
      initialLoaded = resolve;
    });
    server.use(
      http.get("/api/tools/installations", () => {
        installationRequests += 1;
        if (installationRequests === 1) initialLoaded();
        if (installationRequests === 2)
          return HttpResponse.json(
            { detail: "installation refresh unavailable" },
            { status: 503 },
          );
        return HttpResponse.json({
          installations:
            installationRequests < 4
              ? []
              : [
                  {
                    id: "ff-ready",
                    package_kind: "ffmpeg",
                    state: "ready",
                    active: true,
                    release_identity: "8.0",
                    source_name: "trusted",
                    created_at: operation.created_at,
                    executable_versions: { ffmpeg: "8.0" },
                  },
                ],
        });
      }),
      http.get("/api/tools/catalog", ({ request }) =>
        HttpResponse.json({
          package_kind: new URL(request.url).searchParams.get("package_kind"),
          releases: [{ identity: "8.0", source: "trusted", artifacts: [] }],
          platform: state().platform,
        }),
      ),
      http.get(`/api/operations/${operation.id}`, async () => {
        await initialInstallations;
        return HttpResponse.json({
          ...operation,
          state: "succeeded",
          stage: "verified",
        });
      }),
    );
    const first = renderSetup(state(paths));
    await selectVersion("ffmpeg", "8.0");
    await screen.findByRole("button", {
      name: `Повторить загрузку установок ${operation.id}`,
    });
    expect(localStorage.getItem("melotrove.setup.operations")).toBe(
      operation.id,
    );
    first.unmount();
    renderSetup(state(paths));
    expect(TestEventSource.instances).toHaveLength(2);
    await screen.findByText("8.0: Активна");
    expect(localStorage.getItem("melotrove.setup.operations")).toBe("");
  });

  it("reads a failed operation over REST without an SSE open and recovers after disconnect", async () => {
    localStorage.setItem("melotrove.setup.operations", operation.id);
    let available = true;
    const retry = vi.fn(() =>
      HttpResponse.json({ ...operation, state: "queued" }),
    );
    server.use(
      http.get("/api/tools/installations", () =>
        HttpResponse.json({ installations: [] }),
      ),
      http.get("/api/tools/catalog", ({ request }) =>
        HttpResponse.json({
          package_kind: new URL(request.url).searchParams.get("package_kind"),
          releases: [],
          platform: state().platform,
        }),
      ),
      http.get(`/api/operations/${operation.id}`, () =>
        available
          ? HttpResponse.json({
              ...operation,
              state: "failed",
              safe_error: "download unavailable",
            })
          : HttpResponse.json(
              { detail: "snapshot unavailable" },
              { status: 503 },
            ),
      ),
      http.post(`/api/operations/${operation.id}/retry`, retry),
    );
    renderSetup(state(paths));
    await screen.findByRole("button", {
      name: `Повторить операцию ${operation.id}`,
    });
    expect(TestEventSource.instances).toHaveLength(1);
    await act(async () =>
      TestEventSource.instances[0].dispatchEvent(new Event("error")),
    );
    expect(
      screen.getByRole("button", {
        name: `Повторить загрузку операции ${operation.id}`,
      }),
    ).toBeVisible();
    available = false;
    await click(`Повторить загрузку операции ${operation.id}`);
    await waitFor(() =>
      expect(screen.getByRole("alert")).toHaveTextContent(
        "snapshot unavailable",
      ),
    );
    available = true;
    await click(`Повторить загрузку операции ${operation.id}`);
    await waitFor(() =>
      expect(
        screen.queryByRole("button", {
          name: `Повторить загрузку операции ${operation.id}`,
        }),
      ).toBeNull(),
    );
    await click(`Повторить операцию ${operation.id}`);
    await waitFor(() => expect(retry).toHaveBeenCalledOnce());
  });

  it("does not overwrite a newer SSE REST snapshot with a late initial response", async () => {
    localStorage.setItem("melotrove.setup.operations", operation.id);
    let releaseInitial: (() => void) | undefined;
    onTestFinished(() => releaseInitial?.());
    let calls = 0;
    let started: (request: Request) => void = () => {};
    const initialStarted = new Promise<Request>((resolve) => {
      started = resolve;
    });
    server.use(
      http.get("/api/tools/installations", () =>
        HttpResponse.json({ installations: [] }),
      ),
      http.get("/api/tools/catalog", ({ request }) =>
        HttpResponse.json({
          package_kind: new URL(request.url).searchParams.get("package_kind"),
          releases: [],
          platform: state().platform,
        }),
      ),
      http.get(`/api/operations/${operation.id}`, ({ request }) => {
        calls += 1;
        if (calls > 1)
          return HttpResponse.json({ ...operation, bytes_completed: 90 });
        return new Promise<Response>((resolve) => {
          releaseInitial = () => resolve(HttpResponse.json(operation));
          started(request);
        });
      }),
    );
    const mounted = renderSetup(state(paths));
    const oldRequest = await initialStarted;
    const aborted = new Promise<void>((resolve) => {
      oldRequest.signal.addEventListener("abort", () => resolve(), {
        once: true,
      });
    });
    await act(async () =>
      TestEventSource.instances[0].dispatchEvent(new Event("open")),
    );
    await aborted;
    await screen.findByText(/running, download, 90/);
    await act(async () => releaseInitial?.());
    expect(screen.getByText(/running, download, 90/)).toBeVisible();
    mounted.unmount();
  });

  it("hides stale MusicBrainz verification for edited mode and URL", async () => {
    localStorage.setItem("melotrove.setup.step", "4");
    server.use(
      http.get("/api/setup", () => HttpResponse.json(state(verified, true))),
    );
    const mounted = renderSetup(state(verified, true));
    expect(screen.getByText(/Проверено:/)).toBeVisible();
    fireEvent.click(screen.getByRole("radio", { name: "Self-hosted" }));
    expect(screen.queryByText(/Проверено:/)).toBeNull();
    expect(screen.getByText(/Конфигурация изменена/)).toBeVisible();
    fireEvent.change(screen.getByLabelText("MusicBrainz base URL"), {
      target: { value: "https://mb.example.com" },
    });
    expect(screen.queryByText(/Проверено:/)).toBeNull();
    fireEvent.click(screen.getByRole("radio", { name: "Public" }));
    expect(screen.getByText(/Проверено:/)).toBeVisible();
    mounted.unmount();
    renderSetup(
      state(
        {
          ...verified,
          musicbrainz_mode: "self-hosted",
          musicbrainz_base_url: "https://old.example",
        },
        true,
      ),
    );
    expect(screen.getByText(/Проверено:/)).toBeVisible();
    fireEvent.change(screen.getByLabelText("MusicBrainz base URL"), {
      target: { value: "https://new.example" },
    });
    expect(screen.queryByText(/Проверено:/)).toBeNull();
  });

  it("recovers server-only operations after a failed discovery without remounting", async () => {
    let available = false;
    const listed = vi.fn(() =>
      available
        ? HttpResponse.json({
            operations: [
              { ...operation, state: "failed", safe_error: "server failure" },
            ],
          })
        : HttpResponse.json(
            { detail: "discovery unavailable" },
            { status: 503 },
          ),
    );
    server.use(
      http.get("/api/operations", listed),
      http.get("/api/tools/installations", () =>
        HttpResponse.json({ installations: [] }),
      ),
      http.get("/api/tools/catalog", ({ request }) =>
        HttpResponse.json({
          package_kind: new URL(request.url).searchParams.get("package_kind"),
          releases: [],
          platform: state().platform,
        }),
      ),
      http.get(`/api/operations/${operation.id}`, () =>
        HttpResponse.json({
          ...operation,
          state: "failed",
          safe_error: "server failure",
        }),
      ),
    );
    renderSetup(state(paths));
    await waitFor(() => expect(screen.getByRole("alert")).toHaveFocus());
    expect(screen.getByRole("alert")).toHaveTextContent(
      "discovery unavailable",
    );
    available = true;
    await click("Повторить загрузку операций");
    await screen.findByRole("button", {
      name: `Повторить операцию ${operation.id}`,
    });
    expect(screen.queryByText("discovery unavailable")).toBeNull();
    expect(listed).toHaveBeenCalledTimes(2);
    expect(localStorage.getItem("melotrove.setup.operations")).toBe(
      operation.id,
    );
  });

  it("keeps keyboard selection and accessible roles for publication and metadata controls", async () => {
    server.use(
      http.put(
        "/api/setup/runtime",
        () => new HttpResponse(null, { status: 204 }),
      ),
      http.get("/api/setup", () =>
        HttpResponse.json(state({ ...installed, publication_format: "mka" })),
      ),
    );
    renderSetup(state(installed));
    expect(
      screen.getByRole("radiogroup", { name: "Publication format" }),
    ).toBeVisible();
    const source = screen.getByRole("radio", { name: "Исходный формат" });
    source.focus();
    fireEvent.keyDown(source, { key: "ArrowRight", code: "ArrowRight" });
    fireEvent.keyUp(source, { key: "ArrowRight", code: "ArrowRight" });
    expect(screen.getByRole("radio", { name: "MKA remux" })).toBeChecked();
    await click("Продолжить");
    await screen.findByRole("heading", {
      level: 2,
      name: "Провайдеры метаданных",
    });
    const lrclib = screen.getByRole("checkbox", { name: "LRCLIB включён" });
    expect(lrclib).toBeChecked();
    lrclib.focus();
    fireEvent.click(lrclib);
    expect(lrclib).not.toBeChecked();
  });
});
