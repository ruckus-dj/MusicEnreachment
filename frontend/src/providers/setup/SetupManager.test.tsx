import "@testing-library/jest-dom/vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type {
  OperationResponse,
  SetupStateBody,
} from "../../api/generated/client.schemas";
import { I18nProvider, useI18n } from "../../app/i18n/host";
import { createMemoryStorage } from "../../test/memoryStorage";
import { server } from "../../test/server";
import { SetupManager } from "./SetupManager";
import { setupDraftKey, setupDraftVersion } from "./setupDraft";

const settings: SetupStateBody["settings"] = {
  tools_directory: "",
  output_directory: "",
  work_directory: "",
  publication_format: "",
  publication_path_template:
    '{ALBUM_PRIMARY_ARTIST} [{ALBUM_PRIMARY_ARTIST_MBID_PART | ALBUM_PRIMARY_ARTIST_ID}]/{ALBUM_TYPE | "Unclassified"}/{ALBUM_YEAR | "Unknown year"} - {ALBUM_NAME} [{ALBUM_MBID_PART | ALBUM_ID}]/{DISC_NUMBER:02}-{TRACK_NUMBER:02} - {TRACK_NAME}.{EXT}',
  publication_remove_sources: false,
  publication_auto_complete_albums: false,
  musicbrainz_mode: "public",
  musicbrainz_base_url: "",
  musicbrainz_self_hosted_throttle: false,
  musicbrainz_self_hosted_delay_seconds: 0.5,
  lrclib_enabled: true,
  sha256_enabled: true,
  log_level: "info",
  source_file_concurrency: 4,
  matching_pair_threshold: 0.7,
  has_acoustid_application_key: false,
  has_github_token: false,
};
function state(
  overrides: Partial<SetupStateBody["settings"]> = {},
  healthy = false,
  extra: Partial<SetupStateBody> = {},
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
    musicbrainz_configuration_identity: "",
    genre_catalog: { ready: false, status: "idle", count: 0 },
    ...extra,
  };
}
function catalogOperation(
  id: string,
  operationState: string,
): OperationResponse {
  const at = "2026-11-23T12:00:00Z";
  return {
    id,
    kind: "genre_catalog_refresh",
    state: operationState,
    stage: "fetch",
    bytes_completed: 0,
    created_at: at,
    updated_at: at,
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
  Object.defineProperty(window.HTMLDialogElement.prototype, "showModal", {
    configurable: true,
    value(this: HTMLDialogElement) {
      this.open = true;
    },
  });
  Object.defineProperty(window.HTMLDialogElement.prototype, "close", {
    configurable: true,
    value(this: HTMLDialogElement) {
      this.open = false;
    },
  });
  vi.stubGlobal("localStorage", createMemoryStorage());
  TestEventSource.instances = [];
  TestEventSource.onCreated = undefined;
  vi.stubGlobal("EventSource", TestEventSource);
  server.use(
    http.get("/api/operations", () => HttpResponse.json({ operations: [] })),
    http.get("/api/tools/installations", () =>
      HttpResponse.json({ installations: [] }),
    ),
    http.get("/api/tools/catalog", ({ request }) =>
      HttpResponse.json({
        package_kind: new URL(request.url).searchParams.get("package_kind"),
        releases: [],
        platform: {
          diagnostic: false,
          supported: true,
          goos: "linux",
          goarch: "amd64",
        },
      }),
    ),
  );
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  window.sessionStorage.clear();
  window.history.replaceState(null, "", "/");
});
function setSetupHash(step: string) {
  window.history.replaceState(null, "", `/#/setup?step=${step}`);
}
function renderSetup(value: SetupStateBody, onCompleted = vi.fn()) {
  localStorage.setItem("melotrove-language", "ru");
  return render(
    <I18nProvider>
      <SetupManager initialState={value} onCompleted={onCompleted} />
    </I18nProvider>,
  );
}
async function click(name: string) {
  fireEvent.click(screen.getByRole("button", { name }));
}
function heading(name: string) {
  return screen.getByRole("heading", { level: 2, name });
}

function seedAutoCompleteAlbumsDraft(value: boolean, saved: boolean) {
  window.sessionStorage.setItem(
    setupDraftKey,
    JSON.stringify({
      version: setupDraftVersion,
      publication: { autoCompleteAlbums: { value, saved } },
    }),
  );
}

function LocaleSwitch() {
  const { setPreference } = useI18n();
  return (
    <button type="button" onClick={() => setPreference("en")}>
      Switch to English
    </button>
  );
}

