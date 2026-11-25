import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { getGetIncomingGroupMatchingQueryKey } from "../../api/generated/client";
import type {
  IncomingGroupsBody,
  LibraryFirstPublicationPreviewResponse,
  MatchingManualBody,
  MatchingManualConfirmBody,
  MatchingManualPreview,
  MatchingReview,
  OperationResponse,
} from "../../api/generated/client.schemas";
import { I18nProvider, useI18n } from "../../app/i18n/host";
import { useHashLocation } from "../../app/navigation/hashLocation";
import { createMemoryStorage } from "../../test/memoryStorage";
import { server } from "../../test/server";
import {
  nextIncomingConfirmOrder,
  saveIncomingConfirmReceipt,
} from "./confirmReceipts";
import { IncomingGroupReviewScreen } from "./IncomingGroupReviewScreen";
import {
  type IncomingGroupsRoute,
  incomingGroupMatchingHref,
  incomingGroupsListHref,
  incomingGroupsListRoute,
  parseIncomingGroupsRoute,
} from "./incomingGroupsRoute";

const ids = {
  group: "group-1",
  memberOne: "member-1",
  memberTwo: "member-2",
  releaseOne: "release-1",
  releaseTwo: "release-2",
  trackOne: "track-1",
  trackOneAgain: "track-1b",
  trackTwo: "track-2",
};

const releases = [
  {
    provider_release_id: ids.releaseOne,
    release_id: "provider:album-one",
    title: "Первое издание",
    tracklist_complete: true,
    // Review layout coordinates are zero-based (medium/position start at 0).
    // The repeated recording spans two discs, so the one-based preview body
    // must report medium 1 for disc 0 and medium 2 for disc 1.
    positions: [
      {
        track_id: ids.trackOne,
        medium: 0,
        position: 0,
        displayed_number: "1",
        recording_id: "recording-repeat",
        title: "Открытие",
      },
      {
        track_id: ids.trackOneAgain,
        medium: 1,
        position: 0,
        displayed_number: "2",
        recording_id: "recording-repeat",
        title: "Открытие (повтор записи)",
      },
    ],
  },
  {
    provider_release_id: ids.releaseTwo,
    release_id: "provider:album-two",
    title: "Второе издание",
    tracklist_complete: true,
    positions: [
      {
        track_id: ids.trackTwo,
        medium: 0,
        position: 0,
        displayed_number: "1",
        recording_id: "recording-other",
        title: "Другая версия",
      },
    ],
  },
];

function incomingGroups(): IncomingGroupsBody {
  return {
    epoch: 1,
    revision: "group-revision",
    groups: [
      {
        id: ids.group,
        revision: "group-revision",
        manual: false,
        members: [ids.memberOne, ids.memberTwo],
        unready_members: [],
        diagnostics: [],
      },
    ],
    files: [
      {
        variant_id: ids.memberOne,
        capture_analysis_id: "cap-1",
        capture_identity: "capture-1",
        tags: { TITLE: ["Открытие"], ALBUM: ["Исходник"] },
        locations: [
          {
            location_id: "location-1",
            relative_path: "folder/01.flac",
            configured_path: "/inbox",
            inventory_path: "/inbox",
            root_id: "root-1",
            size_bytes: 100,
            mtime: "2026-01-01",
            technical_identity: "tech-1",
            work_id: "work-1",
          },
        ],
      },
      {
        variant_id: ids.memberTwo,
        capture_analysis_id: "cap-2",
        capture_identity: "capture-2",
        tags: { TITLE: ["Без ребра"], ARTIST: ["Исполнитель"] },
        locations: [
          {
            location_id: "location-2",
            relative_path: "folder/02.flac",
            configured_path: "/inbox",
            inventory_path: "/inbox",
            root_id: "root-1",
            size_bytes: 100,
            mtime: "2026-01-01",
            technical_identity: "tech-2",
            work_id: "work-2",
          },
        ],
      },
    ],
  };
}

function review(overrides: Partial<MatchingReview> = {}): MatchingReview {
  return {
    evaluated: true,
    proposal_available: true,
    evaluation_revision: "evaluation-1",
    group_id: ids.group,
    authority_mode: "automatic",
    authority_revision: 1,
    current_linked_manual_media_source_ids: [],
    members: [ids.memberOne, ids.memberTwo],
    releases,
    candidates: [
      {
        file_member_id: ids.memberOne,
        release_id: ids.releaseOne,
        release_confidence: 0.87,
        edges: [{ medium: 0, position: 0 }],
        positions: [
          {
            track_id: ids.trackOne,
            medium: 0,
            position: 0,
            displayed_number: "1",
            assignment_confidence: 0.87,
          },
        ],
      },
    ],
    proposal: {
      selected_release: null,
      releases,
      files: [
        {
          file_member_id: ids.memberOne,
          raw_tags: { ALBUM: ["Исходник"], TITLE: ["Открытие"] },
          diagnostics: [],
        },
        {
          file_member_id: ids.memberTwo,
          raw_tags: { TITLE: ["Без ребра"] },
          diagnostics: [],
        },
      ],
    },
    ...overrides,
  };
}

