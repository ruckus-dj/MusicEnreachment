import "@testing-library/jest-dom/vitest";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { LocalProviderItem } from "../../api/generated/client.schemas";
import { I18nProvider } from "../../app/i18n/host";
import { createMemoryStorage } from "../../test/memoryStorage";

const api = vi.hoisted(() => ({
  createLocalProviderArtist: vi.fn(),
  createLocalProviderRelease: vi.fn(),
  getGenreCatalog: vi.fn(),
  getIncomingGroupManualPickerSearch: vi.fn(),
  listLocalProviderArtists: vi.fn(),
  listLocalProviderRecordings: vi.fn(),
  listLocalProviderReleases: vi.fn(),
  readLocalProviderArtist: vi.fn(),
  readLocalProviderRelease: vi.fn(),
  searchGenreCatalog: vi.fn(),
  startIncomingGroupManualPickerSearch: vi.fn(),
}));

vi.mock("../../api/generated/client", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../api/generated/client")>()),
  ...api,
}));

import { OwnCatalogScreen } from "./OwnCatalogScreen";

type ListParams = {
  q?: string;
  provider?: string;
  limit?: number;
  offset?: number;
};

const genre = {
  id: "mbid-genre",
  source_name: "indie rock",
  name: "Indie Rock",
};

const PAGE_SIZE = 50;
const RELEASE_TOTAL = 52;
// Outside the first page: proving the filter queries the whole Local list.
const DETAIL_POSITION = 51;
const UNIQUE_POSITION = 52;
const UNIQUE_TITLE = "Unique Target Release";
const releaseId = (position: number) =>
  `00000000-0000-4000-8001-${String(position).padStart(12, "0")}`;

function releaseRow(position: number): LocalProviderItem {
  const title =
    position === UNIQUE_POSITION ? UNIQUE_TITLE : `Local Release ${position}`;
  return {
    id: releaseId(position),
    kind: "release",
    provider_id: "local-provider",
    provider_code: "local",
    provider_key: `local-release-${position}`,
    title,
    release_group_title: title,
    credits: [],
    genres: [],
    // Paged list rows omit tracks; the detail read supplies them.
    tracks: null,
  };
}

const releases = Array.from({ length: RELEASE_TOTAL }, (_, index) =>
  releaseRow(index + 1),
);

const artists: LocalProviderItem[] = [1, 2, 3, 4].map((position) => ({
  id: `00000000-0000-4000-8002-${String(position).padStart(12, "0")}`,
  kind: "artist",
  provider_id: "local-provider",
  provider_code: "local",
  provider_key: `local-artist-${position}`,
  name: `Local Artist ${position}`,
  credits: [],
  genres: [],
  tracks: null,
}));

const recordings: LocalProviderItem[] = [1, 2, 3, 4].map((position) => ({
  id: `recording-${position}`,
  kind: "recording",
  provider_id: "local-provider",
  provider_code: "local",
  provider_key: `local-recording-${position}`,
  title: `Local Recording ${position}`,
  credits: [],
  genres: [],
  tracks: null,
}));

function page(rows: LocalProviderItem[], params: ListParams = {}) {
  const query = (params.q ?? "").trim().toLowerCase();
  const matched = query
    ? rows.filter((row) =>
        `${row.name ?? ""} ${row.title ?? ""}`.toLowerCase().includes(query),
      )
    : rows;
  const offset = params.offset ?? 0;
  const limit = params.limit ?? PAGE_SIZE;
  return {
    status: 200,
    data: {
      items: matched.slice(offset, offset + limit),
      limit,
      offset,
      total: matched.length,
    },
  };
}

function detail(item: LocalProviderItem, position: number) {
  if (item.kind !== "release" || position !== DETAIL_POSITION) return item;
  return {
    ...item,
    tracks: [
      {
        id: `track-${position}`,
        provider_id: "local-provider",
        provider_code: "local",
        recording_id: `recording-${position}`,
        recording_title: `Detail Track ${position}`,
        recording_duration_ms: 180_000,
        position: 0,
        displayed_number: "1",
        title_override: null,
        duration_override_ms: null,
      },
    ],
  };
}

