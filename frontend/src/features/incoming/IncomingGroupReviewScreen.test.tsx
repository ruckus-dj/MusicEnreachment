import {
  cleanup,
  fireEvent,
  render,
  screen,
  within,
} from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type {
  IncomingGroupsBody,
  MatchingManualBody,
  MatchingManualConfirmBody,
  MatchingManualPreview,
  MatchingReview,
} from "../../api/generated/client.schemas";
import { server } from "../../test/server";
import { IncomingGroupReviewScreen } from "./IncomingGroupReviewScreen";

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

function setup(reviewBody = review()) {
  server.use(
    http.get("/api/incoming-groups", () => HttpResponse.json(incomingGroups())),
    http.get("/api/incoming-groups/:groupId/matching", () =>
      HttpResponse.json(reviewBody),
    ),
  );
  render(<IncomingGroupReviewScreen />);
}

async function chooseRelease(title = "Первое издание") {
  await screen.findByRole("heading", { name: "Сохраненная оценка" });
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

describe("incoming manual group review", () => {
  it("shows every member and all release layouts without inferring assignments; drafts are per release", async () => {
    setup();
    await chooseRelease();
    expect(
      screen.getByRole("heading", { name: /Участник 2/ }),
    ).toBeInTheDocument();
    expect(screen.getAllByText(/Без ребра/)).toHaveLength(2);
    const layout = screen.getByRole("region", {
      name: "Макет издания: Первое издание",
    });
    expect(within(layout).getByText(/повтор записи/i)).toBeInTheDocument();
    expect(screen.getByText("неизвестно")).toBeInTheDocument();
    const assignmentSelects = screen.getAllByLabelText("Назначение");
    expect(assignmentSelects).toHaveLength(2);
    expect(assignmentSelects[0]).toHaveValue("");
    expect(assignmentSelects[1]).toHaveValue("");

    fireEvent.change(assignmentSelects[0], { target: { value: ids.trackOne } });
    fireEvent.change(screen.getByLabelText("Издание (выбор вручную)"), {
      target: { value: ids.releaseTwo },
    });
    expect(screen.getAllByLabelText("Назначение")[0]).toHaveValue("");
    fireEvent.change(screen.getAllByLabelText("Назначение")[0], {
      target: { value: ids.trackTwo },
    });
    fireEvent.change(screen.getByLabelText("Издание (выбор вручную)"), {
      target: { value: ids.releaseOne },
    });
    expect(screen.getAllByLabelText("Назначение")[0]).toHaveValue(ids.trackOne);
  });

  it("sends one-based assignment coordinates for a repeated recording on another disc", async () => {
    setup();
    await chooseRelease();
    fireEvent.change(screen.getAllByLabelText("Назначение")[0], {
      target: { value: ids.trackOneAgain },
    });
    let previewBody: MatchingManualBody | undefined;
    server.use(
      http.post(
        "/api/incoming-groups/:groupId/matching/preview",
        async ({ request }) => {
          previewBody = (await request.json()) as MatchingManualBody;
          return HttpResponse.json(preview(1));
        },
      ),
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Предпросмотр всего назначения" }),
    );
    await screen.findByRole("heading", {
      name: "Предпросмотр полного назначения",
    });
    expect(previewBody).toMatchObject({
      provider_release_id: ids.releaseOne,
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

  it("invalidates preview after edits and confirms partial receipt; network retry preserves request id and payload", async () => {
    setup();
    await chooseRelease();
    fireEvent.change(screen.getAllByLabelText("Назначение")[0], {
      target: { value: ids.trackOne },
    });
    let previewBody: unknown;
    server.use(
      http.post(
        "/api/incoming-groups/:groupId/matching/preview",
        async ({ request }) => {
          previewBody = await request.json();
          return HttpResponse.json(preview(1));
        },
      ),
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Предпросмотр всего назначения" }),
    );
    await screen.findByRole("heading", {
      name: "Предпросмотр полного назначения",
    });
    expect(previewBody).toMatchObject({
      assignments: [
        {
          file_member_id: ids.memberOne,
          provider_track_id: ids.trackOne,
          medium: 1,
          position: 1,
        },
      ],
    });

    fireEvent.change(screen.getAllByLabelText("Назначение")[0], {
      target: { value: "" },
    });
    expect(
      screen.queryByRole("heading", {
        name: "Предпросмотр полного назначения",
      }),
    ).not.toBeInTheDocument();
    fireEvent.change(screen.getAllByLabelText("Назначение")[0], {
      target: { value: ids.trackOne },
    });
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
    fireEvent.click(
      screen.getByRole("button", { name: "Повторить подтверждение" }),
    );
    expect(
      await screen.findByRole("heading", {
        name: /Подтверждено · локальное издание local-release-42/,
      }),
    ).toBeInTheDocument();
    expect(requestBodies).toHaveLength(2);
    expect(requestBodies[1]).toEqual(requestBodies[0]);
    expect(requestBodies[0].request_id).toBeTruthy();
    expect(
      within(
        screen.getByRole("region", {
          name: "Подтверждено · локальное издание local-release-42",
        }),
      ).getByText(/остаётся во входящих/),
    ).toBeInTheDocument();
  });

  it("keeps the draft and asks for deliberate reload on conflict; reports unevaluated and unavailable states", async () => {
    setup();
    await chooseRelease();
    fireEvent.change(screen.getAllByLabelText("Назначение")[0], {
      target: { value: ids.trackOne },
    });
    server.use(
      http.post("/api/incoming-groups/:groupId/matching/preview", () =>
        HttpResponse.json(preview(1)),
      ),
      http.post("/api/incoming-groups/:groupId/matching/confirm", () =>
        HttpResponse.json(
          { detail: "matching evaluation or target changed" },
          { status: 409 },
        ),
      ),
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Предпросмотр всего назначения" }),
    );
    await screen.findByRole("heading", {
      name: "Предпросмотр полного назначения",
    });
    fireEvent.click(screen.getByRole("button", { name: "Явно подтвердить" }));
    expect(await screen.findByText(/Черновик сохранён/)).toBeInTheDocument();
    expect(screen.getAllByLabelText("Назначение")[0]).toHaveValue(ids.trackOne);
    expect(
      screen.getByRole("button", { name: "Обновить сохранённую оценку" }),
    ).toBeInTheDocument();

    const unevaluated = review({
      evaluated: false,
      proposal_available: false,
      proposal: undefined,
    });
    cleanup();
    setup(unevaluated);
    expect(
      await screen.findByText(/Оценка ещё не выполнялась/),
    ).toBeInTheDocument();
    const unavailable = review({
      proposal_available: false,
      proposal: undefined,
    });
    cleanup();
    setup(unavailable);
    expect(
      await screen.findByText(/предложения недоступны или неполны/),
    ).toBeInTheDocument();
  });
});
