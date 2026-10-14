import { fireEvent, render, screen } from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { beforeEach, describe, expect, it } from "vitest";
import type {
  IncomingGroupsBody,
  MatchingReview,
} from "../../api/generated/client.schemas";
import { server } from "../../test/server";
import { IncomingGroupReviewScreen } from "./IncomingGroupReviewScreen";

const groupA = "group-a";
const groupB = "group-b";
const sourceOne = "media-source-one";
const sourceTwo = "media-source-two";
const memberId = "variant-a";
const providerReleaseId = "provider-release-a";
const providerTrackId = "provider-track-a";

function groups(): IncomingGroupsBody {
  return {
    epoch: 1,
    revision: "list-revision",
    groups: [groupA, groupB].map((id) => ({
      id,
      revision: `${id}-revision`,
      manual: false,
      members: id === groupA ? [memberId] : [],
      unready_members: [],
      diagnostics: [],
    })),
    files: [
      {
        variant_id: memberId,
        capture_analysis_id: "analysis-a",
        capture_identity: "capture-a",
        tags: { TITLE: ["Unchanged title"] },
        locations: [],
      },
    ],
  };
}

function review(
  groupId = groupA,
  mode = "manual",
  authorityRevision = 4,
): MatchingReview {
  return {
    evaluated: true,
    proposal_available: true,
    evaluation_revision: "evaluation-a",
    group_id: groupId,
    authority_mode: mode,
    authority_revision: authorityRevision,
    current_linked_manual_media_source_ids: [sourceOne, sourceTwo],
    members: groupId === groupA ? [memberId] : [],
    releases:
      groupId === groupA
        ? [
            {
              provider_release_id: providerReleaseId,
              release_id: "release-a",
              title: "Release A",
              tracklist_complete: true,
              positions: [
                {
                  track_id: providerTrackId,
                  medium: 0,
                  position: 0,
                  displayed_number: "1",
                  recording_id: "recording-a",
                  title: "Track A",
                },
              ],
            },
          ]
        : [],
    candidates: [],
    proposal: {
      files: [
        {
          file_member_id: memberId,
          raw_tags: { ALBUM: ["Original album metadata"] },
          diagnostics: [],
        },
      ],
    },
  };
}

function mount() {
  let mode = "manual";
  server.use(
    http.get("/api/incoming-groups", () => HttpResponse.json(groups())),
    http.get("/api/incoming-groups/:groupId/matching", ({ params }) =>
      HttpResponse.json(review(params.groupId as string, mode)),
    ),
  );
  render(<IncomingGroupReviewScreen />);
  return {
    setMode: (next: string) => {
      mode = next;
    },
  };
}

function manualPreview() {
  return {
    evaluation_revision: "evaluation-a",
    group_id: groupA,
    local_release_id: "local-a",
    members: [],
    omitted_member_ids: [memberId],
    preview_digest: "digest-a",
    provider_release_id: providerReleaseId,
    selected_release: {
      provider_release_id: providerReleaseId,
      release_id: "release-a",
      title: "Release A",
      tracklist_complete: true,
      positions: [],
    },
  };
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}

beforeEach(() => {
  window.location.hash = "";
});