const listParams = (fn: typeof api.listLocalProviderReleases) =>
  fn.mock.calls.map(([params]) => (params ?? {}) as ListParams);
const releaseParams = () => listParams(api.listLocalProviderReleases);
const lastReleaseParams = () => releaseParams().at(-1) ?? {};

const providerIsLocal = (params: ListParams) =>
  params.provider === undefined || params.provider === "local";

function field(...patterns: RegExp[]) {
  for (const pattern of patterns) {
    const [match] = screen.queryAllByLabelText(pattern);
    if (match) return match;
  }
  throw new Error(`No field matches ${patterns.join(" or ")}`);
}

function press(pattern: RegExp) {
  const matches = screen.getAllByRole("button", { name: pattern });
  return matches[matches.length - 1];
}

beforeEach(() => {
  window.history.replaceState(null, "", "/#/own-catalog");
  const storage = createMemoryStorage();
  storage.setItem("melotrove-language", "en");
  vi.stubGlobal("localStorage", storage);

  api.listLocalProviderReleases.mockImplementation((params?: ListParams) =>
    Promise.resolve(page(releases, params)),
  );
  api.listLocalProviderArtists.mockImplementation((params?: ListParams) =>
    Promise.resolve(page(artists, params)),
  );
  api.listLocalProviderRecordings.mockImplementation((params?: ListParams) =>
    Promise.resolve(page(recordings, params)),
  );
  api.readLocalProviderRelease.mockImplementation((id: string) => {
    const item = releases.find((row) => row.id === id);
    return Promise.resolve(
      item
        ? {
            status: 200,
            data: detail(item, Number(id.slice(-12))),
          }
        : { status: 404, data: { title: "Not Found", status: 404 } },
    );
  });
  api.readLocalProviderArtist.mockImplementation((id: string) => {
    const item = artists.find((row) => row.id === id);
    return Promise.resolve(
      item
        ? { status: 200, data: item }
        : { status: 404, data: { title: "Not Found", status: 404 } },
    );
  });
  api.createLocalProviderRelease.mockImplementation(
    (body: Record<string, unknown>) =>
      Promise.resolve({
        status: 200,
        data: {
          ...releaseRow(1),
          id: releaseId(RELEASE_TOTAL + 1),
          provider_key: "created-release",
          title: String(body.title),
          release_group_title: String(body.release_group_title),
        },
      }),
  );
  api.createLocalProviderArtist.mockImplementation(
    (body: Record<string, unknown>) =>
      Promise.resolve({
        status: 200,
        data: {
          ...artists[0],
          id: "00000000-0000-4000-8002-000000000005",
          provider_key: "created-artist",
          name: String(body.name),
          genres: (body.genres as LocalProviderItem["genres"]) ?? [],
        },
      }),
  );
  api.getGenreCatalog.mockResolvedValue({
    status: 200,
    data: {
      active_operation_id: null,
      count: 1,
      items: null,
      last_attempt_at: "2026-11-07T12:00:00Z",
      last_error: null,
      refresh_status: "succeeded",
      updated_at: "2026-11-07T12:00:00Z",
    },
  });
  api.searchGenreCatalog.mockResolvedValue({
    status: 200,
    data: { items: [genre] },
  });
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.clearAllMocks();
});

