import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { expect, it, vi } from "vitest";
import { server } from "../../test/server";
import { FirstPublicationPreview } from "./FirstPublicationPreview";

const preview = (releaseId: string, positionId: string) => ({
  release_id: releaseId,
  position_id: positionId,
  epoch: "expected-epoch",
  context_digest: "a".repeat(64),
  selected_source: {
    root_id: "root",
    location_id: "location",
    media_variant_id: "variant",
    stream_index: 0,
    codec: "flac",
  },
  transform: { action: "copy", output_container: "mka", extension: "mka" },
  path_components: ["Исполнитель", "Альбом", "Песня.mka"],
  metadata_revisions: { title: 5 },
  warnings: ["Предупреждение"],
  blockers: [] as string[],
  native_status: "native verification required",
});

function renderPreview(
  releaseId: string,
  positionId: string,
  onAdmitted = vi.fn(),
  onRefresh = vi.fn(),
) {
  return render(
    <FirstPublicationPreview
      releaseId={releaseId}
      positionId={positionId}
      onAdmitted={onAdmitted}
      onRefresh={onRefresh}
    />,
  );
}

it("loads a read-only plan and does not confirm automatically", async () => {
  let gets = 0;
  let posts = 0;
  server.use(
    http.get(
      "*/api/library/releases/release-1/positions/position-1/publication/preview",
      () => {
        gets += 1;
        return HttpResponse.json({
          ...preview("release-1", "position-1"),
          blockers: ["Требуется проверка"],
        });
      },
    ),
    http.post(
      "*/api/library/releases/release-1/positions/position-1/publication",
      () => {
        posts += 1;
        return HttpResponse.json({}, { status: 202 });
      },
    ),
  );
  renderPreview("release-1", "position-1");
  fireEvent.click(
    screen.getByRole("button", { name: "Предпросмотр публикации" }),
  );
  expect(
    await screen.findByText(/Предпросмотр не проверяет возможность публикации/),
  ).toBeInTheDocument();
  expect(
    screen.getByText(/Исполнитель\/Альбом\/Песня.mka/),
  ).toBeInTheDocument();
  expect(screen.getByText(/Предупреждение/)).toBeInTheDocument();
  expect(screen.getByText(/Требуется проверка/)).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Опубликовать" })).toBeDisabled();
  fireEvent.click(screen.getByRole("button", { name: "Опубликовать" }));
  expect(gets).toBe(1);
  expect(posts).toBe(0);
});

it("confirms the displayed digest and epoch, then refreshes publication state", async () => {
  const bodies: unknown[] = [];
  const admitted = vi.fn();
  const refresh = vi.fn();
  server.use(
    http.get(
      "*/api/library/releases/release-2/positions/position-2/publication/preview",
      () => HttpResponse.json(preview("release-2", "position-2")),
    ),
    http.post(
      "*/api/library/releases/release-2/positions/position-2/publication",
      async ({ request }) => {
        bodies.push(await request.json());
        return HttpResponse.json(
          {
            id: "operation-1",
            kind: "library_publication",
            state: "queued",
            stage: "queued",
            bytes_completed: 0,
            created_at: "now",
            updated_at: "now",
          },
          { status: 202 },
        );
      },
    ),
  );
  renderPreview("release-2", "position-2", admitted, refresh);
  fireEvent.click(
    screen.getByRole("button", { name: "Предпросмотр публикации" }),
  );
  expect(
    await screen.findByRole("button", { name: "Опубликовать" }),
  ).toBeEnabled();
  expect(bodies).toEqual([]);
  fireEvent.click(screen.getByRole("button", { name: "Опубликовать" }));
  expect(await screen.findByText(/operation-1/)).toBeInTheDocument();
  expect(bodies).toEqual([
    { preview_digest: "a".repeat(64), expected_epoch: "expected-epoch" },
  ]);
  expect(admitted).toHaveBeenCalledTimes(1);
  fireEvent.click(
    screen.getByRole("button", { name: "Обновить состояние публикации" }),
  );
  expect(refresh).toHaveBeenCalledTimes(1);
});