describe("SetupManager", () => {
  it("preserves the tools directory across language changes and saves it before output is set", async () => {
    const current = state({
      tools_directory: "/srv/tools",
    });
    const requests = vi.fn();
    const saved = vi.fn();
    const checked = vi.fn();
    localStorage.setItem("melotrove-language", "ru");
    localStorage.setItem("melotrove.setup.step", "directories");
    server.use(
      http.get("/api/setup", () => {
        requests();
        return HttpResponse.json(current);
      }),
      http.get("/api/tools/installations", () => {
        requests();
        return HttpResponse.json({ installations: [] });
      }),
      http.put("/api/setup/runtime", async ({ request }) => {
        saved(await request.json());
        return new HttpResponse(null, { status: 204 });
      }),
      http.post("/api/setup/paths/check", () => {
        checked();
        return new HttpResponse(null, { status: 400 });
      }),
    );

    render(
      <I18nProvider>
        <LocaleSwitch />
        <SetupManager initialState={current} onCompleted={vi.fn()} />
      </I18nProvider>,
    );

    const toolsDirectory = screen.getByRole("textbox", {
      name: "Каталог инструментов",
    });
    expect(toolsDirectory).toHaveValue("/srv/tools");
    fireEvent.click(screen.getByRole("button", { name: "Switch to English" }));

    expect(screen.getByRole("textbox", { name: "Tools directory" })).toBe(
      toolsDirectory,
    );
    expect(toolsDirectory).toHaveValue("/srv/tools");
    expect(
      screen.queryByRole("textbox", { name: "Output directory" }),
    ).toBeNull();
    expect(requests).not.toHaveBeenCalled();
    await click("Continue");
    await waitFor(() => expect(heading("Tools")).toBeVisible());
    expect(saved).toHaveBeenCalledExactlyOnceWith({
      tools_directory: "/srv/tools",
    });
    expect(checked).not.toHaveBeenCalled();
  });

  it("discovers tool operations through the scoped selector", async () => {
    let selector: string | null = null;
    const operations = vi.fn(({ request }: { request: Request }) => {
      selector = new URL(request.url).searchParams.get("tool_discovery");
      return HttpResponse.json({ operations: [] });
    });
    server.use(http.get("/api/operations", operations));
    localStorage.setItem("melotrove.setup.step", "tools");

    renderSetup(state(paths));

    await waitFor(() => expect(operations).toHaveBeenCalled());
    expect(selector).toBe("install");
  });

  it("shows install progress by stage without leaking byte counts into the result", async () => {
    const current = state(paths);
    const queued: OperationResponse = {
      id: "ffmpeg-install",
      kind: "install",
      state: "queued",
      stage: "resolve",
      target_identity: "ffmpeg:btbn:9.0:linux:amd64",
      bytes_completed: 0,
      created_at: "2026-11-23T12:00:00Z",
      updated_at: "2026-11-23T12:00:00Z",
    };
    let snapshot = queued;
    server.use(
      http.get("/api/setup", () => HttpResponse.json(current)),
      http.get("/api/operations", () =>
        HttpResponse.json({ operations: [queued] }),
      ),
      http.get("/api/operations/ffmpeg-install", () =>
        HttpResponse.json(snapshot),
      ),
    );
    localStorage.setItem("melotrove.setup.step", "tools");
    renderSetup(current);

    await screen.findByText(/FFmpeg.*(?:ожидание|в очереди)/i);
    expect(screen.queryByRole("progressbar")).not.toBeInTheDocument();

    function update(next: Partial<OperationResponse>, timestamp: string) {
      snapshot = { ...snapshot, ...next, updated_at: timestamp };
      TestEventSource.instances
        .at(-1)
        ?.dispatchEvent(new Event("operation-changed"));
    }
    update(
      { state: "running", stage: "download", bytes_completed: 80_000_000 },
      "2026-11-23T12:00:01Z",
    );
    const indeterminate = await screen.findByRole("progressbar");
    expect(indeterminate).toHaveAccessibleName();
    expect(indeterminate).not.toHaveAttribute("aria-valuenow");
    expect(indeterminate).not.toHaveAttribute("value");

    update({ bytes_total: 320_000_000 }, "2026-11-23T12:00:02Z");
    expect(await screen.findByText(/25\s*%/)).toBeVisible();
    expect(screen.queryByText(/байт/i)).not.toBeInTheDocument();

    update({ stage: "verify" }, "2026-11-23T12:00:03Z");
    await screen.findByText(/проверка/i);
    const verifying = screen.getByRole("progressbar");
    expect(verifying).not.toHaveAttribute("aria-valuenow");
    expect(verifying).not.toHaveAttribute("value");
    expect(screen.queryByText(/25\s*%/)).not.toBeInTheDocument();

    update({ state: "succeeded", stage: "succeeded" }, "2026-11-23T12:00:04Z");
    const result = /FFmpeg 9\.0\s*[—·-]\s*установлен\.?$/i;
    await waitFor(() => expect(screen.getAllByText(result)).toHaveLength(1));
    expect(screen.queryByRole("progressbar")).not.toBeInTheDocument();
    expect(screen.queryByText(/байт|завершена/i)).not.toBeInTheDocument();
  });

  it("confirms only the exact Setup tool conflicts and keeps a failed start in the dialog", async () => {
    const current = state(paths);
    const conflict = "/srv/tools/ffmpeg/bin/ffmpeg";
    const preflights = vi.fn();
    const starts = vi.fn();
    server.use(
      http.get("/api/tools/catalog", ({ request }) => {
        const kind = new URL(request.url).searchParams.get("package_kind");
        return HttpResponse.json({
          package_kind: kind,
          platform: current.platform,
          releases: kind === "ffmpeg" ? [{ identity: "8.0.1" }] : [],
        });
      }),
      http.post("/api/tools/installations/preflight", async ({ request }) => {
        preflights(await request.json());
        return HttpResponse.json({
          preflight_token: "setup-preflight-token",
          conflicts: [conflict],
          targets: [conflict, "/srv/tools/ffmpeg/bin/ffprobe"],
        });
      }),
      http.post("/api/tools/installations", async ({ request }) => {
        starts(await request.json());
        return HttpResponse.json(
          { detail: "stale preflight" },
          { status: 409 },
        );
      }),
    );
    localStorage.setItem("melotrove.setup.step", "tools");
    renderSetup(current);

    const picker = await screen.findByRole("button", {
      name: /Версия · ffmpeg/,
    });
    await waitFor(() => expect(picker).toBeEnabled());
    fireEvent.click(picker);
    fireEvent.click(await screen.findByRole("option", { name: "8.0.1" }));
    const install = screen.getByRole("button", { name: "Установить FFmpeg" });
    await waitFor(() => expect(install).toBeEnabled());
    fireEvent.click(install);

    const dialog = await screen.findByRole("dialog", {
      name: "Перезапись только следующих файлов:",
    });
    expect(preflights).toHaveBeenCalledExactlyOnceWith({
      package_kind: "ffmpeg",
      release_identity: "8.0.1",
    });
    expect(starts).not.toHaveBeenCalled();
    expect(within(dialog).getByText(conflict)).toBeVisible();
    fireEvent.click(
      within(dialog).getByRole("button", { name: "Подтвердить перезапись" }),
    );
    const problem = await within(dialog).findByRole("alert");
    expect(problem).toBeVisible();
    expect(starts).toHaveBeenCalledExactlyOnceWith({
      preflight_token: "setup-preflight-token",
      confirmed_conflicts: [conflict],
    });
  });

  it("requires a catalog proof for the verified provider before continuing", async () => {
    let current = state(installed);
    const lrclib = vi.fn();
    let acceptLrclib = () => {};
    const held = new Promise<void>((resolve) => {
      acceptLrclib = resolve;
    });
    const saved = vi.fn((body: Partial<SetupStateBody["settings"]>) => {
      current = {
        ...current,
        settings: { ...current.settings, ...body },
      };
      return new HttpResponse(null, { status: 204 });
    });
    const checkedPaths = vi.fn();
    const savedProvider = vi.fn(async ({ request }: { request: Request }) => {
      await request.json();
      current = {
        ...current,
        musicbrainz_configuration_identity: "identity-1",
      };
      return HttpResponse.json({ configuration_identity: "identity-1" });
    });
    const checkedProvider = vi.fn(async ({ request }: { request: Request }) => {
      await request.json();
      current = {
        ...current,
        settings: {
          ...current.settings,
          musicbrainz_verified_at: "2026-11-20T12:00:00Z",
        },
      };
      return HttpResponse.json({ success: true });
    });
    const catalogBodies = vi.fn();
    let catalogAttempt = 0;
    const failedOperation = catalogOperation("catalog-op-1", "failed");
    const readyOperation = catalogOperation("catalog-op-2", "succeeded");
    server.use(
      http.get("/api/setup", () => HttpResponse.json(current)),
      http.put("/api/setup/runtime", async ({ request }) =>
        saved((await request.json()) as Partial<SetupStateBody["settings"]>),
      ),
      http.post("/api/setup/paths/check", async ({ request }) => {
        checkedPaths(await request.json());
        return HttpResponse.json(paths);
      }),
      http.put("/api/setup/musicbrainz", savedProvider),
      http.post("/api/setup/musicbrainz/check", checkedProvider),
      http.post("/api/setup/genre-catalog/refresh", async ({ request }) => {
        catalogBodies(await request.json());
        catalogAttempt += 1;
        if (catalogAttempt === 1) {
          current = {
            ...current,
            genre_catalog: { ready: false, status: "failed", count: 0 },
          };
          return HttpResponse.json(failedOperation, { status: 202 });
        }
        current = {
          ...current,
          genre_catalog: { ready: true, status: "ready", count: 120 },
        };
        return HttpResponse.json(readyOperation, { status: 202 });
      }),
      http.get("/api/operations/catalog-op-1", () =>
        HttpResponse.json(failedOperation),
      ),
      http.get("/api/operations/catalog-op-2", () =>
        HttpResponse.json(readyOperation),
      ),
      http.put("/api/setup/lrclib", async ({ request }) => {
        lrclib(await request.json());
        await held;
        return new HttpResponse(null, { status: 204 });
      }),
    );
    renderSetup(current);
    fireEvent.click(screen.getByRole("radio", { name: "Исходный формат" }));
    fireEvent.click(screen.getByRole("checkbox", { name: "LRCLIB включён" }));
    expect(screen.getByRole("button", { name: "Продолжить" })).toBeDisabled();
    const load = screen.getByRole("button", { name: "Загрузить каталог" });
    expect(load).toBeDisabled();
    fireEvent.click(load);
    expect(savedProvider).not.toHaveBeenCalled();
    expect(checkedProvider).not.toHaveBeenCalled();
    expect(catalogBodies).not.toHaveBeenCalled();
    await click("Проверить");
    await waitFor(() => expect(checkedProvider).toHaveBeenCalledTimes(1));
    expect(savedProvider).toHaveBeenCalledTimes(1);
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "Загрузить каталог" }),
      ).toBeEnabled(),
    );
    await click("Загрузить каталог");
    await waitFor(() => expect(catalogBodies).toHaveBeenCalledTimes(1));
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Повторить" })).toBeVisible(),
    );
    await click("Повторить");
    await waitFor(() => expect(catalogBodies).toHaveBeenCalledTimes(2));
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "Продолжить" }),
      ).not.toBeDisabled(),
    );
    expect(savedProvider).toHaveBeenCalledTimes(1);
    expect(checkedProvider).toHaveBeenCalledTimes(1);
    expect(catalogBodies).toHaveBeenNthCalledWith(1, {
      configuration_identity: "identity-1",
    });
    expect(catalogBodies).toHaveBeenNthCalledWith(2, {
      configuration_identity: "identity-1",
    });
    await click("Продолжить");
    try {
      await waitFor(() =>
        expect(lrclib).toHaveBeenCalledExactlyOnceWith({ enabled: false }),
      );
      expect(checkedPaths).toHaveBeenLastCalledWith(paths);
      expect(saved).toHaveBeenLastCalledWith({
        output_directory: paths.output_directory,
        publication_format: "source",
        publication_remove_sources: false,
      });
      expect(screen.getByRole("button", { name: "Назад" })).toBeDisabled();
      expect(screen.getByRole("button", { name: "Продолжить" })).toBeDisabled();
    } finally {
      acceptLrclib();
    }
    await waitFor(() => expect(heading("Итог")).toBeVisible());
  });

  it("clears the failed MusicBrainz check after a corrected URL loads genres", async () => {
    const homepage = "https://musicbrainz.example.org";
    const apiURL = `${homepage}/ws/2`;
    const connectivityError =
      "MusicBrainz connectivity check failed. Retry the check.";
    const localizedError =
      /не удалось проверить подключение к API MusicBrainz/i;
    const verifiedAt = "2026-11-27T10:37:41.039981848Z";
    let current = state({
      ...installed,
      musicbrainz_mode: "self-hosted",
      musicbrainz_base_url: homepage,
    });
    const operation = catalogOperation("catalog-after-retry", "succeeded");
    const checks = vi.fn();
    server.use(
      http.get("/api/setup", () => HttpResponse.json(current)),
      http.put("/api/setup/musicbrainz", async ({ request }) => {
        const { base_url } = (await request.json()) as { base_url: string };
        current = state(
          { ...current.settings, musicbrainz_base_url: base_url },
          false,
          { musicbrainz_configuration_identity: base_url },
        );
        return HttpResponse.json({ configuration_identity: base_url });
      }),
      http.post("/api/setup/musicbrainz/check", () => {
        checks();
        if (current.settings.musicbrainz_base_url !== apiURL)
          return HttpResponse.json({
            success: false,
            error: connectivityError,
          });
        current.settings.musicbrainz_verified_at = verifiedAt;
        return HttpResponse.json({ success: true });
      }),
      http.post("/api/setup/genre-catalog/refresh", () => {
        current.genre_catalog = { ready: true, status: "ready", count: 120 };
        return HttpResponse.json(operation, { status: 202 });
      }),
      http.get("/api/operations/catalog-after-retry", () =>
        HttpResponse.json(operation),
      ),
    );
    renderSetup(current);
    const field = screen.getByRole("textbox", {
      name: "Базовый URL MusicBrainz",
    });
    expect(
      screen.getByRole("button", { name: "Загрузить каталог" }),
    ).toBeDisabled();
    await click("Проверить");
    await waitFor(() => expect(field).toHaveAttribute("aria-invalid", "true"));
    expect(field).toHaveAccessibleDescription(localizedError);
    expect(screen.queryByText(connectivityError)).not.toBeInTheDocument();
    fireEvent.change(field, { target: { value: apiURL } });
    expect(field).not.toHaveAttribute("aria-invalid", "true");
    expect(
      screen.getByRole("button", { name: "Загрузить каталог" }),
    ).toBeDisabled();
    fireEvent.blur(field);
    await waitFor(() => expect(checks).toHaveBeenCalledTimes(2));
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "Загрузить каталог" }),
      ).toBeEnabled(),
    );
    await click("Загрузить каталог");
    expect(await screen.findByText(/доступно жанров: 120/i)).toBeVisible();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(field).not.toHaveAttribute("aria-invalid", "true");
    expect(field).not.toHaveAccessibleDescription(localizedError);
    expect(screen.queryByText(verifiedAt)).not.toBeInTheDocument();
    expect(checks).toHaveBeenCalledTimes(2);
  });

  it("checks the public MusicBrainz service as soon as it is selected", async () => {
    let current = state({
      ...installed,
      musicbrainz_mode: "self-hosted",
      musicbrainz_base_url: "https://musicbrainz.example.org/ws/2",
    });
    const saved = vi.fn();
    const checked = vi.fn();
    const loadedCatalog = vi.fn();
    server.use(
      http.get("/api/setup", () => HttpResponse.json(current)),
      http.put("/api/setup/musicbrainz", async ({ request }) => {
        saved(await request.json());
        current = state({ ...installed, musicbrainz_mode: "public" }, false, {
          musicbrainz_configuration_identity: "public-identity",
        });
        return HttpResponse.json({
          configuration_identity: "public-identity",
        });
      }),
      http.post("/api/setup/musicbrainz/check", async ({ request }) => {
        checked(await request.json());
        current.settings.musicbrainz_verified_at = "2026-11-20T12:00:00Z";
        return HttpResponse.json({ success: true });
      }),
      http.post("/api/setup/genre-catalog/refresh", () => {
        loadedCatalog();
        return HttpResponse.json({}, { status: 500 });
      }),
    );
    renderSetup(current);
    const load = screen.getByRole("button", { name: "Загрузить каталог" });
    expect(load).toBeDisabled();
    fireEvent.click(screen.getByRole("radio", { name: "Публичный сервис" }));
    await waitFor(() => expect(load).toBeEnabled());
    expect(saved).toHaveBeenCalledExactlyOnceWith({
      mode: "public",
      base_url: "",
    });
    expect(checked).toHaveBeenCalledExactlyOnceWith({
      configuration_identity: "public-identity",
    });
    expect(loadedCatalog).not.toHaveBeenCalled();
  });

  it("completes setup with the identity rendered by the summary", async () => {
    const ready = state(
      {
        ...installed,
        output_directory: paths.output_directory,
        publication_format: "source",
        musicbrainz_verified_at: "2026-11-20T12:00:00Z",
      },
      true,
      {
        genre_catalog: { ready: true, status: "ready", count: 120 },
        musicbrainz_configuration_identity: "identity-1",
      },
    );
    localStorage.setItem("melotrove.setup.step", "summary");
    const completed = vi.fn();
    const onCompleted = vi.fn();
    const readSetup = vi.fn();
    server.use(
      http.get("/api/setup", () => {
        readSetup();
        return HttpResponse.json(ready);
      }),
      http.post("/api/setup/complete", async ({ request }) => {
        completed(await request.json());
        return new HttpResponse(null, { status: 204 });
      }),
    );
    renderSetup(ready, onCompleted);
    const recheck = screen.getByRole("button", { name: "Обновить проверку" });
    const readsBeforeRecheck = readSetup.mock.calls.length;
    await click("Обновить проверку");
    await waitFor(() => {
      expect(readSetup.mock.calls.length).toBeGreaterThan(readsBeforeRecheck);
      const currentRecheck = screen.getByRole("button", {
        name: "Обновить проверку",
      });
      expect(currentRecheck).toBe(recheck);
      expect(currentRecheck).toBeEnabled();
    });
    await click("Завершить настройку");
    await waitFor(() =>
      expect(completed).toHaveBeenCalledWith({
        configuration_identity: "identity-1",
      }),
    );
    await waitFor(() => expect(onCompleted).toHaveBeenCalledTimes(1));
  });

  it("returns a stale restored summary to the publication step", async () => {
    const ready = state(
      {
        ...installed,
        output_directory: paths.output_directory,
        publication_format: "source",
        musicbrainz_verified_at: "2026-11-20T12:00:00Z",
      },
      true,
      {
        genre_catalog: { ready: true, status: "ready", count: 120 },
        musicbrainz_configuration_identity: "identity-1",
      },
    );
    let current = ready;
    localStorage.setItem("melotrove.setup.step", "summary");
    const completed = vi.fn();
    server.use(
      http.get("/api/setup", () => HttpResponse.json(current)),
      http.post("/api/setup/complete", async ({ request }) => {
        completed(await request.json());
        return new HttpResponse(null, { status: 204 });
      }),
    );
    renderSetup(ready);
    current = {
      ...current,
      musicbrainz_configuration_identity: "identity-2",
    };
    await click("Завершить настройку");
    await waitFor(() => expect(heading("Публикация")).toBeVisible());
    expect(completed).not.toHaveBeenCalled();
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "больше не соответствует",
    );
  });

  it("prefers a valid step hash over the stored navigation hint", async () => {
    const ready = state(
      {
        ...installed,
        output_directory: paths.output_directory,
        publication_format: "source",
        musicbrainz_verified_at: "2026-11-20T12:00:00Z",
      },
      true,
      {
        genre_catalog: { ready: true, status: "ready", count: 120 },
        musicbrainz_configuration_identity: "identity-1",
      },
    );
    localStorage.setItem("melotrove.setup.step", "tools");
    setSetupHash("summary");

    renderSetup(ready);

    expect(heading("Итог")).toBeVisible();
    await waitFor(() =>
      expect(window.location.hash).toBe("#/setup?step=summary"),
    );
  });

  it("clamps a direct high step hash to the earliest incomplete step", async () => {
    localStorage.setItem("melotrove.setup.step", "summary");
    setSetupHash("summary");

    renderSetup(state());

    expect(heading("Каталоги")).toBeVisible();
    await waitFor(() =>
      expect(window.location.hash).toBe("#/setup?step=directories"),
    );
  });

  it("does not open tools before the tools are active", async () => {
    const current = state(paths);
    server.use(http.get("/api/setup", () => HttpResponse.json(current)));
    setSetupHash("summary");

    renderSetup(current);

    expect(heading("Инструменты")).toBeVisible();
    await waitFor(() =>
      expect(window.location.hash).toBe("#/setup?step=tools"),
    );
  });

  it("replaces an unknown step hash with the canonical first step", async () => {
    localStorage.setItem("melotrove.setup.step", "summary");
    setSetupHash("unknown");

    renderSetup(state());

    expect(heading("Каталоги")).toBeVisible();
    await waitFor(() =>
      expect(window.location.hash).toBe("#/setup?step=directories"),
    );
  });

  it("keeps unsaved publication values and the step hash across a remount", async () => {
    const current = state(
      {
        ...installed,
        musicbrainz_verified_at: "2026-11-20T12:00:00Z",
      },
      false,
      {
        genre_catalog: { ready: true, status: "ready", count: 120 },
        musicbrainz_configuration_identity: "identity-1",
      },
    );
    server.use(http.get("/api/setup", () => HttpResponse.json(current)));
    setSetupHash("publication");

    const first = renderSetup(current);
    fireEvent.click(screen.getByRole("radio", { name: "Свой сервер" }));
    const url = screen.getByRole("textbox", {
      name: "Базовый URL MusicBrainz",
    });
    const draftURL = "https://musicbrainz.example.org/ws/2";
    fireEvent.change(url, { target: { value: draftURL } });
    fireEvent.click(screen.getByRole("radio", { name: "M4A" }));
    fireEvent.click(screen.getByRole("checkbox", { name: "LRCLIB включён" }));
    expect(url).toHaveValue(draftURL);
    expect(screen.getByRole("radio", { name: "M4A" })).toBeChecked();
    first.unmount();

    renderSetup(current);

    expect(
      await screen.findByRole("textbox", { name: "Базовый URL MusicBrainz" }),
    ).toHaveValue(draftURL);
    expect(screen.getByRole("radio", { name: "M4A" })).toBeChecked();
    expect(
      screen.getByRole("checkbox", { name: "LRCLIB включён" }),
    ).not.toBeChecked();
    expect(window.location.hash).toBe("#/setup?step=publication");
  });

  it("masks an existing AcoustID key and shows verification only after an accepted save", async () => {
    let current = state({ ...installed, has_acoustid_application_key: true });
    const keyURL = "/api/settings/acoustid/application-key";
    const rejected = "rejected-test-key";
    const accepted = "accepted-test-key";
    const checked = vi.fn();
    server.use(
      http.get("/api/setup", () => HttpResponse.json(current)),
      http.put(keyURL, async ({ request }) => {
        const { key } = (await request.json()) as { key: string };
        checked(key);
        if (key === rejected) {
          return HttpResponse.json(
            { detail: "invalid API key" },
            { status: 422 },
          );
        }
        current = state({
          ...installed,
          has_acoustid_application_key: true,
          acoustid_application_key_verified: true,
        });
        return new HttpResponse(null, { status: 204 });
      }),
      http.delete(keyURL, () => {
        current = state(installed);
        return new HttpResponse(null, { status: 204 });
      }),
    );
    setSetupHash("publication");
    const field = () => screen.getByLabelText("Ключ приложения AcoustID");
    const verified = { name: "Ключ AcoustID проверен." };
    const first = renderSetup(current);
    expect(field()).toHaveValue("");
    expect(field().getAttribute("placeholder")).toMatch(/^•+$/);
    expect(screen.queryByRole("img", verified)).not.toBeInTheDocument();

    fireEvent.change(field(), { target: { value: rejected } });
    await click("Сохранить ключ");
    await waitFor(() =>
      expect(field()).toHaveAttribute("aria-invalid", "true"),
    );
    expect(field()).toHaveAccessibleDescription(/AcoustID отклонил/);
    expect(field()).toHaveValue(rejected);
    expect(screen.queryByText("invalid API key")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Удалить ключ" })).toBeEnabled();

    fireEvent.change(field(), { target: { value: accepted } });
    await click("Сохранить ключ");
    expect(await screen.findByRole("img", verified)).toBeInTheDocument();
    expect(checked.mock.calls).toEqual([[rejected], [accepted]]);
    expect(field()).toHaveValue("");
    expect(field()).not.toHaveAttribute("aria-invalid");
    first.unmount();

    renderSetup(current);
    expect(field()).toHaveValue("");
    expect(screen.getByRole("img", verified)).toBeInTheDocument();
    await click("Удалить ключ");
    await waitFor(() => expect(field()).not.toHaveAttribute("placeholder"));
    expect(screen.queryByRole("img", verified)).not.toBeInTheDocument();
  });

  it("never persists entered secrets across a remount", async () => {
    const current = state(
      {
        ...installed,
        musicbrainz_verified_at: "2026-11-20T12:00:00Z",
      },
      false,
      {
        genre_catalog: { ready: true, status: "ready", count: 120 },
        musicbrainz_configuration_identity: "identity-1",
      },
    );
    server.use(http.get("/api/setup", () => HttpResponse.json(current)));
    setSetupHash("publication");
    const secret = "acoustid-secret-value-93f2";
    const password = "musicbrainz-url-password-93f2";
    const endpoint = "https://musicbrainz.example.org/ws/2";

    const first = renderSetup(current);
    fireEvent.change(screen.getByLabelText("Ключ приложения AcoustID"), {
      target: { value: secret },
    });
    fireEvent.click(screen.getByRole("radio", { name: "Свой сервер" }));
    const url = screen.getByRole("textbox", {
      name: "Базовый URL MusicBrainz",
    });
    for (const value of [
      `https://listener:${password}@musicbrainz.example.org/ws/2`,
      `${endpoint}?token=${password}`,
      `${endpoint}#${password}`,
    ]) {
      fireEvent.change(url, { target: { value } });
      for (const storage of [window.sessionStorage, window.localStorage]) {
        for (let index = 0; index < storage.length; index += 1) {
          const key = storage.key(index);
          const stored = key === null ? "" : storage.getItem(key);
          expect(stored).not.toContain(secret);
          expect(stored).not.toContain(password);
        }
      }
    }
    first.unmount();

    renderSetup(current);
    expect(screen.getByLabelText("Ключ приложения AcoustID")).toHaveValue("");
    expect(window.location.hash).toBe("#/setup?step=publication");
  });

  it("keeps unsaved publication changes in view when a Summary link is opened", async () => {
    const ready = state(
      {
        ...installed,
        publication_format: "source",
        musicbrainz_verified_at: "2026-11-20T12:00:00Z",
      },
      true,
      {
        genre_catalog: { ready: true, status: "ready", count: 120 },
        musicbrainz_configuration_identity: "identity-1",
      },
    );
    setSetupHash("publication");
    const first = renderSetup(ready);
    fireEvent.click(screen.getByRole("radio", { name: "M4A" }));
    first.unmount();

    setSetupHash("summary");
    renderSetup(ready);
    expect(heading("Публикация")).toBeVisible();
    expect(screen.getByRole("radio", { name: "M4A" })).toBeChecked();
    await waitFor(() =>
      expect(window.location.hash).toBe("#/setup?step=publication"),
    );
  });

  it("saves an explicit false auto-complete choice restored from the draft", async () => {
    const current = state(
      {
        ...installed,
        output_directory: paths.output_directory,
        publication_format: "source",
        publication_auto_complete_albums: true,
        musicbrainz_verified_at: "2026-11-20T12:00:00Z",
      },
      false,
      {
        genre_catalog: { ready: true, status: "ready", count: 120 },
        musicbrainz_configuration_identity: "identity-1",
      },
    );
    const saved = vi.fn((body: Partial<SetupStateBody["settings"]>) => {
      current.settings = { ...current.settings, ...body };
      return new HttpResponse(null, { status: 204 });
    });
    server.use(
      http.get("/api/setup", () => HttpResponse.json(current)),
      http.post("/api/setup/paths/check", () => HttpResponse.json(paths)),
      http.put("/api/setup/runtime", async ({ request }) =>
        saved((await request.json()) as Partial<SetupStateBody["settings"]>),
      ),
      http.put(
        "/api/setup/lrclib",
        () => new HttpResponse(null, { status: 204 }),
      ),
    );
    seedAutoCompleteAlbumsDraft(false, true);
    setSetupHash("publication");

    renderSetup(current);
    await click("Продолжить");

    await waitFor(() => expect(saved).toHaveBeenCalledTimes(1));
    expect(saved).toHaveBeenCalledWith(
      expect.objectContaining({ publication_auto_complete_albums: false }),
    );
    const setting = await screen.findByText("Автопубликация", {
      selector: "dt",
    });
    expect(setting.nextElementSibling).toHaveTextContent("Выключена");
  });

  it("saves auto-complete enabled from the publication checkbox", async () => {
    const current = state(
      {
        ...installed,
        output_directory: paths.output_directory,
        publication_format: "source",
        publication_auto_complete_albums: false,
        musicbrainz_verified_at: "2026-11-20T12:00:00Z",
      },
      false,
      {
        genre_catalog: { ready: true, status: "ready", count: 120 },
        musicbrainz_configuration_identity: "identity-1",
      },
    );
    const saved = vi.fn((body: Partial<SetupStateBody["settings"]>) => {
      current.settings = { ...current.settings, ...body };
      return new HttpResponse(null, { status: 204 });
    });
    server.use(
      http.get("/api/setup", () => HttpResponse.json(current)),
      http.post("/api/setup/paths/check", () => HttpResponse.json(paths)),
      http.put("/api/setup/runtime", async ({ request }) =>
        saved((await request.json()) as Partial<SetupStateBody["settings"]>),
      ),
      http.put(
        "/api/setup/lrclib",
        () => new HttpResponse(null, { status: 204 }),
      ),
    );
    setSetupHash("publication");

    renderSetup(current);
    const checkbox = screen.getByRole("checkbox", {
      name: "Автоматически публиковать полностью сопоставленные альбомы",
    });
    expect(checkbox).not.toBeChecked();
    fireEvent.click(checkbox);
    expect(checkbox).toBeChecked();
    await click("Продолжить");

    await waitFor(() => expect(saved).toHaveBeenCalledTimes(1));
    expect(saved).toHaveBeenCalledWith(
      expect.objectContaining({ publication_auto_complete_albums: true }),
    );
  });

  it("keeps the current auto-complete setting when the draft was based on another value", async () => {
    const current = state(
      {
        ...installed,
        output_directory: paths.output_directory,
        publication_format: "source",
        publication_auto_complete_albums: true,
        musicbrainz_verified_at: "2026-11-20T12:00:00Z",
      },
      false,
      {
        genre_catalog: { ready: true, status: "ready", count: 120 },
        musicbrainz_configuration_identity: "identity-1",
      },
    );
    const saved = vi.fn((body: Partial<SetupStateBody["settings"]>) => {
      current.settings = { ...current.settings, ...body };
      return new HttpResponse(null, { status: 204 });
    });
    server.use(
      http.get("/api/setup", () => HttpResponse.json(current)),
      http.post("/api/setup/paths/check", () => HttpResponse.json(paths)),
      http.put("/api/setup/runtime", async ({ request }) =>
        saved((await request.json()) as Partial<SetupStateBody["settings"]>),
      ),
      http.put(
        "/api/setup/lrclib",
        () => new HttpResponse(null, { status: 204 }),
      ),
    );
    seedAutoCompleteAlbumsDraft(false, false);
    setSetupHash("publication");

    renderSetup(current);
    expect(
      screen.getByRole("checkbox", {
        name: "Автоматически публиковать полностью сопоставленные альбомы",
      }),
    ).toBeChecked();
    await click("Продолжить");

    await waitFor(() => expect(saved).toHaveBeenCalledTimes(1));
    expect(saved.mock.calls[0]?.[0]).not.toHaveProperty(
      "publication_auto_complete_albums",
    );
  });

  it("keeps an explicit false auto-complete draft blocking a Summary link across a remount", async () => {
    const ready = state(
      {
        ...installed,
        output_directory: paths.output_directory,
        publication_format: "source",
        publication_auto_complete_albums: true,
        musicbrainz_verified_at: "2026-11-20T12:00:00Z",
      },
      true,
      {
        genre_catalog: { ready: true, status: "ready", count: 120 },
        musicbrainz_configuration_identity: "identity-1",
      },
    );
    server.use(http.get("/api/setup", () => HttpResponse.json(ready)));
    seedAutoCompleteAlbumsDraft(false, true);
    setSetupHash("summary");

    const first = renderSetup(ready);
    expect(heading("Публикация")).toBeVisible();
    await waitFor(() =>
      expect(window.location.hash).toBe("#/setup?step=publication"),
    );
    first.unmount();

    setSetupHash("summary");
    renderSetup(ready);
    expect(heading("Публикация")).toBeVisible();
    await waitFor(() =>
      expect(window.location.hash).toBe("#/setup?step=publication"),
    );
  });

  it("restores steps on browser Back and Forward without another setup save", async () => {
    setSetupHash("publication");
    renderSetup(state(installed));
    expect(heading("Публикация")).toBeVisible();

    await click("Назад");
    await waitFor(() => expect(heading("Инструменты")).toBeVisible());
    expect(window.location.hash).toBe("#/setup?step=tools");

    window.history.back();
    await waitFor(() => expect(heading("Публикация")).toBeVisible());
    expect(window.location.hash).toBe("#/setup?step=publication");

    window.history.forward();
    await waitFor(() => expect(heading("Инструменты")).toBeVisible());
    expect(window.location.hash).toBe("#/setup?step=tools");
  });

  it("does not finish setup if browser Back leaves Summary during its last read", async () => {
    const ready = state(
      {
        ...installed,
        publication_format: "source",
        musicbrainz_verified_at: "2026-11-20T12:00:00Z",
      },
      true,
      {
        genre_catalog: { ready: true, status: "ready", count: 120 },
        musicbrainz_configuration_identity: "identity-1",
      },
    );
    let releaseRead = () => {};
    const held = new Promise<void>((resolve) => {
      releaseRead = resolve;
    });
    const reading = vi.fn();
    const complete = vi.fn(() => new HttpResponse(null, { status: 204 }));
    server.use(
      http.get("/api/setup", async () => {
        reading();
        await held;
        return HttpResponse.json(ready);
      }),
      http.post("/api/setup/complete", complete),
    );
    setSetupHash("publication");
    window.history.pushState(null, "", "#/setup?step=summary");
    renderSetup(ready);
    expect(heading("Итог")).toBeVisible();

    try {
      await click("Завершить настройку");
      await waitFor(() => expect(reading).toHaveBeenCalled());
      window.history.back();
      await waitFor(() => expect(heading("Публикация")).toBeVisible());
    } finally {
      releaseRead();
    }
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Продолжить" })).toBeEnabled(),
    );
    expect(complete).not.toHaveBeenCalled();
    expect(window.location.hash).toBe("#/setup?step=publication");
  });
});