it("loads the first Local-only page and keeps independent count badges in accessible names", async () => {
  render(
    <I18nProvider>
      <OwnCatalogScreen />
    </I18nProvider>,
  );

  expect(
    await screen.findByRole("tab", { name: "Releases: 52" }),
  ).toBeInTheDocument();
  expect(
    screen.getByRole("tab", { name: "Recordings: 4" }),
  ).toBeInTheDocument();
  expect(screen.getByRole("tab", { name: "Artists: 4" })).toBeInTheDocument();
  expect(await screen.findByText("Local Release 1")).toBeInTheDocument();

  await waitFor(() =>
    expect(listParams(api.listLocalProviderArtists)).toContainEqual(
      expect.objectContaining({ provider: "local", limit: 1, offset: 0 }),
    ),
  );
  await waitFor(() =>
    expect(listParams(api.listLocalProviderRecordings)).toContainEqual(
      expect.objectContaining({ provider: "local", limit: 1, offset: 0 }),
    ),
  );
  expect(releaseParams()[0]).toEqual(
    expect.objectContaining({ provider: "local", limit: 50, offset: 0 }),
  );

  // The initial page never sends a query and never loads all providers.
  for (const params of [
    ...listParams(api.listLocalProviderArtists),
    ...listParams(api.listLocalProviderRecordings),
    ...releaseParams(),
  ]) {
    expect(params).not.toHaveProperty("q");
    expect(providerIsLocal(params)).toBe(true);
  }
  // The far end of the Local list is not on the first server page.
  expect(screen.queryByText(UNIQUE_TITLE)).toBeNull();
});

it("pages, filters the whole Local list from offset zero, and restores filter and page after record details", async () => {
  render(
    <I18nProvider>
      <OwnCatalogScreen />
    </I18nProvider>,
  );

  await screen.findByText("Local Release 1");
  const pager = () =>
    screen.getByRole("navigation", { name: "Own catalog pages" });
  const secondPageRow = `Local Release ${DETAIL_POSITION}`;

  fireEvent.click(within(pager()).getByRole("button", { name: "Next page" }));
  await waitFor(() =>
    expect(lastReleaseParams()).toEqual(
      expect.objectContaining({ offset: PAGE_SIZE }),
    ),
  );
  expect(await screen.findByText(secondPageRow)).toBeInTheDocument();
  expect(within(pager()).getByRole("status")).toHaveTextContent(/Page 2 of 2/);

  // A paged row has no tracks, so opening it must read the record detail.
  let completeDetail: (() => void) | undefined;
  api.readLocalProviderRelease.mockImplementationOnce(
    () =>
      new Promise((resolve) => {
        completeDetail = () =>
          resolve({
            status: 200,
            data: detail(releaseRow(DETAIL_POSITION), DETAIL_POSITION),
          });
      }),
  );
  fireEvent.click(
    screen.getByRole("button", {
      name: new RegExp(`^${secondPageRow}\\b`),
    }),
  );
  await waitFor(() =>
    expect(
      api.readLocalProviderRelease.mock.calls.some(
        ([id]) => id === releaseId(DETAIL_POSITION),
      ),
    ).toBe(true),
  );
  expect(
    screen.queryByRole("button", { name: "Edit" }),
  ).not.toBeInTheDocument();
  completeDetail?.();
  expect(
    await screen.findByRole("heading", {
      name: new RegExp(`^${secondPageRow}\\b`),
    }),
  ).toBeInTheDocument();
  expect(
    (await screen.findAllByText(new RegExp(`Detail Track ${DETAIL_POSITION}`)))
      .length,
  ).toBeGreaterThan(0);
  expect(screen.getByRole("button", { name: "Edit" })).toBeInTheDocument();
  expect(window.location.hash).toBe(
    `#/own-catalog?type=releases&id=${releaseId(DETAIL_POSITION)}`,
  );
  fireEvent.click(screen.getByRole("button", { name: "Back" }));
  expect(await screen.findByText(secondPageRow)).toBeInTheDocument();
  expect(within(pager()).getByRole("status")).toHaveTextContent(/Page 2 of 2/);

  // The filter applies to the whole server-side list, not the loaded page.
  const filter = screen.getByRole("searchbox", { name: "Filter all records" });
  fireEvent.change(filter, { target: { value: "Unique" } });
  await waitFor(() =>
    expect(lastReleaseParams()).toEqual(
      expect.objectContaining({ q: "Unique", offset: 0 }),
    ),
  );
  expect(await screen.findByText(UNIQUE_TITLE)).toBeInTheDocument();
  expect(within(pager()).getByRole("status")).toHaveTextContent(/Page 1 of 1/);

  // Clearing the filter omits the query and returns to the first page.
  fireEvent.change(filter, { target: { value: "" } });
  await waitFor(() => expect(lastReleaseParams()).not.toHaveProperty("q"));
  expect(await screen.findByText("Local Release 1")).toBeInTheDocument();
  expect(within(pager()).getByRole("status")).toHaveTextContent(/Page 1 of 2/);

  // Filter and page survive opening a record from a filtered, paged view.
  fireEvent.change(filter, { target: { value: "Local Release" } });
  await waitFor(() =>
    expect(lastReleaseParams()).toEqual(
      expect.objectContaining({ q: "Local Release", offset: 0 }),
    ),
  );
  const nextPage = within(pager()).getByRole("button", { name: "Next page" });
  await waitFor(() => expect(nextPage).toBeEnabled());
  fireEvent.click(nextPage);
  await waitFor(() =>
    expect(lastReleaseParams()).toEqual(
      expect.objectContaining({ q: "Local Release", offset: PAGE_SIZE }),
    ),
  );
  fireEvent.click(
    await screen.findByRole("button", {
      name: new RegExp(`^${secondPageRow}\\b`),
    }),
  );
  expect(
    await screen.findByRole("heading", {
      name: new RegExp(`^${secondPageRow}\\b`),
    }),
  ).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Back" }));
  expect(await screen.findByText(secondPageRow)).toBeInTheDocument();
  expect(
    screen.getByRole("searchbox", { name: "Filter all records" }),
  ).toHaveValue("Local Release");
  expect(within(pager()).getByRole("status")).toHaveTextContent(/Page 2 of 2/);
});

