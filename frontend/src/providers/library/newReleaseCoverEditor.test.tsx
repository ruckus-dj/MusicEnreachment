import "@testing-library/jest-dom/vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { ReleaseCoverSummary } from "../../api/generated/client.schemas";
import { I18nProvider } from "../../app/i18n/host";
import { createMemoryStorage } from "../../test/memoryStorage";
import { server } from "../../test/server";
import { ReleaseCoverEditorOutlet } from "../../ui/features/library/ReleaseCoverEditorOutlet";
import { ReleaseCoverEditor } from "./newReleaseCoverEditor";

const releaseID = "release-1";
const candidateThumbnail =
  "/api/library/releases/release-1/cover/candidates/front.jpg";

function coverSummary(
  overrides: Partial<ReleaseCoverSummary> = {},
): ReleaseCoverSummary {
  return {
    release_id: releaseID,
    revision: 1,
    mode: "automatic",
    ...overrides,
  };
}

function renderEditor(epoch?: string) {
  const onOperationsAdmitted = vi.fn();
  const onCoverSnapshot = vi.fn();
  const view = render(
    <I18nProvider>
      <ReleaseCoverEditorOutlet
        locale="en"
        source={{
          bind: (children) => (
            <ReleaseCoverEditor
              releaseId={releaseID}
              epoch={epoch}
              onOperationsAdmitted={onOperationsAdmitted}
              onCoverSnapshot={onCoverSnapshot}
            >
              {children}
            </ReleaseCoverEditor>
          ),
        }}
      />
    </I18nProvider>,
  );
  return { ...view, onOperationsAdmitted, onCoverSnapshot };
}

async function openCandidates() {
  fireEvent.click(
    screen.getByRole("button", { name: "Choose Cover Art Archive artwork" }),
  );
}

beforeEach(() => {
  const storage = createMemoryStorage();
  storage.setItem("melotrove-language", "en");
  vi.stubGlobal("localStorage", storage);
});

afterEach(() => {
  vi.unstubAllGlobals();
});

it("selects a candidate as pending and keeps manual upload on the refreshed revision", async () => {
  const selectBodies: unknown[] = [];
  const saveBodies: unknown[] = [];
  server.use(
    http.get("*/api/library/releases/:releaseId/cover", () =>
      HttpResponse.json(coverSummary()),
    ),
    http.get("*/api/library/releases/:releaseId/cover/candidates", () =>
      HttpResponse.json({
        items: [
          {
            id: "candidate-front",
            front: true,
            back: false,
            types: ["Front", "Booklet"],
            comment: "Front cover",
            thumbnail_url: candidateThumbnail,
          },
        ],
      }),
    ),
    http.post(
      "*/api/library/releases/:releaseId/cover/candidates/:imageId/select",
      async ({ request, params }) => {
        expect(params.imageId).toBe("candidate-front");
        selectBodies.push(await request.json());
        return HttpResponse.json(
          coverSummary({
            revision: 2,
            mode: "manual",
            pending_generation: "candidate-generation",
            pending_mime: "image/jpeg",
            pending_sha256: "a".repeat(64),
          }),
        );
      },
    ),
    http.patch(
      "*/api/library/releases/:releaseId/cover",
      async ({ request }) => {
        saveBodies.push(await request.json());
        return HttpResponse.json(
          coverSummary({
            revision: 3,
            mode: "manual",
            pending_generation: "uploaded-generation",
            pending_mime: "image/png",
            pending_sha256: "b".repeat(64),
          }),
        );
      },
    ),
  );

  const test = renderEditor("epoch-1");
  await screen.findByText("No cover selected.");
  await openCandidates();

  const candidate = await screen.findByRole("button", {
    name: "Select artwork: Front, Booklet, Front cover",
  });
  const thumbnail = test.container.querySelector("img");
  expect(thumbnail).toHaveAttribute("src", candidateThumbnail);
  expect(thumbnail).toHaveAttribute("loading", "lazy");
  expect(thumbnail).toHaveAttribute("referrerpolicy", "no-referrer");
  fireEvent.click(candidate);

  expect(
    await screen.findByText(
      "Artwork saved as pending cover. Publications were not changed.",
    ),
  ).toBeVisible();
  expect(selectBodies).toEqual([{ expected_revision: 1 }]);
  expect(screen.getByText("Pending cover — not published yet")).toBeVisible();
  expect(
    screen.getByRole("button", { name: "Apply to all published tracks" }),
  ).toBeEnabled();
  expect(test.onCoverSnapshot).toHaveBeenLastCalledWith(releaseID, undefined);

  const bytes = new Uint8Array([1, 2, 3]);
  const file = new File([bytes], "cover.png", { type: "image/png" });
  Object.defineProperty(file, "arrayBuffer", {
    value: async () => bytes.buffer,
  });
  fireEvent.change(screen.getByLabelText("Choose PNG or JPEG"), {
    target: { files: [file] },
  });
  fireEvent.click(screen.getByRole("button", { name: "Save pending cover" }));

  await waitFor(() =>
    expect(
      screen.getByText(
        "Cover saved as pending. Publications were not changed.",
      ),
    ).toBeVisible(),
  );
  expect(saveBodies).toEqual([
    {
      expected_revision: 2,
      image: "AQID",
      mime: "image/png",
    },
  ]);
  const pendingImage = screen.getByRole("img", {
    name: "Pending release cover",
  });
  expect(pendingImage).toHaveAttribute(
    "src",
    expect.stringContaining("uploaded-generation"),
  );
});

