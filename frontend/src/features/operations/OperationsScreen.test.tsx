import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { afterEach, expect, it, vi } from "vitest";
import { I18nProvider, useI18n } from "../../i18n/I18nProvider";
import { createMemoryStorage } from "../../test/memoryStorage";
import { server } from "../../test/server";
import { OperationsScreen } from "./OperationsScreen";

const releaseId = "123e4567-e89b-42d3-a456-426614174000";
const positionId = "223e4567-e89b-42d3-a456-426614174000";

function LocaleSwitcher() {
  const { locale, setPreference } = useI18n();
  return (
    <button
      type="button"
      onClick={() => setPreference(locale === "en" ? "ru" : "en")}
    >
      Switch language
    </button>
  );
}

function renderScreen(withLocaleSwitcher = false) {
  const storage = createMemoryStorage();
  storage.setItem("melotrove-language", "en");
  vi.stubGlobal("localStorage", storage);
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <I18nProvider>
        {withLocaleSwitcher && <LocaleSwitcher />}
        <OperationsScreen />
      </I18nProvider>
    </QueryClientProvider>,
  );
}

afterEach(() => {
  window.location.hash = "";
  vi.unstubAllGlobals();
});

it("shows loading then current operations with target links, diagnostics and failed retry", async () => {
  let retried = false;
  server.use(
    http.get("/api/operations/current", ({ request }) => {
      const url = new URL(request.url);
      expect(url.searchParams.get("status")).toBe("attention");
      expect(url.searchParams.get("offset")).toBe("0");
      expect(url.searchParams.get("kind")).toBeNull();
      expect(url.searchParams.get("q")).toBeNull();
      if (retried) {
        return HttpResponse.json(
          { detail: "List temporarily unavailable" },
          { status: 503 },
        );
      }
      return HttpResponse.json({
        operations: [
          {
            id: "failed-id",
            kind: "scan_source",
            state: "failed",
            stage: "scan_files",
            bytes_completed: 12,
            bytes_total: 30,
            target_identity: "/music",
            target_label: "Music library",
            target_ref: { kind: "source_root", root_id: "root-1" },
            safe_error: "Permission denied",
            created_at: "2026-01-01T00:00:00Z",
            updated_at: "2026-01-01T00:01:00Z",
          },
          {
            id: "other-failure",
            kind: "scan_source",
            state: "failed",
            stage: "scan_files",
            bytes_completed: 0,
            target_label: "Archive",
            safe_error: "Disk not available",
            created_at: "2026-01-01T00:00:00Z",
            updated_at: "2026-01-01T00:01:00Z",
          },
        ],
        total: 2,
      });
    }),
    http.post("/api/operations/failed-id/retry", () => {
      retried = true;
      return HttpResponse.json({
        id: "failed-id",
        kind: "scan_source",
        state: "queued",
        stage: "queued",
        bytes_completed: 0,
        created_at: "2026-01-01T00:00:00Z",
        updated_at: "2026-01-01T00:02:00Z",
      });
    }),
  );

  renderScreen();
  expect(screen.getByRole("status")).toHaveTextContent("Loading");
  const target = await screen.findByRole("link", { name: "Music library" });
  const row = screen.getByRole("row", { name: /Music library/ });
  expect(target).toHaveAttribute("href", "#/sources/root-1");
  expect(within(row).getByText("Source scan")).toBeInTheDocument();
  expect(screen.getByText("Permission denied")).toBeInTheDocument();
  expect(screen.getByText("12 / 30 bytes")).toBeInTheDocument();

  fireEvent.click(within(row).getByRole("button", { name: "Details" }));
  expect(screen.getByRole("dialog")).toHaveTextContent("Permission denied");
  fireEvent.click(screen.getByRole("button", { name: "Close" }));

  fireEvent.click(within(row).getByRole("button", { name: "Retry" }));
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "List temporarily unavailable",
  );
  expect(
    screen.queryByRole("link", { name: "Music library" }),
  ).not.toBeInTheDocument();
  expect(screen.getByText("Archive")).toBeInTheDocument();
  expect(
    screen.queryByRole("button", { name: "Retry" }),
  ).not.toBeInTheDocument();
});

