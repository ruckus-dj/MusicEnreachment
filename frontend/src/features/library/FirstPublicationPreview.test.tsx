import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
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
  resolved_path_components: ["Исполнитель", "Альбом", "Песня (2).mka"],
  metadata_revisions: { title: 5 },
  warnings: ["Предупреждение"],
  blockers: [] as string[],
  native_status: "native verification required",
});

const candidates = [
  {
    root_id: "root-a",
    location_id: "location-a",
    media_variant_id: "variant-a",
    stream_index: 0,
    codec: "flac",
    title: "Основной звук",
    language: "rus",
    channels: 2,
    audio_stream_count: 2,
    unsupported_audio_stream_count: 1,
  },
  {
    root_id: "root-b",
    location_id: "location-b",
    media_variant_id: "variant-b",
    stream_index: 1,
    codec: "aac",
    title: "Комментаторская дорожка",
    language: "eng",
    channels: 6,
    audio_stream_count: 2,
    unsupported_audio_stream_count: 1,
  },
];

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
    screen.getByText(/Исполнитель\/Альбом\/Песня \(2\).mka/),
  ).toBeInTheDocument();
  expect(
    screen.queryByText(
      /Планируемый относительный путь: Исполнитель\/Альбом\/Песня.mka/,
    ),
  ).not.toBeInTheDocument();
  expect(screen.getByText(/Предупреждение/)).toBeInTheDocument();
  expect(screen.getByText(/Требуется проверка/)).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Опубликовать" })).toBeDisabled();
  fireEvent.click(screen.getByRole("button", { name: "Опубликовать" }));
  expect(gets).toBe(1);
  expect(posts).toBe(0);
});

it("does not allow confirmation when the native resolved path is missing", async () => {
  server.use(
    http.get(
      "*/api/library/releases/release-unresolved/positions/position-unresolved/publication/preview",
      () => {
        const response = preview("release-unresolved", "position-unresolved");
        delete (response as { resolved_path_components?: string[] })
          .resolved_path_components;
        return HttpResponse.json(response);
      },
    ),
  );
  renderPreview("release-unresolved", "position-unresolved");
  fireEvent.click(
    screen.getByRole("button", { name: "Предпросмотр публикации" }),
  );
  expect(
    await screen.findByRole("button", { name: "Опубликовать" }),
  ).toBeDisabled();
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

it("locks source choice and retry after admission result becomes uncertain", async () => {
  let posts = 0;
  server.use(
    http.get(
      "*/api/library/releases/release-uncertain/positions/position-uncertain/publication/preview",
      ({ request }) => {
        const query = new URL(request.url).searchParams;
        const selected = query.has("location_id");
        return HttpResponse.json({
          ...preview("release-uncertain", "position-uncertain"),
          selected_source: selected
            ? candidates.find(
                (candidate) =>
                  candidate.location_id === query.get("location_id"),
              )
            : preview("release-uncertain", "position-uncertain")
                .selected_source,
          source_candidates: candidates,
        });
      },
    ),
    http.post(
      "*/api/library/releases/release-uncertain/positions/position-uncertain/publication",
      () => {
        posts += 1;
        return HttpResponse.json(
          { detail: "temporary failure" },
          { status: 500 },
        );
      },
    ),
  );
  renderPreview("release-uncertain", "position-uncertain");
  fireEvent.click(
    screen.getByRole("button", { name: "Предпросмотр публикации" }),
  );
  fireEvent.click(
    await screen.findByLabelText(/Основной звук · flac, поток 0/),
  );
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "Опубликовать" })).toBeEnabled(),
  );
  fireEvent.click(screen.getByRole("button", { name: "Опубликовать" }));
  fireEvent.click(await screen.findByRole("button", { name: "Опубликовать" }));
  expect(await screen.findByRole("alert")).toHaveTextContent(
    /Результат запуска неизвестен/,
  );
  expect(
    screen.getByLabelText(/Комментаторская дорожка · aac, поток 1/),
  ).toBeDisabled();
  expect(
    screen.getByRole("button", { name: "Предпросмотр публикации" }),
  ).toBeDisabled();
  expect(screen.getByRole("button", { name: "Опубликовать" })).toBeDisabled();
  fireEvent.click(screen.getByRole("button", { name: "Опубликовать" }));
  expect(posts).toBe(1);
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

it("shows desired tags from the API plan, preserving order and inventing none", async () => {
  server.use(
    http.get(
      "*/api/library/releases/release-tags/positions/position-tags/publication/preview",
      () =>
        HttpResponse.json({
          ...preview("release-tags", "position-tags"),
          desired_tags: {
            ARTISTS: ["Second Artist", "First Artist"],
            ARTIST: ["Second Artist feat. First Artist"],
            TITLE: ["Песня"],
          },
        }),
    ),
  );
  renderPreview("release-tags", "position-tags");
  fireEvent.click(
    screen.getByRole("button", { name: "Предпросмотр публикации" }),
  );
  const section = await screen.findByRole("region", {
    name: "Предпросмотр первой публикации",
  });
  const heading = within(section).getByRole("heading", {
    name: "Планируемые теги (не фактическое чтение файла)",
  });
  const tagsBlock = heading.parentElement;
  expect(tagsBlock).not.toBeNull();
  expect(
    within(tagsBlock as HTMLElement).getByText(
      "ARTISTS: Second Artist, First Artist",
    ),
  ).toBeInTheDocument();
  expect(
    within(tagsBlock as HTMLElement).getByText(
      "ARTIST: Second Artist feat. First Artist",
    ),
  ).toBeInTheDocument();
  expect(
    within(tagsBlock as HTMLElement).getByText("TITLE: Песня"),
  ).toBeInTheDocument();
  expect(tagsBlock?.querySelectorAll("p")).toHaveLength(3);
  expect(
    within(tagsBlock as HTMLElement).queryByText(/^GENRE:/),
  ).not.toBeInTheDocument();
  expect(
    within(tagsBlock as HTMLElement).queryByText(/^ALBUM:/),
  ).not.toBeInTheDocument();
});