it("requires a fresh explicit preview before retrying a stale confirmation", async () => {
  let gets = 0;
  let posts = 0;
  server.use(
    http.get(
      "*/api/library/releases/release-3/positions/position-3/publication/preview",
      () => {
        gets += 1;
        return HttpResponse.json(preview("release-3", "position-3"));
      },
    ),
    http.post(
      "*/api/library/releases/release-3/positions/position-3/publication",
      () => {
        posts += 1;
        return HttpResponse.json({ detail: "stale" }, { status: 409 });
      },
    ),
  );
  renderPreview("release-3", "position-3");
  fireEvent.click(
    screen.getByRole("button", { name: "Предпросмотр публикации" }),
  );
  fireEvent.click(await screen.findByRole("button", { name: "Опубликовать" }));
  expect(await screen.findByText(/Предпросмотр устарел/)).toBeInTheDocument();
  expect(posts).toBe(1);
  expect(
    screen.queryByRole("button", { name: "Опубликовать" }),
  ).not.toBeInTheDocument();
  fireEvent.click(
    screen.getByRole("button", { name: "Предпросмотр публикации" }),
  );
  await waitFor(() => expect(gets).toBe(2));
  fireEvent.click(await screen.findByRole("button", { name: "Опубликовать" }));
  await waitFor(() => expect(posts).toBe(2));
});

it("ignores a delayed admission response after navigating to another position", async () => {
  let resolveAdmission: ((response: Response) => void) | undefined;
  let admissionStarted = false;
  const admitted = vi.fn();
  server.use(
    http.get(
      "*/api/library/releases/release-old/positions/position-old/publication/preview",
      () => HttpResponse.json(preview("release-old", "position-old")),
    ),
    http.post(
      "*/api/library/releases/release-old/positions/position-old/publication",
      () => {
        admissionStarted = true;
        return new Promise<Response>((resolve) => {
          resolveAdmission = resolve;
        });
      },
    ),
  );
  const view = renderPreview("release-old", "position-old", admitted);
  fireEvent.click(
    screen.getByRole("button", { name: "Предпросмотр публикации" }),
  );
  fireEvent.click(await screen.findByRole("button", { name: "Опубликовать" }));
  await waitFor(() => expect(admissionStarted).toBe(true));
  view.rerender(
    <FirstPublicationPreview
      releaseId="release-new"
      positionId="position-new"
      onAdmitted={admitted}
      onRefresh={vi.fn()}
    />,
  );
  await act(async () => {
    resolveAdmission?.(
      HttpResponse.json({ id: "old-operation" }, { status: 202 }),
    );
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
  expect(admitted).not.toHaveBeenCalled();
  expect(screen.queryByText(/old-operation/)).not.toBeInTheDocument();
});

it("does not invoke the admission callback after unmount", async () => {
  let resolveAdmission: ((response: Response) => void) | undefined;
  let admissionStarted = false;
  const admitted = vi.fn();
  server.use(
    http.get(
      "*/api/library/releases/release-unmount/positions/position-unmount/publication/preview",
      () => HttpResponse.json(preview("release-unmount", "position-unmount")),
    ),
    http.post(
      "*/api/library/releases/release-unmount/positions/position-unmount/publication",
      () => {
        admissionStarted = true;
        return new Promise<Response>((resolve) => {
          resolveAdmission = resolve;
        });
      },
    ),
  );
  const view = renderPreview("release-unmount", "position-unmount", admitted);
  fireEvent.click(
    screen.getByRole("button", { name: "Предпросмотр публикации" }),
  );
  fireEvent.click(await screen.findByRole("button", { name: "Опубликовать" }));
  await waitFor(() => expect(admissionStarted).toBe(true));
  view.unmount();
  await act(async () => {
    resolveAdmission?.(
      HttpResponse.json({ id: "unmounted-operation" }, { status: 202 }),
    );
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
  expect(admitted).not.toHaveBeenCalled();
});

it("shows a safe error when the preview request fails", async () => {
  server.use(
    http.get(
      "*/api/library/releases/release-4/positions/position-4/publication/preview",
      () => HttpResponse.json({}, { status: 500 }),
    ),
  );
  renderPreview("release-4", "position-4");
  fireEvent.click(
    screen.getByRole("button", { name: "Предпросмотр публикации" }),
  );
  expect(await screen.findByRole("alert")).toHaveTextContent("HTTP 500");
});
