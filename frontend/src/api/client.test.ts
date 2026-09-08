import { describe, expect, it, vi } from "vitest";
import type { SourceRoot } from "../types";
import {
  api,
  createSourceRoot,
  listManualActions,
  listSourceRootCandidates,
  listSourceRoots,
  moveStorageOutput,
  removeSourceRoot,
  selectEffectiveSource,
  submitRecordingCorrection,
} from "./client";

describe("API error messages", () => {
  it.each([
    ["Ошибка сервера", "Ошибка сервера"],
    ["", ""],
    [null, "Не удалось выполнить запрос"],
    [{ reason: "invalid" }, "Не удалось выполнить запрос"],
  ])("preserves server detail or fallback for %s", async (detail, message) => {
    const fetchMock = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValue(Response.json({ detail }, { status: 500 }));
    try {
      await expect(api("/api/library/records")).rejects.toMatchObject({
        name: "ApiError",
        status: 500,
        message,
      });
    } finally {
      fetchMock.mockRestore();
    }
  });
});

describe("source-root API client", () => {
  it("uses the list, candidate, create, and removal contracts", async () => {
    const root: SourceRoot = {
      id: "root-archive",
      display_name: "Архив",
      canonical_path: "/data/sources/archive",
      enabled: true,
      scan_state: "never_scanned",
    };
    const fetchMock = vi.spyOn(globalThis, "fetch").mockImplementation(async (input, init) => {
      const url = String(input);
      const method = init?.method ?? "GET";
      const body = init?.body ? JSON.parse(String(init.body)) : null;
      if (url === "/api/settings/source-roots" && method === "GET") {
        return Response.json({ items: [root] });
      }
      if (url === "/api/settings/source-roots" && method === "POST") {
        expect(body).toEqual({ path: root.canonical_path, display_name: root.display_name });
        return Response.json(root, { status: 201 });
      }
      if (url === "/api/settings/source-roots/candidates" && method === "GET") {
        return Response.json({
          items: [{ name: "archive", canonical_path: root.canonical_path }],
        });
      }
      if (url === `/api/settings/source-roots/${root.id}` && method === "DELETE") {
        return new Response(null, { status: 204 });
      }
      return Response.json({ detail: "unexpected request" }, { status: 500 });
    });

    await expect(listSourceRoots()).resolves.toEqual({ items: [root] });
    await expect(listSourceRootCandidates()).resolves.toEqual({
      items: [{ name: "archive", canonical_path: root.canonical_path }],
    });
    await expect(
      createSourceRoot({ path: root.canonical_path, display_name: root.display_name }),
    ).resolves.toEqual(root);
    await expect(removeSourceRoot(root.id)).resolves.toBeUndefined();
    expect(fetchMock).toHaveBeenCalledTimes(4);
    fetchMock.mockRestore();
  });

  it("posts the manual effective-source selection to the record endpoint", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(
      Response.json({
        source_id: "source-b",
        baseline_source_id: "source-b",
        policy_version: "quality-policy-v1",
      }),
    );

    await expect(selectEffectiveSource("record-1", { source_id: "source-b" })).resolves.toEqual({
      source_id: "source-b",
      baseline_source_id: "source-b",
      policy_version: "quality-policy-v1",
    });

    expect(fetchMock).toHaveBeenCalledWith(
      "/api/library/records/record-1/effective-source",
      expect.objectContaining({
        method: "POST",
        body: JSON.stringify({ source_id: "source-b" }),
      }),
    );
    fetchMock.mockRestore();
  });

  it("requests only the selected manual-action category", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(
      Response.json({
        items: [],
        counts: { analysis_error: 4, needs_review: 2 },
      }),
    );

    await expect(listManualActions("needs-review")).resolves.toEqual({
      items: [],
      counts: { analysis_error: 4, needs_review: 2 },
    });
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/library/manual-actions?action=needs-review",
      expect.objectContaining({ headers: { "content-type": "application/json" } }),
    );
    fetchMock.mockRestore();
  });

  it("posts the source-scoped recording correction with only the recording MBID", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(
      Response.json({
        recording_mbid: "f31c102e-5e6c-4c33-8a57-52c3c2a3ea6a",
        record_id: "record-1",
      }),
    );

    await expect(
      submitRecordingCorrection("record-1", "source-a", {
        recording_mbid: "f31c102e-5e6c-4c33-8a57-52c3c2a3ea6a",
      }),
    ).resolves.toEqual({
      recording_mbid: "f31c102e-5e6c-4c33-8a57-52c3c2a3ea6a",
      record_id: "record-1",
    });

    expect(fetchMock).toHaveBeenCalledWith(
      "/api/library/records/record-1/sources/source-a/musicbrainz/override",
      expect.objectContaining({
        method: "POST",
        body: JSON.stringify({
          recording_mbid: "f31c102e-5e6c-4c33-8a57-52c3c2a3ea6a",
        }),
      }),
    );
    fetchMock.mockRestore();
  });
});

it("returns the durable pending storage migration without claiming completion", async () => {
  const pending = { output_root: "/media/old", state: "migrating", generation: 1 };
  const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(Response.json(pending));
  try {
    expect(await moveStorageOutput("/media/new")).toEqual(pending);
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/settings/storage/output",
      expect.objectContaining({
        method: "PUT",
        body: JSON.stringify({ path: "/media/new" }),
      }),
    );
  } finally {
    fetchMock.mockRestore();
  }
});
