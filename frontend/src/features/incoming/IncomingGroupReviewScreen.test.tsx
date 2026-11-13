import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type {
  IncomingGroupsBody,
  MatchingManualConfirmBody,
  MatchingManualPreview,
  MatchingReview,
} from "../../api/generated/client.schemas";
import { I18nProvider, useI18n } from "../../i18n/I18nProvider";
import { createMemoryStorage } from "../../test/memoryStorage";
import { server } from "../../test/server";
import { IncomingGroupReviewScreen } from "./IncomingGroupReviewScreen";
import { localMatchingMessages } from "./localMatchingMessages";

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
    selected_release: releases[0],
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
) {
  const storage = createMemoryStorage();
  storage.setItem("melotrove-language", locale);
  vi.stubGlobal("localStorage", storage);
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
  render(
    <I18nProvider>
      {withLocaleToggle ? <LocaleToggle /> : null}
      <IncomingGroupReviewScreen />
    </I18nProvider>,
  );
}

async function chooseRelease(title = "Первое издание") {
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
] as const)("shows the localized Local provider matching summary (%s)", async (locale) => {
  setup(review(), locale);
  expect(
    await screen.findByText(localMatchingMessages[locale].sectionSummary),
  ).toBeInTheDocument();
});

it("changes locale without resetting assignments, preview, feedback, or queries", async () => {
  const counts: RequestCounts = { groups: 0, review: 0, preview: 0 };
  setup(review(), "ru", counts, true);
  await chooseRelease();
  fireEvent.change(screen.getAllByLabelText("Назначение")[0], {
    target: { value: ids.trackOne },
  });
  fireEvent.click(
    screen.getByRole("button", { name: "Предпросмотр всего назначения" }),
  );
  await screen.findByRole("heading", {
    name: "Предпросмотр полного назначения",
  });

  server.use(
    http.post("/api/incoming-groups/:groupId/matching/confirm", () =>
      HttpResponse.json({ detail: "Conflict" }, { status: 409 }),
    ),
  );
  fireEvent.click(screen.getByRole("button", { name: "Явно подтвердить" }));
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "Данные оценки или группы изменились",
  );
  const requestsBeforeLocaleChange = { ...counts };

  fireEvent.click(screen.getByRole("button", { name: "Switch locale" }));

  expect(
    await screen.findByRole("heading", { name: "Full assignment preview" }),
  ).toBeInTheDocument();
  expect(screen.getAllByLabelText("Assignment")[0]).toHaveValue(ids.trackOne);
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
  expect(screen.getAllByLabelText("Assignment")[0]).toHaveValue(ids.trackOne);
  expect(
    screen.queryByRole("heading", { name: "Full assignment preview" }),
  ).not.toBeInTheDocument();
  const requestsBeforeSecondLocaleChange = { ...counts };

  fireEvent.click(screen.getByRole("button", { name: "Switch locale" }));
  expect(
    await screen.findByText(
      "Оценка обновлена. Черновик назначений сохранён для выбранного издания.",
    ),
  ).toBeInTheDocument();
  expect(screen.getAllByLabelText("Назначение")[0]).toHaveValue(ids.trackOne);
  expect(counts).toEqual(requestsBeforeSecondLocaleChange);
});

describe("incoming manual group review", () => {
  it("confirms partial receipt; network retry preserves request id and payload", async () => {
    setup(
      review({
        authority_mode: "manual",
        current_linked_manual_media_source_ids: [
          "550e8400-e29b-41d4-a716-446655440000",
        ],
      }),
      "ru",
    );
    expect(await screen.findAllByText("folder/01.flac")).not.toHaveLength(0);
    expect(
      screen.queryByText("550e8400-e29b-41d4-a716-446655440000"),
    ).not.toBeInTheDocument();
    expect(screen.queryByText(ids.memberOne)).not.toBeInTheDocument();
    expect(screen.queryByText("recording-repeat")).not.toBeInTheDocument();
    await chooseRelease();
    expect(
      screen.getAllByText("Диск 1 · трек 1 · Открытие").length,
    ).toBeGreaterThan(0);
    fireEvent.change(screen.getAllByLabelText("Назначение")[0], {
      target: { value: ids.trackOne },
    });
    server.use(
      http.post("/api/incoming-groups/:groupId/matching/preview", () =>
        HttpResponse.json(preview(1)),
      ),
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Предпросмотр всего назначения" }),
    );
    await screen.findByRole("heading", {
      name: "Предпросмотр полного назначения",
    });

    const requestBodies: MatchingManualConfirmBody[] = [];
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
          return HttpResponse.json(preview(1));
        },
      ),
    );
    fireEvent.click(screen.getByRole("button", { name: "Явно подтвердить" }));
    await screen.findByRole("alert");
    server.use(
      http.get("/api/incoming-groups", () => {
        return HttpResponse.json({ ...incomingGroups(), files: [] });
      }),
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Повторить подтверждение" }),
    );
    expect(
      await screen.findByRole("heading", {
        name: "Подтверждено · локальное издание",
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
          name: "Подтверждено · локальное издание",
        }),
      ).getByText(/остаётся во входящих/),
    ).toBeInTheDocument();
    expect(
      within(
        screen.getByRole("region", {
          name: "Подтверждено · локальное издание",
        }),
      ).getByText("folder/02.flac остаётся во входящих"),
    ).toBeInTheDocument();
  });

  it("inspects a file and position without changing assignment drafts", async () => {
    setup(review(), "ru");
    await chooseRelease();

    const fileCard = screen
      .getAllByLabelText("Назначение")[1]
      .closest("article");
    expect(fileCard).not.toBeNull();
    fireEvent.click(
      within(fileCard as HTMLElement).getByRole("button", {
        name: /folder\/02\.flac/,
      }),
    );
    fireEvent.click(
      screen.getByRole("button", {
        name: "Диск 2 · трек 2 · Открытие (повтор записи)",
      }),
    );

    expect(
      screen.getByRole("heading", {
        name: "Осмотр: folder/02.flac · Диск 2 · трек 2 · Открытие (повтор записи)",
      }),
    ).toBeInTheDocument();
    expect(
      screen
        .getAllByLabelText<HTMLSelectElement>("Назначение")
        .map((select) => select.value),
    ).toEqual(["", ""]);
  });
});
