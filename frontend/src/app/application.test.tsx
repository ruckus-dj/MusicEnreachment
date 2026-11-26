import "@testing-library/jest-dom/vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { act, useState } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type {
  SetupStateBody,
  SourceRootResponse,
} from "../api/generated/client.schemas";
import { incomingGroupsListHref } from "../providers/incoming/incomingGroupsRoute";
import { productionMeloTroveProvider } from "../providers/meloTroveProvider";
import {
  resetSettingsDraftMemory,
  settingsDraftStorageKey,
} from "../providers/settings/settingsDraft";
import { createMemoryStorage } from "../test/memoryStorage";
import { server } from "../test/server";
import { MeloTroveUI } from "../ui/app/MeloTroveUI";
import { I18nProvider } from "./i18n/host";

function Application() {
  const [queryClient] = useState(() => new QueryClient());

  return (
    <QueryClientProvider client={queryClient}>
      <I18nProvider>
        <MeloTroveUI provider={productionMeloTroveProvider} />
      </I18nProvider>
    </QueryClientProvider>
  );
}

const state: SetupStateBody = {
  completed: false,
  musicbrainz_configuration_identity: "",
  genre_catalog: { ready: false, status: "idle", count: 0 },
  platform: {
    diagnostic: false,
    supported: true,
    goos: "linux",
    goarch: "amd64",
  },
  settings: {
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
  processing_mode: "in_place",
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
  window.location.hash = "";
  const localStorage = createMemoryStorage();
  localStorage.setItem("melotrove-language", "ru");
  vi.stubGlobal("localStorage", localStorage);
  vi.stubGlobal("sessionStorage", createMemoryStorage());
  resetSettingsDraftMemory();
  server.use(
    http.get("/api/sources", () => HttpResponse.json({ sources: [] })),
    http.get("/api/incoming-groups", () =>
      HttpResponse.json({ epoch: 0, revision: "", groups: [], files: [] }),
    ),
    http.get("/api/incoming-groups/count", () =>
      HttpResponse.json({ count: 0 }),
    ),
    http.get("/api/operations", () => HttpResponse.json({ operations: [] })),
    http.get("/api/operations/current", () =>
      HttpResponse.json({ operations: [], total: 0 }),
    ),
    http.get("/api/sources/:sourceId/locations", () =>
      HttpResponse.json({ locations: [] }),
    ),
  );
});
afterEach(() => {
  cleanup();
  resetSettingsDraftMemory();
  window.location.hash = "";
  vi.unstubAllGlobals();
});
describe("application integration", () => {
  it("provides a working skip link and one accessible current-section navigation", async () => {
    const setupRequests = vi.fn();
    server.use(
      http.get("/api/setup", () => {
        setupRequests();
        return HttpResponse.json(completedState);
      }),
    );
    window.location.hash = "/operations";
    render(<Application />);

    const navigation = await screen.findByRole("navigation", {
      name: "Основная навигация",
    });
    expect(navigation.querySelectorAll("a")).toHaveLength(6);
    expect(
      screen.queryByRole("link", { name: "Публикации" }),
    ).not.toBeInTheDocument();
    expect(
      screen.getByRole("link", { name: "Операции", current: "page" }),
    ).toBeInTheDocument();
    const content = screen.getByRole("main");
    const shellHeader = screen
      .getAllByRole("banner")
      .find((header) => !content.contains(header));
    if (!shellHeader) throw new Error("Missing shell header");
    expect(shellHeader).toHaveTextContent("Операции");

    fireEvent.click(screen.getByRole("button", { name: /Язык интерфейса/ }));
    fireEvent.click(await screen.findByRole("option", { name: "English" }));
    expect(
      await screen.findByRole("navigation", { name: "Main navigation" }),
    ).toHaveTextContent(
      /Incoming\d*LibraryOwn catalogueSourcesOperationsSettings/,
    );
    expect(
      screen.getByRole("link", { name: "Operations", current: "page" }),
    ).toBeInTheDocument();
    expect(shellHeader).toHaveTextContent("Operations");
    expect(setupRequests).toHaveBeenCalledTimes(1);

    expect(content).toHaveAttribute("id", "content");
    expect(
      within(shellHeader).getByRole("link", {
        name: "MeloTrove",
      }),
    ).toHaveAttribute("href", "#/");
    expect(content).toHaveAttribute("tabindex", "-1");
    fireEvent.click(screen.getByRole("button", { name: "Skip to content" }));
    expect(document.activeElement).toBe(content);
    expect(window.location.hash).toBe("#/operations");
  });

  it("loads source roots through the production application root without a supplied query context", async () => {
    server.use(
      http.get("/api/setup", () => HttpResponse.json(completedState)),
      http.get("/api/operations", () => HttpResponse.json({ operations: [] })),
      http.get("/api/sources", () =>
        HttpResponse.json({ sources: [sourceRoot] }),
      ),
    );
    window.location.hash = "/sources";
    render(<Application />);
    expect(
      await screen.findByRole("heading", { level: 1, name: "Источники" }),
    ).toBeVisible();
    const grid = within(
      await screen.findByRole("grid", { name: "Подключённые каталоги" }),
    );
    expect(await grid.findByText("/srv/inbox")).toBeVisible();
  });

  it("guards dirty Settings drafts across anchor and browser navigation attempts", async () => {
    const settingsState: SetupStateBody = {
      ...completedState,
      settings: {
        ...completedState.settings,
        tools_directory: "/srv/tools",
        output_directory: "/srv/music",
        work_directory: "/srv/work",
        publication_path_template: "{TRACK_NAME}.{EXT}",
      },
    };
    server.use(
      http.get("/api/setup", () => HttpResponse.json(completedState)),
      http.get("/api/settings", () => HttpResponse.json(settingsState)),
      http.get("/api/tools/installations", () =>
        HttpResponse.json({ installations: [] }),
      ),
      http.get("/api/operations", () => HttpResponse.json({ operations: [] })),
      http.get("/api/genres/catalog", () =>
        HttpResponse.json({
          count: 0,
          items: [],
          last_attempt_at: null,
          last_error: null,
          refresh_status: "idle",
          active_operation_id: null,
          updated_at: null,
        }),
      ),
      http.get("/api/tools/catalog", ({ request }) =>
        HttpResponse.json({
          package_kind: new URL(request.url).searchParams.get("package_kind"),
          releases: [],
        }),
      ),
    );
    window.location.hash = "/settings";
    const view = render(<Application />);

    expect(
      await screen.findByRole("heading", { level: 1, name: "Настройки" }),
    ).toBeVisible();
    const template = await screen.findByLabelText("Шаблон пути публикации");
    act(() => {
      fireEvent.change(template, {
        target: { value: "draft-{TRACK_NAME}.{EXT}" },
      });
      fireEvent.click(screen.getByRole("link", { name: "Операции" }));
    });

    expect(
      await screen.findByRole("dialog", { name: "Несохранённые настройки" }),
    ).toBeVisible();
    const operationsLink = screen.getByRole("link", { name: "Операции" });
    fireEvent.click(screen.getByRole("button", { name: "Остаться" }));
    expect(window.location.hash).toBe("#/settings");
    expect(
      screen.queryByRole("dialog", { name: "Несохранённые настройки" }),
    ).not.toBeInTheDocument();
    expect(document.activeElement).toBe(operationsLink);

    fireEvent.click(operationsLink);
    expect(
      await screen.findByRole("dialog", { name: "Несохранённые настройки" }),
    ).toBeVisible();
    view.rerender(<Application />);
    fireEvent.click(
      screen.getByRole("button", { name: "Уйти, сохранив черновик" }),
    );

    expect(
      await screen.findByRole("link", { name: "Операции", current: "page" }),
    ).toBeVisible();
    expect(sessionStorage.getItem(settingsDraftStorageKey)).toContain(
      "draft-{TRACK_NAME}.{EXT}",
    );

    fireEvent.click(screen.getByRole("link", { name: "Настройки" }));
    expect(
      await screen.findByRole("heading", { level: 1, name: "Настройки" }),
    ).toBeVisible();
    expect(
      await screen.findByDisplayValue("draft-{TRACK_NAME}.{EXT}"),
    ).toBeVisible();

    act(() => {
      window.history.pushState(null, "", "/#/operations");
      window.dispatchEvent(new HashChangeEvent("hashchange"));
    });
    expect(
      await screen.findByRole("dialog", { name: "Несохранённые настройки" }),
    ).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: "Остаться" }));
    expect(window.location.hash).toBe("#/settings");

    act(() => {
      window.history.pushState(null, "", "/#/operations");
      window.dispatchEvent(new HashChangeEvent("hashchange"));
    });
    expect(
      await screen.findByRole("dialog", { name: "Несохранённые настройки" }),
    ).toBeVisible();
    fireEvent.click(
      screen.getByRole("button", { name: "Уйти, сохранив черновик" }),
    );
    expect(
      await screen.findByRole("link", { name: "Операции", current: "page" }),
    ).toBeVisible();
  });

  it("keeps the legacy publications URL inside the library without standalone navigation", async () => {
    server.use(
      http.get("/api/setup", () => HttpResponse.json(completedState)),
      http.get("*/api/library/artists", () =>
        HttpResponse.json({
          items: [],
          total: 0,
          limit: 50,
          offset: 0,
          summary: {
            artist_count: 0,
            release_count: 0,
            position_count: 0,
            published_position_count: 0,
          },
        }),
      ),
    );
    window.location.hash = "/publications";
    render(<Application />);

    expect(
      await screen.findByRole("heading", { level: 1, name: "Медиатека" }),
    ).toBeVisible();
    expect(
      screen.getByRole("navigation", { name: "Основная навигация" }),
    ).not.toHaveTextContent("Публикации");
  });

  it("shows the incoming count on a library route without loading the full group list", async () => {
    const groupListRequests = vi.fn();
    server.use(
      http.get("/api/setup", () => HttpResponse.json(completedState)),
      http.get("*/api/library/artists", () =>
        HttpResponse.json({
          items: [],
          total: 0,
          limit: 50,
          offset: 0,
          summary: {
            artist_count: 0,
            release_count: 0,
            position_count: 0,
            published_position_count: 0,
          },
        }),
      ),
      http.get("/api/incoming-groups/count", () =>
        HttpResponse.json({ count: 3 }),
      ),
      http.get("/api/incoming-groups", () => {
        groupListRequests();
        return HttpResponse.json({
          epoch: 0,
          revision: "",
          groups: [],
          files: [],
        });
      }),
    );
    window.location.hash = "/library";
    render(<Application />);

    const navigation = await screen.findByRole("navigation", {
      name: "Основная навигация",
    });
    await waitFor(() =>
      expect(
        within(
          within(navigation).getByRole("link", { name: /Входящие/ }),
        ).getByText("3"),
      ).toBeInTheDocument(),
    );
    expect(groupListRequests).not.toHaveBeenCalled();
  });

  it("restores a direct incoming group matching link with a back link to the list", async () => {
    const groupId = "11111111-1111-4111-8111-111111111111";
    const memberId = "22222222-2222-4222-8222-222222222222";
    server.use(
      http.get("/api/setup", () => HttpResponse.json(completedState)),
      http.get("/api/incoming-groups", () =>
        HttpResponse.json({
          epoch: 1,
          revision: "group-revision",
          groups: [
            {
              id: groupId,
              revision: "group-revision",
              manual: false,
              members: [memberId],
              unready_members: [],
              diagnostics: [],
            },
          ],
          files: [],
        }),
      ),
      http.get("/api/incoming-groups/:groupId/matching", () =>
        HttpResponse.json({
          evaluated: false,
          proposal_available: false,
          group_id: groupId,
          authority_mode: "automatic",
          authority_revision: 1,
          candidates: [],
          current_linked_manual_media_source_ids: [],
          members: [memberId],
          releases: [],
        }),
      ),
    );
    window.location.hash = `/incoming-groups/${groupId}/matching`;
    render(<Application />);

    expect(
      await screen.findByRole("heading", {
        level: 1,
        name: /Сопоставление/,
      }),
    ).toBeVisible();
    await waitFor(() =>
      expect(
        within(
          within(
            screen.getByRole("navigation", { name: "Основная навигация" }),
          ).getByRole("link", { name: /Входящие/ }),
        ).getByText("1"),
      ).toBeInTheDocument(),
    );
    await waitFor(() =>
      expect(
        screen.getByRole("link", { name: "← Все входящие группы" }),
      ).toHaveAttribute("href", incomingGroupsListHref([groupId])),
    );
    expect(window.location.hash).toBe(`#/incoming-groups/${groupId}/matching`);
  });

  it("keeps a blocked product deep link on the diagnostic startup view", async () => {
    server.use(
      http.get("/api/setup", () =>
        HttpResponse.json({
          ...completedState,
          platform: {
            ...completedState.platform,
            diagnostic: true,
            supported: false,
            reason: "unsupported platform",
          },
        }),
      ),
    );
    window.location.hash = "/operations";
    render(<Application />);

    expect(
      await screen.findByRole("heading", {
        level: 1,
        name: "Диагностика платформы",
      }),
    ).toBeVisible();
    expect(screen.getByRole("alert")).toHaveTextContent("unsupported platform");
    expect(
      screen.queryByRole("navigation", { name: "Основная навигация" }),
    ).not.toBeInTheDocument();
    expect(window.location.hash).toBe("#/operations");
  });
});