it.each([
  "install",
  "move_tools_root",
])("dismisses a failed %s operation and restores heading focus", async (kind) => {
  let dismissed = false;
  server.use(
    http.get("/api/operations/current", () =>
      HttpResponse.json(
        dismissed
          ? { operations: [], total: 0 }
          : {
              operations: [
                {
                  id: "failed-tool",
                  kind,
                  state: "failed",
                  stage: "download",
                  bytes_completed: 0,
                  target_label: "FFmpeg 7.1",
                  safe_error: "Download failed",
                  created_at: "2026-01-01T00:00:00Z",
                  updated_at: "2026-01-01T00:00:00Z",
                },
              ],
              total: 1,
            },
      ),
    ),
    http.delete("/api/operations/failed-tool", () => {
      dismissed = true;
      return new HttpResponse(null, { status: 204 });
    }),
  );

  renderScreen();
  const dismiss = await screen.findByRole("button", { name: "Dismiss" });
  dismiss.focus();
  fireEvent.click(dismiss);

  expect(
    await screen.findByRole("heading", { name: "No current operations" }),
  ).toBeInTheDocument();
  expect(screen.getByText("Operation dismissed.")).toBeInTheDocument();
  await waitFor(() =>
    expect(document.activeElement).toBe(
      screen.getByRole("heading", { level: 1, name: "Current operations" }),
    ),
  );
});

it("keeps the current rows and language while a running operation is pending", async () => {
  let operationReads = 0;
  server.use(
    http.get("/api/operations/current", () => {
      operationReads += 1;
      return HttpResponse.json({
        operations: [
          {
            id: "pending-id",
            kind: "scan_source",
            state: "running",
            stage: "traversing",
            bytes_completed: 12,
            bytes_total: 30,
            target_identity: "/music",
            target_label: "Music library",
            target_ref: { kind: "source_root", root_id: "root-1" },
            created_at: "2026-01-01T00:00:00Z",
            updated_at: "2026-01-01T00:01:00Z",
          },
        ],
        total: 1,
      });
    }),
  );

  renderScreen(true);
  expect(await screen.findByText("Source scan")).toBeInTheDocument();
  expect(
    screen.getByRole("heading", { name: "Current operations" }),
  ).toBeInTheDocument();

  fireEvent.click(screen.getByRole("button", { name: "Switch language" }));

  expect(
    screen.getByRole("heading", { name: "Текущие операции" }),
  ).toBeInTheDocument();
  expect(screen.getByText("Сканирование источника")).toBeInTheDocument();
  expect(
    within(screen.getByRole("grid")).getByText("Выполняется"),
  ).toBeInTheDocument();
  expect(operationReads).toBe(1);
});

it("uses URL filters, clamps a page after the list shrinks and keeps position links", async () => {
  window.location.hash =
    "#/operations?status=failed&kind=publication&offset=100";
  let reads = 0;
  server.use(
    http.get("/api/operations/current", ({ request }) => {
      const url = new URL(request.url);
      reads += 1;
      expect(url.searchParams.get("status")).toBe("failed");
      expect(url.searchParams.get("kind")).toBe("publication");
      expect(url.searchParams.get("offset")).toBe(
        reads === 1 ? "100" : reads === 2 ? "50" : "0",
      );
      return HttpResponse.json({
        operations:
          reads === 1
            ? []
            : [
                {
                  id: "failed-publication",
                  kind: "publish_track_v1",
                  state: "failed",
                  stage: "write_tags",
                  bytes_completed: 0,
                  target_label: "Northern Lines · position 01",
                  target_ref: {
                    kind: "position",
                    release_id: releaseId,
                    position_id: positionId,
                  },
                  safe_error: "Write failed",
                  created_at: "2026-01-01T00:00:00Z",
                  updated_at: "2026-01-01T00:01:00Z",
                },
              ],
        total: 60,
      });
    }),
  );

  renderScreen();
  expect(
    await screen.findByRole("link", { name: "Northern Lines · position 01" }),
  ).toHaveAttribute(
    "href",
    `#/library/releases/${releaseId}/positions/${positionId}`,
  );
  expect(await screen.findByText("51–60 of 60")).toBeInTheDocument();
  await waitFor(() =>
    expect(window.location.hash).toBe(
      "#/operations?status=failed&kind=publication&offset=50",
    ),
  );

  fireEvent.click(screen.getByRole("button", { name: "Previous" }));
  await waitFor(() => expect(reads).toBe(3));
  expect(window.location.hash).toBe(
    "#/operations?status=failed&kind=publication",
  );
});
