import "@testing-library/jest-dom/vitest";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import type { LibraryFirstPublicationPreviewResponse } from "../../api/generated/client.schemas";

const api = vi.hoisted(() => ({
  getLibraryFirstPublicationPreview: vi.fn(),
  startLibraryFirstPublication: vi.fn(),
}));

vi.mock("../../api/generated/client", () => api);

import { FirstPublicationPreview } from "./FirstPublicationPreview";

const preview = {
  blockers: [],
  context_digest: "frozen-digest",
  epoch: "epoch-1",
  metadata_revisions: {},
  resolved_path_components: ["Artist", "Release", "01 - Track.flac"],
  selected_source: {
    audio_stream_count: 1,
    codec: "FLAC",
    language: "",
    location_id: "location-1",
    media_variant_id: "variant-1",
    requires_explicit_stream_selection: false,
    root_id: "root-1",
    stream_index: 0,
    title: "Track",
    unsupported_audio_stream_count: 0,
  },
  source_candidates: [],
  transform: { action: "copy", output_container: "flac" },
} as unknown as LibraryFirstPublicationPreviewResponse;

function component(refreshToken: number, onAdmitted = vi.fn()) {
  return (
    <FirstPublicationPreview
      releaseId="release-1"
      positionId="position-1"
      refreshToken={refreshToken}
      onAdmitted={onAdmitted}
      onRefresh={vi.fn()}
    />
  );
}

afterEach(() => vi.clearAllMocks());

it("keeps a pending admission fenced across a metadata refresh and delivers its admitted operation", async () => {
  api.getLibraryFirstPublicationPreview.mockResolvedValue({
    status: 200,
    data: preview,
  });
  let resolveAdmission!: (response: {
    status: number;
    data: { id: string };
  }) => void;
  api.startLibraryFirstPublication.mockImplementation(
    () =>
      new Promise((resolve) => {
        resolveAdmission = resolve;
      }),
  );
  const onAdmitted = vi.fn();
  const view = render(component(0, onAdmitted));
  fireEvent.click(
    screen.getByRole("button", { name: "Предпросмотр публикации" }),
  );
  await screen.findByRole("button", { name: "Опубликовать" });
  fireEvent.click(screen.getByRole("button", { name: "Опубликовать" }));

  view.rerender(component(1, onAdmitted));
  expect(
    screen.getByRole("button", { name: "Проверка и запуск…" }),
  ).toBeDisabled();
  expect(api.getLibraryFirstPublicationPreview).toHaveBeenCalledTimes(1);

  await act(async () =>
    resolveAdmission({ status: 202, data: { id: "operation-1" } }),
  );
  await waitFor(() => expect(onAdmitted).toHaveBeenCalledWith("operation-1"));
  expect(
    screen.getByRole("button", { name: "Предпросмотр публикации" }),
  ).toBeDisabled();
  expect(api.startLibraryFirstPublication).toHaveBeenCalledTimes(1);
});

it("retains the disabled fence after an uncertain admission when metadata refreshes", async () => {
  api.getLibraryFirstPublicationPreview.mockResolvedValue({
    status: 200,
    data: preview,
  });
  api.startLibraryFirstPublication.mockRejectedValue(
    new Error("connection lost"),
  );
  const view = render(component(0));
  fireEvent.click(
    screen.getByRole("button", { name: "Предпросмотр публикации" }),
  );
  await screen.findByRole("button", { name: "Опубликовать" });
  fireEvent.click(screen.getByRole("button", { name: "Опубликовать" }));
  await screen.findByRole("alert");
  expect(screen.getByRole("button", { name: "Опубликовать" })).toBeDisabled();

  view.rerender(component(1));
  expect(
    screen.getByRole("button", { name: "Предпросмотр публикации" }),
  ).toBeDisabled();
  expect(screen.getByRole("button", { name: "Опубликовать" })).toBeDisabled();
  expect(api.getLibraryFirstPublicationPreview).toHaveBeenCalledTimes(1);
  expect(api.startLibraryFirstPublication).toHaveBeenCalledTimes(1);
});

it("invalidates an unsubmitted preview and reloads its digest when metadata refreshes", async () => {
  api.getLibraryFirstPublicationPreview
    .mockResolvedValueOnce({ status: 200, data: preview })
    .mockResolvedValueOnce({
      status: 200,
      data: { ...preview, context_digest: "new-digest" },
    });
  const view = render(component(0));
  fireEvent.click(
    screen.getByRole("button", { name: "Предпросмотр публикации" }),
  );
  await screen.findByText("План первой публикации");

  view.rerender(component(1));
  await waitFor(() =>
    expect(api.getLibraryFirstPublicationPreview).toHaveBeenCalledTimes(2),
  );
  expect(screen.getByRole("button", { name: "Опубликовать" })).toBeEnabled();
  api.startLibraryFirstPublication.mockResolvedValue({
    status: 202,
    data: { id: "operation-after-refresh" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Опубликовать" }));
  await waitFor(() =>
    expect(api.startLibraryFirstPublication).toHaveBeenCalledWith(
      "release-1",
      "position-1",
      expect.objectContaining({ preview_digest: "new-digest" }),
    ),
  );
});
