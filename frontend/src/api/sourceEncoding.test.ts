import { afterEach, expect, it, vi } from "vitest";
import { applySourceEncoding, getSourceEncoding, previewSourceEncoding } from "./client";

afterEach(() => vi.restoreAllMocks());

it.each(["codec", "decode"] as const)(
  "uses source-scoped encoding endpoints with %s, revision and abort signal",
  async (mode) => {
    const fetcher = vi
      .spyOn(globalThis, "fetch")
      .mockImplementation(async () => Response.json({ fields: [] }));
    const request = {
      expected_revision: 3,
      choices: [{ field_id: 7, mode, decode_codec: "cp1251" as const }],
    };
    const signal = new AbortController().signal;
    await getSourceEncoding("a/b", signal);
    await previewSourceEncoding("a/b", request, signal);
    await applySourceEncoding("a/b", request);
    expect(fetcher.mock.calls.map(([url]) => url)).toEqual([
      "/api/sources/a%2Fb/encoding",
      "/api/sources/a%2Fb/encoding/preview",
      "/api/sources/a%2Fb/encoding/apply",
    ]);
    expect(fetcher.mock.calls[1][1]).toMatchObject({
      method: "POST",
      body: JSON.stringify(request),
      signal,
    });
  },
);

it("preserves structured atomic validation errors", async () => {
  const detail = {
    source_id: "a",
    source_revision: 3,
    valid: false,
    fields: [{ field_id: 7, value: null, status: "error", error: "raw_evidence_unavailable" }],
  };
  vi.spyOn(globalThis, "fetch").mockResolvedValue(Response.json({ detail }, { status: 422 }));
  await expect(
    applySourceEncoding("a", { expected_revision: 3, choices: [{ field_id: 7, mode: "keep" }] }),
  ).rejects.toMatchObject({ status: 422, detail });
});
