import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { HttpResponse, http } from "msw";
import type { ReactNode } from "react";
import { afterEach, expect, it } from "vitest";
import type { LibraryPublicationListItemResponse } from "../../api/generated/client.schemas";
import { server } from "../../test/server";
import { LibraryPublicationsScreen } from "./LibraryPublicationsScreen";

const releaseId = "123e4567-e89b-42d3-a456-426614174001";
const positionId = "123e4567-e89b-42d3-a456-426614174002";

function publication(
  overrides: Partial<LibraryPublicationListItemResponse> = {},
): LibraryPublicationListItemResponse {
  return {
    release_id: releaseId,
    position_id: positionId,
    release_title: "Синий альбом",
    title: "Открытие",
    artists: [
      {
        artist_id: "123e4567-e89b-42d3-a456-426614174003",
        credit_name: "Анна",
        join_phrase: " & ",
      },
      {
        artist_id: "123e4567-e89b-42d3-a456-426614174004",
        credit_name: "Борис",
        join_phrase: "",
      },
    ],
    medium: 1,
    position: 2,
    displayed_number: "02",
    output_root: "/srv/music",
    output_epoch: "epoch-1",
    relative_path: "Анна/Синий альбом/02 - Открытие.mka",
    generation: "generation-1",
    validated_at: "2026-10-20T12:30:00Z",
    audio: {
      codec: "flac",
      container: "matroska",
      lossless: true,
      stream_index: 0,
      supported_source_container: true,
      alac_capabilities_verified: false,
    },
    ...overrides,
  };
}

function renderScreen(screenNode: ReactNode) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>{screenNode}</QueryClientProvider>,
  );
}

function page(
  items: LibraryPublicationListItemResponse[],
  total: number,
  url: URL,
) {
  return HttpResponse.json({
    items,
    total,
    limit: 50,
    offset: Number(url.searchParams.get("offset") ?? 0),
  });
}

afterEach(() => {
  cleanup();
  window.location.hash = "";
});

it("shows publication facts from the current endpoint without implying a freshness check", async () => {
  window.location.hash = "#/publications";
  server.use(
    http.get("*/api/library/publications", ({ request }) => {
      const url = new URL(request.url);
      expect(url.searchParams.get("limit")).toBe("50");
      expect(url.searchParams.get("offset")).toBe("0");
      return page([publication()], 1, url);
    }),
  );

  renderScreen(<LibraryPublicationsScreen />);

  expect(
    await screen.findByRole("link", { name: "Синий альбом" }),
  ).toHaveAttribute("href", `#/library/releases/${releaseId}`);
  expect(screen.getByRole("link", { name: "1.02 Открытие" })).toHaveAttribute(
    "href",
    `#/library/releases/${releaseId}/positions/${positionId}`,
  );
  expect(screen.getByText("Анна & Борис")).toBeInTheDocument();
  expect(screen.getByText("flac")).toBeInTheDocument();
  expect(screen.getByText("Контейнер: matroska")).toBeInTheDocument();
  expect(screen.getByText("/srv/music")).toBeInTheDocument();
  expect(
    screen.getByText("Анна/Синий альбом/02 - Открытие.mka"),
  ).toBeInTheDocument();
  const expectedTime = new Intl.DateTimeFormat("ru", {
    dateStyle: "medium",
    timeStyle: "short",
  }).format(new Date("2026-10-20T12:30:00Z"));
  expect(screen.getByText(expectedTime)).toHaveAttribute(
    "datetime",
    "2026-10-20T12:30:00Z",
  );
  expect(
    screen.getByRole("navigation", { name: "Страницы публикаций" }),
  ).toHaveTextContent("1–1 из 1");
  expect(screen.queryByText(/проверен|актуален|свеж/i)).not.toBeInTheDocument();
});

it("submits bounded search and pages through the current response using the URL", async () => {
  window.location.hash = "#/publications?q=old&offset=50";
  const requests: URL[] = [];
  server.use(
    http.get("*/api/library/publications", ({ request }) => {
      const url = new URL(request.url);
      requests.push(url);
      return page([publication()], 101, url);
    }),
  );
  renderScreen(<LibraryPublicationsScreen />);
  await screen.findByRole("link", { name: "Синий альбом" });
  expect(requests[0].searchParams.get("q")).toBe("old");
  expect(requests[0].searchParams.get("offset")).toBe("50");

  fireEvent.change(
    screen.getByRole("searchbox", { name: "Поиск по публикациям" }),
    {
      target: { value: "  новый запрос  " },
    },
  );
  fireEvent.click(screen.getByRole("button", { name: "Найти" }));
  await waitFor(() =>
    expect(window.location.hash).toBe(
      "#/publications?q=%D0%BD%D0%BE%D0%B2%D1%8B%D0%B9+%D0%B7%D0%B0%D0%BF%D1%80%D0%BE%D1%81",
    ),
  );
  await waitFor(() =>
    expect(requests.at(-1)?.searchParams.get("q")).toBe("новый запрос"),
  );
  expect(requests.at(-1)?.searchParams.get("limit")).toBe("50");
  expect(requests.at(-1)?.searchParams.get("offset")).toBe("0");

  fireEvent.click(screen.getByRole("button", { name: "Далее" }));
  await waitFor(() => expect(window.location.hash).toContain("offset=50"));
  await waitFor(() =>
    expect(requests.at(-1)?.searchParams.get("offset")).toBe("50"),
  );
  expect(requests.at(-1)?.searchParams.get("q")).toBe("новый запрос");
});

it("shows a useful empty state with library and source links", async () => {
  window.location.hash = "#/publications";
  server.use(
    http.get("*/api/library/publications", ({ request }) =>
      page([], 0, new URL(request.url)),
    ),
  );
  renderScreen(<LibraryPublicationsScreen />);

  expect(
    await screen.findByRole("heading", { name: "Публикаций не найдено" }),
  ).toBeInTheDocument();
  expect(
    screen.getByRole("link", { name: "Открыть медиатеку" }),
  ).toHaveAttribute("href", "#/library");
  expect(
    screen.getByRole("link", { name: "Открыть источники" }),
  ).toHaveAttribute("href", "#/sources");
});

it("reports endpoint errors and retries the same current-list request", async () => {
  window.location.hash = "#/publications";
  let requestCount = 0;
  server.use(
    http.get("*/api/library/publications", ({ request }) => {
      requestCount += 1;
      if (requestCount === 1)
        return HttpResponse.json(
          { detail: "Список временно недоступен." },
          { status: 503 },
        );
      return page(
        [publication({ output_root: null })],
        1,
        new URL(request.url),
      );
    }),
  );
  renderScreen(<LibraryPublicationsScreen />);

  expect(await screen.findByRole("alert")).toHaveTextContent(
    "Список временно недоступен.",
  );
  fireEvent.click(screen.getByRole("button", { name: "Повторить" }));
  expect(
    await screen.findByRole("link", { name: "Синий альбом" }),
  ).toBeInTheDocument();
  expect(screen.getByText("Не указан в записи")).toBeInTheDocument();
  await waitFor(() => expect(requestCount).toBe(2));
});