it("requires an explicit source when several streams exist and admits only the previewed choice", async () => {
  const getQueries: URLSearchParams[] = [];
  const bodies: unknown[] = [];
  server.use(
    http.get(
      "*/api/library/releases/release-choice/positions/position-choice/publication/preview",
      ({ request }) => {
        const query = new URL(request.url).searchParams;
        getQueries.push(query);
        const selected = query.has("location_id")
          ? candidates.find(
              (item) => item.location_id === query.get("location_id"),
            )
          : undefined;
        return HttpResponse.json({
          ...preview("release-choice", "position-choice"),
          selected_source:
            selected ??
            preview("release-choice", "position-choice").selected_source,
          source_candidates: candidates,
        });
      },
    ),
    http.post(
      "*/api/library/releases/release-choice/positions/position-choice/publication",
      async ({ request }) => {
        bodies.push(await request.json());
        return HttpResponse.json({ id: "choice-operation" }, { status: 202 });
      },
    ),
  );
  renderPreview("release-choice", "position-choice");
  fireEvent.click(
    screen.getByRole("button", { name: "Предпросмотр публикации" }),
  );
  const primaryOption = await screen.findByLabelText(
    /Основной звук · flac, поток 0/,
  );
  expect(screen.getByLabelText("Источник не выбран")).toBeChecked();
  expect(screen.getByRole("button", { name: "Опубликовать" })).toBeDisabled();
  expect(getQueries[0]?.size).toBe(0);

  fireEvent.click(primaryOption);
  await waitFor(() => expect(getQueries).toHaveLength(2));
  expect(getQueries[1]?.get("location_id")).toBe("location-a");
  expect(getQueries[1]?.get("media_variant_id")).toBe("variant-a");
  expect(getQueries[1]?.get("stream_index")).toBe("0");
  expect(
    await screen.findByRole("button", { name: "Опубликовать" }),
  ).toBeEnabled();
  fireEvent.click(screen.getByRole("button", { name: "Опубликовать" }));
  await screen.findByText(/choice-operation/);
  expect(bodies).toEqual([
    {
      preview_digest: "a".repeat(64),
      expected_epoch: "expected-epoch",
      location_id: "location-a",
      media_variant_id: "variant-a",
      stream_index: 0,
    },
  ]);
});

it("invalidates a source preview and reports when its selected source was removed", async () => {
  let selectedGets = 0;
  const refresh = vi.fn();
  server.use(
    http.get(
      "*/api/library/releases/release-removed/positions/position-removed/publication/preview",
      ({ request }) => {
        const selected = new URL(request.url).searchParams.has("location_id");
        if (selected) selectedGets += 1;
        return HttpResponse.json({
          ...preview("release-removed", "position-removed"),
          selected_source: selected
            ? candidates[0]
            : preview("release-removed", "position-removed").selected_source,
          source_candidates: candidates,
        });
      },
    ),
    http.post(
      "*/api/library/releases/release-removed/positions/position-removed/publication",
      () =>
        HttpResponse.json(
          { detail: "selected source changed" },
          { status: 409 },
        ),
    ),
  );
  renderPreview("release-removed", "position-removed", vi.fn(), refresh);
  fireEvent.click(
    screen.getByRole("button", { name: "Предпросмотр публикации" }),
  );
  fireEvent.click(
    await screen.findByLabelText(/Основной звук · flac, поток 0/),
  );
  await waitFor(() => expect(selectedGets).toBe(1));
  fireEvent.click(await screen.findByRole("button", { name: "Опубликовать" }));
  expect(await screen.findByRole("alert")).toHaveTextContent(
    /Выбранный источник больше недоступен/,
  );
  expect(refresh).toHaveBeenCalledTimes(1);
  expect(
    screen.queryByRole("button", { name: "Опубликовать" }),
  ).not.toBeInTheDocument();
});

it("requires explicit selection for a sole eligible stream when audio facts include unsupported streams", async () => {
  let posts = 0;
  server.use(
    http.get(
      "*/api/library/releases/release-sole/positions/position-sole/publication/preview",
      ({ request }) => {
        const query = new URL(request.url).searchParams;
        const selected =
          query.get("location_id") === candidates[0].location_id &&
          query.get("media_variant_id") === candidates[0].media_variant_id &&
          query.get("stream_index") === String(candidates[0].stream_index);
        return HttpResponse.json({
          ...preview("release-sole", "position-sole"),
          selected_source: selected ? candidates[0] : undefined,
          blockers: selected ? [] : ["explicit stream selection is required"],
          source_candidates: [
            {
              ...candidates[0],
              audio_stream_count: 2,
              unsupported_audio_stream_count: 1,
            },
          ],
        });
      },
    ),
    http.post(
      "*/api/library/releases/release-sole/positions/position-sole/publication",
      () => {
        posts += 1;
        return HttpResponse.json({ id: "sole-operation" }, { status: 202 });
      },
    ),
  );
  renderPreview("release-sole", "position-sole");
  fireEvent.click(
    screen.getByRole("button", { name: "Предпросмотр публикации" }),
  );
  expect(
    await screen.findByRole("button", { name: "Опубликовать" }),
  ).toBeDisabled();
  expect(screen.getByLabelText("Источник не выбран")).toBeInTheDocument();
  expect(posts).toBe(0);
  fireEvent.click(screen.getByLabelText(/Основной звук · flac, поток 0/));
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "Опубликовать" })).toBeEnabled(),
  );
  expect(posts).toBe(0);
});
