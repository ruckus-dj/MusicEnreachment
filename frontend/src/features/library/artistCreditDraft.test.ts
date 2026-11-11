import { expect, it } from "vitest";
import {
  adaptArtistCreditMetadata,
  serializeArtistCreditChange,
} from "./artistCreditDraft";

const metadata = (
  desired: Record<string, { state: string; value?: unknown }>,
) =>
  ({ envelope: { desired } }) as Parameters<
    typeof adaptArtistCreditMetadata
  >[0];

it("keeps a missing desired key distinct from explicit terminal states", () => {
  const missing = adaptArtistCreditMetadata(metadata({}), () => "missing-key");
  const explicitAbsent = adaptArtistCreditMetadata(
    metadata({ artist_credit: { state: "absent" } }),
    () => "explicit-absent",
  );
  const explicitEmpty = adaptArtistCreditMetadata(
    metadata({ artist_credit: { state: "explicit_empty" } }),
    () => "explicit-empty",
  );

  expect(missing.draft).toEqual({
    hasDesiredOverride: false,
    state: "absent",
    rows: [],
  });
  expect(serializeArtistCreditChange(missing.draft)).toBeUndefined();
  expect(explicitAbsent.draft.hasDesiredOverride).toBe(true);
  expect(serializeArtistCreditChange(explicitAbsent.draft)).toEqual({
    state: "absent",
  });
  expect(serializeArtistCreditChange(explicitEmpty.draft)).toEqual({
    state: "explicit_empty",
  });
});
