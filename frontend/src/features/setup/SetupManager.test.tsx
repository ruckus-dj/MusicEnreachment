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
import type { SetupStateBody } from "../../api/generated/client.schemas";
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

describe("SetupManager", () => {
  it("requires an explicit format and submits the current publication settings", async () => {
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
    expect(saved).toHaveBeenCalledWith({
      publication_format: "source",
      publication_remove_sources: false,
    });
  });
});