describe("incoming matching authority actions", () => {
  it("unlinks only explicitly selected IDs, preserves metadata, and retries with the same request ID", async () => {
    mount();
    expect(
      await screen.findByRole("region", {
        name: "Текущий режим управления сопоставлением",
      }),
    ).toHaveTextContent("manual");
    expect(screen.getByText("Original album metadata")).toBeInTheDocument();
    const checkboxes = screen.getAllByRole("checkbox");
    expect(checkboxes).toHaveLength(2);
    expect(checkboxes[0]).not.toBeChecked();
    expect(checkboxes[1]).not.toBeChecked();
    fireEvent.click(checkboxes[1]);

    const bodies: Array<Record<string, unknown>> = [];
    let failOnce = true;
    server.use(
      http.post(
        "/api/incoming-groups/:groupId/matching/unlink",
        async ({ request }) => {
          bodies.push((await request.json()) as Record<string, unknown>);
          if (failOnce) {
            failOnce = false;
            return HttpResponse.error();
          }
          return HttpResponse.json({
            authority_revision: 5,
            group_id: groupA,
            group_revision: `${groupA}-revision`,
            mode: "manual",
            removed_source_ids: [sourceTwo],
          });
        },
      ),
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Убрать выбранные связи" }),
    );
    await screen.findByRole("alert");
    fireEvent.click(
      screen.getByRole("button", { name: "Повторить удаление связей" }),
    );
    expect(await screen.findByRole("status")).toHaveTextContent(
      "Связи удалены. Ручной режим сохранён.",
    );
    expect(bodies).toHaveLength(2);
    expect(bodies[0]).toEqual(bodies[1]);
    expect(bodies[0]).toMatchObject({
      expected_authority_revision: 4,
      expected_group_revision: `${groupA}-revision`,
      source_ids: [sourceTwo],
    });
    expect(bodies[0].request_id).toBeTruthy();
    expect(screen.getByText("Ревизия:")).toBeInTheDocument();
    expect(screen.getByText("Original album metadata")).toBeInTheDocument();
  });

  it("returns authority to automatic and waits for refreshed mode without changing evaluation data", async () => {
    const state = mount();
    const bodies: Array<Record<string, unknown>> = [];
    server.use(
      http.post(
        "/api/incoming-groups/:groupId/matching/return-automatic",
        async ({ request }) => {
          bodies.push((await request.json()) as Record<string, unknown>);
          state.setMode("automatic");
          return HttpResponse.json({
            authority_revision: 5,
            group_id: groupA,
            group_revision: `${groupA}-revision`,
            mode: "automatic",
            requested_generation: 6,
          });
        },
      ),
    );
    expect(
      await screen.findByRole("button", {
        name: "Вернуть к автоматическому сопоставлению",
      }),
    ).toBeInTheDocument();
    fireEvent.click(
      screen.getByRole("button", {
        name: "Вернуть к автоматическому сопоставлению",
      }),
    );
    const authority = await screen.findByRole("region", {
      name: "Текущий режим управления сопоставлением",
    });
    expect(authority).toHaveTextContent("automatic");
    expect(authority).toHaveTextContent(/Ожидается новая оценка/);
    expect(
      screen.getByRole("heading", { name: "Сохраненная оценка" }),
    ).toBeInTheDocument();
    expect(screen.getByText("evaluation-a")).toBeInTheDocument();
    expect(bodies).toHaveLength(1);
    expect(bodies[0]).toMatchObject({
      expected_authority_revision: 4,
      expected_group_revision: `${groupA}-revision`,
    });
    expect(bodies[0].request_id).toBeTruthy();
  });

  it("ignores a late unlink response after switching groups", async () => {
    mount();
    await screen.findByRole("region", {
      name: "Текущий режим управления сопоставлением",
    });
    fireEvent.click(screen.getAllByRole("checkbox")[0]);
    let resolve!: (response: Response) => void;
    server.use(
      http.post(
        "/api/incoming-groups/:groupId/matching/unlink",
        () =>
          new Promise<Response>((done) => {
            resolve = done;
          }),
      ),
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Убрать выбранные связи" }),
    );
    fireEvent.change(screen.getByLabelText("Входящая группа"), {
      target: { value: groupB },
    });
    expect(
      await screen.findByRole("region", {
        name: "Текущий режим управления сопоставлением",
      }),
    ).toHaveTextContent("manual");
    resolve(
      HttpResponse.json({
        authority_revision: 5,
        group_id: groupA,
        group_revision: `${groupA}-revision`,
        mode: "manual",
        removed_source_ids: [sourceOne],
      }),
    );
    await Promise.resolve();
    expect(
      screen.queryByText("Связи удалены. Ручной режим сохранён."),
    ).not.toBeInTheDocument();
  });

  it("blocks authority controls while a manual preview is pending", async () => {
    mount();
    await screen.findByRole("heading", { name: "Сохраненная оценка" });
    fireEvent.change(screen.getByLabelText("Издание (выбор вручную)"), {
      target: { value: providerReleaseId },
    });
    fireEvent.click(screen.getAllByRole("checkbox")[0]);

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

    expect(
      screen.getByRole("button", { name: "Убрать выбранные связи" }),
    ).toBeDisabled();
    expect(
      screen.getByRole("button", {
        name: "Вернуть к автоматическому сопоставлению",
      }),
    ).toBeDisabled();
    expect(screen.getAllByRole("checkbox")[0]).toBeDisabled();
    pending.resolve(HttpResponse.json(manualPreview()));
  });

  it("blocks preview and confirmation controls while authority is pending", async () => {
    mount();
    await screen.findByRole("heading", { name: "Сохраненная оценка" });
    fireEvent.change(screen.getByLabelText("Издание (выбор вручную)"), {
      target: { value: providerReleaseId },
    });
    server.use(
      http.post("/api/incoming-groups/:groupId/matching/preview", () =>
        HttpResponse.json(manualPreview()),
      ),
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Предпросмотр всего назначения" }),
    );
    await screen.findByRole("button", { name: "Явно подтвердить" });
    fireEvent.click(screen.getAllByRole("checkbox")[0]);

    const pending = deferred<Response>();
    server.use(
      http.post(
        "/api/incoming-groups/:groupId/matching/unlink",
        () => pending.promise,
      ),
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Убрать выбранные связи" }),
    );

    expect(
      screen.getByRole("button", { name: "Предпросмотр всего назначения" }),
    ).toBeDisabled();
    expect(
      screen.getByRole("button", { name: "Явно подтвердить" }),
    ).toBeDisabled();
    pending.resolve(
      HttpResponse.json({
        authority_revision: 5,
        group_id: groupA,
        group_revision: `${groupA}-revision`,
        mode: "manual",
        removed_source_ids: [sourceOne],
      }),
    );
  });

  it("invalidates confirmation after a 409 authority refresh without changing evaluation", async () => {
    let authorityRevision = 4;
    let groupReads = 0;
    let reviewReads = 0;
    server.use(
      http.get("/api/incoming-groups", () => {
        groupReads += 1;
        return HttpResponse.json(groups());
      }),
      http.get("/api/incoming-groups/:groupId/matching", ({ params }) => {
        reviewReads += 1;
        return HttpResponse.json(
          review(params.groupId as string, "manual", authorityRevision),
        );
      }),
      http.post("/api/incoming-groups/:groupId/matching/preview", () =>
        HttpResponse.json(manualPreview()),
      ),
      http.post("/api/incoming-groups/:groupId/matching/unlink", () => {
        authorityRevision = 5;
        return HttpResponse.json(
          { detail: "authority changed" },
          { status: 409 },
        );
      }),
    );
    render(<IncomingGroupReviewScreen />);
    await screen.findByRole("heading", { name: "Сохраненная оценка" });
    fireEvent.change(screen.getByLabelText("Издание (выбор вручную)"), {
      target: { value: providerReleaseId },
    });
    fireEvent.click(
      screen.getByRole("button", { name: "Предпросмотр всего назначения" }),
    );
    await screen.findByRole("button", { name: "Явно подтвердить" });
    fireEvent.click(screen.getAllByRole("checkbox")[0]);
    fireEvent.click(
      screen.getByRole("button", { name: "Убрать выбранные связи" }),
    );

    expect(await screen.findByText("5")).toBeInTheDocument();
    expect(await screen.findByText("evaluation-a")).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Явно подтвердить" }),
    ).not.toBeInTheDocument();
    expect(groupReads).toBeGreaterThan(1);
    expect(reviewReads).toBeGreaterThan(1);

    fireEvent.click(
      screen.getByRole("button", { name: "Предпросмотр всего назначения" }),
    );
    expect(
      await screen.findByRole("button", { name: "Явно подтвердить" }),
    ).toBeInTheDocument();
  });

  it("keeps same-group draft controls and authority actions locked while a manual confirmation is pending", async () => {
    mount();
    await screen.findByRole("heading", { name: "Сохраненная оценка" });
    fireEvent.change(screen.getByLabelText("Издание (выбор вручную)"), {
      target: { value: providerReleaseId },
    });
    fireEvent.click(screen.getAllByRole("checkbox")[0]);
    server.use(
      http.post("/api/incoming-groups/:groupId/matching/preview", () =>
        HttpResponse.json(manualPreview()),
      ),
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Предпросмотр всего назначения" }),
    );
    await screen.findByRole("button", { name: "Явно подтвердить" });

    const unlinkCalls: string[] = [];
    const pending = deferred<Response>();
    server.use(
      http.post(
        "/api/incoming-groups/:groupId/matching/confirm",
        () => pending.promise,
      ),
      http.post("/api/incoming-groups/:groupId/matching/unlink", () => {
        unlinkCalls.push("unlink");
        return HttpResponse.json({
          authority_revision: 5,
          group_id: groupA,
          group_revision: `${groupA}-revision`,
          mode: "manual",
          removed_source_ids: [sourceOne],
        });
      }),
    );
    fireEvent.click(screen.getByRole("button", { name: "Явно подтвердить" }));

    const releaseSelect = screen.getByLabelText("Издание (выбор вручную)");
    const assignmentSelect = screen.getByLabelText("Назначение");
    expect(releaseSelect).toBeDisabled();
    expect(assignmentSelect).toBeDisabled();

    // Synthetic change events must not bypass the imperative same-group guard.
    fireEvent.change(releaseSelect, { target: { value: "" } });
    expect(releaseSelect).toHaveValue(providerReleaseId);
    fireEvent.change(assignmentSelect, { target: { value: providerTrackId } });
    expect(assignmentSelect).toHaveValue("");

    const unlinkButton = screen.getByRole("button", {
      name: "Убрать выбранные связи",
    });
    expect(unlinkButton).toBeDisabled();
    expect(
      screen.getByRole("button", {
        name: "Вернуть к автоматическому сопоставлению",
      }),
    ).toBeDisabled();
    fireEvent.click(unlinkButton);
    expect(unlinkCalls).toHaveLength(0);

    pending.resolve(HttpResponse.json(manualPreview()));
    await screen.findByRole("status");
  });
});