it("opens a direct detail link and only edits a successfully loaded record", async () => {
  const record = {
    ...detail(releaseRow(DETAIL_POSITION), DETAIL_POSITION),
    release_group_title: "Exact release group",
  };
  window.history.replaceState(
    null,
    "",
    `/#/own-catalog?type=releases&id=${record.id}`,
  );
  let completeDetail: (() => void) | undefined;
  api.readLocalProviderRelease.mockImplementationOnce(
    () =>
      new Promise((resolve) => {
        completeDetail = () => resolve({ status: 200, data: record });
      }),
  );
  render(
    <I18nProvider>
      <OwnCatalogScreen />
    </I18nProvider>,
  );
  expect(api.readLocalProviderRelease).toHaveBeenCalledWith(
    record.id,
    expect.objectContaining({ signal: expect.any(AbortSignal) }),
  );
  expect(
    screen.queryByRole("button", { name: "Edit" }),
  ).not.toBeInTheDocument();
  completeDetail?.();
  fireEvent.click(await screen.findByRole("button", { name: "Edit" }));
  expect(field(/^release title$/i)).toHaveValue("Exact release group");
  expect(screen.getByText(`Detail Track ${DETAIL_POSITION}`)).toBeVisible();
  fireEvent.click(screen.getByRole("button", { name: "Back" }));
  fireEvent.click(screen.getByRole("button", { name: "Back" }));
  expect(
    await screen.findByRole("tab", { name: "Releases: 52" }),
  ).toHaveAttribute("aria-selected", "true");
  expect(window.location.hash).toBe("#/own-catalog");
  act(() => {
    window.history.pushState(
      null,
      "",
      "/#/own-catalog?type=releases&id=../malformed",
    );
    window.dispatchEvent(new HashChangeEvent("hashchange"));
  });
  expect(await screen.findByRole("alert")).toBeVisible();
  expect(api.readLocalProviderRelease).toHaveBeenCalledTimes(1);
  expect(
    screen.queryByRole("button", { name: "Edit" }),
  ).not.toBeInTheDocument();
});