function preview(assignments: number): MatchingManualPreview {
  return {
    evaluation_revision: "evaluation-1",
    group_id: ids.group,
    local_release_id: "local-release-42",
    members: assignments
      ? [
          {
            file_member_id: ids.memberOne,
            locations: ["folder/01.flac"],
            assignment: {
              file_member_id: ids.memberOne,
              provider_track_id: ids.trackOne,
              medium: 1,
              position: 1,
            },
          },
        ]
      : [],
    omitted_member_ids: [ids.memberTwo],
    preview_digest: "digest-1",
    provider_release_id: ids.releaseOne,
    selected_release: {
      ...releases[0],
      // Frozen preview layouts may use one-based coordinates already.
      positions: releases[0].positions.map((position) => ({
        ...position,
        medium: position.medium + 1,
        position: position.position + 1,
      })),
    },
  };
}

type RequestCounts = { groups: number; review: number; preview: number };

function LocaleToggle() {
  const { locale, setPreference } = useI18n();
  return (
    <button
      type="button"
      onClick={() => setPreference(locale === "ru" ? "en" : "ru")}
    >
      Switch locale
    </button>
  );
}

function setup(
  reviewBody = review(),
  locale: "en" | "ru" = "ru",
  counts?: RequestCounts,
  withLocaleToggle = false,
  initialRoute: IncomingGroupsRoute = incomingGroupsListRoute,
) {
  const storage = createMemoryStorage();
  storage.setItem("melotrove-language", locale);
  vi.stubGlobal("localStorage", storage);
  window.location.hash =
    initialRoute.view === "list"
      ? incomingGroupsListHref(initialRoute.expandedGroupIds)
      : incomingGroupMatchingHref(
          initialRoute.groupId,
          initialRoute.releaseId,
          {
            localReleaseId: initialRoute.localReleaseId,
            prepareReleaseId: initialRoute.prepareReleaseId,
          },
        );
  server.use(
    http.get("/api/incoming-groups", () => {
      if (counts) counts.groups += 1;
      return HttpResponse.json(incomingGroups());
    }),
    http.get("/api/incoming-groups/:groupId/matching", () => {
      if (counts) counts.review += 1;
      return HttpResponse.json(reviewBody);
    }),
    http.post("/api/incoming-groups/:groupId/matching/preview", () => {
      if (counts) counts.preview += 1;
      return HttpResponse.json(preview(1));
    }),
  );
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={queryClient}>
      <I18nProvider>
        {withLocaleToggle ? <LocaleToggle /> : null}
        <IncomingRouteHarness />
      </I18nProvider>
    </QueryClientProvider>,
  );
  return queryClient;
}

function IncomingRouteHarness() {
  const hash = useHashLocation();
  const route = parseIncomingGroupsRoute(hash) ?? incomingGroupsListRoute;
  return <IncomingGroupReviewScreen route={route} />;
}

async function openMatching(locale: "en" | "ru" = "ru") {
  const label = locale === "ru" ? "Сопоставление" : "Matching";
  return screen.findByRole("region", { name: new RegExp(label) });
}

async function chooseRelease(title = "Первое издание") {
  await openMatching();
  await screen.findByRole(
    "heading",
    { name: "Сохраненная оценка" },
    { timeout: 5000 },
  );
  fireEvent.change(screen.getByLabelText("Издание (выбор вручную)"), {
    target: {
      value: title === "Первое издание" ? ids.releaseOne : ids.releaseTwo,
    },
  });
}

function assignFromRail(file: string, position: string, locale = "ru") {
  fireEvent.click(
    screen.getByRole("button", {
      name: locale === "ru" ? `Выбрать ${file}` : `Select ${file}`,
    }),
  );
  fireEvent.click(
    screen.getByRole("button", {
      name:
        locale === "ru"
          ? `Добавить ${file} к позиции ${position}`
          : `Add ${file} to ${position}`,
    }),
  );
}

function expectAssigned(file: string, position: string, locale = "ru") {
  expect(
    screen.getByRole("button", {
      name:
        locale === "ru"
          ? `Снять ${file} с позиции ${position}`
          : `Remove ${file} from ${position}`,
    }),
  ).toBeInTheDocument();
}

