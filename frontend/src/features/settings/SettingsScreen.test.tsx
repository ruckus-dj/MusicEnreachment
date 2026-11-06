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
import { createMemoryStorage } from "../../test/memoryStorage";
import { server } from "../../test/server";
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
    publication_path_template:
      '{ALBUM_PRIMARY_ARTIST} [{ALBUM_PRIMARY_ARTIST_MBID_PART | ALBUM_PRIMARY_ARTIST_ID}]/{ALBUM_TYPE | "Unclassified"}/{ALBUM_YEAR | "Unknown year"} - {ALBUM_NAME} [{ALBUM_MBID_PART | ALBUM_ID}]/{DISC_NUMBER:02}-{TRACK_NUMBER:02} - {TRACK_NAME}.{EXT}',
    publication_remove_sources: false,
    source_file_concurrency: 4,
    musicbrainz_mode: "public",
    musicbrainz_base_url: "",
    musicbrainz_self_hosted_throttle: false,
    musicbrainz_self_hosted_delay_seconds: 0.5,
    musicbrainz_verified_at: "2026-09-01T00:00:00Z",
    lrclib_enabled: true,
    sha256_enabled: true,
    log_level: "info",
    active_ffmpeg_installation_id: "ff-active",
    active_fpcalc_installation_id: "fp-active",
    output_case_sensitive: true,
    output_unicode_normalization: "none",
    has_acoustid_application_key: false,
    has_github_token: false,
  },
};
const activeFF = {
  id: "ff-active",
  package_kind: "ffmpeg",
  version: "ffmpeg 8.0 + ffprobe 8.0",
  source_name: "fixture",
  state: "ready",
  active: true,
  created_at: "2026-01-01T00:00:00Z",
};
const inactiveFF = {
  ...activeFF,
  id: "ff-old",
  version: "ffmpeg 7.0 + ffprobe 7.0",
  active: false,
};
const activeFP = {
  ...activeFF,
  id: "fp-active",
  package_kind: "fpcalc",
  version: "1.6.0",
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
  Object.defineProperty(window.HTMLDialogElement.prototype, "showModal", {
    configurable: true,
    value(this: HTMLDialogElement) {
      this.setAttribute("open", "");
      this.querySelector<HTMLElement>(
        "button:not(:disabled), input:not(:disabled), select:not(:disabled), textarea:not(:disabled)",
      )?.focus();
    },
  });
  Object.defineProperty(window.HTMLDialogElement.prototype, "close", {
    configurable: true,
    value(this: HTMLDialogElement) {
      this.removeAttribute("open");
      this.dispatchEvent(new Event("close"));
    },
  });
  vi.stubGlobal("localStorage", createMemoryStorage());
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

describe("SettingsScreen", () => {
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
    const response = nextResponseFor("/api/settings/sha256", "PUT");
    fireEvent.click(toggle);
    await waitFor(() => expect(saved).toHaveBeenCalledTimes(1));
    expect(screen.getByText("Выполняется запрос…")).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Сохранить SHA-256" }),
    ).not.toBeInTheDocument();
    releaseSave();
    await response;
    await waitFor(() => expect(saved).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(reads).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(toggle).not.toBeChecked());
    expect(
      screen.getByText("Настройка SHA-256 сохранена."),
    ).toBeInTheDocument();
  });

  it("autosaves publication format and log level without saving an unrelated concurrency draft", async () => {
    let publicationFormat = "mka";
    let logLevel = "info";
    const runtimeWrites = vi.fn(async ({ request }: { request: Request }) => {
      const body = (await request.json()) as { publication_format: string };
      expect(body).toEqual({ publication_format: "m4a" });
      publicationFormat = body.publication_format;
      return new HttpResponse(null, { status: 204 });
    });
    const logWrites = vi.fn(async ({ request }: { request: Request }) => {
      const body = (await request.json()) as { level: string };
      expect(body).toEqual({ level: "debug" });
      logLevel = body.level;
      return new HttpResponse(null, { status: 204 });
    });
    server.use(
      http.put("/api/settings/runtime", runtimeWrites),
      http.put("/api/settings/log-level", logWrites),
      http.get("/api/settings", () =>
        json({
          ...settings,
          settings: {
            ...settings.settings,
            publication_format: publicationFormat,
            log_level: logLevel,
          },
        }),
      ),
    );
    render(<SettingsScreen />);
    const concurrency = await screen.findByLabelText("Одновременные файлы");
    fireEvent.change(concurrency, { target: { value: "8" } });

    fireEvent.click(screen.getByRole("button", { name: /Publication format/ }));
    fireEvent.click(await screen.findByRole("option", { name: "M4A" }));
    await waitFor(() => expect(runtimeWrites).toHaveBeenCalledTimes(1));
    expect(
      screen.getByText(/M4A сохраняет AAC и ALAC без перекодирования/),
    ).toHaveAttribute("role", "note");
    expect(concurrency).toHaveValue(8);
    const shaCheckbox = screen.getByLabelText("Вычислять SHA-256");
    expect(shaCheckbox).toHaveAttribute("aria-describedby", "sha256-help");
    expect(
      screen.getByText(/Настройка применяется к последующим анализам/),
    ).toBeInTheDocument();
    expect(concurrency).toHaveAttribute(
      "aria-describedby",
      "source-file-concurrency-help",
    );
    expect(
      screen.getByText(/Положительное целое число файлов/),
    ).toBeInTheDocument();
    expect(
      screen.getByText("Есть несохранённое изменение параллельности."),
    ).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: /Log level/ }));
    fireEvent.click(await screen.findByRole("option", { name: "debug" }));
    await waitFor(() => expect(logWrites).toHaveBeenCalledTimes(1));
    expect(runtimeWrites).toHaveBeenCalledTimes(1);
    expect(logWrites).toHaveBeenCalledTimes(1);
  });

  it("retries an explicit false SHA-256 value without requiring a different toggle selection", async () => {
    let fail = true;
    let enabled = true;
    const bodies: unknown[] = [];
    const writes = vi.fn(async ({ request }: { request: Request }) => {
      const body = (await request.json()) as { enabled: boolean };
      bodies.push(body);
      if (fail)
        return HttpResponse.json(
          { message: "temporarily unavailable" },
          { status: 500 },
        );
      enabled = body.enabled;
      return new HttpResponse(null, { status: 204 });
    });
    server.use(
      http.put("/api/settings/sha256", writes),
      http.get("/api/settings", () =>
        json({
          ...settings,
          settings: { ...settings.settings, sha256_enabled: enabled },
        }),
      ),
    );
    render(<SettingsScreen />);
    const toggle = await screen.findByLabelText("Вычислять SHA-256");
    fireEvent.click(toggle);
    await waitFor(() => expect(writes).toHaveBeenCalledTimes(1));
    expect(toggle).not.toBeChecked();
    expect(screen.getByRole("alert")).toBeInTheDocument();

    fail = false;
    fireEvent.click(
      screen.getByRole("button", {
        name: "Повторить сохранение: SHA-256 (выключено)",
      }),
    );
    await waitFor(() => expect(writes).toHaveBeenCalledTimes(2));
    expect(bodies).toEqual([{ enabled: false }, { enabled: false }]);
    expect(toggle).not.toBeChecked();
  });

  it("offers a retry for a failed publication-format write using the captured value", async () => {
    let fail = true;
    let format = "mka";
    const bodies: unknown[] = [];
    const writes = vi.fn(async ({ request }: { request: Request }) => {
      const body = (await request.json()) as { publication_format: string };
      bodies.push(body);
      if (fail)
        return HttpResponse.json({ message: "write failed" }, { status: 500 });
      format = body.publication_format;
      return new HttpResponse(null, { status: 204 });
    });
    server.use(
      http.put("/api/settings/runtime", writes),
      http.get("/api/settings", () =>
        json({
          ...settings,
          settings: { ...settings.settings, publication_format: format },
        }),
      ),
    );
    render(<SettingsScreen />);
    await screen.findByLabelText("Одновременные файлы");
    fireEvent.click(screen.getByRole("button", { name: /Publication format/ }));
    fireEvent.click(await screen.findByRole("option", { name: "M4A" }));
    await waitFor(() => expect(writes).toHaveBeenCalledTimes(1));
    expect(
      screen.getByRole("button", {
        name: "Повторить сохранение: формат публикации (M4A)",
      }),
    ).toBeInTheDocument();

    fail = false;
    fireEvent.click(
      screen.getByRole("button", {
        name: "Повторить сохранение: формат публикации (M4A)",
      }),
    );
    await waitFor(() => expect(writes).toHaveBeenCalledTimes(2));
    expect(bodies).toEqual([
      { publication_format: "m4a" },
      { publication_format: "m4a" },
    ]);
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: /Publication format/ }),
      ).toHaveTextContent("M4A"),
    );
  });

  it("does not expose a retry for an older failed selection after a newer choice succeeds", async () => {
    const failFirst = true;
    let releaseFirst!: () => void;
    const firstReady = new Promise<void>((resolve) => {
      releaseFirst = resolve;
    });
    let format = "mka";
    const bodies: unknown[] = [];
    const writes = vi.fn(async ({ request }: { request: Request }) => {
      const body = (await request.json()) as { publication_format: string };
      bodies.push(body);
      if (bodies.length === 1) {
        await firstReady;
        if (failFirst)
          return HttpResponse.json(
            { message: "stale write failed" },
            { status: 500 },
          );
      }
      format = body.publication_format;
      return new HttpResponse(null, { status: 204 });
    });
    server.use(
      http.put("/api/settings/runtime", writes),
      http.get("/api/settings", () =>
        json({
          ...settings,
          settings: { ...settings.settings, publication_format: format },
        }),
      ),
    );
    render(<SettingsScreen />);
    await screen.findByLabelText("Одновременные файлы");
    const formatButton = screen.getByRole("button", {
      name: /Publication format/,
    });
    fireEvent.click(formatButton);
    fireEvent.click(await screen.findByRole("option", { name: "M4A" }));
    await waitFor(() => expect(writes).toHaveBeenCalledTimes(1));

    fireEvent.click(formatButton);
    fireEvent.click(
      await screen.findByRole("option", { name: "Исходный формат" }),
    );
    releaseFirst();
    await waitFor(() => expect(writes).toHaveBeenCalledTimes(2));
    expect(bodies).toEqual([
      { publication_format: "m4a" },
      { publication_format: "source" },
    ]);
    await waitFor(() =>
      expect(formatButton).toHaveTextContent("Исходный формат"),
    );
    expect(
      screen.queryByRole("button", {
        name: "Повторить сохранение: формат публикации (M4A)",
      }),
    ).not.toBeInTheDocument();
  });

  it("retries a failed log-level confirmation read without repeating its accepted PUT", async () => {
    let reads = 0;
    const logWrites = vi.fn(async ({ request }: { request: Request }) => {
      expect(await request.json()).toEqual({ level: "debug" });
      return new HttpResponse(null, { status: 204 });
    });
    server.use(
      http.put("/api/settings/log-level", logWrites),
      http.get("/api/settings", () => {
        reads += 1;
        if (reads === 2)
          return HttpResponse.json({ message: "read failed" }, { status: 500 });
        return json({
          ...settings,
          settings: {
            ...settings.settings,
            log_level: reads > 2 ? "debug" : "info",
          },
        });
      }),
    );
    render(<SettingsScreen />);
    await screen.findByLabelText("Одновременные файлы");
    fireEvent.click(screen.getByRole("button", { name: /Log level/ }));
    fireEvent.click(await screen.findByRole("option", { name: "debug" }));
    await waitFor(() => expect(reads).toBe(2));
    expect(logWrites).toHaveBeenCalledTimes(1);
    const retry = screen.getByRole("button", {
      name: "Повторить подтверждение: уровень журнала (debug)",
    });
    expect(retry).toBeInTheDocument();

    fireEvent.click(retry);
    await waitFor(() => expect(reads).toBe(3));
    expect(logWrites).toHaveBeenCalledTimes(1);
    expect(
      screen.queryByRole("button", {
        name: "Повторить подтверждение: уровень журнала (debug)",
      }),
    ).not.toBeInTheDocument();
  });

  it("keeps numeric saves gated until concurrent general autosaves and their reads finish", async () => {
    let sourceConcurrency = 4;
    let shaEnabled = true;
    let releaseFirstSave!: () => void;
    const firstSaveReady = new Promise<void>((resolve) => {
      releaseFirstSave = resolve;
    });
    const runtimeBodies: unknown[] = [];
    const runtimeWrites = vi.fn(async ({ request }: { request: Request }) => {
      const body = (await request.json()) as {
        source_file_concurrency?: number;
      };
      runtimeBodies.push(body);
      if (runtimeBodies.length === 1) await firstSaveReady;
      if (body.source_file_concurrency !== undefined)
        sourceConcurrency = body.source_file_concurrency;
      return new HttpResponse(null, { status: 204 });
    });
    const shaWrites = vi.fn(async ({ request }: { request: Request }) => {
      expect(await request.json()).toEqual({ enabled: false });
      shaEnabled = false;
      return new HttpResponse(null, { status: 204 });
    });
    server.use(
      http.put("/api/settings/runtime", runtimeWrites),
      http.put("/api/settings/sha256", shaWrites),
      http.get("/api/settings", () =>
        json({
          ...settings,
          settings: {
            ...settings.settings,
            source_file_concurrency: sourceConcurrency,
            sha256_enabled: shaEnabled,
          },
        }),
      ),
    );
    render(<SettingsScreen />);
    const concurrency = await screen.findByLabelText("Одновременные файлы");
    fireEvent.change(concurrency, { target: { value: "5" } });
    const saveConcurrency = screen.getByRole("button", {
      name: "Сохранить параллельность",
    });
    fireEvent.click(saveConcurrency);
    await waitFor(() => expect(runtimeWrites).toHaveBeenCalledTimes(1));

    fireEvent.click(screen.getByLabelText("Вычислять SHA-256"));
    await waitFor(() => expect(shaWrites).toHaveBeenCalledTimes(1));
    await waitFor(() =>
      expect(
        screen.getByText("Настройка SHA-256 сохранена."),
      ).toBeInTheDocument(),
    );
    expect(saveConcurrency).toBeDisabled();
    fireEvent.click(saveConcurrency);
    expect(runtimeWrites).toHaveBeenCalledTimes(1);

    releaseFirstSave();
    await waitFor(() => expect(saveConcurrency).toBeEnabled());
    fireEvent.change(concurrency, { target: { value: "6" } });
    fireEvent.click(saveConcurrency);
    await waitFor(() => expect(runtimeWrites).toHaveBeenCalledTimes(2));
    expect(runtimeBodies).toEqual([
      { source_file_concurrency: 5 },
      { source_file_concurrency: 6 },
    ]);
  });

  it("serializes accepted autosaves across remount and lets a fresh choice win", async () => {
    let format = "mka";
    let level = "info";
    let releaseFormat!: () => void;
    const formatReady = new Promise<void>((resolve) => {
      releaseFormat = resolve;
    });
    const runtimeBodies: unknown[] = [];
    const runtimeWrites = vi.fn(async ({ request }: { request: Request }) => {
      const body = (await request.json()) as { publication_format: string };
      runtimeBodies.push(body);
      if (runtimeBodies.length === 1) await formatReady;
      format = body.publication_format;
      return new HttpResponse(null, { status: 204 });
    });
    const logWrites = vi.fn(async ({ request }: { request: Request }) => {
      const body = (await request.json()) as { level: string };
      level = body.level;
      return new HttpResponse(null, { status: 204 });
    });
    const settingsReads = vi.fn(() => {
      return json({
        ...settings,
        settings: {
          ...settings.settings,
          publication_format: format,
          log_level: level,
        },
      });
    });
    const installationReads = vi.fn(({ request }: { request: Request }) => {
      const kind = new URL(request.url).searchParams.get("package_kind");
      return json({
        installations: [activeFF, activeFP].filter(
          (item) => item.package_kind === kind,
        ),
      });
    });
    server.use(
      http.get("/api/settings", settingsReads),
      http.get("/api/tools/installations", installationReads),
      http.put("/api/settings/runtime", runtimeWrites),
      http.put("/api/settings/log-level", logWrites),
    );

    const oldView = render(<SettingsScreen />);
    await screen.findByLabelText("Одновременные файлы");
    fireEvent.click(screen.getByRole("button", { name: /Publication format/ }));
    fireEvent.click(await screen.findByRole("option", { name: "M4A" }));
    await waitFor(() => expect(runtimeWrites).toHaveBeenCalledTimes(1));
    fireEvent.click(screen.getByRole("button", { name: /Log level/ }));
    fireEvent.click(await screen.findByRole("option", { name: "debug" }));
    expect(screen.getByRole("button", { name: /Log level/ })).toHaveTextContent(
      "debug",
    );
    expect(logWrites).not.toHaveBeenCalled();

    oldView.unmount();
    render(<SettingsScreen />);
    await waitFor(() => expect(installationReads).toHaveBeenCalledTimes(4));
    expect(settingsReads).toHaveBeenCalledTimes(1);

    releaseFormat();
    await waitFor(() => expect(logWrites).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(settingsReads).toHaveBeenCalledTimes(4));
    await screen.findByLabelText("Одновременные файлы");
    expect(
      screen.getByRole("button", { name: /Publication format/ }),
    ).toHaveTextContent("M4A");
    expect(screen.getByRole("button", { name: /Log level/ })).toHaveTextContent(
      "debug",
    );

    fireEvent.click(screen.getByRole("button", { name: /Publication format/ }));
    fireEvent.click(
      await screen.findByRole("option", { name: "Исходный формат" }),
    );
    await waitFor(() => expect(runtimeWrites).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(settingsReads).toHaveBeenCalledTimes(5));
    expect(runtimeBodies).toEqual([
      { publication_format: "m4a" },
      { publication_format: "source" },
    ]);
    expect(
      screen.getByRole("button", { name: /Publication format/ }),
    ).toHaveTextContent("Исходный формат");
  });

  it("continues queued autosaves and initial loading after an earlier write fails", async () => {
    let level = "info";
    let releaseFormat!: () => void;
    const formatReady = new Promise<void>((resolve) => {
      releaseFormat = resolve;
    });
    const runtimeWrites = vi.fn(async () => {
      await formatReady;
      return HttpResponse.json(
        { message: "format write failed" },
        { status: 500 },
      );
    });
    const logWrites = vi.fn(async ({ request }: { request: Request }) => {
      const body = (await request.json()) as { level: string };
      level = body.level;
      return new HttpResponse(null, { status: 204 });
    });
    const settingsReads = vi.fn(() => {
      return json({
        ...settings,
        settings: { ...settings.settings, log_level: level },
      });
    });
    const installationReads = vi.fn(({ request }: { request: Request }) => {
      const kind = new URL(request.url).searchParams.get("package_kind");
      return json({
        installations: [activeFF, activeFP].filter(
          (item) => item.package_kind === kind,
        ),
      });
    });
    server.use(
      http.get("/api/settings", settingsReads),
      http.get("/api/tools/installations", installationReads),
      http.put("/api/settings/runtime", runtimeWrites),
      http.put("/api/settings/log-level", logWrites),
    );

    const oldView = render(<SettingsScreen />);
    await screen.findByLabelText("Одновременные файлы");
    fireEvent.click(screen.getByRole("button", { name: /Publication format/ }));
    fireEvent.click(await screen.findByRole("option", { name: "M4A" }));
    await waitFor(() => expect(runtimeWrites).toHaveBeenCalledTimes(1));
    fireEvent.click(screen.getByRole("button", { name: /Log level/ }));
    fireEvent.click(await screen.findByRole("option", { name: "debug" }));
    oldView.unmount();
    render(<SettingsScreen />);
    await waitFor(() => expect(installationReads).toHaveBeenCalledTimes(4));
    expect(settingsReads).toHaveBeenCalledTimes(1);

    releaseFormat();
    await waitFor(() => expect(logWrites).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(settingsReads).toHaveBeenCalledTimes(3));
    await screen.findByLabelText("Одновременные файлы");
    expect(screen.getByRole("button", { name: /Log level/ })).toHaveTextContent(
      "debug",
    );
    expect(
      screen.queryByRole("button", {
        name: "Повторить сохранение: формат публикации (M4A)",
      }),
    ).not.toBeInTheDocument();
  });
});
