import { JSDOM } from "jsdom";
import "@testing-library/jest-dom/vitest";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { OperationResponse } from "../../api/generated/client.schemas";
import { nextResponse, server } from "../../test/server";
import { SettingsScreen } from "./SettingsScreen";

const settings = {
  completed: true,
  configuration_health: { healthy: true, problems: [] as string[] },
  platform: {
    diagnostic: false,
    goos: "linux",
    goarch: "amd64",
    supported: true,
  },
  settings: {
    tools_directory: "/srv/tools",
    output_directory: "/srv/music",
    publication_format: "mka",
    musicbrainz_mode: "public",
    musicbrainz_base_url: "",
    musicbrainz_verified_at: "2026-09-01T00:00:00Z",
    lrclib_enabled: true,
    sha256_enabled: true,
    log_level: "info",
    active_ffmpeg_installation_id: "ff-active",
    active_fpcalc_installation_id: "fp-active",
    output_case_sensitive: true,
    output_unicode_normalization: "none",
  },
};
const activeFF = {
  id: "ff-active",
  package_kind: "ffmpeg",
  release_identity: "ff-1",
  source_name: "fixture",
  state: "ready",
  active: true,
  executable_versions: {},
  created_at: "2026-01-01T00:00:00Z",
};
const inactiveFF = {
  ...activeFF,
  id: "ff-old",
  release_identity: "ff-0",
  active: false,
};
const failedFF = {
  ...inactiveFF,
  id: "ff-failed",
  release_identity: "ff-failed",
  state: "failed",
};
const activeFP = {
  ...activeFF,
  id: "fp-active",
  package_kind: "fpcalc",
  release_identity: "fp-1",
};
const operation = {
  id: "op-1",
  kind: "move",
  state: "queued",
  stage: "copy",
  bytes_completed: 0,
  bytes_total: 10,
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-01T00:00:00Z",
};
const json = (body: unknown) => HttpResponse.json(body as never);
function nextResponseFor(path: string, method?: string) {
  return new Promise<void>((resolve) => {
    const listener = ({ request }: { request: Request }) => {
      if (
        new URL(request.url).pathname !== path ||
        (method && request.method !== method)
      )
        return;
      server.events.removeListener("response:mocked", listener);
      resolve();
    };
    server.events.on("response:mocked", listener);
  });
}
function nextResponsesFor(
  expected: Array<{ path: string; method: string; packageKind?: string }>,
) {
  return new Promise<void>((resolve) => {
    const remaining = [...expected];
    const listener = ({ request }: { request: Request }) => {
      const url = new URL(request.url);
      const index = remaining.findIndex(
        (item) =>
          url.pathname === item.path &&
          request.method === item.method &&
          (item.packageKind === undefined ||
            url.searchParams.get("package_kind") === item.packageKind),
      );
      if (index < 0) return;
      remaining.splice(index, 1);
      if (remaining.length === 0) {
        server.events.removeListener("response:mocked", listener);
        resolve();
      }
    };
    server.events.on("response:mocked", listener);
  });
}
class TestEventSource extends EventTarget {
  static instances: TestEventSource[] = [];
  close = vi.fn();
  constructor(public url: string) {
    super();
    TestEventSource.instances.push(this);
  }
}
function common({
  installed = [activeFF, inactiveFF, activeFP],
  operations = [] as unknown[],
  healthy = true,
} = {}) {
  server.use(
    http.get("/api/settings", () =>
      json({
        ...settings,
        configuration_health: {
          healthy,
          problems: healthy ? [] : ["FFmpeg недоступен"],
        },
      }),
    ),
    http.get("/api/tools/installations", ({ request }) => {
      const kind = new URL(request.url).searchParams.get("package_kind");
      return json({
        installations: installed.filter((item) => item.package_kind === kind),
      });
    }),
    http.get("/api/operations", () => json({ operations })),
    http.get("/api/tools/catalog", ({ request }) => {
      const kind = new URL(request.url).searchParams.get("package_kind");
      return json({
        package_kind: kind,
        releases: [
          { identity: `${kind}-release`, source: "fixture", artifacts: [] },
        ],
      });
    }),
    http.get("/api/operations/:id", () => json(operation)),
  );
}

