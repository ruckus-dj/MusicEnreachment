import { defaultScheduler } from "@tanstack/query-core";
import { notifyManager } from "@tanstack/react-query";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  within,
} from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import type {
  IncomingGroupsBody,
  MatchingReview,
} from "../../api/generated/client.schemas";
import { nextResponse, server } from "../../test/server";
import { IncomingGroupReviewScreen } from "./IncomingGroupReviewScreen";

const groupA = "group-a";
const groupB = "group-b";
const memberId = "variant-a";
const providerReleaseId = "provider-release-a";
const providerTrackId = "provider-track-a";
const groupsPath = "/api/incoming-groups";
const matchingPath = `/api/incoming-groups/${groupA}/matching`;

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
    current_linked_manual_media_source_ids: [
      "media-source-one",
      "media-source-two",
    ],
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

async function mount(configure?: () => void) {
  let mode = "manual";
  server.use(
    http.get(groupsPath, () => HttpResponse.json(groups())),
    http.get("/api/incoming-groups/:groupId/matching", ({ params }) =>
      HttpResponse.json(review(params.groupId as string, mode)),
    ),
  );
  configure?.();
  const groupsResponse = nextResponse(groupsPath);
  const matchingResponse = nextResponse(matchingPath);
  render(<IncomingGroupReviewScreen />);
  await act(async () => groupsResponse);
  await act(async () => matchingResponse);
  await screen.findByRole("region", {
    name: "Текущий режим управления сопоставлением",
  });
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
  notifyManager.setScheduler(queueMicrotask);
  window.location.hash = "";
});

afterEach(() => {
  cleanup();
  notifyManager.setScheduler(defaultScheduler);
});

describe("incoming matching authority actions", () => {
  it("does not expose or issue source-unlink actions", async () => {
    let unlinkRequests = 0;
    await mount(() =>
      server.use(
        http.post("/api/incoming-groups/:groupId/matching/unlink", () => {
          unlinkRequests += 1;
          return HttpResponse.json({});
        }),
      ),
    );
    expect(
      screen.getByText(/Удаление источников выполняется вне этого экрана/),
    ).toBeInTheDocument();
    expect(
      screen.getByText(/media-source-one, media-source-two/),
    ).toBeInTheDocument();
    const authority = screen.getByRole("region", {
      name: "Текущий режим управления сопоставлением",
    });
    expect(within(authority).queryByRole("checkbox")).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: /связи/i }),
    ).not.toBeInTheDocument();
    expect(unlinkRequests).toBe(0);
  });

  it("returns authority to automatic without changing the saved evaluation", async () => {
    const state = await mount();
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
    const groupsRefresh = nextResponse(groupsPath);
    const matchingRefresh = nextResponse(matchingPath);
    const postResponse = nextResponse(
      `/api/incoming-groups/${groupA}/matching/return-automatic`,
      "POST",
    );
    fireEvent.click(
      screen.getByRole("button", {
        name: "Вернуть к автоматическому сопоставлению",
      }),
    );
    await act(async () => postResponse);
    await act(async () => Promise.all([groupsRefresh, matchingRefresh]));
    const authority = await screen.findByRole("region", {
      name: "Текущий режим управления сопоставлением",
    });
    expect(authority).toHaveTextContent("automatic");
    expect(authority).toHaveTextContent(/Ожидается новая оценка/);
    expect(screen.getByText("evaluation-a")).toBeInTheDocument();
    expect(bodies).toHaveLength(1);
    expect(bodies[0]).toMatchObject({
      expected_authority_revision: 4,
      expected_group_revision: `${groupA}-revision`,
    });
    expect(bodies[0].request_id).toBeTruthy();
  });

  it("keeps draft and authority controls locked while confirmation is pending", async () => {
    await mount();
    fireEvent.change(screen.getByLabelText("Издание (выбор вручную)"), {
      target: { value: providerReleaseId },
    });
    fireEvent.change(screen.getByLabelText("Назначение"), {
      target: { value: providerTrackId },
    });
    server.use(
      http.post("/api/incoming-groups/:groupId/matching/preview", () =>
        HttpResponse.json(manualPreview()),
      ),
    );
    const previewResponse = nextResponse(
      `/api/incoming-groups/${groupA}/matching/preview`,
      "POST",
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Предпросмотр всего назначения" }),
    );
    await act(async () => previewResponse);
    await screen.findByRole("button", { name: "Явно подтвердить" });

    const pending = deferred<Response>();
    const requestEntered = deferred<void>();
    const postResponse = nextResponse(
      `/api/incoming-groups/${groupA}/matching/confirm`,
      "POST",
    );
    const groupsRefresh = nextResponse(groupsPath);
    const matchingRefresh = nextResponse(matchingPath);
    server.use(
      http.post("/api/incoming-groups/:groupId/matching/confirm", () => {
        requestEntered.resolve();
        return pending.promise;
      }),
    );
    fireEvent.click(screen.getByRole("button", { name: "Явно подтвердить" }));
    await act(async () => requestEntered.promise);
    const releaseSelect = screen.getByLabelText("Издание (выбор вручную)");
    const assignmentSelect = screen.getByLabelText("Назначение");
    expect(releaseSelect).toBeDisabled();
    expect(assignmentSelect).toBeDisabled();
    expect(
      screen.getByRole("button", {
        name: "Вернуть к автоматическому сопоставлению",
      }),
    ).toBeDisabled();
    pending.resolve(HttpResponse.json(manualPreview()));
    await act(async () => postResponse);
    await act(async () => Promise.all([groupsRefresh, matchingRefresh]));
  });
});
