import { expect, it } from "vitest";
import {
  appendDemoLocalRecord,
  findDemoRecordings,
  newDemoLocalRecord,
  selectDemoRelease,
} from "../../fixtures/localReleaseDemo";
import {
  createIncomingState,
  getDraft,
  type IncomingState,
  incomingFixtures,
  makeGroupPlan,
} from "../incoming/incomingModel";
import { createLocalProviderState } from "../local-catalog/localProviderModel";

function groupById(state: IncomingState, id: string) {
  const group = state.groups.find((item) => item.id === id);
  if (!group) throw new Error(`missing incoming group ${id}`);
  return group;
}

it("selects a freshly saved release only on the named group and keeps publication state", () => {
  const state = createIncomingState();
  const files = incomingFixtures.files;
  const north = groupById(state, "north");
  const hours = groupById(state, "hours");
  getDraft(north, files);
  getDraft(hours, files);
  state.plan = makeGroupPlan(state, north, files, incomingFixtures.releases);
  state.inventoryVersion = 7;
  state.selectedTarget = "north-f1";
  state.notice = "previous";
  state.noticeDescriptor = { code: "nothingToPublish" };
  const plan = structuredClone(state.plan);
  const groupsBefore = structuredClone(state.groups);

  const saved = newDemoLocalRecord({
    title: "Small Hours (Local)",
    artistIds: ["arden"],
  });
  const savedTarget = { id: saved.id, title: saved.title ?? "" };
  const next = selectDemoRelease(state, "hours", savedTarget, true);
  const nextHours = groupById(next, "hours");

  expect(nextHours).toEqual({
    ...hours,
    mode: "provider",
    candidateId: saved.id,
    drafts: { ...hours.drafts, [saved.id]: {} },
  });
  expect(Object.hasOwn(nextHours.drafts, saved.id)).toBe(true);
  expect(nextHours.drafts[saved.id]).toEqual({});
  expect(next.groups.filter((item) => item.id !== "hours")).toEqual(
    groupsBefore.filter((item) => item.id !== "hours"),
  );

  expect(next.groupId).toBe("hours");
  expect(next.selectedTarget).toBeNull();
  expect(next.notice).toBe("");
  expect(next.noticeDescriptor).toBeNull();
  expect(next.uiNotice).toEqual({
    namespace: "library",
    id: "localReleaseSelected",
    params: { title: saved.title },
  });

  expect(next.plan).toEqual(plan);
  expect(next.plan?.groupId).toBe("north");
  expect(next.plan?.target.title).toBe("Northern Lines");
  expect(next.inventoryVersion).toBe(7);

  expect(state.groups).toEqual(groupsBefore);
  expect(state.groupId).toBe("north");
  expect(selectDemoRelease(state, "missing", savedTarget, true)).toBe(state);
});

it("finds the fixture recordings and appends one catalog kind without touching the other", () => {
  const local = createLocalProviderState();
  const recordings = local.items.recordings;
  const releases = local.items.releases;

  expect(
    findDemoRecordings(recordings, "First Light").map(
      ({ id, provider, title }) => ({ id, provider, title }),
    ),
  ).toEqual([
    { id: "demo-rec-north-1", provider: "MusicBrainz", title: "First Light" },
  ]);
  expect(findDemoRecordings(recordings, "Still Water")).toEqual([]);

  const localRecording = recordings.find(
    (item) => item.provider === "Local" && item.id === "local-recording-green",
  );
  expect(localRecording).toBeDefined();

  const savedRecording = newDemoLocalRecord({
    title: "First Light",
    artistIds: ["local-artist-vale"],
  });
  const withRecording = appendDemoLocalRecord(
    local,
    "recordings",
    savedRecording,
  );

  expect(withRecording.items.releases).toEqual(releases);
  expect(withRecording.items.recordings).toContainEqual(localRecording);
  expect(
    findDemoRecordings(withRecording.items.recordings, "First Light").map(
      (item) => item.provider,
    ),
  ).toEqual(["MusicBrainz", "Local"]);

  const savedRelease = newDemoLocalRecord({
    title: "First Light",
    artistIds: ["local-artist-vale"],
  });
  const withRelease = appendDemoLocalRecord(
    withRecording,
    "releases",
    savedRelease,
  );

  expect(withRelease.items.recordings).toEqual(withRecording.items.recordings);
  expect(withRelease.items.releases).toEqual([...releases, savedRelease]);
  expect(local.items.recordings).not.toContainEqual(savedRecording);
  expect(local.items.releases).not.toContainEqual(savedRelease);
});
