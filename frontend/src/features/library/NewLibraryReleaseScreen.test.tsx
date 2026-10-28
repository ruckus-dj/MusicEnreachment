import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { expect, it } from "vitest";
import type { LibraryRecordingOption } from "../../api/generated/client.schemas";
import { server } from "../../test/server";
import { NewLibraryReleaseScreen } from "./NewLibraryReleaseScreen";

const releaseID = "123e4567-e89b-42d3-a456-426614174000";
function renderScreen() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <NewLibraryReleaseScreen />
    </QueryClientProvider>,
  );
}

it("sends an actually empty optional release and navigates only after the real create response", async () => {
  let received: Record<string, unknown> | undefined;
  server.use(
    http.post("*/api/library/releases", async ({ request }) => {
      received = (await request.json()) as Record<string, unknown>;
      return HttpResponse.json(
        { release_id: releaseID, media: [], positions: [] },
        { status: 201 },
      );
    }),
  );
  renderScreen();
  expect(
    screen.getByRole("button", { name: "Создать локальный релиз" }),
  ).toBeEnabled();
  fireEvent.click(
    screen.getByRole("button", { name: "Создать локальный релиз" }),
  );
  await waitFor(() =>
    expect(window.location.hash).toBe(`#/library/releases/${releaseID}`),
  );
  expect(received).toMatchObject({
    title: "",
    release_date: "",
    country: "",
    label: "",
    catalog_number: "",
    release_type: "",
    new_artists: [],
    new_recordings: [],
    credits: [],
    media: [],
    cover: null,
  });
});

