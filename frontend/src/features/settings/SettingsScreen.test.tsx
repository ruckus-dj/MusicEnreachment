import "@testing-library/jest-dom/vitest";
import {
  act,
  cleanup,
  fireEvent,
  render as renderDom,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { HttpResponse, http } from "msw";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider, useI18n } from "../../i18n/I18nProvider";
import { createMemoryStorage } from "../../test/memoryStorage";
import { server } from "../../test/server";
import { SettingsScreen } from "./SettingsScreen";
import {
  readSettingsDraft,
  resetSettingsDraftMemory,
  writeSettingsDraft,
} from "./settingsDraft";

let setTestLanguage: ((language: "en" | "ru") => void) | undefined;
function TestLanguageBridge() {
  setTestLanguage = useI18n().setPreference;
  return null;
}
function render(ui: ReactNode) {
  return renderDom(
    <I18nProvider>
      <TestLanguageBridge />
      {ui}
    </I18nProvider>,
  );
}

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
    http.get("/api/genres/catalog", () =>
      json({
        count: 0,
        items: [],
        last_attempt_at: null,
        last_error: null,
        refresh_status: "idle",
        active_operation_id: null,
        updated_at: null,
      }),
    ),
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
  localStorage.setItem("melotrove-language", "ru");
  vi.stubGlobal("sessionStorage", createMemoryStorage());
  sessionStorage.clear();
  resetSettingsDraftMemory();
  TestEventSource.instances = [];
  setTestLanguage = undefined;
  vi.stubGlobal("EventSource", TestEventSource);
  common();
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe("SettingsScreen", () => {
  it("keeps a text draft and its field mounted when switching from Russian to English", async () => {
    render(<SettingsScreen />);
    const concurrency = await screen.findByLabelText("Одновременные файлы");
    fireEvent.change(concurrency, { target: { value: "8" } });
    await waitFor(() =>
      expect(
        readSettingsDraft(sessionStorage)?.sourceFileConcurrency?.value,
      ).toBe("8"),
    );

    act(() => setTestLanguage?.("en"));

    expect(await screen.findByLabelText("Concurrent files")).toBe(concurrency);
    expect(concurrency).toHaveValue("8");
    expect(
      readSettingsDraft(sessionStorage)?.sourceFileConcurrency?.value,
    ).toBe("8");
  });

  it("restores an invalid raw concurrency draft without applying it", async () => {
    writeSettingsDraft(sessionStorage, {
      sourceFileConcurrency: {
        value: "not-a-number",
        baseline: "4",
        revision: 3,
      },
    });
    const checked = vi.fn(() => json({ success: true, error: "" }));
    const settingsWrites = vi.fn(() => new HttpResponse(null, { status: 204 }));
    server.use(
      http.post("/api/settings/musicbrainz/check", checked),
      http.put("/api/settings/runtime", settingsWrites),
      http.put("/api/settings/musicbrainz", settingsWrites),
    );

    render(<SettingsScreen />);

    const concurrency = await screen.findByLabelText("Одновременные файлы");
    await waitFor(() => expect(concurrency).toHaveValue("not-a-number"));
    expect(
      screen.getByText("Есть несохранённое изменение параллельности."),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Сохранить параллельность" }),
    ).toBeDisabled();
    fireEvent.click(
      screen.getByRole("button", { name: "Сохранить и проверить MusicBrainz" }),
    );
    await waitFor(() => expect(checked).toHaveBeenCalledTimes(1));
    expect(settingsWrites).not.toHaveBeenCalled();
    expect(
      await screen.findByText("MusicBrainz проверен."),
    ).toBeInTheDocument();
    expect(
      readSettingsDraft(sessionStorage)?.sourceFileConcurrency?.value,
    ).toBe("not-a-number");
  });

  it("loads the staged work directory independently of the output directory", async () => {
    const workDirectory = "/srv/staging-work";
    server.use(
      http.get("/api/settings", () =>
        json({
          ...settings,
          settings: { ...settings.settings, work_directory: workDirectory },
        }),
      ),
    );

    render(<SettingsScreen />);

    expect(
      await screen.findByRole("textbox", {
        name: "Рабочая папка для подготовленных копий",
      }),
    ).toHaveValue(workDirectory);
    expect(
      screen.getByRole("textbox", { name: "Каталог публикации" }),
    ).toHaveValue("/srv/music");
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
    let publicationRemoveSources = false;
    let lrclibEnabled = true;
    let logLevel = "info";
    let releaseFirstFormatWrite!: () => void;
    const firstFormatWrite = new Promise<void>((resolve) => {
      releaseFirstFormatWrite = resolve;
    });
    const musicBrainz = {
      mode: "self-hosted",
      baseURL: "https://saved.musicbrainz.test",
      throttle: false,
      delay: 0.5,
    };
    const runtimeWrites = vi.fn(async ({ request }: { request: Request }) => {
      const body = (await request.json()) as {
        publication_format?: string;
        publication_remove_sources?: boolean;
        expected: {
          publication_format?: string;
          publication_remove_sources?: boolean;
        };
      };
      if (body.publication_format !== undefined) {
        if (body.publication_format === "m4a") {
          expect(body).toEqual({
            publication_format: "m4a",
            expected: { publication_format: "source" },
          });
          await firstFormatWrite;
        } else {
          expect(body).toEqual({
            publication_format: "source",
            expected: { publication_format: "m4a" },
          });
        }
        publicationFormat = body.publication_format;
      } else {
        expect(body).toEqual({
          publication_remove_sources: true,
          expected: { publication_remove_sources: false },
        });
        publicationRemoveSources = body.publication_remove_sources ?? false;
      }
      return new HttpResponse(null, { status: 204 });
    });
    const logWrites = vi.fn(async ({ request }: { request: Request }) => {
      const body = (await request.json()) as { level: string };
      expect(body).toEqual({ level: "debug" });
      logLevel = body.level;
      return new HttpResponse(null, { status: 204 });
    });
    const lrclibWrites = vi.fn(async ({ request }: { request: Request }) => {
      expect(await request.json()).toEqual({ enabled: false });
      lrclibEnabled = false;
      return new HttpResponse(null, { status: 204 });
    });
    const musicBrainzWrites = vi.fn(
      async ({ request }: { request: Request }) => {
        const body = (await request.json()) as Record<string, unknown>;
        if ("self_hosted_throttle" in body) {
          expect(body).toEqual({
            self_hosted_throttle: true,
            expected: { self_hosted_throttle: false },
          });
          musicBrainz.throttle = true;
        } else {
          expect(body).toEqual({
            mode: "public",
            base_url: "",
            expected: {
              mode: "self-hosted",
              base_url: "https://saved.musicbrainz.test",
            },
          });
          musicBrainz.mode = "public";
          musicBrainz.baseURL = "";
        }
        return new HttpResponse(null, { status: 204 });
      },
    );
    server.use(
      http.put("/api/settings/runtime", runtimeWrites),
      http.put("/api/settings/log-level", logWrites),
      http.put("/api/settings/lrclib", lrclibWrites),
      http.put("/api/settings/musicbrainz", musicBrainzWrites),
      http.get("/api/settings", () =>
        json({
          ...settings,
          settings: {
            ...settings.settings,
            publication_format: publicationFormat,
            publication_remove_sources: publicationRemoveSources,
            lrclib_enabled: lrclibEnabled,
            log_level: logLevel,
            musicbrainz_mode: musicBrainz.mode,
            musicbrainz_base_url: musicBrainz.baseURL,
            musicbrainz_self_hosted_throttle: musicBrainz.throttle,
            musicbrainz_self_hosted_delay_seconds: musicBrainz.delay,
          },
        }),
      ),
    );
    render(<SettingsScreen />);
    const concurrency = await screen.findByLabelText("Одновременные файлы");
    fireEvent.change(concurrency, { target: { value: "8 invalid" } });
    publicationFormat = "source";
    fireEvent.click(screen.getByRole("button", { name: "Обновить состояние" }));
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: /Формат публикации/ }),
      ).toHaveTextContent("Исходный формат"),
    );

    fireEvent.click(
      await screen.findByRole("button", { name: /Формат публикации/ }),
    );
    fireEvent.click(await screen.findByRole("option", { name: "M4A" }));
    await waitFor(() => expect(runtimeWrites).toHaveBeenCalledTimes(1));
    expect(
      screen.getByText(/M4A сохраняет AAC и ALAC без перекодирования/),
    ).toHaveAttribute("role", "note");
    fireEvent.click(
      await screen.findByRole("button", { name: /Формат публикации/ }),
    );
    fireEvent.click(
      await screen.findByRole("option", { name: "Исходный формат" }),
    );
    expect(runtimeWrites).toHaveBeenCalledTimes(1);
    releaseFirstFormatWrite();
    await waitFor(() => expect(runtimeWrites).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(publicationFormat).toBe("source"));
    expect(
      await screen.findByText("Формат публикации сохранён."),
    ).toBeInTheDocument();
    expect(concurrency).toHaveValue("8 invalid");
    const shaCheckbox = screen.getByLabelText("Вычислять SHA-256");
    expect(shaCheckbox).toHaveAttribute("aria-describedby", "sha256-help");
    expect(
      screen.getByText(/Настройка применяется к последующим анализам/),
    ).toBeInTheDocument();
    const concurrencyDescriptionIds =
      concurrency.getAttribute("aria-describedby")?.split(/\s+/) ?? [];
    expect(concurrencyDescriptionIds).toEqual(
      expect.arrayContaining([
        "source-file-concurrency-help",
        "settings-concurrency-error",
      ]),
    );
    expect(
      screen.getByText(
        "Введите положительное целое число, точно представимое в браузере.",
      ),
    ).toHaveAttribute("id", "settings-concurrency-error");
    expect(
      screen.getByText(/Положительное целое число файлов/),
    ).toBeInTheDocument();
    expect(
      screen.getByText("Есть несохранённое изменение параллельности."),
    ).toBeInTheDocument();

    fireEvent.change(
      screen.getByRole("textbox", { name: "Базовый URL MusicBrainz" }),
      {
        target: { value: "https://unsaved.musicbrainz.test" },
      },
    );
    fireEvent.change(
      screen.getByRole("textbox", { name: "Интервал запросов (секунды)" }),
      {
        target: { value: "invalid delay" },
      },
    );
    fireEvent.click(
      screen.getByLabelText(
        "Ограничивать частоту запросов к собственному MusicBrainz",
      ),
    );
    await waitFor(() => expect(musicBrainzWrites).toHaveBeenCalledTimes(1));
    fireEvent.change(screen.getByLabelText("Режим MusicBrainz"), {
      target: { value: "public" },
    });
    await waitFor(() => expect(musicBrainzWrites).toHaveBeenCalledTimes(2));

    fireEvent.click(
      screen.getByLabelText("Разрешить удаление исходников после публикации"),
    );
    await waitFor(() => expect(runtimeWrites).toHaveBeenCalledTimes(3));
    expect(
      await screen.findByText("Настройка удаления исходников сохранена."),
    ).toBeInTheDocument();
    fireEvent.click(screen.getByLabelText("LRCLIB включён"));
    await waitFor(() => expect(lrclibWrites).toHaveBeenCalledTimes(1));
    expect(
      await screen.findByText("Настройка LRCLIB сохранена."),
    ).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: /Уровень журнала/ }));
    fireEvent.click(await screen.findByRole("option", { name: "debug" }));
    await waitFor(() => expect(logWrites).toHaveBeenCalledTimes(1));
    expect(runtimeWrites).toHaveBeenCalledTimes(3);
    expect(logWrites).toHaveBeenCalledTimes(1);
    expect(musicBrainzWrites).toHaveBeenCalledTimes(2);
    await waitFor(() =>
      expect(
        readSettingsDraft(sessionStorage)?.sourceFileConcurrency?.value,
      ).toBe("8 invalid"),
    );
    expect(readSettingsDraft(sessionStorage)?.musicBrainzURL?.value).toBe(
      "https://unsaved.musicbrainz.test",
    );
    expect(readSettingsDraft(sessionStorage)?.musicBrainzDelay?.value).toBe(
      "invalid delay",
    );
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
    fireEvent.click(screen.getByRole("button", { name: /Формат публикации/ }));
    fireEvent.click(await screen.findByRole("option", { name: "M4A" }));
    await waitFor(() => expect(writes).toHaveBeenCalledTimes(1));
    const retry = await screen.findByRole("button", {
      name: "Повторить сохранение: формат публикации (M4A)",
    });
    expect(retry).toBeInTheDocument();

    fail = false;
    fireEvent.click(retry);
    await waitFor(() => expect(writes).toHaveBeenCalledTimes(2));
    expect(bodies).toEqual([
      {
        publication_format: "m4a",
        expected: { publication_format: "mka" },
      },
      {
        publication_format: "m4a",
        expected: { publication_format: "mka" },
      },
    ]);
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: /Формат публикации/ }),
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
      name: /Формат публикации/,
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
      {
        publication_format: "m4a",
        expected: { publication_format: "mka" },
      },
      {
        publication_format: "source",
        expected: { publication_format: "mka" },
      },
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
    fireEvent.click(screen.getByRole("button", { name: /Уровень журнала/ }));
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
      {
        source_file_concurrency: 5,
        expected: { source_file_concurrency: 4 },
      },
      {
        source_file_concurrency: 6,
        expected: { source_file_concurrency: 5 },
      },
    ]);
  });

  it("serializes accepted autosaves across remount and lets a fresh choice win", async () => {
    let format = "mka";
    let level = "info";
    let failNextFormatConfirmation = false;
    let releaseFormat!: () => void;
    const formatReady = new Promise<void>((resolve) => {
      releaseFormat = resolve;
    });
    const runtimeBodies: unknown[] = [];
    const runtimeWrites = vi.fn(async ({ request }: { request: Request }) => {
      const body = (await request.json()) as {
        publication_format: string;
        expected: { publication_format: string };
      };
      runtimeBodies.push(body);
      if (runtimeBodies.length === 1) await formatReady;
      format = body.publication_format;
      if (runtimeBodies.length === 3) failNextFormatConfirmation = true;
      return new HttpResponse(null, { status: 204 });
    });
    const logWrites = vi.fn(async ({ request }: { request: Request }) => {
      const body = (await request.json()) as { level: string };
      level = body.level;
      return new HttpResponse(null, { status: 204 });
    });
    const settingsReads = vi.fn(() => {
      if (failNextFormatConfirmation) {
        failNextFormatConfirmation = false;
        return HttpResponse.json(
          { detail: "confirmation read failed" },
          { status: 500 },
        );
      }
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
    fireEvent.click(screen.getByRole("button", { name: /Формат публикации/ }));
    fireEvent.click(await screen.findByRole("option", { name: "M4A" }));
    await waitFor(() => expect(runtimeWrites).toHaveBeenCalledTimes(1));
    fireEvent.click(screen.getByRole("button", { name: /Уровень журнала/ }));
    fireEvent.click(await screen.findByRole("option", { name: "debug" }));
    expect(
      screen.getByRole("button", { name: /Уровень журнала/ }),
    ).toHaveTextContent("debug");
    expect(logWrites).not.toHaveBeenCalled();

    oldView.unmount();
    render(<SettingsScreen />);
    await waitFor(() => expect(installationReads).toHaveBeenCalledTimes(4));
    expect(settingsReads).toHaveBeenCalledTimes(1);

    releaseFormat();
    await waitFor(() => expect(logWrites).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(settingsReads).toHaveBeenCalledTimes(4));
    const formatButton = await screen.findByRole("button", {
      name: /Формат публикации/,
    });
    expect(formatButton).toHaveTextContent("M4A");
    expect(
      screen.getByRole("button", { name: /Уровень журнала/ }),
    ).toHaveTextContent("debug");

    fireEvent.click(formatButton);
    fireEvent.click(
      await screen.findByRole("option", { name: "Исходный формат" }),
    );
    await waitFor(() => expect(runtimeWrites).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(settingsReads).toHaveBeenCalledTimes(5));
    expect(runtimeBodies).toEqual([
      {
        publication_format: "m4a",
        expected: { publication_format: "mka" },
      },
      {
        publication_format: "source",
        expected: { publication_format: "m4a" },
      },
    ]);
    expect(
      screen.getByRole("button", { name: /Формат публикации/ }),
    ).toHaveTextContent("Исходный формат");

    fireEvent.click(
      await screen.findByRole("button", { name: /Формат публикации/ }),
    );
    fireEvent.click(await screen.findByRole("option", { name: "M4A" }));
    await waitFor(() => expect(runtimeWrites).toHaveBeenCalledTimes(3));
    const retryConfirmation = await screen.findByRole("button", {
      name: "Повторить подтверждение: формат публикации (M4A)",
    });
    fireEvent.click(retryConfirmation);
    expect(
      await screen.findByText("Настройка подтверждена сервером."),
    ).toBeInTheDocument();

    const publicationFormatAfterConfirmation = await screen.findByRole(
      "button",
      { name: /Формат публикации/ },
    );
    fireEvent.click(publicationFormatAfterConfirmation);
    fireEvent.click(
      await screen.findByRole("option", { name: "Исходный формат" }),
    );
    await waitFor(() => expect(runtimeWrites).toHaveBeenCalledTimes(4));
    expect(runtimeBodies.slice(2)).toEqual([
      {
        publication_format: "m4a",
        expected: { publication_format: "source" },
      },
      {
        publication_format: "source",
        expected: { publication_format: "m4a" },
      },
    ]);
  });

  it("keeps a dirty group through refresh and 409 until explicitly replaced or taken saved", async () => {
    let sourceConcurrency = 4;
    const template = settings.settings.publication_path_template;
    let firstConflictAttempt = true;
    const writes = vi.fn(async ({ request }: { request: Request }) => {
      const body = (await request.json()) as {
        source_file_concurrency: number;
        expected: { source_file_concurrency: number };
      };
      if (firstConflictAttempt) {
        firstConflictAttempt = false;
        expect(body).toEqual({
          source_file_concurrency: 8,
          expected: { source_file_concurrency: 6 },
        });
        sourceConcurrency = 7;
        return HttpResponse.json(
          { detail: "settings changed" },
          { status: 409 },
        );
      }
      return new HttpResponse(null, { status: 204 });
    });
    server.use(
      http.get("/api/settings", () =>
        json({
          ...settings,
          settings: {
            ...settings.settings,
            source_file_concurrency: sourceConcurrency,
            publication_path_template: template,
          },
        }),
      ),
      http.put("/api/settings/runtime", writes),
    );
    render(<SettingsScreen />);
    const concurrency = await screen.findByLabelText("Одновременные файлы");
    const templateInput = screen.getByLabelText("Шаблон пути публикации");
    fireEvent.change(concurrency, { target: { value: "8" } });
    fireEvent.change(templateInput, {
      target: { value: "{ALBUM_NAME}/draft" },
    });

    sourceConcurrency = 6;
    fireEvent.click(screen.getByRole("button", { name: "Обновить состояние" }));
    const conflict = await screen.findByRole("status", {
      name: "Конфликт настроек: параллельность анализа",
    });
    fireEvent.click(
      within(conflict).getByRole("button", {
        name: "Продолжить редактирование: параллельность анализа",
      }),
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Сохранить параллельность" }),
    );
    const confirmation = await screen.findByRole("dialog", {
      name: "Заменить сохранённое значение?",
    });
    expect(confirmation).toHaveTextContent("6");
    fireEvent.click(
      screen.getByRole("button", { name: "Заменить сохранённое значение" }),
    );
    await waitFor(() => expect(writes).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(concurrency).toHaveValue("8"));
    expect(templateInput).toHaveValue("{ALBUM_NAME}/draft");

    const renewedConflict = await screen.findByRole("status", {
      name: "Конфликт настроек: параллельность анализа",
    });
    expect(renewedConflict).toHaveTextContent("7");
    const takeSaved = within(renewedConflict).getByRole("button", {
      name: "Взять сохранённое: параллельность анализа",
    });
    await waitFor(() => expect(takeSaved).toBeEnabled());
    fireEvent.click(takeSaved);
    await waitFor(() =>
      expect(screen.getByLabelText("Одновременные файлы")).toHaveValue("7"),
    );
    expect(templateInput).toHaveValue("{ALBUM_NAME}/draft");
    expect(template).toBe(settings.settings.publication_path_template);
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
    fireEvent.click(screen.getByRole("button", { name: /Формат публикации/ }));
    fireEvent.click(await screen.findByRole("option", { name: "M4A" }));
    await waitFor(() => expect(runtimeWrites).toHaveBeenCalledTimes(1));
    fireEvent.click(screen.getByRole("button", { name: /Уровень журнала/ }));
    fireEvent.click(await screen.findByRole("option", { name: "debug" }));
    oldView.unmount();
    render(<SettingsScreen />);
    await waitFor(() => expect(installationReads).toHaveBeenCalledTimes(4));
    expect(settingsReads).toHaveBeenCalledTimes(1);

    releaseFormat();
    await waitFor(() => expect(logWrites).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(settingsReads).toHaveBeenCalledTimes(3));
    await screen.findByLabelText("Одновременные файлы");
    expect(
      screen.getByRole("button", { name: /Уровень журнала/ }),
    ).toHaveTextContent("debug");
    expect(
      screen.queryByRole("button", {
        name: "Повторить сохранение: формат публикации (M4A)",
      }),
    ).not.toBeInTheDocument();
  });
});
