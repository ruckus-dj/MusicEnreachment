import { HttpResponse, http } from "msw";
import { afterEach, describe, expect, it } from "vitest";
import type { InstallPreflightBody } from "../../api/generated/client.schemas";
import { server } from "../../test/server";
import {
  loadManagedToolCatalogs,
  preflightManagedToolInstall,
  startManagedToolInstall,
} from "./toolWorkflow";

describe("managed tool workflow", () => {
  afterEach(() => server.resetHandlers());

  it("loads both managed-tool catalogs", async () => {
    const requestedKinds: string[] = [];
    server.use(
      http.get("/api/tools/catalog", ({ request }) => {
        const kind = new URL(request.url).searchParams.get("package_kind");
        if (kind) requestedKinds.push(kind);
        return HttpResponse.json({
          package_kind: kind,
          releases: [
            { identity: `${kind}-1`, source: "trusted", artifacts: [] },
          ],
          platform: {
            diagnostic: false,
            supported: true,
            goos: "linux",
            goarch: "amd64",
          },
        });
      }),
    );

    const catalogs = await loadManagedToolCatalogs();

    expect(requestedKinds.sort()).toEqual(["ffmpeg", "fpcalc"]);
    expect(catalogs.ffmpeg.releases?.[0].identity).toBe("ffmpeg-1");
    expect(catalogs.fpcalc.releases?.[0].identity).toBe("fpcalc-1");
  });

  it("preflights the selected kind and release and starts with approved conflicts", async () => {
    const preflightRequests: unknown[] = [];
    let startRequest: unknown;
    server.use(
      http.post("/api/tools/installations/preflight", async ({ request }) => {
        preflightRequests.push(await request.json());
        return HttpResponse.json({
          preflight_token: "preflight-token",
          conflicts: ["bin/ffmpeg"],
          targets: ["bin/ffmpeg"],
        });
      }),
      http.post("/api/tools/installations", async ({ request }) => {
        startRequest = await request.json();
        return HttpResponse.json({
          id: "operation-1",
          kind: "install",
          state: "queued",
          stage: "queued",
          bytes_completed: 0,
          bytes_total: 0,
        });
      }),
    );

    const plan = await preflightManagedToolInstall("ffmpeg", "7.0");
    const operation = await startManagedToolInstall(plan, ["bin/ffmpeg"]);

    expect(preflightRequests).toEqual([
      { package_kind: "ffmpeg", release_identity: "7.0" },
    ]);
    expect(startRequest).toEqual({
      preflight_token: "preflight-token",
      confirmed_conflicts: ["bin/ffmpeg"],
    });
    expect(operation.id).toBe("operation-1");
  });

  it("uses the conflict list by default and reports API details", async () => {
    const startBodies: unknown[] = [];
    server.use(
      http.post("/api/tools/installations", async ({ request }) => {
        startBodies.push(await request.json());
        return HttpResponse.json({
          id: "operation-2",
          kind: "install",
          state: "queued",
          stage: "queued",
          bytes_completed: 0,
          bytes_total: 0,
        });
      }),
    );

    const plan: InstallPreflightBody = {
      preflight_token: "token-2",
      conflicts: ["bin/tool"],
      targets: ["bin/tool"],
    };
    await startManagedToolInstall(plan);
    expect(startBodies).toEqual([
      { preflight_token: "token-2", confirmed_conflicts: ["bin/tool"] },
    ]);

    server.use(
      http.get("/api/tools/catalog", () =>
        HttpResponse.json({ detail: "Каталог недоступен." }, { status: 503 }),
      ),
    );
    await expect(loadManagedToolCatalogs()).rejects.toThrow(
      "Каталог недоступен.",
    );
  });
});
