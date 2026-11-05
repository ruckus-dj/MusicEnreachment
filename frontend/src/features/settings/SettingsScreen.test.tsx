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
});
