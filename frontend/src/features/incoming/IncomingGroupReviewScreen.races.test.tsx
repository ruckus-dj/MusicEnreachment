import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { beforeEach, describe, expect, it } from "vitest";
import type {
  IncomingGroupsBody,
  MatchingReview,
} from "../../api/generated/client.schemas";
import { server } from "../../test/server";
import { IncomingGroupReviewScreen } from "./IncomingGroupReviewScreen";

const groups = ["group-a", "group-b"];
const members = ["member-a", "member-b"];
const releases = ["release-a", "release-b"];
const tracks = ["track-a", "track-b"];

function groupsBody(): IncomingGroupsBody {
  return {
    epoch: 1,
    revision: "revision",
    groups: groups.map((id, index) => ({
      id,
      revision: "revision",
      manual: false,
      members: [members[index]],
      unready_members: [],
      diagnostics: [],
    })),
    files: [],
  };
}

function review(groupId: string, index: number): MatchingReview {
  return {
    evaluated: true,
    proposal_available: true,
    evaluation_revision: `evaluation-${groupId}`,
    group_id: groupId,
    authority_mode: "automatic",
    authority_revision: 1,
    current_linked_manual_media_source_ids: [],
    members: [members[index]],
    releases: [
      {
        provider_release_id: releases[index],
        release_id: `provider:${releases[index]}`,
        title: `Release ${index}`,
        tracklist_complete: true,
        positions: [
          {
            track_id: tracks[index],
            medium: 0,
            position: 0,
            displayed_number: "1",
            recording_id: `recording-${index}`,
            title: `Track ${index}`,
          },
        ],
      },
    ],
    candidates: [],
    proposal: { selected_release: null, files: [] },
  };
}

function setup() {
  server.use(
    http.get("/api/incoming-groups", () => HttpResponse.json(groupsBody())),
    http.get("/api/incoming-groups/:groupId/matching", ({ params }) =>
      HttpResponse.json(
        review(
          params.groupId as string,
          groups.indexOf(params.groupId as string),
        ),
      ),
    ),
  );
  render(<IncomingGroupReviewScreen />);
}

// Readiness gate: the "Сохраненная оценка" heading only renders once the
// active group's matching request has resolved, so it is the correct async
// condition to await before touching the review controls. CI runs this suite
// alongside competing Go/Postgres load, where that request can take well over
// Testing Library's 1s default; wait on the rendered condition with an explicit
// bounded timeout instead of an arbitrary sleep.
const readinessTimeout = 5000;

function awaitSavedEvaluation() {
  return screen.findByRole(
    "heading",
    { name: "Сохраненная оценка" },
    { timeout: readinessTimeout },
  );
}

async function chooseCurrentRelease() {
  await awaitSavedEvaluation();
  fireEvent.change(screen.getByLabelText("Издание (выбор вручную)"), {
    target: { value: releases[0] },
  });
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}

const previewBody = {
  evaluation_revision: "evaluation-group-a",
  group_id: "group-a",
  local_release_id: "local-a",
  members: [],
  omitted_member_ids: [members[0]],
  preview_digest: "digest-a",
  provider_release_id: releases[0],
  selected_release: {
    provider_release_id: releases[0],
    release_id: "provider:release-a",
    title: "Release 0",
    tracklist_complete: true,
    positions: [],
  },
};

beforeEach(() => {
  window.location.hash = "";
});