beforeEach(() => {
  window.location.hash = "";
  vi.restoreAllMocks();
});

afterEach(() => {
  vi.unstubAllGlobals();
});

it.each([
  "ru",
  "en",
] as const)("opens a deep-linked Local release in the common board without a saved evaluation (%s)", async (locale) => {
  server.use(
    http.get("/api/provider-catalog/releases/local-edition", () =>
      HttpResponse.json({
        id: "local-edition",
        title: "Local compilation",
        kind: "release",
        provider_code: "local",
        tracks: [
          {
            id: "local-track",
            recording_id: "local-recording",
            recording_title: "Local track",
            position: 0,
            displayed_number: "1",
          },
        ],
      }),
    ),
  );
  setup(
    review({
      evaluated: false,
      proposal_available: false,
      members: [],
      releases: [],
    }),
    locale,
    undefined,
    false,
    {
      view: "matching",
      groupId: ids.group,
      localReleaseId: "local-edition",
    },
  );
  const matching = await openMatching(locale);
  expect(
    await within(matching).findByRole("heading", { name: /Local compilation/ }),
  ).toBeVisible();
  const workspace = matching.querySelector("[data-matching-workspace]");
  expect(workspace).toBeInTheDocument();
  expect(workspace?.previousElementSibling).toHaveAttribute(
    "data-matching-candidates",
  );
  const position =
    locale === "ru"
      ? "Диск 1 · трек 1 · Local track"
      : "Disc 1 · track 1 · Local track";
  assignFromRail("folder/01.flac", position, locale);
  expectAssigned("folder/01.flac", position, locale);
});

it("opens one group matching route from the list and keeps the draft when returning", async () => {
  setup();
  expect(
    await screen.findByRole("region", { name: "Разбор входящих групп" }),
  ).toBeVisible();
  expect(
    screen.queryByRole("region", { name: /Сопоставление/ }),
  ).not.toBeInTheDocument();

  fireEvent.click(await screen.findByText("Открыть группу «Исходник»"));
  await waitFor(() =>
    expect(window.location.hash).toBe(incomingGroupMatchingHref(ids.group)),
  );
  expect(screen.getByRole("region", { name: /Сопоставление/ })).toBeVisible();
  expect(
    screen.queryByRole("region", { name: "Разбор входящих групп" }),
  ).not.toBeInTheDocument();

  await chooseRelease();
  expect(window.location.hash).toBe(
    incomingGroupMatchingHref(ids.group, ids.releaseOne),
  );
  assignFromRail("folder/01.flac", "Диск 1 · трек 1 · Открытие");
  fireEvent.click(screen.getByRole("link", { name: "← Все входящие группы" }));
  await waitFor(() =>
    expect(window.location.hash).toBe(incomingGroupsListHref([ids.group])),
  );
  expect(
    screen.queryByRole("region", { name: /Сопоставление/ }),
  ).not.toBeInTheDocument();
  fireEvent.click(await screen.findByText("Открыть группу «Исходник»"));
  await waitFor(() =>
    expect(window.location.hash).toBe(incomingGroupMatchingHref(ids.group)),
  );
  await chooseRelease();
  expectAssigned("folder/01.flac", "Диск 1 · трек 1 · Открытие");
});

it("previews and confirms only the chosen file when creating a new group", async () => {
  setup();
  const split = vi.fn();
  const confirm = vi.fn();
  const original = incomingGroups();
  const source = original.groups?.[0];
  if (!source) throw new Error("Missing source group fixture");
  const confirmed = {
    ...original,
    epoch: 2,
    revision: "confirmed-revision",
    groups: [
      {
        ...source,
        id: "confirmed-singleton",
        revision: "singleton-revision",
        manual: true,
        members: [ids.memberOne],
      },
      {
        ...source,
        id: "confirmed-remainder",
        revision: "remainder-revision",
        manual: true,
        members: [ids.memberTwo],
      },
    ],
  };
  server.use(
    http.post("/api/incoming-groups/preview", async ({ request }) => {
      split(await request.json());
      return HttpResponse.json({ ...original, draft_revision: "split-draft" });
    }),
    http.post("/api/incoming-groups/confirm", async ({ request }) => {
      confirm(await request.json());
      return HttpResponse.json(confirmed);
    }),
  );
  fireEvent.click(
    await screen.findByRole("button", { name: "Файлы группы «Исходник»" }),
  );
  fireEvent.click(
    await screen.findByRole("button", { name: "Действия с файлом: Открытие" }),
  );
  const create = await screen.findByRole("menuitem", {
    name: "Создать новую группу",
  });
  expect(split).not.toHaveBeenCalled();
  expect(confirm).not.toHaveBeenCalled();
  fireEvent.click(create);

  const action = {
    actions: [
      { kind: "split", group_id: ids.group, member_ids: [ids.memberOne] },
    ],
    base_revision: original.revision,
    epoch: original.epoch,
  };
  await waitFor(() =>
    expect(confirm.mock.calls).toEqual([
      [{ ...action, draft_revision: "split-draft" }],
    ]),
  );
  expect(split.mock.calls).toEqual([[action]]);
  await waitFor(() =>
    expect(window.location.hash).toBe(
      incomingGroupsListHref(["confirmed-remainder"]),
    ),
  );
  await waitFor(() =>
    expect(
      screen
        .getAllByRole("link", { name: /Открыть группу/ })
        .map((link) => link.getAttribute("href")),
    ).toEqual([
      incomingGroupMatchingHref("confirmed-singleton"),
      incomingGroupMatchingHref("confirmed-remainder"),
    ]),
  );
});