it("reports a selected cover when the authority returns no pending cover", async () => {
  const selectBodies: unknown[] = [];
  server.use(
    http.get("*/api/library/releases/:releaseId/cover", () =>
      HttpResponse.json(coverSummary()),
    ),
    http.get("*/api/library/releases/:releaseId/cover/candidates", () =>
      HttpResponse.json({
        items: [
          {
            id: "candidate-back",
            front: false,
            back: true,
            types: ["Back"],
            comment: "",
            thumbnail_url: candidateThumbnail,
          },
        ],
      }),
    ),
    http.post(
      "*/api/library/releases/:releaseId/cover/candidates/:imageId/select",
      async ({ request }) => {
        selectBodies.push(await request.json());
        return HttpResponse.json(
          coverSummary({
            revision: 2,
            mode: "manual",
            selected_generation: "selected-generation",
            selected_mime: "image/jpeg",
            selected_sha256: "c".repeat(64),
          }),
        );
      },
    ),
  );

  const test = renderEditor(undefined);
  await screen.findByText("No cover selected.");
  await openCandidates();
  fireEvent.click(
    await screen.findByRole("button", { name: "Select artwork: Back" }),
  );

  expect(
    await screen.findByText("Artwork selected as the release cover."),
  ).toBeVisible();
  expect(selectBodies).toEqual([{ expected_revision: 1 }]);
  expect(screen.getByText("Selected cover")).toBeVisible();
  expect(screen.queryByText("Pending cover — not published yet")).toBeNull();
  expect(
    screen.getByRole("button", { name: "Apply to all published tracks" }),
  ).toBeDisabled();
  expect(test.onCoverSnapshot).toHaveBeenCalledWith(
    releaseID,
    "selected-generation",
  );
});