beforeEach(() => {
  Object.defineProperty(HTMLDialogElement.prototype, "showModal", {
    configurable: true,
    value(this: HTMLDialogElement) {
      this.setAttribute("open", "");
      this.querySelector<HTMLElement>(
        "button:not(:disabled), input:not(:disabled), select:not(:disabled), textarea:not(:disabled)",
      )?.focus();
    },
  });
  Object.defineProperty(HTMLDialogElement.prototype, "close", {
    configurable: true,
    value(this: HTMLDialogElement) {
      this.removeAttribute("open");
      this.dispatchEvent(new Event("close"));
    },
  });
  vi.stubGlobal(
    "localStorage",
    new JSDOM("", { url: "http://localhost" }).window.localStorage,
  );
  localStorage.clear();
  TestEventSource.instances = [];
  vi.stubGlobal("EventSource", TestEventSource);
  common();
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

async function pressEnter(target: HTMLElement) {
  target.focus();
  fireEvent.keyDown(target, { key: "Enter", code: "Enter" });
  fireEvent.keyUp(target, { key: "Enter", code: "Enter" });
}

describe("SettingsScreen", () => {
  it("moves focus into each dialog when opened with Enter", async () => {
    render(<SettingsScreen />);
    const moveTrigger = await screen.findByRole("button", {
      name: "Перенести каталог",
    });
    await waitFor(() => expect(moveTrigger).not.toBeDisabled());
    await pressEnter(moveTrigger);
    const moveDialog = await screen.findByRole("dialog", {
      name: "Перенос Tools directory",
    });
    expect(
      within(moveDialog).getByLabelText("Новый Tools directory"),
    ).toHaveFocus();
    fireEvent.click(within(moveDialog).getByRole("button", { name: "Отмена" }));

    server.use(
      http.post("/api/tools/installations/preflight", () =>
        HttpResponse.json({
          preflight_token: "focus-token",
          targets: ["/srv/tools/ffmpeg/new/ffmpeg"],
          conflicts: ["/srv/tools/ffmpeg/new/ffmpeg"],
        }),
      ),
    );
    await screen.findByRole("option", { name: /ffmpeg-release/ });
    fireEvent.change(screen.getByLabelText("Версия ffmpeg"), {
      target: { value: "ffmpeg-release" },
    });
    const installTrigger = within(
      screen.getByRole("region", { name: "FFmpeg package" }),
    ).getByRole("button", { name: "Установить без активации" });
    await waitFor(() => expect(installTrigger).not.toBeDisabled());
    await pressEnter(installTrigger);
    const installDialog = await screen.findByRole("dialog", {
      name: /Подтверждение установки/,
    });
    expect(
      within(installDialog).getByRole("button", {
        name: "Подтвердить перечисленные конфликты",
      }),
    ).toHaveFocus();
  });

  it("closes the move dialog on Escape and restores focus to its trigger", async () => {
    render(<SettingsScreen />);
    const trigger = await screen.findByRole("button", {
      name: "Перенести каталог",
    });
    await waitFor(() => expect(trigger).not.toBeDisabled());
    await pressEnter(trigger);
    const dialog = await screen.findByRole("dialog", {
      name: "Перенос Tools directory",
    });
    fireEvent.keyDown(dialog, { key: "Escape", code: "Escape" });
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(trigger).toHaveFocus();
  });

  it("closes the move dialog on Cancel and restores focus to its trigger", async () => {
    render(<SettingsScreen />);
    const trigger = await screen.findByRole("button", {
      name: "Перенести каталог",
    });
    await waitFor(() => expect(trigger).not.toBeDisabled());
    await pressEnter(trigger);
    const dialog = await screen.findByRole("dialog", {
      name: "Перенос Tools directory",
    });
    fireEvent.click(within(dialog).getByRole("button", { name: "Отмена" }));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(trigger).toHaveFocus();
  });

  it("loads typed settings, health, and separate package inventories without redirecting", async () => {
    render(<SettingsScreen />);
    expect(await screen.findByText("/srv/tools")).toBeTruthy();
    expect(screen.getByLabelText("Output directory")).toHaveProperty(
      "value",
      "/srv/music",
    );
    expect(screen.getByText("FFmpeg package")).toBeTruthy();
    expect(screen.getByText("fpcalc", { selector: "h3" })).toBeTruthy();
    expect(screen.getByText("Конфигурация исправна.")).toBeTruthy();
    expect(window.location.hash).not.toBe("#/setup");
  });

  it("keeps the initial-load retry visible and focused through repeated refusals", async () => {
    let settingsReads = 0;
    let markRetryRequested!: () => void;
    const retryRequested = new Promise<void>((resolve) => {
      markRetryRequested = resolve;
    });
    let releaseRetry!: (response: Response) => void;
    const retryResponse = new Promise<Response>((resolve) => {
      releaseRetry = resolve;
    });
    server.use(
      http.get("/api/settings", () => {
        settingsReads += 1;
        if (settingsReads === 1) return HttpResponse.json({}, { status: 503 });
        if (settingsReads === 2) {
          markRetryRequested();
          return retryResponse;
        }
        return json(settings);
      }),
    );

    render(<SettingsScreen />);
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("503");
    await waitFor(() => expect(alert).toHaveFocus());
    expect(screen.queryByLabelText("Output directory")).not.toBeInTheDocument();

    const retry = screen.getByRole("button", { name: "Повторить загрузку" });
    retry.focus();
    await pressEnter(retry);
    await retryRequested;
    expect(
      screen.getByRole("button", { name: "Повторить загрузку" }),
    ).toBeDisabled();
    expect(screen.getByRole("alert")).toBeInTheDocument();

    await act(async () => {
      releaseRetry(HttpResponse.json({}, { status: 503 }));
      await retryResponse;
    });
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "Повторить загрузку" }),
      ).toBeEnabled(),
    );
    expect(await screen.findByRole("alert")).toHaveTextContent("503");

    server.use(http.get("/api/settings", () => json(settings)));
    fireEvent.click(screen.getByRole("button", { name: "Повторить загрузку" }));
    expect(await screen.findByLabelText("Output directory")).toHaveValue(
      "/srv/music",
    );
    expect(
      screen.queryByRole("button", { name: "Повторить загрузку" }),
    ).toBeNull();
  });

  it.each([
    "installations",
    "operations",
  ] as const)("retries the initial load when %s fails without exposing partial settings", async (failedRequest) => {
    let failedOnce = false;
    if (failedRequest === "installations") {
      server.use(
        http.get("/api/tools/installations", ({ request }) => {
          const kind = new URL(request.url).searchParams.get("package_kind");
          if (kind === "ffmpeg" && !failedOnce) {
            failedOnce = true;
            return HttpResponse.json({}, { status: 503 });
          }
          return json({
            installations: [activeFF, inactiveFF, activeFP].filter(
              (item) => item.package_kind === kind,
            ),
          });
        }),
      );
    } else {
      server.use(
        http.get("/api/operations", () => {
          if (!failedOnce) {
            failedOnce = true;
            return HttpResponse.json({}, { status: 503 });
          }
          return json({ operations: [] });
        }),
      );
    }

    render(<SettingsScreen />);
    expect(await screen.findByRole("alert")).toHaveTextContent("503");
    expect(screen.queryByLabelText("Output directory")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Повторить загрузку" }));
    expect(await screen.findByLabelText("Output directory")).toHaveValue(
      "/srv/music",
    );
    expect(
      screen.queryByRole("button", { name: "Повторить загрузку" }),
    ).toBeNull();
  });

  it("ignores initial-load responses after unmount", async () => {
    let markSettingsRequested!: () => void;
    const settingsRequested = new Promise<void>((resolve) => {
      markSettingsRequested = resolve;
    });
    let releaseSettings!: (response: Response) => void;
    const settingsResponse = new Promise<Response>((resolve) => {
      releaseSettings = resolve;
    });
    server.use(
      http.get("/api/settings", () => {
        markSettingsRequested();
        return settingsResponse;
      }),
    );
    const view = render(<SettingsScreen />);
    await settingsRequested;
    view.unmount();
    await act(async () => {
      releaseSettings(json(settings));
      await settingsResponse;
    });
  });

  it("automatically loads catalogs after the initial operations request completes", async () => {
    let releaseOperations!: () => void;
    const operationsGate = new Promise<void>((resolve) => {
      releaseOperations = resolve;
    });
    let markOperationsRequested!: () => void;
    const operationsRequested = new Promise<void>((resolve) => {
      markOperationsRequested = resolve;
    });
    const catalogRequests: string[] = [];
    server.use(
      http.get("/api/operations", async () => {
        markOperationsRequested();
        await operationsGate;
        return json({ operations: [] });
      }),
      http.get("/api/tools/catalog", ({ request }) => {
        const kind = new URL(request.url).searchParams.get("package_kind");
        if (kind) catalogRequests.push(kind);
        return json({
          package_kind: kind,
          releases: [
            { identity: `${kind}-release`, source: "fixture", artifacts: [] },
          ],
        });
      }),
    );

    render(<SettingsScreen />);
    await operationsRequested;
    expect(catalogRequests).toEqual([]);

    await act(async () => {
      releaseOperations();
      await operationsGate;
    });

    expect(await screen.findByLabelText("Output directory")).toHaveValue(
      "/srv/music",
    );
    expect(
      await screen.findByRole("option", { name: /ffmpeg-release/ }),
    ).toBeTruthy();
    expect(
      await screen.findByRole("option", { name: /fpcalc-release/ }),
    ).toBeTruthy();
    await waitFor(() =>
      expect(catalogRequests.sort()).toEqual(["ffmpeg", "fpcalc"]),
    );
  });

  it("saves settings and rereads server state, while showing validation errors", async () => {
    const calls: string[] = [];
    server.use(
      http.put("/api/settings/runtime", async ({ request }) => {
        calls.push("put");
        expect(await request.json()).toEqual({
          output_directory: "/new",
          publication_format: "source",
        });
        return new HttpResponse(null, { status: 204 });
      }),
      http.get("/api/settings", () => {
        calls.push("get");
        return json({
          ...settings,
          settings: {
            ...settings.settings,
            output_directory: calls.includes("put")
              ? "/new"
              : settings.settings.output_directory,
          },
        });
      }),
    );
    render(<SettingsScreen />);
    fireEvent.change(await screen.findByLabelText("Output directory"), {
      target: { value: "/new" },
    });
    await screen.findByRole("option", { name: /ffmpeg-release/ });
    fireEvent.change(screen.getByLabelText("Publication format"), {
      target: { value: "source" },
    });
    const response = nextResponseFor("/api/settings/runtime", "PUT");
    await act(async () => {
      fireEvent.click(
        screen.getByRole("button", { name: "Сохранить публикацию" }),
      );
      await response;
    });
    await waitFor(() => expect(calls).toContain("get"));
    expect(calls.indexOf("put")).toBeLessThan(calls.lastIndexOf("get"));
    expect(screen.getByLabelText("Output directory")).toHaveProperty(
      "value",
      "/new",
    );
    server.use(
      http.put("/api/settings/runtime", () =>
        HttpResponse.json({ detail: "Каталог недоступен" }, { status: 400 }),
      ),
    );
    const alertFocused = new Promise<HTMLElement>((resolve, reject) => {
      const timeout = setTimeout(() => {
        document.removeEventListener("focusin", onFocus);
        reject(new Error("validation alert was not focused"));
      }, 5000);
      function onFocus(event: FocusEvent) {
        const target = event.target;
        if (
          !(target instanceof HTMLElement) ||
          target.getAttribute("role") !== "alert" ||
          !target.textContent?.includes("Каталог недоступен")
        )
          return;
        clearTimeout(timeout);
        document.removeEventListener("focusin", onFocus);
        resolve(target);
      }
      document.addEventListener("focusin", onFocus);
    });
    fireEvent.click(
      screen.getByRole("button", { name: "Сохранить публикацию" }),
    );
    const focusedAlert = await alertFocused;
    const validation = await screen.findByRole("alert");
    expect(validation).toHaveTextContent("Каталог недоступен");
    expect(validation).toBe(focusedAlert);
    expect(validation).toHaveFocus();
  });

  it("saves MusicBrainz and LRCLIB changes through typed APIs", async () => {
    const mb = vi.fn();
    const lyrics = vi.fn();
    server.use(
      http.put("/api/settings/musicbrainz", async ({ request }) => {
        mb(await request.json());
        return new HttpResponse(null, { status: 204 });
      }),
      http.post("/api/settings/musicbrainz/check", () =>
        json({ success: true }),
      ),
      http.put("/api/settings/lrclib", async ({ request }) => {
        lyrics(await request.json());
        return new HttpResponse(null, { status: 204 });
      }),
    );
    render(<SettingsScreen />);
    fireEvent.change(await screen.findByLabelText("MusicBrainz mode"), {
      target: { value: "self-hosted" },
    });
    fireEvent.change(screen.getByLabelText("MusicBrainz base URL"), {
      target: { value: "https://music.example" },
    });
    fireEvent.click(
      screen.getByRole("button", { name: "Сохранить и проверить MusicBrainz" }),
    );
    await waitFor(() =>
      expect(mb).toHaveBeenCalledWith({
        mode: "self-hosted",
        base_url: "https://music.example",
      }),
    );
    expect(await screen.findByText("MusicBrainz проверен.")).toBeTruthy();
    fireEvent.click(screen.getByLabelText("LRCLIB включён"));
    fireEvent.click(screen.getByRole("button", { name: "Сохранить LRCLIB" }));
    await waitFor(() =>
      expect(lyrics).toHaveBeenCalledWith({ enabled: false }),
    );
  });

  it.each([
    true,
    false,
  ])("loads the persisted SHA-256 setting (%s) without a client default", async (enabled) => {
    server.use(
      http.get("/api/settings", () =>
        json({
          ...settings,
          settings: { ...settings.settings, sha256_enabled: enabled },
        }),
      ),
    );
    render(<SettingsScreen />);
    expect(await screen.findByLabelText("Вычислять SHA-256")).toHaveProperty(
      "checked",
      enabled,
    );
  });

  it.each([
    true,
    false,
  ])("persists the SHA-256 setting (%s) across an inspector-style screen remount", async (enabled) => {
    let persisted = !enabled;
    server.use(
      http.get("/api/settings", () =>
        json({
          ...settings,
          settings: { ...settings.settings, sha256_enabled: persisted },
        }),
      ),
      http.put("/api/settings/sha256", async ({ request }) => {
        const body = (await request.json()) as { enabled: boolean };
        persisted = body.enabled;
        return new HttpResponse(null, { status: 204 });
      }),
    );
    const first = render(<SettingsScreen />);
    const toggle = await screen.findByLabelText("Вычислять SHA-256");
    expect(toggle).toHaveProperty("checked", !enabled);
    fireEvent.click(toggle);
    const saved = nextResponseFor("/api/settings/sha256", "PUT");
    fireEvent.click(screen.getByRole("button", { name: "Сохранить SHA-256" }));
    await saved;
    await waitFor(() => expect(toggle).toHaveProperty("checked", enabled));
    expect(
      screen.getByText(
        /не удаляет сохранённые результаты и не запускает массовый пересчёт/,
      ),
    ).toBeVisible();

    first.unmount();
    render(<SettingsScreen />);
    expect(await screen.findByLabelText("Вычислять SHA-256")).toHaveProperty(
      "checked",
      enabled,
    );
  });

  it("does not invent a SHA-256 value while settings are loading", async () => {
    let releaseSettings!: () => void;
    const settingsReady = new Promise<void>((resolve) => {
      releaseSettings = resolve;
    });
    server.use(
      http.get("/api/settings", async () => {
        await settingsReady;
        return json(settings);
      }),
    );
    render(<SettingsScreen />);
    expect(
      screen.queryByLabelText("Вычислять SHA-256"),
    ).not.toBeInTheDocument();
    releaseSettings();
    expect(await screen.findByLabelText("Вычислять SHA-256")).toHaveProperty(
      "checked",
      true,
    );
  });

  it("saves an explicit false SHA-256 value, waits for the save, then refreshes server state", async () => {
    let enabled = true;
    let releaseSave!: () => void;
    const saveReady = new Promise<void>((resolve) => {
      releaseSave = resolve;
    });
    const saved = vi.fn(async ({ request }: { request: Request }) => {
      await saveReady;
      const body = await request.json();
      expect(body).toEqual({ enabled: false });
      enabled = false;
      return new HttpResponse(null, { status: 204 });
    });
    const reads = vi.fn(() =>
      json({
        ...settings,
        settings: { ...settings.settings, sha256_enabled: enabled },
      }),
    );
    server.use(
      http.put("/api/settings/sha256", saved),
      http.get("/api/settings", reads),
    );
    render(<SettingsScreen />);
    const toggle = await screen.findByLabelText("Вычислять SHA-256");
    fireEvent.click(toggle);
    const response = nextResponseFor("/api/settings/sha256", "PUT");
    fireEvent.click(screen.getByRole("button", { name: "Сохранить SHA-256" }));
    await waitFor(() => expect(saved).toHaveBeenCalledTimes(1));
    expect(screen.getByText("Выполняется запрос…")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Сохранить SHA-256" }),
    ).toBeDisabled();
    releaseSave();
    await response;
    await waitFor(() => expect(saved).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(reads).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(toggle).not.toBeChecked());
    expect(
      screen.getByText("Настройка SHA-256 сохранена."),
    ).toBeInTheDocument();
  });

  it("keeps a rejected SHA-256 draft across refresh while refreshing the server snapshot", async () => {
    const saved = vi.fn();
    const reads = vi.fn();
    server.use(
      http.put("/api/settings/sha256", () => {
        saved();
        return HttpResponse.json(
          { detail: "Не удалось сохранить" },
          { status: 500 },
        );
      }),
      http.get("/api/settings", () => {
        reads();
        return json(settings);
      }),
    );
    render(<SettingsScreen />);
    const toggle = await screen.findByLabelText("Вычислять SHA-256");
    fireEvent.click(toggle);
    fireEvent.click(screen.getByRole("button", { name: "Сохранить SHA-256" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Не удалось сохранить",
    );
    expect(saved).toHaveBeenCalledTimes(1);
    expect(toggle).not.toBeChecked();
    const refreshed = nextResponseFor("/api/settings", "GET");
    await act(async () => {
      fireEvent.click(
        screen.getByRole("button", { name: "Обновить состояние" }),
      );
      await refreshed;
    });
    expect(toggle).not.toBeChecked();
    expect(reads).toHaveBeenCalledTimes(2);
    expect(
      screen.getByText("Есть несохранённые изменения SHA-256."),
    ).toBeInTheDocument();
  });

  it("preserves a dirty publication draft when LRCLIB is saved and read back", async () => {
    let enabled = true;
    server.use(
      http.put("/api/settings/lrclib", async ({ request }) => {
        enabled = ((await request.json()) as { enabled: boolean }).enabled;
        return new HttpResponse(null, { status: 204 });
      }),
      http.get("/api/settings", () =>
        json({
          ...settings,
          settings: { ...settings.settings, lrclib_enabled: enabled },
        }),
      ),
    );
    render(<SettingsScreen />);
    fireEvent.change(await screen.findByLabelText("Output directory"), {
      target: { value: "/draft-output" },
    });
    fireEvent.click(screen.getByLabelText("LRCLIB включён"));
    fireEvent.click(screen.getByRole("button", { name: "Сохранить LRCLIB" }));
    await waitFor(() =>
      expect(screen.getByLabelText("LRCLIB включён")).not.toBeChecked(),
    );
    expect(screen.getByLabelText("Output directory")).toHaveValue(
      "/draft-output",
    );
    expect(
      screen.getByText("Есть несохранённые изменения публикации."),
    ).toBeInTheDocument();
  });

  it("preserves dirty MusicBrainz settings when a tool operation completes", async () => {
    let operationState = "running";
    let markOperationLoaded!: () => void;
    const operationLoaded = new Promise<void>((resolve) => {
      markOperationLoaded = resolve;
    });
    common({
      operations: [{ ...operation, kind: "install", state: "running" }],
    });
    server.use(
      http.get("/api/operations/:id", () => {
        const snapshot = json({
          ...operation,
          kind: "install",
          state: operationState,
        });
        if (operationState === "running") markOperationLoaded();
        return snapshot;
      }),
    );
    render(<SettingsScreen />);
    fireEvent.change(await screen.findByLabelText("MusicBrainz mode"), {
      target: { value: "self-hosted" },
    });
    fireEvent.change(screen.getByLabelText("MusicBrainz base URL"), {
      target: { value: "https://draft.example" },
    });
    await waitFor(() =>
      expect(TestEventSource.instances.length).toBeGreaterThan(0),
    );
    await operationLoaded;
    await screen.findByText(/install: running/);
    operationState = "succeeded";
    const settingsRefresh = nextResponseFor("/api/settings", "GET");
    await act(async () => {
      TestEventSource.instances[0].dispatchEvent(
        new Event("operation-changed"),
      );
      await settingsRefresh;
    });
    expect(screen.getByLabelText("MusicBrainz mode")).toHaveValue(
      "self-hosted",
    );
    expect(screen.getByLabelText("MusicBrainz base URL")).toHaveValue(
      "https://draft.example",
    );
    expect(
      screen.getByText("Есть несохранённые изменения MusicBrainz."),
    ).toBeInTheDocument();
  });

  it("keeps dirty move inputs and remove-old policy during an unrelated refresh", async () => {
    render(<SettingsScreen />);
    fireEvent.click(
      await screen.findByRole("button", { name: "Перенести каталог" }),
    );
    const path = screen.getByLabelText("Новый Tools directory");
    fireEvent.change(path, { target: { value: "/srv/unsaved-tools" } });
    const removeOld = screen.getByLabelText(
      "Удалить прежние управляемые файлы после успешного переноса",
    );
    fireEvent.click(removeOld);
    const refreshed = nextResponseFor("/api/settings", "GET");
    await act(async () => {
      fireEvent.click(
        screen.getByRole("button", { name: "Обновить состояние" }),
      );
      await refreshed;
    });
    expect(path).toHaveValue("/srv/unsaved-tools");
    expect(removeOld).toBeChecked();
  });

  it("invalidates pending and confirmed move plans when the server source root changes without replacing the destination draft", async () => {
    let reads = 0;
    let sourceDirectory = "/srv/tools";
    let operationState = "running";
    let markOperationLoaded!: () => void;
    const operationLoaded = new Promise<void>((resolve) => {
      markOperationLoaded = resolve;
    });
    let releasePreflight!: (response: Response) => void;
    let preflightStarted!: () => void;
    const pendingPreflight = new Promise<void>((resolve) => {
      preflightStarted = resolve;
    });
    let preflightCalls = 0;
    common({
      operations: [{ ...operation, kind: "install", state: "running" }],
    });
    server.use(
      http.get("/api/operations", () =>
        json({
          operations:
            operationState === "running"
              ? [{ ...operation, kind: "install", state: "running" }]
              : [],
        }),
      ),
      http.get("/api/settings", () => {
        reads += 1;
        return json({
          ...settings,
          settings: { ...settings.settings, tools_directory: sourceDirectory },
        });
      }),
      http.post("/api/tools/move/preflight", () => {
        preflightCalls += 1;
        if (preflightCalls === 1) {
          return new Promise<Response>((resolve) => {
            releasePreflight = resolve;
            preflightStarted();
          });
        }
        return json({
          preflight_token: `source-token-${preflightCalls}`,
          conflicts: [],
          managed_file_count: 1,
        });
      }),
      http.get("/api/operations/:id", () => {
        const snapshot = json({
          ...operation,
          kind: "install",
          state: operationState,
        });
        if (operationState === "running") markOperationLoaded();
        return snapshot;
      }),
    );
    const initialOperationSnapshot = nextResponseFor(
      "/api/operations/op-1",
      "GET",
    );
    render(<SettingsScreen />);
    fireEvent.click(
      await screen.findByRole("button", { name: "Перенести каталог" }),
    );
    const moveDialog = screen.getByRole("dialog", {
      name: "Перенос Tools directory",
    });
    await operationLoaded;
    await initialOperationSnapshot;
    await screen.findByText(/install: running/);
    const path = screen.getByLabelText("Новый Tools directory");
    fireEvent.change(path, { target: { value: "/srv/destination-draft" } });
    fireEvent.click(screen.getByRole("button", { name: "Проверить перенос" }));
    await pendingPreflight;

    sourceDirectory = "/srv/tools-external-change";
    operationState = "succeeded";
    const changedSource = nextResponseFor("/api/settings", "GET");
    await act(async () => {
      TestEventSource.instances[0].dispatchEvent(
        new Event("operation-changed"),
      );
      await changedSource;
    });
    await act(async () => {
      releasePreflight(
        json({
          preflight_token: "late-old-source-token",
          conflicts: [],
          managed_file_count: 1,
        }),
      );
    });
    expect(path).toHaveValue("/srv/destination-draft");
    expect(
      within(moveDialog).queryByRole("button", {
        name: "Подтвердить перенос",
      }),
    ).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Проверить перенос" }));
    await within(moveDialog).findByText("/srv/tools-external-change");
    const confirmMove = within(moveDialog).getByRole("button", {
      name: "Подтвердить перенос",
    });
    await waitFor(() => expect(confirmMove).toBeEnabled());
    sourceDirectory = "/srv/tools-newer-external-change";
    const changedAgain = nextResponseFor("/api/settings", "GET");
    const manualRefresh = screen.getByRole("button", {
      name: "Обновить состояние",
    });
    expect(manualRefresh).toBeEnabled();
    await act(async () => {
      fireEvent.click(manualRefresh);
      await changedAgain;
    });
    expect(path).toHaveValue("/srv/destination-draft");
    expect(
      within(moveDialog).queryByRole("button", {
        name: "Подтвердить перенос",
      }),
    ).not.toBeInTheDocument();
    // Initial load, operation success, and manual refresh each read Settings.
    expect(reads).toBe(3);
  });

  it("preserves edit-and-revert revisions across a pending PUT and readback", async () => {
    let releaseSave!: () => void;
    let releaseRead!: () => void;
    const saveGate = new Promise<void>((resolve) => {
      releaseSave = resolve;
    });
    const readGate = new Promise<void>((resolve) => {
      releaseRead = resolve;
    });
    let reads = 0;
    let readbackStarted!: () => void;
    const readbackRequest = new Promise<void>((resolve) => {
      readbackStarted = resolve;
    });
    server.use(
      http.put("/api/settings/runtime", async () => {
        await saveGate;
        return new HttpResponse(null, { status: 204 });
      }),
      http.get("/api/settings", async () => {
        reads += 1;
        if (reads > 1) {
          readbackStarted();
          await readGate;
          return json({
            ...settings,
            settings: { ...settings.settings, output_directory: "/submitted" },
          });
        }
        return json(settings);
      }),
    );
    render(<SettingsScreen />);
    const output = await screen.findByLabelText("Output directory");
    fireEvent.change(output, { target: { value: "/submitted" } });
    fireEvent.click(
      screen.getByRole("button", { name: "Сохранить публикацию" }),
    );
    await waitFor(() => expect(reads).toBe(1));
    fireEvent.change(output, { target: { value: "/srv/music" } });
    releaseSave();
    await readbackRequest;
    fireEvent.change(output, { target: { value: "/temporary-edit" } });
    fireEvent.change(output, { target: { value: "/srv/music" } });
    const readbackCompleted = nextResponseFor("/api/settings", "GET");
    releaseRead();
    await act(async () => {
      await readbackCompleted;
    });
    await screen.findByText("Настройки публикации сохранены.");
    await waitFor(() => expect(output).toHaveValue("/srv/music"));
    expect(
      screen.getByText("Есть несохранённые изменения публикации."),
    ).toBeInTheDocument();
  });

  it("serializes operation and save settings reads while preserving unrelated drafts", async () => {
    let reads = 0;
    let activeReads = 0;
    let maximumActiveReads = 0;
    let releaseOperationRefresh!: () => void;
    let operationRefreshStarted!: () => void;
    const operationRefreshGate = new Promise<void>((resolve) => {
      releaseOperationRefresh = resolve;
    });
    const operationRefreshRequest = new Promise<void>((resolve) => {
      operationRefreshStarted = resolve;
    });
    const save = vi.fn(() => new HttpResponse(null, { status: 204 }));
    let operationState = "running";
    let markOperationLoaded!: () => void;
    const operationLoaded = new Promise<void>((resolve) => {
      markOperationLoaded = resolve;
    });
    let markTerminalOperationsLoaded!: () => void;
    const terminalOperationsLoaded = new Promise<void>((resolve) => {
      markTerminalOperationsLoaded = resolve;
    });
    common({
      operations: [{ ...operation, kind: "install", state: "running" }],
    });
    server.use(
      http.put("/api/settings/log-level", save),
      http.get("/api/operations", () => {
        if (operationState === "succeeded") markTerminalOperationsLoaded();
        return json({
          operations:
            operationState === "running"
              ? [{ ...operation, kind: "install", state: "running" }]
              : [],
        });
      }),
      http.get("/api/settings", async () => {
        reads += 1;
        const read = reads;
        activeReads += 1;
        maximumActiveReads = Math.max(maximumActiveReads, activeReads);
        try {
          if (read === 2) {
            operationRefreshStarted();
            await operationRefreshGate;
          }
          return json({
            ...settings,
            settings: {
              ...settings.settings,
              output_directory: settings.settings.output_directory,
              log_level: read >= 3 ? "debug" : "info",
            },
          });
        } finally {
          activeReads -= 1;
        }
      }),
      http.get("/api/operations/:id", () => {
        const snapshot = json({
          ...operation,
          kind: "install",
          state: operationState,
        });
        if (operationState === "running") markOperationLoaded();
        return snapshot;
      }),
    );
    const initialOperationSnapshot = nextResponseFor(
      "/api/operations/op-1",
      "GET",
    );
    render(<SettingsScreen />);
    await screen.findByLabelText("Output directory");
    await operationLoaded;
    await initialOperationSnapshot;
    await screen.findByText(/install: running/);
    operationState = "succeeded";
    await act(async () => {
      TestEventSource.instances[0].dispatchEvent(
        new Event("operation-changed"),
      );
      await Promise.resolve();
    });
    await operationRefreshRequest;

    const output = screen.getByLabelText("Output directory");
    fireEvent.change(output, { target: { value: "/unsaved-output" } });
    fireEvent.change(screen.getByLabelText("Log level"), {
      target: { value: "debug" },
    });
    const saveResponse = nextResponseFor("/api/settings/log-level", "PUT");
    fireEvent.click(screen.getByRole("button", { name: "Применить уровень" }));
    await saveResponse;
    await waitFor(() => expect(save).toHaveBeenCalledTimes(1));
    await act(async () => {
      await Promise.resolve();
      releaseOperationRefresh();
      await terminalOperationsLoaded;
    });
    await waitFor(() => expect(reads).toBe(3));
    await waitFor(() =>
      expect(screen.queryByRole("group", { name: "Операция op-1" })).toBeNull(),
    );
    await screen.findByText("Уровень журнала применён.");

    expect(reads).toBe(3);
    expect(output).toHaveValue("/unsaved-output");
    expect(screen.getByLabelText("Log level")).toHaveValue("debug");
    expect(screen.getByText("Уровень журнала применён.")).toBeInTheDocument();
    expect(maximumActiveReads).toBe(1);
  });

  it("distinguishes an accepted mutation from a failed server readback", async () => {
    const put = vi.fn(() => new HttpResponse(null, { status: 204 }));
    let reads = 0;
    server.use(
      http.put("/api/settings/lrclib", put),
      http.get("/api/settings", () => {
        reads += 1;
        return reads === 1
          ? json(settings)
          : HttpResponse.json({ detail: "read failed" }, { status: 500 });
      }),
    );
    render(<SettingsScreen />);
    fireEvent.click(await screen.findByLabelText("LRCLIB включён"));
    fireEvent.click(screen.getByRole("button", { name: "Сохранить LRCLIB" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "приняты сервером",
    );
    expect(
      screen.queryByText("Настройка LRCLIB сохранена."),
    ).not.toBeInTheDocument();
    expect(put).toHaveBeenCalledTimes(1);
    expect(screen.getByLabelText("LRCLIB включён")).not.toBeChecked();
  });

  it("sends log-level change to backend and rereads it", async () => {
    const saved = vi.fn();
    let rereads = 0;
    server.use(
      http.put("/api/settings/log-level", async ({ request }) => {
        saved(await request.json());
        return new HttpResponse(null, { status: 204 });
      }),
      http.get("/api/settings", () => {
        rereads++;
        return json({
          ...settings,
          settings: {
            ...settings.settings,
            log_level: rereads > 1 ? "debug" : "info",
          },
        });
      }),
    );
    render(<SettingsScreen />);
    fireEvent.change(await screen.findByLabelText("Log level"), {
      target: { value: "debug" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Применить уровень" }));
    await waitFor(() => expect(saved).toHaveBeenCalledWith({ level: "debug" }));
    expect(await screen.findByText("Уровень журнала применён.")).toBeTruthy();
    expect(rereads).toBeGreaterThan(1);
    expect(screen.getByLabelText("Log level")).toHaveProperty("value", "debug");
  });

  it("observes 24-hour catalog cooldown on reload and always requests manual refresh", async () => {
    const timestamp = new Date(Date.now() - 60_000).toISOString();
    localStorage.setItem("melotrove.catalog-checked-at", timestamp);
    const catalogRequest = vi.fn(({ request }: { request: Request }) => {
      const kind = new URL(request.url).searchParams.get("package_kind");
      return json({
        package_kind: kind,
        releases: [
          { identity: `${kind}-manual`, source: "fixture", artifacts: [] },
        ],
      });
    });
    server.use(http.get("/api/tools/catalog", catalogRequest));
    render(<SettingsScreen />);
    expect(
      await screen.findByText(/Последняя успешная проверка каталога/),
    ).toBeTruthy();
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "Обновить состояние" }),
      ).not.toBeDisabled(),
    );
    const refreshed = nextResponse();
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Обновить каталог" }));
      await refreshed;
    });
    await screen.findByRole("option", { name: /ffmpeg-manual/ });
    expect(catalogRequest).toHaveBeenCalledTimes(2);
    expect(localStorage.getItem("melotrove.catalog-checked-at")).not.toBe(
      timestamp,
    );
  });

  it("makes one automatic check after cooldown and does not repeat on later state refresh", async () => {
    localStorage.setItem(
      "melotrove.catalog-checked-at",
      new Date(Date.now() - 25 * 60 * 60 * 1000).toISOString(),
    );
    const catalogRequest = vi.fn(({ request }: { request: Request }) => {
      const kind = new URL(request.url).searchParams.get("package_kind");
      return json({
        package_kind: kind,
        releases: [
          { identity: `${kind}-automatic`, source: "fixture", artifacts: [] },
        ],
      });
    });
    server.use(http.get("/api/tools/catalog", catalogRequest));
    render(<SettingsScreen />);
    await screen.findByText(/Последняя успешная проверка каталога/);
    await screen.findByRole("option", { name: /ffmpeg-automatic/ });
    expect(catalogRequest).toHaveBeenCalledTimes(2);
    const stateReload = nextResponseFor("/api/settings", "GET");
    await act(async () => {
      fireEvent.click(
        screen.getByRole("button", { name: "Обновить состояние" }),
      );
      await stateReload;
    });
    expect(catalogRequest).toHaveBeenCalledTimes(2);
  });

  it("installs without activating and confirms exactly the preflight conflict list", async () => {
    const installOperation = { ...operation, kind: "install" };
    const install = vi.fn(async ({ request }: { request: Request }) => {
      expect(await request.json()).toEqual({
        preflight_token: "token",
        confirmed_conflicts: ["/srv/tools/ffmpeg/new/ffmpeg"],
      });
      return json(installOperation);
    });
    server.use(
      http.get("/api/operations/:id", () => json(installOperation)),
      http.post("/api/tools/installations/preflight", () =>
        json({
          preflight_token: "token",
          targets: ["/srv/tools/ffmpeg/new/ffmpeg"],
          conflicts: ["/srv/tools/ffmpeg/new/ffmpeg"],
        }),
      ),
      http.post("/api/tools/installations", install),
    );
    render(<SettingsScreen />);
    await screen.findByRole("option", { name: /ffmpeg-release/ });
    fireEvent.change(screen.getByLabelText("Версия ffmpeg"), {
      target: { value: "ffmpeg-release" },
    });
    fireEvent.click(
      within(screen.getByRole("region", { name: "FFmpeg package" })).getByRole(
        "button",
        { name: "Установить без активации" },
      ),
    );
    expect(await screen.findByRole("dialog")).toHaveTextContent(
      "/srv/tools/ffmpeg/new/ffmpeg",
    );
    fireEvent.click(
      screen.getByRole("button", {
        name: "Подтвердить перечисленные конфликты",
      }),
    );
    await waitFor(() => expect(install).toHaveBeenCalled());
    expect(await screen.findByText(/install: queued/)).toBeTruthy();
    expect(screen.getByText("ff-1 — активна")).toBeTruthy();
  });

  it("keeps an installation preflight refusal on the screen and focuses its screen alert", async () => {
    server.use(
      http.post("/api/tools/installations/preflight", () =>
        HttpResponse.json(
          { detail: "Каталог установки недоступен" },
          { status: 409 },
        ),
      ),
    );
    render(<SettingsScreen />);
    await screen.findByRole("option", { name: /ffmpeg-release/ });
    fireEvent.change(screen.getByLabelText("Версия ffmpeg"), {
      target: { value: "ffmpeg-release" },
    });
    const trigger = within(
      screen.getByRole("region", { name: "FFmpeg package" }),
    ).getByRole("button", { name: "Установить без активации" });
    await waitFor(() => expect(trigger).not.toBeDisabled());
    fireEvent.click(trigger);

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Каталог установки недоступен");
    await waitFor(() => expect(alert).toHaveFocus());
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(alert.closest("dialog")).toBeNull();
  });

  it("shows move preflight errors and retries inside the move dialog", async () => {
    let preflightCalls = 0;
    server.use(
      http.post("/api/tools/move/preflight", () => {
        preflightCalls += 1;
        if (preflightCalls === 1)
          return HttpResponse.json(
            { detail: "Проверка переноса отклонена" },
            { status: 400 },
          );
        return json({
          preflight_token: "move-retry-token",
          conflicts: [],
          managed_file_count: 1,
        });
      }),
    );
    render(<SettingsScreen />);
    const trigger = await screen.findByRole("button", {
      name: "Перенести каталог",
    });
    await waitFor(() => expect(trigger).not.toBeDisabled());
    fireEvent.click(trigger);
    const dialog = screen.getByRole("dialog", {
      name: "Перенос Tools directory",
    });
    const path = within(dialog).getByLabelText("Новый Tools directory");
    fireEvent.change(path, { target: { value: "/srv/move-retry" } });
    fireEvent.click(
      within(dialog).getByRole("button", { name: "Проверить перенос" }),
    );

    const alert = await within(dialog).findByRole("alert");
    expect(alert).toHaveTextContent("Проверка переноса отклонена");
    expect(alert).toHaveFocus();
    expect(alert.closest("dialog")).toBe(dialog);
    expect(path).toHaveValue("/srv/move-retry");

    fireEvent.change(path, { target: { value: "/srv/move-fixed" } });
    fireEvent.click(
      within(dialog).getByRole("button", { name: "Проверить перенос" }),
    );
    expect(
      await within(dialog).findByText("Управляемых файлов: 1"),
    ).toBeInTheDocument();
    expect(within(dialog).queryByRole("alert")).not.toBeInTheDocument();
    expect(path).toHaveValue("/srv/move-fixed");
  });

  it("keeps a rejected download in its dialog, focuses it, and permits retry", async () => {
    let startCalls = 0;
    const installOperation = { ...operation, kind: "install" };
    server.use(
      http.get("/api/operations/:id", () => json(installOperation)),
      http.post("/api/tools/installations/preflight", () =>
        json({
          preflight_token: "download-token",
          targets: ["/srv/tools/ffmpeg/new/ffmpeg"],
          conflicts: ["/srv/tools/ffmpeg/new/ffmpeg"],
        }),
      ),
      http.post("/api/tools/installations", () => {
        startCalls += 1;
        if (startCalls === 1)
          return HttpResponse.json(
            { detail: "Загрузка отклонена" },
            { status: 409 },
          );
        return json(installOperation);
      }),
    );
    render(<SettingsScreen />);
    await screen.findByRole("option", { name: /ffmpeg-release/ });
    fireEvent.change(screen.getByLabelText("Версия ffmpeg"), {
      target: { value: "ffmpeg-release" },
    });
    const trigger = within(
      screen.getByRole("region", { name: "FFmpeg package" }),
    ).getByRole("button", { name: "Установить без активации" });
    await waitFor(() => expect(trigger).not.toBeDisabled());
    fireEvent.click(trigger);
    let dialog = await screen.findByRole("dialog", {
      name: /Подтверждение установки/,
    });
    fireEvent.click(
      within(dialog).getByRole("button", {
        name: "Подтвердить перечисленные конфликты",
      }),
    );

    const alert = await within(dialog).findByRole("alert");
    expect(alert).toHaveTextContent("Загрузка отклонена");
    expect(alert).toHaveFocus();
    expect(alert.closest("dialog")).toBe(dialog);
    expect(screen.queryByText("Загрузка отклонена")).toBe(alert);

    fireEvent.keyDown(dialog, { key: "Escape", code: "Escape" });
    await waitFor(() =>
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
    );
    expect(trigger).toHaveFocus();
    fireEvent.click(trigger);
    dialog = await screen.findByRole("dialog", {
      name: /Подтверждение установки/,
    });
    expect(within(dialog).queryByRole("alert")).not.toBeInTheDocument();
    fireEvent.click(
      within(dialog).getByRole("button", {
        name: "Подтвердить перечисленные конфликты",
      }),
    );
    await screen.findByRole("group", { name: "Операция op-1" });
    await waitFor(() =>
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
    );
    expect(trigger).toHaveFocus();
    expect(startCalls).toBe(2);
  });

  it("invalidates a refused move token, leaves inputs editable, and retries with a fresh token", async () => {
    let preflightCalls = 0;
    const submissions: unknown[] = [];
    server.use(
      http.post("/api/tools/move/preflight", () => {
        preflightCalls += 1;
        return json({
          preflight_token: `fresh-move-token-${preflightCalls}`,
          conflicts: ["/srv/new-tools/conflict"],
          managed_file_count: 2,
        });
      }),
      http.post("/api/tools/move", async ({ request }) => {
        submissions.push(await request.json());
        if (submissions.length === 1)
          return HttpResponse.json(
            { detail: "Срок проверки истёк" },
            { status: 409 },
          );
        return json(operation);
      }),
    );
    render(<SettingsScreen />);
    const trigger = await screen.findByRole("button", {
      name: "Перенести каталог",
    });
    await waitFor(() => expect(trigger).not.toBeDisabled());
    fireEvent.click(trigger);
    let dialog = screen.getByRole("dialog", {
      name: "Перенос Tools directory",
    });
    const path = within(dialog).getByLabelText("Новый Tools directory");
    fireEvent.change(path, { target: { value: "/srv/new-tools" } });
    fireEvent.click(
      within(dialog).getByRole("button", { name: "Проверить перенос" }),
    );
    await within(dialog).findByText("Управляемых файлов: 2");
    fireEvent.click(
      within(dialog).getByRole("button", { name: "Подтвердить перенос" }),
    );

    const alert = await within(dialog).findByRole("alert");
    expect(alert).toHaveTextContent("Срок проверки истёк");
    expect(alert).toHaveFocus();
    expect(alert.closest("dialog")).toBe(dialog);
    expect(path).toHaveValue("/srv/new-tools");
    expect(
      within(dialog).queryByRole("button", { name: "Подтвердить перенос" }),
    ).not.toBeInTheDocument();

    fireEvent.keyDown(dialog, { key: "Escape", code: "Escape" });
    await waitFor(() =>
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
    );
    expect(trigger).toHaveFocus();
    fireEvent.click(trigger);
    dialog = screen.getByRole("dialog", { name: "Перенос Tools directory" });
    const reopenedPath = within(dialog).getByLabelText("Новый Tools directory");
    expect(within(dialog).queryByRole("alert")).not.toBeInTheDocument();
    expect(reopenedPath).toHaveValue("/srv/new-tools");

    fireEvent.change(reopenedPath, {
      target: { value: "/srv/new-tools-fixed" },
    });
    fireEvent.click(
      within(dialog).getByRole("button", { name: "Проверить перенос" }),
    );
    await within(dialog).findByText("Управляемых файлов: 2");
    expect(within(dialog).queryByRole("alert")).not.toBeInTheDocument();
    fireEvent.click(
      within(dialog).getByRole("button", { name: "Подтвердить перенос" }),
    );
    await screen.findByRole("group", { name: "Операция op-1" });
    expect(submissions).toEqual([
      {
        preflight_token: "fresh-move-token-1",
        confirmed_conflicts: ["/srv/new-tools/conflict"],
      },
      {
        preflight_token: "fresh-move-token-2",
        confirmed_conflicts: ["/srv/new-tools/conflict"],
      },
    ]);
    expect(trigger).toHaveFocus();
  });

  it("ignores late install and move start refusals after closing and reopening their dialogs", async () => {
    let resolveInstallStart!: (response: Response) => void;
    let resolveMoveStart!: (response: Response) => void;
    let installPreflightCount = 0;
    let movePreflightCount = 0;
    server.use(
      http.get("/api/operations/:id", () => json(operation)),
      http.post("/api/tools/installations/preflight", () => {
        installPreflightCount += 1;
        return json({
          preflight_token: `install-late-${installPreflightCount}`,
          targets: ["/srv/tools/ffmpeg/new/ffmpeg"],
          conflicts: ["/srv/tools/ffmpeg/new/ffmpeg"],
        });
      }),
      http.post(
        "/api/tools/installations",
        () =>
          new Promise<Response>((resolve) => {
            resolveInstallStart = resolve;
          }),
      ),
      http.post("/api/tools/move/preflight", () => {
        movePreflightCount += 1;
        return json({
          preflight_token: `move-late-${movePreflightCount}`,
          conflicts: [],
          managed_file_count: 1,
        });
      }),
      http.post(
        "/api/tools/move",
        () =>
          new Promise<Response>((resolve) => {
            resolveMoveStart = resolve;
          }),
      ),
    );
    render(<SettingsScreen />);

    await screen.findByRole("option", { name: /ffmpeg-release/ });
    fireEvent.change(screen.getByLabelText("Версия ffmpeg"), {
      target: { value: "ffmpeg-release" },
    });
    const installTrigger = within(
      screen.getByRole("region", { name: "FFmpeg package" }),
    ).getByRole("button", { name: "Установить без активации" });
    await waitFor(() => expect(installTrigger).not.toBeDisabled());
    fireEvent.click(installTrigger);
    let installDialog = await screen.findByRole("dialog", {
      name: /Подтверждение установки/,
    });
    fireEvent.click(
      within(installDialog).getByRole("button", {
        name: "Подтвердить перечисленные конфликты",
      }),
    );
    await waitFor(() => expect(resolveInstallStart).toBeTypeOf("function"));
    fireEvent.keyDown(installDialog, { key: "Escape", code: "Escape" });
    await waitFor(() =>
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
    );
    await waitFor(() => expect(installTrigger).not.toBeDisabled());
    fireEvent.click(installTrigger);
    installDialog = await screen.findByRole("dialog", {
      name: /Подтверждение установки/,
    });
    await act(async () => {
      resolveInstallStart(
        HttpResponse.json(
          { detail: "Поздний отказ загрузки" },
          { status: 409 },
        ),
      );
    });
    expect(
      screen.getByRole("dialog", { name: /Подтверждение установки/ }),
    ).toBeInTheDocument();
    expect(within(installDialog).queryByRole("alert")).not.toBeInTheDocument();
    expect(installDialog.contains(document.activeElement)).toBe(true);

    fireEvent.keyDown(installDialog, { key: "Escape", code: "Escape" });
    await waitFor(() =>
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
    );
    const moveTrigger = await screen.findByRole("button", {
      name: "Перенести каталог",
    });
    await waitFor(() => expect(moveTrigger).not.toBeDisabled());
    fireEvent.click(moveTrigger);
    let moveDialog = screen.getByRole("dialog", {
      name: "Перенос Tools directory",
    });
    fireEvent.click(
      within(moveDialog).getByRole("button", { name: "Проверить перенос" }),
    );
    await within(moveDialog).findByText("Управляемых файлов: 1");
    fireEvent.click(
      within(moveDialog).getByRole("button", { name: "Подтвердить перенос" }),
    );
    await waitFor(() => expect(resolveMoveStart).toBeTypeOf("function"));
    fireEvent.keyDown(moveDialog, { key: "Escape", code: "Escape" });
    await waitFor(() =>
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
    );
    await waitFor(() => expect(moveTrigger).not.toBeDisabled());
    fireEvent.click(moveTrigger);
    moveDialog = screen.getByRole("dialog", {
      name: "Перенос Tools directory",
    });
    await act(async () => {
      resolveMoveStart(
        HttpResponse.json(
          { detail: "Поздний отказ переноса" },
          { status: 409 },
        ),
      );
    });
    expect(
      screen.getByRole("dialog", { name: "Перенос Tools directory" }),
    ).toBeInTheDocument();
    expect(within(moveDialog).queryByRole("alert")).not.toBeInTheDocument();
    expect(
      within(moveDialog).getByRole("button", { name: "Проверить перенос" }),
    ).toBeInTheDocument();
    expect(moveDialog.contains(document.activeElement)).toBe(true);
  });

  it("registers late successful install starts without closing a newer dialog or clearing its busy state", async () => {
    const installOperation = (id: string) => ({
      ...operation,
      id,
      kind: "install",
    });
    const resolveStarts: Array<(response: Response) => void> = [];
    server.use(
      http.get("/api/operations/:id", ({ params }) =>
        json({ ...operation, id: String(params.id) }),
      ),
      http.post("/api/tools/installations/preflight", () =>
        json({
          preflight_token: "install-late-success",
          targets: ["/srv/tools/ffmpeg/new/ffmpeg"],
          conflicts: ["/srv/tools/ffmpeg/new/ffmpeg"],
        }),
      ),
      http.post(
        "/api/tools/installations",
        () =>
          new Promise<Response>((resolve) => {
            resolveStarts.push(resolve);
          }),
      ),
    );
    render(<SettingsScreen />);

    await screen.findByRole("option", { name: /ffmpeg-release/ });
    fireEvent.change(screen.getByLabelText("Версия ffmpeg"), {
      target: { value: "ffmpeg-release" },
    });
    const trigger = within(
      screen.getByRole("region", { name: "FFmpeg package" }),
    ).getByRole("button", { name: "Установить без активации" });
    await waitFor(() => expect(trigger).not.toBeDisabled());
    fireEvent.click(trigger);
    let dialog = await screen.findByRole("dialog", {
      name: /Подтверждение установки/,
    });
    const confirm = () =>
      within(dialog).getByRole("button", {
        name: "Подтвердить перечисленные конфликты",
      });
    fireEvent.click(confirm());
    await waitFor(() => expect(resolveStarts).toHaveLength(1));

    fireEvent.keyDown(dialog, { key: "Escape", code: "Escape" });
    await waitFor(() =>
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
    );
    fireEvent.click(trigger);
    dialog = await screen.findByRole("dialog", {
      name: /Подтверждение установки/,
    });
    fireEvent.click(confirm());
    await waitFor(() => expect(resolveStarts).toHaveLength(2));

    await act(async () => {
      resolveStarts[0](json(installOperation("install-old")));
    });
    expect(
      screen.getByRole("group", { name: "Операция install-old" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("dialog", { name: /Подтверждение установки/ }),
    ).toBe(dialog);
    expect(confirm()).toBeDisabled();
    expect(dialog.contains(document.activeElement)).toBe(true);

    await act(async () => {
      resolveStarts[1](json(installOperation("install-new")));
    });
    expect(
      screen.getByRole("group", { name: "Операция install-new" }),
    ).toBeInTheDocument();
    await waitFor(() =>
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
    );
  });

  it("registers late successful move starts without closing a newer dialog or clearing its busy state", async () => {
    const moveOperation = (id: string) => ({
      ...operation,
      id,
      kind: "move_tools_root",
    });
    const resolveStarts: Array<(response: Response) => void> = [];
    server.use(
      http.get("/api/operations/:id", ({ params }) =>
        json({ ...operation, id: String(params.id) }),
      ),
      http.post("/api/tools/move/preflight", () =>
        json({
          preflight_token: "move-late-success",
          conflicts: [],
          managed_file_count: 1,
        }),
      ),
      http.post(
        "/api/tools/move",
        () =>
          new Promise<Response>((resolve) => {
            resolveStarts.push(resolve);
          }),
      ),
    );
    render(<SettingsScreen />);

    const trigger = await screen.findByRole("button", {
      name: "Перенести каталог",
    });
    await waitFor(() => expect(trigger).not.toBeDisabled());
    fireEvent.click(trigger);
    let dialog = screen.getByRole("dialog", {
      name: "Перенос Tools directory",
    });
    const confirm = () =>
      within(dialog).getByRole("button", { name: "Подтвердить перенос" });
    fireEvent.click(
      within(dialog).getByRole("button", { name: "Проверить перенос" }),
    );
    await within(dialog).findByText("Управляемых файлов: 1");
    fireEvent.click(confirm());
    await waitFor(() => expect(resolveStarts).toHaveLength(1));

    fireEvent.keyDown(dialog, { key: "Escape", code: "Escape" });
    await waitFor(() =>
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
    );
    await waitFor(() => expect(trigger).not.toBeDisabled());
    fireEvent.click(trigger);
    dialog = screen.getByRole("dialog", { name: "Перенос Tools directory" });
    fireEvent.click(
      within(dialog).getByRole("button", { name: "Проверить перенос" }),
    );
    await within(dialog).findByText("Управляемых файлов: 1");
    fireEvent.click(confirm());
    await waitFor(() => expect(resolveStarts).toHaveLength(2));

    await act(async () => {
      resolveStarts[0](json(moveOperation("move-old")));
    });
    expect(
      screen.getByRole("group", { name: "Операция move-old" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("dialog", { name: "Перенос Tools directory" }),
    ).toBe(dialog);
    expect(confirm()).toBeDisabled();
    expect(dialog.contains(document.activeElement)).toBe(true);

    await act(async () => {
      resolveStarts[1](json(moveOperation("move-new")));
    });
    expect(
      screen.getByRole("group", { name: "Операция move-new" }),
    ).toBeInTheDocument();
    await waitFor(() =>
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
    );
  });

  it("rolls back activation UI when the server rejects re-verification", async () => {
    server.use(
      http.post("/api/tools/installations/ff-old/activate", () =>
        HttpResponse.json(
          { detail: "Проверка версии не прошла" },
          { status: 422 },
        ),
      ),
    );
    render(<SettingsScreen />);
    const activateButton = await screen.findByRole("button", {
      name: "Активировать ff-0",
    });
    await waitFor(() => expect(activateButton).not.toBeDisabled());
    fireEvent.click(activateButton);
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Проверка версии не прошла",
    );
    expect(screen.getByText("ff-1 — активна")).toBeTruthy();
  });

  it("rejects active and operation-busy deletion", async () => {
    const occupiedOperation = {
      ...operation,
      target_installation_id: "ff-old",
    };
    common({ operations: [occupiedOperation] });
    const remove = vi.fn();
    server.use(
      http.delete("/api/tools/installations/:id", remove),
      http.get("/api/operations/:id", () => json(occupiedOperation)),
    );
    render(<SettingsScreen />);
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "Обновить состояние" }),
      ).not.toBeDisabled(),
    );
    const activeDelete = screen.getByRole("button", { name: "Удалить ff-1" });
    expect(activeDelete).toBeDisabled();
    expect(
      screen.getByRole("button", { name: "Активировать ff-1" }),
    ).toBeDisabled();
    await screen.findByRole("group", { name: "Операция op-1" });
    expect(screen.getByRole("button", { name: "Удалить ff-0" })).toBeDisabled();
    expect(remove).not.toHaveBeenCalled();
  });

  it("allows deleting a failed, inactive, unoccupied installation", async () => {
    common({ installed: [activeFF, failedFF, activeFP] });
    const deleted = vi.fn();
    server.use(
      http.delete("/api/tools/installations/ff-failed", async ({ request }) => {
        deleted(await request.json());
        return new HttpResponse(null, { status: 204 });
      }),
    );
    render(<SettingsScreen />);
    const remove = await screen.findByRole("button", {
      name: "Удалить ff-failed",
    });
    await waitFor(() => expect(remove).not.toBeDisabled());
    fireEvent.click(remove);
    await waitFor(() =>
      expect(deleted).toHaveBeenCalledWith({ package_kind: "ffmpeg" }),
    );
  });

  it.each([
    "queued",
    "running",
    "failed",
  ] as const)("keeps deleting a failed installation disabled while operation is %s", async (state) => {
    const occupied = {
      ...operation,
      state,
      target_installation_id: failedFF.id,
    };
    common({
      installed: [activeFF, failedFF, activeFP],
      operations: [occupied],
    });
    server.use(http.get("/api/operations/:id", () => json(occupied)));
    render(<SettingsScreen />);
    await screen.findByRole("group", { name: "Операция op-1" });
    expect(
      screen.getByRole("button", { name: "Удалить ff-failed" }),
    ).toBeDisabled();
  });

  it("submits a move and surfaces a rejected start request", async () => {
    const starts = vi
      .fn()
      .mockResolvedValueOnce(json(operation))
      .mockResolvedValueOnce(
        HttpResponse.json({ detail: "Перенос отклонён" }, { status: 409 }),
      );
    server.use(
      http.post("/api/tools/move/preflight", () =>
        json({
          preflight_token: "move-token",
          conflicts: [],
          managed_file_count: 2,
        }),
      ),
      http.post("/api/tools/move", starts),
    );
    render(<SettingsScreen />);
    const moveButton = await screen.findByRole("button", {
      name: "Перенести каталог",
    });
    await waitFor(() => expect(moveButton).not.toBeDisabled());
    fireEvent.click(moveButton);
    fireEvent.change(screen.getByLabelText("Новый Tools directory"), {
      target: { value: "/srv/new-tools" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Проверить перенос" }));
    await screen.findByText("Управляемых файлов: 2");
    const confirmMoveButton = screen.getByRole("button", {
      name: "Подтвердить перенос",
    });
    await waitFor(() => expect(confirmMoveButton).not.toBeDisabled());
    fireEvent.click(confirmMoveButton);
    await screen.findByRole("group", { name: "Операция op-1" });
    expect(
      screen.getByText("Текущий Tools directory:").parentElement,
    ).toHaveTextContent("/srv/tools");
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "Перенести каталог" }),
      ).not.toBeDisabled(),
    );
    fireEvent.click(screen.getByRole("button", { name: "Перенести каталог" }));
    fireEvent.click(screen.getByRole("button", { name: "Проверить перенос" }));
    await screen.findByText("Управляемых файлов: 2");
    const failedMoveConfirm = screen.getByRole("button", {
      name: "Подтвердить перенос",
    });
    await waitFor(() => expect(failedMoveConfirm).not.toBeDisabled());
    fireEvent.click(failedMoveConfirm);
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Перенос отклонён",
    );
    expect(
      screen.queryByRole("button", { name: "Подтвердить перенос" }),
    ).not.toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Проверить перенос" }),
    ).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Проверить перенос" }));
    expect(
      await screen.findByText("Управляемых файлов: 2"),
    ).toBeInTheDocument();
    expect(
      screen.getByText("Текущий Tools directory:").parentElement,
    ).toHaveTextContent("/srv/tools");
  });

  it("invalidates a move token on every input edit, including removeOld true to false and reverting the path", async () => {
    const preflights: Array<Record<string, unknown>> = [];
    const submissions: unknown[] = [];
    const starts = vi.fn(async ({ request }: { request: Request }) => {
      submissions.push(await request.json());
      return json(operation);
    });
    server.use(
      http.post("/api/tools/move/preflight", async ({ request }) => {
        preflights.push((await request.json()) as Record<string, unknown>);
        return json({
          preflight_token: `move-token-${preflights.length}`,
          conflicts: ["/srv/new-tools/conflict"],
          managed_file_count: 3,
        });
      }),
      http.post("/api/tools/move", starts),
    );
    render(<SettingsScreen />);
    const trigger = await screen.findByRole("button", {
      name: "Перенести каталог",
    });
    await waitFor(() => expect(trigger).not.toBeDisabled());
    fireEvent.click(trigger);
    const dialog = screen.getByRole("dialog", {
      name: "Перенос Tools directory",
    });
    const path = within(dialog).getByLabelText("Новый Tools directory");
    const removeOld = within(dialog).getByRole("checkbox");
    fireEvent.change(path, { target: { value: "/srv/new-tools" } });
    fireEvent.click(removeOld);
    fireEvent.click(
      within(dialog).getByRole("button", { name: "Проверить перенос" }),
    );
    await screen.findByText("Управляемых файлов: 3");
    expect(dialog).toHaveTextContent("Исходный каталог: /srv/tools");
    expect(dialog).toHaveTextContent("Каталог назначения: /srv/new-tools");
    expect(dialog).toHaveTextContent("Удалить прежние управляемые файлы: Да");
    expect(dialog).toHaveTextContent("Конфликтов: 1");
    expect(dialog).toHaveTextContent("/srv/new-tools/conflict");

    // The dangerous true -> false edit invalidates immediately; toggling back
    // cannot resurrect the first token.
    fireEvent.click(removeOld);
    expect(
      screen.queryByRole("button", { name: "Подтвердить перенос" }),
    ).not.toBeInTheDocument();
    fireEvent.click(removeOld);
    expect(
      screen.queryByRole("button", { name: "Подтвердить перенос" }),
    ).not.toBeInTheDocument();

    // Editing the path and reverting it also requires a brand-new preflight.
    fireEvent.change(path, { target: { value: "/srv/other-tools" } });
    fireEvent.change(path, { target: { value: "/srv/new-tools" } });
    expect(
      screen.queryByRole("button", { name: "Подтвердить перенос" }),
    ).not.toBeInTheDocument();

    fireEvent.click(
      within(dialog).getByRole("button", { name: "Проверить перенос" }),
    );
    await waitFor(() => expect(preflights).toHaveLength(2));
    await screen.findByText("Управляемых файлов: 3");
    expect(preflights[1]).toEqual({
      new_tools_directory: "/srv/new-tools",
      remove_old_files: true,
    });
    fireEvent.click(within(dialog).getByRole("checkbox"));
    expect(
      screen.queryByRole("button", { name: "Подтвердить перенос" }),
    ).not.toBeInTheDocument();
    fireEvent.click(
      within(dialog).getByRole("button", { name: "Проверить перенос" }),
    );
    await waitFor(() => expect(preflights).toHaveLength(3));
    await screen.findByText("Управляемых файлов: 3");
    expect(preflights[2]).toEqual({
      new_tools_directory: "/srv/new-tools",
      remove_old_files: false,
    });
    fireEvent.click(
      screen.getByRole("button", { name: "Подтвердить перенос" }),
    );
    await waitFor(() => expect(starts).toHaveBeenCalledTimes(1));
    expect(submissions).toEqual([
      {
        preflight_token: "move-token-3",
        confirmed_conflicts: ["/srv/new-tools/conflict"],
      },
    ]);
    expect(preflights[0]).toEqual({
      new_tools_directory: "/srv/new-tools",
      remove_old_files: true,
    });
  });

  it("ignores move preflight responses from edited inputs, older requests, and closed dialog sessions", async () => {
    const requests: Array<{
      body: Record<string, unknown>;
      respond: (response: Response) => void;
    }> = [];
    server.use(
      http.post("/api/tools/move/preflight", async ({ request }) => {
        const body = (await request.json()) as Record<string, unknown>;
        return await new Promise<Response>((resolve) => {
          requests.push({ body, respond: resolve });
        });
      }),
    );
    render(<SettingsScreen />);
    const trigger = await screen.findByRole("button", {
      name: "Перенести каталог",
    });
    await waitFor(() => expect(trigger).not.toBeDisabled());
    fireEvent.click(trigger);
    let dialog = screen.getByRole("dialog", {
      name: "Перенос Tools directory",
    });
    const path = within(dialog).getByLabelText("Новый Tools directory");
    fireEvent.change(path, { target: { value: "/srv/first" } });
    fireEvent.click(
      within(dialog).getByRole("button", { name: "Проверить перенос" }),
    );
    await waitFor(() => expect(requests).toHaveLength(1));

    fireEvent.change(path, { target: { value: "/srv/second" } });
    fireEvent.click(
      within(dialog).getByRole("button", { name: "Проверить перенос" }),
    );
    await waitFor(() => expect(requests).toHaveLength(2));
    expect(requests.map(({ body }) => body.new_tools_directory)).toEqual([
      "/srv/first",
      "/srv/second",
    ]);

    await act(async () => {
      requests[1].respond(
        json({
          preflight_token: "new-token",
          conflicts: [],
          managed_file_count: 2,
        }),
      );
    });
    expect(
      await screen.findByText("Управляемых файлов: 2"),
    ).toBeInTheDocument();
    await act(async () => {
      requests[0].respond(
        json({
          preflight_token: "old-token",
          conflicts: [],
          managed_file_count: 99,
        }),
      );
    });
    expect(screen.getByText("Управляемых файлов: 2")).toBeInTheDocument();
    expect(
      screen.queryByText("Управляемых файлов: 99"),
    ).not.toBeInTheDocument();

    // A valid response from a prior opening must not populate a reopened dialog.
    fireEvent.change(path, { target: { value: "/srv/third" } });
    fireEvent.click(
      within(dialog).getByRole("button", { name: "Проверить перенос" }),
    );
    await waitFor(() => expect(requests).toHaveLength(3));
    fireEvent.keyDown(dialog, { key: "Escape", code: "Escape" });
    await waitFor(() =>
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
    );
    await waitFor(() => expect(trigger).not.toBeDisabled());
    fireEvent.click(trigger);
    dialog = screen.getByRole("dialog", { name: "Перенос Tools directory" });
    await act(async () => {
      requests[2].respond(
        json({
          preflight_token: "closed-token",
          conflicts: [],
          managed_file_count: 88,
        }),
      );
    });
    expect(
      screen.queryByText("Управляемых файлов: 88"),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Подтвердить перенос" }),
    ).not.toBeInTheDocument();
    expect(within(dialog).getByLabelText("Новый Tools directory")).toHaveValue(
      "/srv/third",
    );
  });

  it("refreshes Settings and inventories after a move operation succeeds", async () => {
    const queued = { ...operation, id: "move-success" };
    let snapshot = queued;
    let moveStarted = false;
    let toolsDirectory = "/srv/tools";
    let settingsReads = 0;
    const installationReads = { ffmpeg: 0, fpcalc: 0 };
    let operationReads = 0;
    server.use(
      http.get("/api/settings", () => {
        settingsReads++;
        return json({
          ...settings,
          settings: { ...settings.settings, tools_directory: toolsDirectory },
        });
      }),
      http.get("/api/tools/installations", ({ request }) => {
        const kind = new URL(request.url).searchParams.get("package_kind") as
          | "ffmpeg"
          | "fpcalc";
        installationReads[kind]++;
        return json({
          installations: kind === "ffmpeg" ? [activeFF] : [activeFP],
        });
      }),
      http.get("/api/operations", () => {
        operationReads++;
        return json({ operations: moveStarted ? [snapshot] : [] });
      }),
      http.get("/api/operations/move-success", () => json(snapshot)),
      http.post("/api/tools/move/preflight", () =>
        json({
          preflight_token: "success-token",
          conflicts: [],
          managed_file_count: 2,
        }),
      ),
      http.post("/api/tools/move", () => {
        moveStarted = true;
        return json(queued);
      }),
    );
    render(<SettingsScreen />);
    const moveButton = await screen.findByRole("button", {
      name: "Перенести каталог",
    });
    await waitFor(() => expect(moveButton).not.toBeDisabled());
    fireEvent.click(moveButton);
    fireEvent.change(screen.getByLabelText("Новый Tools directory"), {
      target: { value: "/srv/new-tools" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Проверить перенос" }));
    await screen.findByText("Управляемых файлов: 2");
    const confirm = screen.getByRole("button", { name: "Подтвердить перенос" });
    await waitFor(() => expect(confirm).not.toBeDisabled());
    fireEvent.click(confirm);
    expect(await screen.findByText(/move: queued/)).toBeTruthy();
    expect(
      screen.getByText("Текущий Tools directory:").parentElement,
    ).toHaveTextContent("/srv/tools");
    const stream = TestEventSource.instances.find((item) =>
      item.url.includes("move-success"),
    );
    expect(stream).toBeTruthy();

    toolsDirectory = "/srv/new-tools";
    snapshot = {
      ...queued,
      state: "succeeded",
      stage: "complete",
      bytes_completed: 10,
    };
    const rereads = nextResponsesFor([
      { path: "/api/operations/move-success", method: "GET" },
      { path: "/api/settings", method: "GET" },
      {
        path: "/api/tools/installations",
        method: "GET",
        packageKind: "ffmpeg",
      },
      {
        path: "/api/tools/installations",
        method: "GET",
        packageKind: "fpcalc",
      },
      { path: "/api/operations", method: "GET" },
    ]);
    await act(async () => {
      stream?.dispatchEvent(new Event("open"));
      await rereads;
    });
    const updatedRoot = await screen.findByText("Текущий Tools directory:");
    expect(updatedRoot.parentElement).toHaveTextContent("/srv/new-tools");
    expect(settingsReads).toBe(2);
    expect(installationReads).toEqual({ ffmpeg: 2, fpcalc: 2 });
    expect(operationReads).toBe(2);
    expect(screen.getByText("ff-1 — активна")).toBeTruthy();
    expect(screen.getByText("fp-1 — активна")).toBeTruthy();
  });

  it("preserves tools root and active installations after a move worker failure", async () => {
    const queued = { ...operation, id: "move-failure" };
    let snapshot: OperationResponse = queued;
    server.use(
      http.post("/api/tools/move/preflight", () =>
        json({
          preflight_token: "failure-token",
          conflicts: [],
          managed_file_count: 2,
        }),
      ),
      http.post("/api/tools/move", () => json(queued)),
      http.get("/api/operations/move-failure", () => json(snapshot)),
    );
    render(<SettingsScreen />);
    const moveButton = await screen.findByRole("button", {
      name: "Перенести каталог",
    });
    await waitFor(() => expect(moveButton).not.toBeDisabled());
    fireEvent.click(moveButton);
    fireEvent.change(screen.getByLabelText("Новый Tools directory"), {
      target: { value: "/srv/new-tools" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Проверить перенос" }));
    await screen.findByText("Управляемых файлов: 2");
    const confirm = screen.getByRole("button", { name: "Подтвердить перенос" });
    await waitFor(() => expect(confirm).not.toBeDisabled());
    fireEvent.click(confirm);
    expect(await screen.findByText(/move: queued/)).toBeTruthy();
    const stream = TestEventSource.instances.find((item) =>
      item.url.includes("move-failure"),
    );
    expect(stream).toBeTruthy();

    snapshot = {
      ...queued,
      state: "failed",
      safe_error: "Worker could not verify copied files",
    };
    const failureRead = nextResponseFor("/api/operations/move-failure", "GET");
    await act(async () => {
      stream?.dispatchEvent(new Event("open"));
      await failureRead;
    });
    expect(
      await screen.findByText(
        /move: failed.*Worker could not verify copied files/,
      ),
    ).toBeTruthy();
    const operationGroup = screen.getByRole("group", {
      name: "Операция move-failure",
    });
    expect(within(operationGroup).getByRole("status")).toHaveTextContent(
      "Worker could not verify copied files",
    );
    expect(
      within(operationGroup).getByRole("button", {
        name: "Повторить операцию move-failure",
      }),
    ).toBeTruthy();
    expect(
      within(operationGroup).getByRole("button", {
        name: "Закрыть ошибку move-failure",
      }),
    ).toBeTruthy();
    expect(
      screen.getByText("Текущий Tools directory:").parentElement,
    ).toHaveTextContent("/srv/tools");
    expect(screen.getByText("ff-1 — активна")).toBeTruthy();
    expect(screen.getByText("fp-1 — активна")).toBeTruthy();
  });

  it("recovers operation snapshots on SSE wake-up and exposes Retry and Dismiss", async () => {
    const failed = { ...operation, state: "failed", safe_error: "disk full" };
    common({ operations: [failed] });
    let reads = 0;
    const retry = vi.fn();
    const dismiss = vi.fn();
    server.use(
      http.get("/api/operations/:id", () => {
        reads++;
        return json(failed);
      }),
      http.post("/api/operations/:id/retry", () => {
        retry();
        return json(failed);
      }),
      http.delete("/api/operations/:id", () => {
        dismiss();
        return new HttpResponse(null, { status: 204 });
      }),
    );
    render(<SettingsScreen />);
    await screen.findByRole("button", { name: "Повторить операцию op-1" });
    const stream = await waitFor(() => {
      const found = TestEventSource.instances.find((item) =>
        item.url.includes("op-1"),
      );
      expect(found).toBeTruthy();
      return found as TestEventSource;
    });
    const woke = nextResponseFor("/api/operations/op-1", "GET");
    await act(async () => {
      stream?.dispatchEvent(new Event("open"));
      await woke;
    });
    expect(reads).toBeGreaterThan(1);
    const readsBeforeError = reads;
    const reconnected = nextResponseFor("/api/operations/op-1", "GET");
    await act(async () => {
      stream?.dispatchEvent(new Event("error"));
      await reconnected;
    });
    expect(reads).toBeGreaterThan(readsBeforeError);
    const retried = nextResponseFor("/api/operations/op-1/retry", "POST");
    await act(async () => {
      fireEvent.click(
        screen.getByRole("button", { name: "Повторить операцию op-1" }),
      );
      await retried;
    });
    expect(retry).toHaveBeenCalled();
    fireEvent.click(
      screen.getByRole("button", { name: "Закрыть ошибку op-1" }),
    );
    await waitFor(() => expect(dismiss).toHaveBeenCalled());
  });

  it("shows health degradation without redirect and leaves unrelated settings operable", async () => {
    common({ healthy: false });
    render(<SettingsScreen />);
    expect(await screen.findByText("FFmpeg недоступен")).toBeTruthy();
    expect(window.location.hash).not.toBe("#/setup");
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "Сохранить публикацию" }),
      ).not.toBeDisabled(),
    );
  });

  it("exposes accessible section names, loading, errors and disabled platform actions", async () => {
    common();
    server.use(
      http.get("/api/settings", () =>
        json({
          ...settings,
          platform: {
            ...settings.platform,
            supported: false,
            reason: "unsupported",
          },
        }),
      ),
    );
    render(<SettingsScreen />);
    expect(
      await screen.findByRole("heading", { name: "Настройки" }),
    ).toHaveFocus();
    expect(
      screen.getByRole("region", { name: "Состояние конфигурации" }),
    ).toBeTruthy();
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "Обновить состояние" }),
      ).not.toBeDisabled(),
    );
    const toolsRoot = screen.getByRole("region", {
      name: "Каталог инструментов",
    });
    expect(
      within(toolsRoot).getByRole("button", { name: "Перенести каталог" }),
    ).toBeDisabled();
    expect(screen.getByRole("alert")).toHaveTextContent("Платформа недоступна");
  });
});