it("keeps a direct matching link and rejects an unknown group without another group", async () => {
  const counts: RequestCounts = { groups: 0, review: 0, preview: 0 };
  setup(review(), "ru", counts, false, {
    view: "matching",
    groupId: "missing-group",
  });
  expect(
    await screen.findByText("Этой входящей группы больше нет в списке."),
  ).toBeInTheDocument();
  expect(screen.queryByLabelText("Издание (выбор вручную)")).toBeNull();
  expect(counts.review).toBe(0);
});

it("restores the selected release from the URL without another first release", async () => {
  setup(review(), "ru", undefined, false, {
    view: "matching",
    groupId: ids.group,
    releaseId: ids.releaseTwo,
  });
  const heading = screen.getByRole("heading", { name: /^Сопоставление/ });
  expect(await screen.findByLabelText("Издание (выбор вручную)")).toHaveValue(
    ids.releaseTwo,
  );
  expect(screen.getByRole("heading", { name: /^Сопоставление/ })).toBe(heading);
  expect(heading).toHaveFocus();
});

it("changes locale without resetting assignments, preview, feedback, or queries", async () => {
  const counts: RequestCounts = { groups: 0, review: 0, preview: 0 };
  setup(review(), "ru", counts, true, {
    view: "matching",
    groupId: ids.group,
  });
  await chooseRelease();
  assignFromRail("folder/01.flac", "Диск 1 · трек 1 · Открытие");
  const tagOverrideTitle = "Теги позиции · Диск 1 · трек 1 · Открытие";
  fireEvent.click(screen.getAllByText(tagOverrideTitle)[0]);
  const firstTrackTags = within(
    screen.getByRole("group", {
      name: tagOverrideTitle,
    }),
  );
  fireEvent.click(
    firstTrackTags.getByRole("checkbox", {
      name: /Переопределить название/,
    }),
  );
  fireEvent.change(firstTrackTags.getByLabelText("Название"), {
    target: { value: "Локальное название" },
  });
  fireEvent.click(
    firstTrackTags.getByRole("checkbox", {
      name: /Переопределить список исполнителей/,
    }),
  );
  fireEvent.click(
    firstTrackTags.getByRole("button", { name: /Добавить исполнителя/ }),
  );
  fireEvent.change(screen.getByLabelText("Исполнитель 1"), {
    target: { value: "Локальный исполнитель" },
  });
  fireEvent.click(
    screen.getByRole("button", { name: "Открыть источник folder/01.flac" }),
  );
  const evidence = screen.getByRole("region", {
    name: "Доказательства текущей оценки",
  });
  expect(
    within(evidence)
      .getAllByText("87%")
      .filter((score) => !score.closest("details")),
  ).toHaveLength(1);
  expect(evidence).not.toHaveTextContent("0.87");
  fireEvent.click(within(evidence).getByText("Исходные теги файла"));
  const rawTagsPre = within(evidence).getByText(/"ALBUM"/);
  expect(rawTagsPre.tagName).toBe("PRE");
  expect(JSON.parse(rawTagsPre.textContent ?? "")).toEqual({
    ALBUM: ["Исходник"],
    TITLE: ["Открытие"],
  });
  fireEvent.click(
    screen.getByRole("button", { name: "Предпросмотр назначений" }),
  );
  const previewRegion = await screen.findByRole("region", {
    name: "Предпросмотр назначений",
  });
  expect(
    within(previewRegion).getByRole("row", {
      name: "folder/01.flac Диск 1 · трек 1 · Открытие",
    }),
  ).toBeVisible();
  expect(
    within(previewRegion).getByRole("row", {
      name: "folder/02.flac Останется во входящих",
    }),
  ).toBeVisible();

  server.use(
    http.post("/api/incoming-groups/:groupId/matching/confirm", () =>
      HttpResponse.json({ detail: "Conflict" }, { status: 409 }),
    ),
  );
  fireEvent.click(screen.getByRole("button", { name: "Сохранить связи" }));
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "Данные оценки или группы изменились",
  );
  const requestsBeforeLocaleChange = { ...counts };

  fireEvent.click(screen.getByRole("button", { name: "Switch locale" }));

  expect(
    await screen.findByRole("heading", { name: "Preview assignments" }),
  ).toBeInTheDocument();
  expectAssigned("folder/01.flac", "Disc 1 · track 1 · Открытие", "en");
  expect(screen.getByLabelText("Title")).toHaveValue("Локальное название");
  expect(screen.getByLabelText("Artist 1")).toHaveValue(
    "Локальный исполнитель",
  );
  const currentEvidence = screen.getByRole("region", {
    name: "Current evaluation evidence",
  });
  expect(
    within(currentEvidence)
      .getAllByText("87%")
      .filter((score) => !score.closest("details")),
  ).toHaveLength(1);
  const currentRawTagsPre = within(currentEvidence).getByText(/"ALBUM"/);
  expect(currentRawTagsPre.tagName).toBe("PRE");
  expect(JSON.parse(currentRawTagsPre.textContent ?? "")).toEqual({
    ALBUM: ["Исходник"],
    TITLE: ["Открытие"],
  });
  expect(screen.getByRole("alert")).toHaveTextContent(
    "Evaluation or group data changed",
  );
  expect(
    screen.getByRole("button", { name: "Refresh saved evaluation" }),
  ).toBeInTheDocument();
  expect(counts).toEqual(requestsBeforeLocaleChange);

  fireEvent.click(
    screen.getByRole("button", { name: "Refresh saved evaluation" }),
  );
  expect(
    await screen.findByText(
      "Evaluation refreshed. Your assignment draft was kept for the selected release.",
    ),
  ).toBeInTheDocument();
  expectAssigned("folder/01.flac", "Disc 1 · track 1 · Открытие", "en");
  expect(
    screen.queryByRole("heading", { name: "Preview assignments" }),
  ).not.toBeInTheDocument();
  const requestsBeforeSecondLocaleChange = { ...counts };

  fireEvent.click(screen.getByRole("button", { name: "Switch locale" }));
  expect(
    await screen.findByText(
      "Оценка обновлена. Черновик назначений сохранён для выбранного издания.",
    ),
  ).toBeInTheDocument();
  expectAssigned("folder/01.flac", "Диск 1 · трек 1 · Открытие");
  expect(counts).toEqual(requestsBeforeSecondLocaleChange);
});