it("preserves intentional text, creates independent rows and retains the draft on create failure", async () => {
  server.use(
    http.post("*/api/library/releases", () =>
      HttpResponse.json({ detail: "temporarily unavailable" }, { status: 503 }),
    ),
  );
  const { unmount } = renderScreen();
  fireEvent.change(screen.getByLabelText("Название релиза"), {
    target: { value: "" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Добавить credit" }));
  fireEvent.change(screen.getByLabelText("Роль"), { target: { value: "" } });
  fireEvent.click(screen.getByRole("button", { name: "Добавить носитель" }));
  fireEvent.click(screen.getByRole("button", { name: "Добавить позицию" }));
  fireEvent.change(screen.getByLabelText("Отображаемый номер"), {
    target: { value: "A1" },
  });
  fireEvent.change(screen.getByLabelText("Название позиции"), {
    target: { value: "Track title" },
  });
  unmount();
  renderScreen();
  expect(screen.getByDisplayValue("A1")).toBeInTheDocument();
  expect(screen.getByDisplayValue("Track title")).toBeInTheDocument();
  fireEvent.click(
    screen.getByRole("button", { name: "Создать локальный релиз" }),
  );
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "temporarily unavailable",
  );
  expect(screen.getByDisplayValue("Track title")).toBeInTheDocument();
});

it("does not treat a conflicting release id as proof that this draft was created", async () => {
  let attemptedID = "";
  let detailReads = 0;
  server.use(
    http.post("*/api/library/releases", async ({ request }) => {
      const body = (await request.json()) as { release_id: string };
      attemptedID = body.release_id;
      return HttpResponse.json({ detail: "already exists" }, { status: 409 });
    }),
    http.get("*/api/library/releases/:id", () => {
      detailReads += 1;
      return HttpResponse.json({ release_id: attemptedID }, { status: 200 });
    }),
  );
  const previousHash = window.location.hash;
  renderScreen();
  fireEvent.click(
    screen.getByRole("button", { name: "Создать локальный релиз" }),
  );
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "не подтверждает создание",
  );
  expect(
    screen.getByRole("link", { name: "Открыть существующий релиз" }),
  ).toHaveAttribute("href", `#/library/releases/${attemptedID}`);
  expect(window.location.hash).toBe(previousHash);
  expect(detailReads).toBe(0);
  expect(
    screen.getByRole("button", { name: "Создать локальный релиз" }),
  ).toBeEnabled();
});

it("retries an uncertain create with the exact same frozen payload and client id", async () => {
  const attempted: Record<string, unknown>[] = [];
  server.use(
    http.post("*/api/library/releases", async ({ request }) => {
      attempted.push((await request.json()) as Record<string, unknown>);
      if (attempted.length === 1) return HttpResponse.error();
      return HttpResponse.json(
        { release_id: attempted[1].release_id, media: [], positions: [] },
        { status: 201 },
      );
    }),
  );
  renderScreen();
  const create = () =>
    fireEvent.click(
      screen.getByRole("button", { name: "Создать локальный релиз" }),
    );
  create();
  expect(await screen.findByRole("alert")).toBeInTheDocument();
  create();
  await waitFor(() =>
    expect(window.location.hash).toBe(
      `#/library/releases/${attempted[0].release_id}`,
    ),
  );
  expect(attempted).toHaveLength(2);
  expect(attempted[1]).toEqual(attempted[0]);
});

it("uses the requested cover page offset and expected checksum image parameter", async () => {
  const offsets: string[] = [];
  const candidate = {
    height: 20,
    label: "Cover",
    media_variant_id: "media-1",
    metadata_result_id: "metadata-1",
    mime: "image/jpeg",
    sha256: "a".repeat(64),
    size: 12,
    width: 20,
  };
  server.use(
    http.get("*/api/library/cover-candidates", ({ request }) => {
      const url = new URL(request.url);
      offsets.push(url.searchParams.get("offset") ?? "");
      return HttpResponse.json(
        { items: [candidate], total: 30 },
        { status: 200 },
      );
    }),
  );
  renderScreen();
  fireEvent.click(screen.getByRole("button", { name: "Показать обложки" }));
  await screen.findByText(/Cover ·/);
  expect(screen.getByRole("presentation")).toHaveAttribute(
    "src",
    `/api/library/cover-candidates/metadata-1/image?expected_sha256=${"a".repeat(64)}`,
  );
  fireEvent.click(screen.getByRole("button", { name: "Далее" }));
  await waitFor(() => expect(offsets).toEqual(["0", "25"]));
});

it("retains the selected local recording identity and label separately from the position title", async () => {
  const recording = {
    artists: [],
    duration_ms: null,
    id: "recording-1",
    provider_links: [],
    title: "Recording label",
  } satisfies LibraryRecordingOption;
  server.use(
    http.get("*/api/library/recordings", () =>
      HttpResponse.json({ items: [recording], total: 1 }, { status: 200 }),
    ),
  );
  renderScreen();
  fireEvent.click(screen.getByRole("button", { name: "Добавить носитель" }));
  const addPosition = screen
    .getAllByRole("button", { name: "Добавить позицию" })
    .at(-1);
  if (!addPosition)
    throw new Error("Expected an add-position button for the new medium");
  fireEvent.click(addPosition);
  const positionTitle = screen.getAllByLabelText("Название позиции").at(-1);
  if (!positionTitle)
    throw new Error("Expected a title field for the new position");
  fireEvent.change(positionTitle, {
    target: { value: "Position title" },
  });
  fireEvent.change(screen.getByLabelText("Recording"), {
    target: { value: "local" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Найти recording" }));
  fireEvent.click(
    await screen.findByRole("button", { name: /Recording label/ }),
  );
  expect(screen.getByDisplayValue("Position title")).toBeInTheDocument();
  expect(
    screen.getByRole("button", { name: /Выбран recording: Recording label/ }),
  ).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Снять выбор" }));
  expect(
    screen.getByRole("button", { name: "Найти recording" }),
  ).toBeInTheDocument();
});

it("reorders credits as stable rows without losing their fields", async () => {
  let submitted: Record<string, unknown> | undefined;
  server.use(
    http.post("*/api/library/releases", async ({ request }) => {
      submitted = (await request.json()) as Record<string, unknown>;
      return HttpResponse.json(
        { release_id: releaseID, media: [], positions: [] },
        { status: 201 },
      );
    }),
  );
  renderScreen();
  fireEvent.click(screen.getByRole("button", { name: "Добавить credit" }));
  fireEvent.click(screen.getByRole("button", { name: "Добавить credit" }));
  const names = screen.getAllByLabelText("Артист — существующий или новое имя");
  fireEvent.change(names[0], { target: { value: "First" } });
  fireEvent.change(names[1], { target: { value: "Second" } });
  const roles = screen.getAllByLabelText("Роль");
  fireEvent.change(roles[0], { target: { value: "Composer" } });
  fireEvent.click(screen.getAllByRole("button", { name: "↓" })[0]);
  expect(
    screen
      .getAllByLabelText("Артист — существующий или новое имя")
      .map((input) => (input as HTMLInputElement).value),
  ).toEqual(["Second", "First"]);
  fireEvent.click(
    screen.getByRole("button", { name: "Создать локальный релиз" }),
  );
  await waitFor(() => expect(submitted).toBeDefined());
  expect(submitted?.new_artists).toEqual(
    expect.arrayContaining([
      expect.objectContaining({ name: "First" }),
      expect.objectContaining({ name: "Second" }),
    ]),
  );
  expect(submitted?.credits).toEqual(
    expect.arrayContaining([expect.objectContaining({ role: "Composer" })]),
  );
});