it("refreshes the cover authority after a selection conflict and waits for an explicit retry", async () => {
  let coverReads = 0;
  const selectBodies: unknown[] = [];
  server.use(
    http.get("*/api/library/releases/:releaseId/cover", () => {
      coverReads += 1;
      if (coverReads === 1) return HttpResponse.json(coverSummary());
      return HttpResponse.json(
        coverSummary({
          revision: 2,
          mode: "manual",
          pending_generation: "server-pending-generation",
          pending_mime: "image/jpeg",
          pending_sha256: "d".repeat(64),
        }),
      );
    }),
    http.get("*/api/library/releases/:releaseId/cover/candidates", () =>
      HttpResponse.json({
        items: [
          {
            id: "candidate-front",
            front: true,
            back: false,
            types: ["Front"],
            comment: "Front cover",
            thumbnail_url: candidateThumbnail,
          },
        ],
      }),
    ),
    http.post(
      "*/api/library/releases/:releaseId/cover/candidates/:imageId/select",
      async ({ request }) => {
        selectBodies.push(await request.json());
        if (selectBodies.length === 1)
          return HttpResponse.json(
            { detail: "cover revision conflict" },
            { status: 409 },
          );
        return HttpResponse.json(
          coverSummary({
            revision: 3,
            mode: "manual",
            pending_generation: "selected-pending-generation",
            pending_mime: "image/jpeg",
            pending_sha256: "e".repeat(64),
          }),
        );
      },
    ),
  );

  renderEditor("epoch-1");
  await screen.findByText("No cover selected.");
  await openCandidates();
  const candidate = await screen.findByRole("button", {
    name: "Select artwork: Front, Front cover",
  });
  fireEvent.click(candidate);

  expect(
    await screen.findByText(
      "The cover changed since the artwork list loaded. The cover state was refreshed; select the artwork again.",
    ),
  ).toBeVisible();
  expect(selectBodies).toEqual([{ expected_revision: 1 }]);
  expect(coverReads).toBe(2);
  expect(screen.getByText("Pending cover — not published yet")).toBeVisible();
  expect(
    screen.getByRole("img", { name: "Pending release cover" }),
  ).toHaveAttribute(
    "src",
    expect.stringContaining("server-pending-generation"),
  );
  expect(
    screen.getByRole("button", { name: "Apply to all published tracks" }),
  ).toBeEnabled();

  fireEvent.click(candidate);
  expect(
    await screen.findByText(
      "Artwork saved as pending cover. Publications were not changed.",
    ),
  ).toBeVisible();
  expect(selectBodies).toEqual([
    { expected_revision: 1 },
    { expected_revision: 2 },
  ]);
  expect(
    screen.getByRole("img", { name: "Pending release cover" }),
  ).toHaveAttribute(
    "src",
    expect.stringContaining("selected-pending-generation"),
  );
});

it("shows a candidate load failure and retries it", async () => {
  let candidateReads = 0;
  server.use(
    http.get("*/api/library/releases/:releaseId/cover", () =>
      HttpResponse.json(coverSummary()),
    ),
    http.get("*/api/library/releases/:releaseId/cover/candidates", () => {
      candidateReads += 1;
      if (candidateReads === 1)
        return HttpResponse.json(
          { detail: "Cover Art Archive is unavailable." },
          { status: 500 },
        );
      return HttpResponse.json({
        items: [
          {
            id: "candidate-front",
            front: true,
            back: false,
            types: ["Front"],
            comment: "",
            thumbnail_url: candidateThumbnail,
          },
        ],
      });
    }),
  );

  renderEditor("epoch-1");
  await screen.findByText("No cover selected.");
  await openCandidates();

  expect(await screen.findByRole("alert")).toHaveTextContent(
    "Cover Art Archive is unavailable.",
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Retry loading artwork" }),
  );

  expect(
    await screen.findByRole("button", {
      name: "Select artwork: Front",
    }),
  ).toBeVisible();
  expect(candidateReads).toBe(2);
});

it("shows artwork still being prepared and retries the gallery", async () => {
  let candidateReads = 0;
  server.use(
    http.get("*/api/library/releases/:releaseId/cover", () =>
      HttpResponse.json(coverSummary()),
    ),
    http.get("*/api/library/releases/:releaseId/cover/candidates", () => {
      candidateReads += 1;
      if (candidateReads === 1)
        return HttpResponse.json(
          { detail: "release artwork is still being downloaded" },
          { status: 503 },
        );
      return HttpResponse.json({
        items: [
          {
            id: "candidate-front",
            front: true,
            back: false,
            types: ["Front"],
            comment: "",
            thumbnail_url: candidateThumbnail,
          },
        ],
      });
    }),
  );

  renderEditor("epoch-1");
  await screen.findByText("No cover selected.");
  await openCandidates();

  expect(
    await screen.findByText(
      "Cover Art Archive artwork is still being prepared. Retry in a moment.",
    ),
  ).toBeVisible();
  fireEvent.click(
    screen.getByRole("button", { name: "Retry loading artwork" }),
  );

  expect(
    await screen.findByRole("button", {
      name: "Select artwork: Front",
    }),
  ).toBeVisible();
  expect(candidateReads).toBe(2);
});
