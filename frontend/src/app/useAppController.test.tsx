// @vitest-environment jsdom

import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { useAppController } from "./useAppController";

afterEach(() => {
  cleanup();
  window.history.replaceState({}, "", "/library/record/record-1/source/source-a");
  vi.restoreAllMocks();
});

function ControllerProbe() {
  const controller = useAppController();
  return (
    <>
      <button type="button" onClick={() => void controller.selectEffectiveSource("source-b")}>
        choose
      </button>
      <button
        type="button"
        onClick={() =>
          void controller.overrideRecording({
            recording_mbid: "f31c102e-5e6c-4c33-8a57-52c3c2a3ea6a",
          })
        }
      >
        correct
      </button>
      <output data-testid="effective-source-id">{controller.effectiveSourceId}</output>
      <output data-testid="effective-source-success">{controller.effectiveSourceSuccess}</output>
      <output data-testid="notice">{controller.notice}</output>
      <output data-testid="correction-error">{controller.recordingCorrectionError}</output>
      <output data-testid="correction-review">{controller.recordingCorrectionReview}</output>
    </>
  );
}

describe("useAppController effective source", () => {
  it("posts the choice and refreshes the record and library state", async () => {
    window.history.replaceState({}, "", "/library/record/record-1/source/source-a");
    const detail = {
      record_id: "record-1",
      source_state: "present",
      processing_state: "complete",
      match_state: "matched",
      publication_state: "current",
      metadata_state: "final",
      sources: [
        { source_id: "source-a", path: "/old.flac", sha256: "a", state: "present" },
        { source_id: "source-b", path: "/new.flac", sha256: "b", state: "present" },
      ],
      publications: [],
      states: {
        source: "present",
        processing: "complete",
        match: "matched",
        publication: "current",
        metadata: "final",
      },
      events: [],
    };
    const fetchMock = vi.spyOn(globalThis, "fetch").mockImplementation(async (input, init) => {
      const url = String(input);
      if (url === "/api/library/records" && !init?.method)
        return Response.json({ items: [detail] });
      if (url === "/api/library/records/record-1" && !init?.method) return Response.json(detail);
      if (url === "/api/library/records/record-1/effective-source") {
        expect(JSON.parse(String(init?.body))).toEqual({ source_id: "source-b" });
        return Response.json({
          source_id: "source-b",
          baseline_source_id: "source-b",
          policy_version: "quality-policy-v1",
        });
      }
      return Response.json({ items: [detail] });
    });

    render(<ControllerProbe />);
    await waitFor(() =>
      expect(screen.getByTestId("effective-source-id").textContent).toBe("source-a"),
    );

    await act(async () => {
      screen.getByRole("button", { name: "choose" }).click();
    });

    await waitFor(() => {
      expect(screen.getByTestId("effective-source-id").textContent).toBe("source-b");
      expect(screen.getByTestId("effective-source-success").textContent).toContain("выбран");
    });
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/library/records/record-1/effective-source",
      expect.objectContaining({ method: "POST" }),
    );
    expect(fetchMock).toHaveBeenCalledWith("/api/library/records/record-1", expect.anything());
    expect(fetchMock).toHaveBeenCalledWith("/api/library/records", expect.anything());
  });
});

describe("useAppController recording correction", () => {
  it("posts only the recording MBID, then refreshes the selected source record", async () => {
    const detail = {
      record_id: "record-1",
      source_state: "present",
      processing_state: "complete",
      match_state: "matched",
      publication_state: "current",
      metadata_state: "final",
      sources: [{ source_id: "source-a", path: "/track.flac", sha256: "a", state: "present" }],
      publications: [],
      states: {
        source: "present",
        processing: "complete",
        match: "matched",
        publication: "current",
        metadata: "final",
      },
      events: [],
    };
    const fetchMock = vi.spyOn(globalThis, "fetch").mockImplementation(async (input, init) => {
      const url = String(input);
      if (url === "/api/library/records" && !init?.method)
        return Response.json({ items: [detail] });
      if (url === "/api/library/records/record-1" && !init?.method) return Response.json(detail);
      if (url.endsWith("/musicbrainz/override")) {
        expect(JSON.parse(String(init?.body))).toEqual({
          recording_mbid: "f31c102e-5e6c-4c33-8a57-52c3c2a3ea6a",
        });
        return Response.json({
          recording_mbid: "f31c102e-5e6c-4c33-8a57-52c3c2a3ea6a",
          record_id: "record-1",
        });
      }
      return Response.json({ items: [detail] });
    });

    window.history.replaceState({}, "", "/library/record/record-1/source/source-a");
    render(<ControllerProbe />);
    await act(async () => {
      screen.getByRole("button", { name: "correct" }).click();
    });

    await waitFor(() => expect(screen.getByTestId("notice").textContent).toContain("исправлена"));
    expect(fetchMock).toHaveBeenCalledWith("/api/library/records/record-1", expect.anything());
    expect(fetchMock).toHaveBeenCalledWith("/api/library/records", expect.anything());
  });
});