it("does not navigate when a create completes after leaving and returning to its tab", async () => {
  let completeCreate: (() => void) | undefined;
  api.createLocalProviderArtist.mockImplementationOnce(
    () =>
      new Promise((resolve) => {
        completeCreate = () => resolve({ status: 200, data: artists[0] });
      }),
  );
  render(
    <I18nProvider>
      <OwnCatalogScreen />
    </I18nProvider>,
  );
  fireEvent.click(await screen.findByRole("tab", { name: "Artists: 4" }));
  fireEvent.click(await screen.findByRole("button", { name: "Create artist" }));
  fireEvent.change(field(/^name/i), { target: { value: "Deferred artist" } });
  fireEvent.click(press(/^(Create (Local )?artist|Save( changes)?)$/i));
  await waitFor(() => expect(api.createLocalProviderArtist).toHaveBeenCalled());
  expect(screen.getByRole("button", { name: "Back" })).toBeDisabled();
  window.history.back();
  expect(await screen.findByText("Local Release 1")).toBeVisible();
  window.history.forward();
  expect(await screen.findByText("Local Artist 1")).toBeVisible();
  await act(async () => completeCreate?.());
  expect(window.location.hash).toBe("#/own-catalog?type=artists");
  expect(api.readLocalProviderArtist).not.toHaveBeenCalled();
});

it("creates a Local release explicitly without searching external editions", async () => {
  render(
    <I18nProvider>
      <OwnCatalogScreen />
    </I18nProvider>,
  );

  await screen.findByText("Local Release 1");
  const releaseCallsBeforeCreate =
    api.listLocalProviderReleases.mock.calls.length;

  fireEvent.click(screen.getByRole("button", { name: "Create release" }));
  // Creation is offered without any provider query.
  expect(api.listLocalProviderReleases.mock.calls.length).toBe(
    releaseCallsBeforeCreate,
  );
  expect(api.startIncomingGroupManualPickerSearch).not.toHaveBeenCalled();
  expect(api.getIncomingGroupManualPickerSearch).not.toHaveBeenCalled();

  fireEvent.change(field(/^release title$/i), {
    target: { value: "New Group" },
  });
  fireEvent.click(screen.getByText("More details", { exact: true }));
  fireEvent.change(field(/^edition title$/i), {
    target: { value: "New Release" },
  });
  fireEvent.click(press(/^(Create (Local )?release|Save( changes)?)$/i));

  await waitFor(() =>
    expect(api.createLocalProviderRelease).toHaveBeenCalled(),
  );
  expect(api.createLocalProviderRelease.mock.calls[0]?.[0]).toMatchObject({
    release_group_title: "New Group",
    title: "New Release",
  });
  for (const params of releaseParams()) {
    expect(params.provider ?? "local").toBe("local");
  }
  expect(api.startIncomingGroupManualPickerSearch).not.toHaveBeenCalled();
  expect(api.getIncomingGroupManualPickerSearch).not.toHaveBeenCalled();
});

it("creates a Local artist with a catalog genre from the explicit create action", async () => {
  render(
    <I18nProvider>
      <OwnCatalogScreen />
    </I18nProvider>,
  );

  fireEvent.click(await screen.findByRole("tab", { name: "Artists: 4" }));
  expect(await screen.findByText("Local Artist 1")).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: /^create.*artist$/i }));

  fireEvent.change(field(/^name/i), {
    target: { value: "Local Artist" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Artist genres" }));
  await waitFor(() => expect(api.getGenreCatalog).toHaveBeenCalled());

  const genreSearch = screen.getByRole("searchbox", {
    name: "Search the genre catalog",
  });
  await waitFor(() => expect(genreSearch).toBeEnabled());
  fireEvent.change(genreSearch, { target: { value: "indie" } });
  const option = await screen.findByRole("option", { name: /Indie Rock/ });
  fireEvent.click(option);
  fireEvent.click(screen.getByRole("button", { name: "Done" }));
  fireEvent.click(press(/^(Create (Local )?artist|Save( changes)?)$/i));

  await waitFor(() => expect(api.createLocalProviderArtist).toHaveBeenCalled());
  expect(api.createLocalProviderArtist.mock.calls[0]?.[0]).toMatchObject({
    name: "Local Artist",
    genres: [expect.objectContaining({ id: "mbid-genre", name: "Indie Rock" })],
  });
  expect(await screen.findByText("Local record created.")).toHaveAttribute(
    "role",
    "status",
  );
});
