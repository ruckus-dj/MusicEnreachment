import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { expect, it, vi } from "vitest";
import { server } from "../../test/server";
import { ReleaseCoverEditor } from "./newReleaseCoverEditor";

it("saves a pending image without applying it and labels fan-out as publication work", async () => {
  const applies: unknown[] = [];
  const saves: unknown[] = [];
  const cover = {
    release_id: "release-a",
    revision: 1,
    mode: "manual",
    pending_generation: "pending-a",
    pending_mime: "image/png",
    pending_sha256: "abc",
  };
  server.use(
    http.get("*/api/library/releases/release-a/cover", () =>
      HttpResponse.json(cover),
    ),
    http.patch(
      "*/api/library/releases/release-a/cover",
      async ({ request }) => {
        saves.push(await request.json());
        return HttpResponse.json({ ...cover, revision: 2 });
      },
    ),
    http.post(
      "*/api/library/releases/release-a/cover/apply",
      async ({ request }) => {
        applies.push(await request.json());
        return HttpResponse.json(
          {
            release_id: "release-a",
            revision: 3,
            selected_generation: "pending-a",
            operations: [
              {
                position_id: "position-a",
                operation_id: "operation-a",
                candidate_generation: "candidate-a",
              },
              {
                position_id: "position-b",
                operation_id: "operation-b",
                candidate_generation: "candidate-b",
              },
            ],
          },
          { status: 202 },
        );
      },
    ),
  );
  const admitted = vi.fn();
  render(
    <ReleaseCoverEditor
      releaseId="release-a"
      epoch="epoch-a"
      onOperationsAdmitted={admitted}
    />,
  );
  const picker = await screen.findByLabelText("Выбрать PNG или JPEG");
  const file = new File([new Uint8Array([1, 2, 3])], "cover.png", {
    type: "image/png",
  });
  fireEvent.change(picker, { target: { files: [file] } });
  fireEvent.click(screen.getByText("Сохранить ожидающую обложку"));
  await waitFor(() => expect(saves).toHaveLength(1));
  expect(picker).toHaveValue("");
  expect(applies).toHaveLength(0);
  expect(screen.getByText(/ещё не опубликована/)).toBeInTheDocument();
  fireEvent.click(screen.getByText("Применить ко всем опубликованным трекам"));
  await waitFor(() => expect(applies).toHaveLength(1));
  expect(admitted).toHaveBeenCalledWith(
    expect.arrayContaining([
      expect.objectContaining({
        position_id: "position-a",
        operation_id: "operation-a",
      }),
      expect.objectContaining({
        position_id: "position-b",
        operation_id: "operation-b",
      }),
    ]),
  );
  expect(
    await screen.findByText(/Применение запущено для 2 опубликованных треков/),
  ).toBeInTheDocument();
});

it("does not let a late cover read replace another release's state", async () => {
  let finishRead: ((response: Response) => void) | undefined;
  server.use(
    http.get(
      "*/api/library/releases/release-a/cover",
      () =>
        new Promise((resolve) => {
          finishRead = resolve;
        }),
    ),
  );
  const view = render(
    <ReleaseCoverEditor
      releaseId="release-a"
      onOperationsAdmitted={() => {}}
    />,
  );
  view.rerender(
    <ReleaseCoverEditor
      releaseId="release-b"
      onOperationsAdmitted={() => {}}
    />,
  );
  await waitFor(() => expect(finishRead).toBeDefined());
  finishRead?.(
    HttpResponse.json({
      release_id: "release-a",
      revision: 1,
      mode: "manual",
      warning: "stale",
    }),
  );
  await waitFor(() =>
    expect(screen.queryByText(/stale/)).not.toBeInTheDocument(),
  );
});

it("resets file and busy state when changing releases during a save", async () => {
  let finishSave: ((response: Response) => void) | undefined;
  server.use(
    http.get("*/api/library/releases/:releaseId/cover", ({ params }) =>
      HttpResponse.json({
        release_id: params.releaseId,
        revision: 1,
        mode: "manual",
      }),
    ),
    http.patch(
      "*/api/library/releases/release-a/cover",
      () =>
        new Promise((resolve) => {
          finishSave = resolve;
        }),
    ),
  );
  const view = render(
    <ReleaseCoverEditor
      releaseId="release-a"
      onOperationsAdmitted={() => {}}
    />,
  );
  const picker = await screen.findByLabelText("Выбрать PNG или JPEG");
  fireEvent.change(picker, {
    target: { files: [new File(["old"], "old.png", { type: "image/png" })] },
  });
  fireEvent.click(screen.getByText("Сохранить ожидающую обложку"));
  await waitFor(() => expect(finishSave).toBeDefined());
  view.rerender(
    <ReleaseCoverEditor
      releaseId="release-b"
      onOperationsAdmitted={() => {}}
    />,
  );
  const newPicker = await screen.findByLabelText("Выбрать PNG или JPEG");
  await waitFor(() => expect(newPicker).toBeEnabled());
  expect(newPicker).toHaveValue("");
  expect(screen.getByText("Сохранить ожидающую обложку")).toBeDisabled();
  fireEvent.change(newPicker, {
    target: { files: [new File(["new"], "new.png", { type: "image/png" })] },
  });
  expect(screen.getByText("Сохранить ожидающую обложку")).toBeEnabled();
  finishSave?.(
    HttpResponse.json({ release_id: "release-a", revision: 2, mode: "manual" }),
  );
  await waitFor(() =>
    expect(screen.getByText("Сохранить ожидающую обложку")).toBeEnabled(),
  );
  expect((newPicker as HTMLInputElement).files?.[0]?.name).toBe("new.png");
});
