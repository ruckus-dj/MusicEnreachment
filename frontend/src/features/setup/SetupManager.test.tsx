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
  OperationResponse,
  SetupStateBody,
} from "../../api/generated/client.schemas";
import { I18nProvider, useI18n } from "../../i18n/I18nProvider";
import { createMemoryStorage } from "../../test/memoryStorage";
import { server } from "../../test/server";
import { SetupManager } from "./SetupManager";

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
});
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
    await click("Загрузить каталог");
    await waitFor(() => expect(checkedProvider).toHaveBeenCalledTimes(1));
    expect(savedProvider).toHaveBeenCalledTimes(1);
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
    server.use(
      http.get("/api/setup", () => HttpResponse.json(ready)),
      http.post("/api/setup/complete", async ({ request }) => {
        completed(await request.json());
        return new HttpResponse(null, { status: 204 });
      }),
    );
    renderSetup(ready, onCompleted);
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
});
