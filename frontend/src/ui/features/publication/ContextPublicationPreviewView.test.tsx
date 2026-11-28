import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { useState } from "react";
import { describe, expect, it } from "vitest";
import type { PublicationPreviewDetailsState } from "../library/PublicationPreviewDetailsView";
import { ContextPublicationPreviewView } from "./ContextPublicationPreviewView";

const details: PublicationPreviewDetailsState = {
  error: "",
  loading: false,
  admittedOperationID: "",
  admissionUncertain: false,
  inspection: {
    workflow: "retained_same_path",
    sourceCandidates: [],
    selectedChoice: null,
    needsExplicitChoice: false,
    sourceChoiceDisabled: true,
    preview: {
      confirmation_capability: { status: "ready" },
      current_comparison: {
        audio_status: "matches",
        cover_status: "matches",
        current_components: ["/srv/music/01.mka"],
        tag_differences: [],
      },
      desired_tags: { TITLE: ["Carrier"] },
      resolved_path: "/srv/music/01.mka",
      selected_source: { codec: "FLAC", path: "/inbox/01.flac" },
    },
  },
};

function Harness({
  state = details,
}: {
  state?: PublicationPreviewDetailsState;
}) {
  const [mounted, setMounted] = useState(true);
  return (
    <section aria-labelledby="publication-title">
      <h2 id="publication-title">Publication</h2>
      {mounted && (
        <ContextPublicationPreviewView
          locale="en"
          details={state}
          reason="Selected metadata and audio will be updated"
          sourceGroupName="test-source"
          workflowControl={{
            disabled: false,
            mode: "ordinary",
            namePrefix: "test",
            onChange: () => {},
            onModeChange: () => {},
            workflow: "retained_same_path",
          }}
          confirmControl={{
            disabled: false,
            label: "publish",
            onConfirm: () => setMounted(false),
          }}
          dialogControl={{
            open: true,
            onOpen: () => {},
            onDismiss: () => setMounted(false),
          }}
        />
      )}
    </section>
  );
}

describe("ContextPublicationPreviewView dialog", () => {
  it("counts cover changes reported by the current comparison", () => {
    render(
      <Harness
        state={{
          ...details,
          inspection: {
            ...details.inspection,
            preview: {
              ...details.inspection.preview,
              current_comparison: {
                audio_status: "matches",
                cover_status: "differs",
                lyrics_status: "matches",
                tag_differences: [],
              },
            },
          },
        }}
      />,
    );
    expect(screen.getByText("1 tag or asset difference.")).toBeVisible();
    expect(
      screen.queryByText("No tag or asset changes."),
    ).not.toBeInTheDocument();
  });

  it("focuses the Publication region when confirming removes the opener", async () => {
    render(<Harness />);
    const dialog = await screen.findByRole("dialog", { name: "Republish" });
    fireEvent.click(
      within(dialog).getByRole("button", { name: "Publish again" }),
    );
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    await waitFor(() =>
      expect(screen.getByRole("region", { name: "Publication" })).toHaveFocus(),
    );
  });
});