describe("incoming review request races", () => {
  it("ignores a preview response after switching groups", async () => {
    setup();
    await chooseCurrentRelease();
    const pending = deferred<Response>();
    server.use(
      http.post(
        "/api/incoming-groups/:groupId/matching/preview",
        () => pending.promise,
      ),
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Предпросмотр всего назначения" }),
    );
    fireEvent.change(screen.getByLabelText("Входящая группа"), {
      target: { value: groups[1] },
    });
    await awaitSavedEvaluation();
    pending.resolve(HttpResponse.json(previewBody));
    await Promise.resolve();
    expect(
      screen.queryByRole("heading", {
        name: "Предпросмотр полного назначения",
      }),
    ).not.toBeInTheDocument();
  });

  it("ignores a preview response after editing away and back", async () => {
    setup();
    await chooseCurrentRelease();
    const pending = deferred<Response>();
    server.use(
      http.post(
        "/api/incoming-groups/:groupId/matching/preview",
        () => pending.promise,
      ),
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Предпросмотр всего назначения" }),
    );
    fireEvent.change(screen.getByLabelText("Назначение"), {
      target: { value: tracks[0] },
    });
    fireEvent.change(screen.getByLabelText("Назначение"), {
      target: { value: "" },
    });
    pending.resolve(HttpResponse.json(previewBody));
    await Promise.resolve();
    expect(
      screen.queryByRole("heading", {
        name: "Предпросмотр полного назначения",
      }),
    ).not.toBeInTheDocument();
  });

  it("discards an in-flight preview when a track tag override changes", async () => {
    setup();
    await chooseCurrentRelease();
    fireEvent.click(
      screen.getByRole("checkbox", {
        name: `Переопределить название для Track 0 (${tracks[0]})`,
      }),
    );
    fireEvent.change(screen.getByLabelText("Название"), {
      target: { value: "Before" },
    });
    const pending = deferred<Response>();
    server.use(
      http.post(
        "/api/incoming-groups/:groupId/matching/preview",
        () => pending.promise,
      ),
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Предпросмотр всего назначения" }),
    );
    fireEvent.change(screen.getByLabelText("Название"), {
      target: { value: "After" },
    });
    pending.resolve(HttpResponse.json(previewBody));
    await Promise.resolve();
    expect(
      screen.queryByRole("heading", {
        name: "Предпросмотр полного назначения",
      }),
    ).not.toBeInTheDocument();
  });

  it("unlocks same-group controls after a refetch changes evaluation revision", async () => {
    let groupAReviews = 0;
    server.use(
      http.get("/api/incoming-groups", () => HttpResponse.json(groupsBody())),
      http.get("/api/incoming-groups/:groupId/matching", ({ params }) => {
        const result = review(
          params.groupId as string,
          groups.indexOf(params.groupId as string),
        );
        if (params.groupId === groups[0] && ++groupAReviews > 1) {
          result.evaluation_revision = "evaluation-group-a-refreshed";
        }
        return HttpResponse.json(result);
      }),
    );
    render(<IncomingGroupReviewScreen />);
    await chooseCurrentRelease();

    const pending = deferred<Response>();
    server.use(
      http.post(
        "/api/incoming-groups/:groupId/matching/preview",
        () => pending.promise,
      ),
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Предпросмотр всего назначения" }),
    );
    expect(screen.getByLabelText("Издание (выбор вручную)")).toBeDisabled();

    fireEvent.change(screen.getByLabelText("Входящая группа"), {
      target: { value: groups[1] },
    });
    await awaitSavedEvaluation();
    fireEvent.change(screen.getByLabelText("Входящая группа"), {
      target: { value: groups[0] },
    });
    await screen.findByText("evaluation-group-a-refreshed");
    expect(screen.getByLabelText("Издание (выбор вручную)")).toBeDisabled();

    pending.resolve(HttpResponse.json(previewBody));
    await waitFor(() => {
      expect(
        screen.getByText("evaluation-group-a-refreshed"),
      ).toBeInTheDocument();
      expect(screen.getByLabelText("Издание (выбор вручную)")).toBeEnabled();
    });
    expect(
      screen.queryByRole("heading", {
        name: "Предпросмотр полного назначения",
      }),
    ).not.toBeInTheDocument();
  });

  it("does not let an old confirmation response replace the current group's receipt", async () => {
    setup();
    await chooseCurrentRelease();
    server.use(
      http.post("/api/incoming-groups/:groupId/matching/preview", () =>
        HttpResponse.json(previewBody),
      ),
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Предпросмотр всего назначения" }),
    );
    await screen.findByRole("heading", {
      name: "Предпросмотр полного назначения",
    });
    const pending = deferred<Response>();
    server.use(
      http.post(
        "/api/incoming-groups/:groupId/matching/confirm",
        () => pending.promise,
      ),
    );
    fireEvent.click(screen.getByRole("button", { name: "Явно подтвердить" }));
    fireEvent.change(screen.getByLabelText("Входящая группа"), {
      target: { value: groups[1] },
    });
    await awaitSavedEvaluation();
    pending.resolve(HttpResponse.json({ ...previewBody, group_id: groups[0] }));
    await Promise.resolve();
    expect(
      screen.queryByRole("heading", { name: /Подтверждено/ }),
    ).not.toBeInTheDocument();
  });

  it("retains the confirmation receipt when the refreshed review drops its selected release", async () => {
    let refreshed = false;
    let submittedPreview: unknown;
    server.use(
      http.get("/api/incoming-groups", () => HttpResponse.json(groupsBody())),
      http.get("/api/incoming-groups/:groupId/matching", ({ params }) => {
        const data = review(
          params.groupId as string,
          groups.indexOf(params.groupId as string),
        );
        if (refreshed) {
          data.releases = [];
          data.proposal = { selected_release: null, files: [] };
        }
        return HttpResponse.json(data);
      }),
      http.post(
        "/api/incoming-groups/:groupId/matching/preview",
        async ({ request }) => {
          submittedPreview = await request.json();
          return HttpResponse.json(previewBody);
        },
      ),
      http.post("/api/incoming-groups/:groupId/matching/confirm", () => {
        refreshed = true;
        return HttpResponse.json({
          ...previewBody,
          local_release_id: "local-confirmed",
        });
      }),
    );
    render(<IncomingGroupReviewScreen />);
    await chooseCurrentRelease();
    fireEvent.change(screen.getByLabelText("Назначение"), {
      target: { value: tracks[0] },
    });
    fireEvent.click(
      screen.getByRole("button", { name: "Предпросмотр всего назначения" }),
    );
    await screen.findByRole("heading", {
      name: "Предпросмотр полного назначения",
    });
    expect(submittedPreview).toMatchObject({
      assignments: [
        {
          file_member_id: members[0],
          medium: 1,
          position: 1,
        },
      ],
    });
    fireEvent.click(screen.getByRole("button", { name: "Явно подтвердить" }));
    expect(
      await screen.findByRole("heading", {
        name: /Подтверждено · локальное издание local-confirmed/,
      }),
    ).toBeInTheDocument();
    expect(await screen.findByText(/Подтверждение сохранено/)).toHaveAttribute(
      "role",
      "status",
    );
  });

  it("keeps the saved receipt counts when a later review is unevaluated with different members", async () => {
    let refreshed = false;
    server.use(
      http.get("/api/incoming-groups", () => HttpResponse.json(groupsBody())),
      http.get("/api/incoming-groups/:groupId/matching", ({ params }) => {
        const data = review(
          params.groupId as string,
          groups.indexOf(params.groupId as string),
        );
        if (refreshed) {
          data.evaluated = false;
          data.proposal_available = false;
          data.members = ["member-x", "member-y", "member-z"];
        }
        return HttpResponse.json(data);
      }),
      http.post("/api/incoming-groups/:groupId/matching/preview", () =>
        HttpResponse.json(previewBody),
      ),
      http.post("/api/incoming-groups/:groupId/matching/confirm", () => {
        refreshed = true;
        return HttpResponse.json({
          ...previewBody,
          local_release_id: "local-snapshot",
          members: [
            {
              file_member_id: members[0],
              locations: [],
              assignment: {
                file_member_id: members[0],
                medium: 1,
                position: 1,
                provider_track_id: tracks[0],
              },
            },
            { file_member_id: members[1], locations: [] },
          ],
          omitted_member_ids: [members[1]],
        });
      }),
    );
    render(<IncomingGroupReviewScreen />);
    await chooseCurrentRelease();
    fireEvent.click(
      screen.getByRole("button", { name: "Предпросмотр всего назначения" }),
    );
    await screen.findByRole("heading", {
      name: "Предпросмотр полного назначения",
    });
    fireEvent.click(screen.getByRole("button", { name: "Явно подтвердить" }));
    expect(
      await screen.findByRole("heading", {
        name: /Подтверждено · локальное издание local-snapshot/,
      }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("region", {
        name: /Подтверждено · локальное издание local-snapshot/,
      }),
    ).toHaveTextContent("Назначено файлов: 1 из 2");

    expect(
      await screen.findByText(/Оценка ещё не выполнялась/),
    ).toBeInTheDocument();
    const receipt = screen.getByRole("region", {
      name: /Подтверждено · локальное издание local-snapshot/,
    });
    expect(receipt).toHaveTextContent("Назначено файлов: 1 из 2");
    expect(receipt).toHaveTextContent("Пропущено: 1");
  });

  it("labels multi-disc positions with the same title, recording and number distinctly", async () => {
    const data = review("group-a", 0);
    data.releases = [
      {
        provider_release_id: releases[0],
        release_id: `provider:${releases[0]}`,
        title: "Сборник",
        tracklist_complete: true,
        positions: [
          {
            track_id: tracks[0],
            medium: 0,
            position: 0,
            displayed_number: "1",
            recording_id: "recording-shared",
            title: "Общая запись",
          },
          {
            track_id: tracks[1],
            medium: 1,
            position: 0,
            displayed_number: "1",
            recording_id: "recording-shared",
            title: "Общая запись",
          },
        ],
      },
    ];
    server.use(
      http.get("/api/incoming-groups", () => HttpResponse.json(groupsBody())),
      http.get("/api/incoming-groups/:groupId/matching", () =>
        HttpResponse.json(data),
      ),
    );
    render(<IncomingGroupReviewScreen />);
    await chooseCurrentRelease();
    const labels = within(screen.getByLabelText("Назначение"))
      .getAllByRole("option")
      .map((option) => option.textContent ?? "");
    expect(labels).toContain(
      "Носитель 1 · 1 · Общая запись (recording-shared)",
    );
    expect(labels).toContain(
      "Носитель 2 · 1 · Общая запись (recording-shared)",
    );
    expect(new Set(labels).size).toBe(labels.length);
  });
});
