import "@testing-library/jest-dom/vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { JSDOM } from "jsdom";
import { HttpResponse, http } from "msw";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { InstallationResponse } from "../../api/generated/client.schemas";
import { server } from "../../test/server";
import { SetupTools } from "./SetupTools";

function installation(
  id: string,
  packageKind: string,
  active: boolean,
): InstallationResponse {
  return {
    id,
    package_kind: packageKind,
    release_identity: "1.0",
    source_name: "trusted",
    state: "ready",
    active,
    created_at: "2026-09-28T00:00:00Z",
    executable_versions: {},
  };
}

function mockCatalogAndOperations(installations: InstallationResponse[]) {
  server.use(
    http.get("/api/operations", () => HttpResponse.json({ operations: [] })),
    http.get("/api/tools/catalog", ({ request }) =>
      HttpResponse.json({
        package_kind: new URL(request.url).searchParams.get("package_kind"),
        releases: [{ identity: "2.0", source: "trusted", artifacts: [] }],
        platform: {
          diagnostic: false,
          supported: true,
          goos: "linux",
          goarch: "amd64",
        },
      }),
    ),
    http.get("/api/tools/installations", () =>
      HttpResponse.json({ installations }),
    ),
  );
}

beforeEach(() => {
  vi.stubGlobal(
    "localStorage",
    new JSDOM("", { url: "http://localhost" }).window.localStorage,
  );
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("SetupTools", () => {
  it("keeps ready packages fixed while allowing the other package's first version selection", async () => {
    mockCatalogAndOperations([installation("ffmpeg-1", "ffmpeg", true)]);
    render(<SetupTools />);

    expect(await screen.findByText("1.0: Активна")).toBeVisible();
    expect(
      screen.queryByRole("button", { name: /Версия ffmpeg/ }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Проверить установку ffmpeg" }),
    ).not.toBeInTheDocument();
    const fpcalcVersion = screen.getByRole("button", { name: /Версия fpcalc/ });
    await waitFor(() => expect(fpcalcVersion).toBeEnabled());
    fireEvent.click(fpcalcVersion);
    fireEvent.click(
      await screen.findByRole("option", { name: "2.0 (trusted)" }),
    );
    expect(
      screen.getByRole("button", { name: "Проверить установку fpcalc" }),
    ).toBeEnabled();
    expect(
      screen.queryByRole("button", { name: /Активировать/ }),
    ).not.toBeInTheDocument();
  });

  it("stops initial setup with an actionable error for multiple ready package versions", async () => {
    mockCatalogAndOperations([
      installation("ffmpeg-1", "ffmpeg", true),
      installation("ffmpeg-2", "ffmpeg", false),
    ]);
    const onBlockedChange = vi.fn();
    render(<SetupTools onBlockedChange={onBlockedChange} />);

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Найдено несколько готовых версий одного пакета",
    );
    expect(screen.getByRole("alert")).toHaveTextContent(
      "Не удаляйте и не переключайте версии",
    );
    await waitFor(() => expect(onBlockedChange).toHaveBeenLastCalledWith(true));
    expect(
      screen.queryByRole("button", { name: /Версия/ }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: /Проверить установку/ }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: /Активировать/ }),
    ).not.toBeInTheDocument();
  });
});