it("uses saved automatic assignments as a baseline and keeps explicit removals", async () => {
  setup(
    review({
      automatic_assignment: {
        provider_release_id: ids.releaseOne,
        assignments: [
          {
            file_member_id: ids.memberOne,
            provider_track_id: ids.trackOne,
          },
        ],
      },
    }),
    "ru",
    undefined,
    false,
    { view: "matching", groupId: ids.group },
  );
  await chooseRelease();

  expectAssigned("folder/01.flac", "Диск 1 · трек 1 · Открытие");
  expect(screen.getByText("оценка 87%")).toBeInTheDocument();

  fireEvent.click(
    screen.getByRole("button", {
      name: "Снять folder/01.flac с позиции Диск 1 · трек 1 · Открытие",
    }),
  );
  expect(
    screen.queryByRole("button", { name: /Снять .* с позиции/ }),
  ).toBeNull();
  expect(
    screen.getByRole("button", { name: "Выбрать folder/01.flac" }),
  ).toBeInTheDocument();

  assignFromRail("folder/01.flac", "Диск 1 · трек 1 · Открытие");
  expectAssigned("folder/01.flac", "Диск 1 · трек 1 · Открытие");
});

describe("incoming manual group review", () => {
  it("confirms partial receipt; network retry preserves request id and payload", async () => {
    const queryClient = setup(
      review({
        authority_mode: "manual",
        current_linked_manual_media_source_ids: [
          "550e8400-e29b-41d4-a716-446655440000",
        ],
      }),
      "ru",
      undefined,
      false,
      { view: "matching", groupId: ids.group },
    );
    await openMatching();
    expect(await screen.findAllByText("folder/01.flac")).not.toHaveLength(0);
    expect(
      await screen.findByRole("button", {
        name: "Вернуть к автоматическому сопоставлению",
      }),
    ).toBeInTheDocument();
    expect(
      screen.queryByText("550e8400-e29b-41d4-a716-446655440000"),
    ).not.toBeInTheDocument();
    expect(screen.queryByText(ids.memberOne)).not.toBeInTheDocument();
    expect(screen.queryByText("recording-repeat")).not.toBeInTheDocument();
    await chooseRelease();
    expect(
      screen.getByRole("listitem", {
        name: "Диск 1 · трек 1 · Открытие",
      }),
    ).toBeInTheDocument();
    assignFromRail("folder/01.flac", "Диск 1 · трек 1 · Открытие");
    server.use(
      http.post("/api/incoming-groups/:groupId/matching/preview", () =>
        HttpResponse.json(preview(1)),
      ),
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Предпросмотр назначений" }),
    );
    await screen.findByRole("heading", {
      name: "Предпросмотр назначений",
    });

    const requestBodies: MatchingManualConfirmBody[] = [];
    const releaseId = "a3da35e5-4aa4-4836-9b8b-eae918e1fccc";
    const positionId = "9b9869bf-50b4-43db-87b7-0be6c4ec9d9c";
    const source = {
      file_member_id: ids.memberOne,
      position_id: positionId,
      track_source_id: "9f302b4d-e26b-4798-8c85-a6bcc5c3b2cc",
      media_variant_id: "7d94800f-99bd-4caf-af42-52b38e1eb01b",
    };
    const publicationPath = `/api/library/releases/${releaseId}/positions/${positionId}/publication`;
    const previewRequests: Record<string, string>[] = [];
    const publicationRequests: unknown[] = [];
    const queuedPublication: OperationResponse = {
      id: "fixture-operation",
      kind: "publish_track_v1",
      state: "queued",
      stage: "queued",
      bytes_completed: 0,
      created_at: "2026-11-22T12:00:00Z",
      updated_at: "2026-11-22T12:00:00Z",
    };
    let publicationSnapshot = queuedPublication;
    const publicationReads = vi.fn();
    let publicationStream: EventTarget | undefined;
    const closePublicationStream = vi.fn();
    vi.stubGlobal(
      "EventSource",
      class extends EventTarget {
        constructor() {
          super();
          publicationStream = this;
        }

        close = closePublicationStream;
      },
    );
    let finishPublicationRequest = () => {};
    const publicationReply = new Promise<void>((resolve) => {
      finishPublicationRequest = resolve;
    });
    const chosenSource = {
      location_id: "f07413f2-ab38-4440-bb15-f43a6d6de158",
      media_variant_id: source.media_variant_id,
      stream_index: 0,
      root_id: "fixture-root",
      codec: "flac",
      title: "",
      language: "",
      audio_stream_count: 1,
      unsupported_audio_stream_count: 0,
      requires_explicit_stream_selection: false,
    };
    const planned: LibraryFirstPublicationPreviewResponse = {
      release_id: releaseId,
      position_id: positionId,
      mode: "ordinary",
      epoch: "301fe18b-0650-4f41-b690-e3b44185a388",
      context_digest: "a".repeat(64),
      native_status: "checked",
      metadata_revisions: {},
      blockers: [],
      warnings: [],
      desired_tags: {},
      selected_source: chosenSource,
      source_candidates: [chosenSource],
      transform: {
        action: "copy",
        bitrate: 0,
        extension: "mka",
        output_container: "mka",
      },
      resolved_path_components: ["Opening.mka"],
    };
    const olderOrder = nextIncomingConfirmOrder();
    let failOnce = true;
    server.use(
      http.post(
        "/api/incoming-groups/:groupId/matching/confirm",
        async ({ request }) => {
          requestBodies.push(
            (await request.json()) as MatchingManualConfirmBody,
          );
          if (failOnce) {
            failOnce = false;
            return HttpResponse.error();
          }
          return HttpResponse.json({
            ...preview(1),
            local_release_id: releaseId,
            confirmed_sources: [source],
          });
        },
      ),
      http.get(`${publicationPath}/preview`, ({ request }) => {
        previewRequests.push(
          Object.fromEntries(new URL(request.url).searchParams),
        );
        return HttpResponse.json(planned);
      }),
      http.post(publicationPath, async ({ request }) => {
        publicationRequests.push(await request.json());
        await publicationReply;
        return HttpResponse.json(queuedPublication, { status: 202 });
      }),
      http.get("/api/operations/fixture-operation", () => {
        publicationReads();
        return HttpResponse.json(publicationSnapshot);
      }),
    );
    fireEvent.click(screen.getByRole("button", { name: "Сохранить связи" }));
    await screen.findByRole("alert");
    server.use(
      http.get("/api/incoming-groups", () => {
        const original = incomingGroups();
        const originalGroup = original.groups?.[0];
        if (!originalGroup || !original.files)
          throw new Error("Missing incoming fixture");
        return HttpResponse.json({
          ...original,
          groups: [
            {
              ...originalGroup,
              id: "remaining-group",
              members: [ids.memberTwo],
            },
          ],
          files: original.files.filter(
            (file) => file.variant_id === ids.memberTwo,
          ),
        });
      }),
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Повторить подтверждение" }),
    );
    expect(
      await screen.findByRole("heading", {
        name: "Связи сопоставления сохранены",
      }),
    ).toBeInTheDocument();
    await waitFor(() =>
      expect(screen.queryByText("folder/01.flac")).not.toBeInTheDocument(),
    );
    expect(requestBodies).toHaveLength(2);
    expect(requestBodies[1]).toEqual(requestBodies[0]);
    expect(requestBodies[0].request_id).toBeTruthy();
    expect(
      within(
        screen.getByRole("region", {
          name: "Остались во входящих",
        }),
      ).getByText(/02\.flac$/),
    ).toBeInTheDocument();
    expect(
      screen.queryByText("Этой входящей группы больше нет в списке."),
    ).not.toBeInTheDocument();
    expect(previewRequests).toEqual([]);
    expect(publicationRequests).toEqual([]);
    fireEvent.click(
      screen.getByRole("button", { name: "Проверить публикацию" }),
    );
    const publication = await screen.findByRole("region", {
      name: "Публикация по сохранённым связям",
    });
    expect(
      within(publication).queryByRole("radio", { name: "Принудительная" }),
    ).not.toBeInTheDocument();
    const confirmPublication = within(publication).getByRole("button", {
      name: "Опубликовать готовые позиции",
    });
    await waitFor(() => expect(confirmPublication).toBeEnabled());
    const proof = {
      kind: "manual",
      request_id: requestBodies[0].request_id,
      track_source_id: source.track_source_id,
    };
    expect(previewRequests).toEqual([
      {
        workflow: "matching_receipt",
        mode: "ordinary",
        receipt_kind: proof.kind,
        receipt_request_id: proof.request_id,
        receipt_track_source_id: proof.track_source_id,
      },
    ]);
    expect(publicationRequests).toEqual([]);
    fireEvent.click(confirmPublication);
    await waitFor(() =>
      expect(publicationRequests).toEqual([
        {
          workflow: "matching_receipt",
          mode: "ordinary",
          preview_digest: planned.context_digest,
          expected_epoch: planned.epoch,
          location_id: chosenSource.location_id,
          media_variant_id: chosenSource.media_variant_id,
          stream_index: 0,
          receipt: proof,
        },
      ]),
    );
    fireEvent.click(
      within(publication.parentElement ?? document.body).getByRole("button", {
        name: "К сохранённым связям",
      }),
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Проверить публикацию" }),
    );
    const reopened = await screen.findByRole("region", {
      name: "Публикация по сохранённым связям",
    });
    expect(
      within(reopened).getByRole("button", {
        name: "Опубликовать готовые позиции",
      }),
    ).toBeDisabled();
    expect(previewRequests).toHaveLength(1);
    finishPublicationRequest();
    expect(
      await within(reopened).findByText(
        "Операция публикации поставлена в очередь.",
      ),
    ).toBeInTheDocument();
    const publicationFrame = within(reopened.parentElement ?? document.body);
    expect(publicationFrame.queryByText("Результаты публикации")).toBeNull();
    await waitFor(() => expect(publicationReads).toHaveBeenCalled());
    publicationSnapshot = {
      ...queuedPublication,
      state: "succeeded",
      stage: "succeeded",
      updated_at: "2026-11-22T12:00:01Z",
    };
    const stream = publicationStream;
    if (!stream) throw new Error("The admitted operation is not followed");
    act(() => {
      stream.dispatchEvent(new Event("operation-changed"));
    });
    expect(
      await publicationFrame.findByText(/Опубликовано позиций: 1\./),
    ).toBeVisible();
    await waitFor(() => expect(closePublicationStream).toHaveBeenCalled());
    expect(publicationRequests).toHaveLength(1);
    fireEvent.click(
      screen.getByRole("button", { name: "К сохранённым связям" }),
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Проверить публикацию" }),
    );
    expect(await screen.findByText(/Опубликовано позиций: 1\./)).toBeVisible();
    expect(publicationStream).toBe(stream);
    expect(publicationRequests).toHaveLength(1);
    fireEvent.click(
      screen.getByRole("button", { name: "К сохранённым связям" }),
    );

    // An older successful response that arrives after this request cannot
    // replace the saved result for the same origin.
    act(() => {
      saveIncomingConfirmReceipt(queryClient, {
        kind: "manual",
        order: olderOrder,
        groupId: ids.group,
        releaseId: ids.releaseOne,
        requestId: "older-matching-request",
        response: { ...preview(1), omitted_member_ids: [] },
        omittedFileLabels: {},
      });
    });
    cleanup();
    render(
      <QueryClientProvider client={queryClient}>
        <I18nProvider>
          <IncomingRouteHarness />
        </I18nProvider>
      </QueryClientProvider>,
    );
    expect(
      await screen.findByRole("heading", {
        name: "Связи сопоставления сохранены",
      }),
    ).toBeInTheDocument();
    expect(
      within(
        screen.getByRole("region", { name: "Остались во входящих" }),
      ).getByText(/02\.flac$/),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Сохранить связи" }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", {
        name: "Вернуть к автоматическому сопоставлению",
      }),
    ).not.toBeInTheDocument();
    act(() => {
      window.location.hash = incomingGroupMatchingHref(
        ids.group,
        ids.releaseTwo,
      );
    });
    expect(
      await screen.findByText("Этой входящей группы больше нет в списке."),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("heading", {
        name: "Связи сопоставления сохранены",
      }),
    ).not.toBeInTheDocument();
  });

  it("does not compare a free file with an unrelated position or change the draft", async () => {
    setup(review(), "ru", undefined, false, {
      view: "matching",
      groupId: ids.group,
    });
    await chooseRelease();

    const inspector = within(screen.getByRole("region", { name: "Осмотр" }));
    const prompt =
      "Выберите файл в позиции релиза, чтобы увидеть совпадение для этой пары.";
    expect(inspector.getByText(prompt)).toBeVisible();
    fireEvent.click(
      screen.getByRole("button", { name: "Выбрать folder/02.flac" }),
    );
    fireEvent.click(
      screen.getByRole("button", {
        name: "Открыть позицию Диск 2 · трек 2 · Открытие (повтор записи)",
      }),
    );

    expect(
      inspector.getByText("Диск 2 · трек 2 · Открытие (повтор записи)"),
    ).toBeVisible();
    expect(inspector.getByText(prompt)).toBeVisible();
    expect(
      inspector.queryByRole("region", {
        name: "Доказательства текущей оценки",
      }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", {
        name: /Снять .* с позиции/,
      }),
    ).toBeNull();
  });

  it("invalidates the old preview after a background revision and keeps the explicit draft", async () => {
    const previewBodies: MatchingManualBody[] = [];
    const queryClient = setup(review(), "ru", undefined, false, {
      view: "matching",
      groupId: ids.group,
    });
    server.use(
      http.post(
        "/api/incoming-groups/:groupId/matching/preview",
        async ({ request }) => {
          previewBodies.push((await request.json()) as MatchingManualBody);
          return HttpResponse.json(preview(1));
        },
      ),
    );
    await chooseRelease();
    const repeatedPosition = "Диск 2 · трек 2 · Открытие (повтор записи)";
    assignFromRail("folder/01.flac", repeatedPosition);

    fireEvent.click(
      screen.getByRole("button", { name: "Предпросмотр назначений" }),
    );
    await screen.findByRole("heading", {
      name: "Предпросмотр назначений",
    });
    expect(previewBodies[0]).toMatchObject({
      expected_evaluation_revision: "evaluation-1",
      assignments: [
        {
          file_member_id: ids.memberOne,
          provider_track_id: ids.trackOneAgain,
          medium: 2,
          position: 1,
        },
      ],
    });

    act(() => {
      queryClient.setQueryData(
        getGetIncomingGroupMatchingQueryKey(ids.group),
        review({ evaluation_revision: "evaluation-2" }),
      );
    });

    // The digest belongs to the replaced revision, so the old preview and its
    // confirmation must disappear while the explicit draft survives.
    await waitFor(() =>
      expect(
        screen.queryByRole("heading", {
          name: "Предпросмотр назначений",
        }),
      ).not.toBeInTheDocument(),
    );
    expect(
      screen.queryByRole("button", { name: "Сохранить связи" }),
    ).not.toBeInTheDocument();
    expectAssigned("folder/01.flac", repeatedPosition);

    fireEvent.click(
      screen.getByRole("button", { name: "Предпросмотр назначений" }),
    );
    await screen.findByRole("heading", {
      name: "Предпросмотр назначений",
    });
    expect(previewBodies).toHaveLength(2);
    expect(previewBodies[1]).toMatchObject({
      expected_evaluation_revision: "evaluation-2",
      assignments: [
        {
          file_member_id: ids.memberOne,
          provider_track_id: ids.trackOneAgain,
          medium: 2,
          position: 1,
        },
      ],
    });
  });
});
