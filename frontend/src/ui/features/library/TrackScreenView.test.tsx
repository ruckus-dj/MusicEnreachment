import { fireEvent, render, screen, within } from "@testing-library/react";
import { type ReactNode, useEffect, useState } from "react";
import { describe, expect, it } from "vitest";
import {
  ContextPublicationPreviewDataBinder,
  type ContextPublicationPreviewModel,
} from "../publication/ContextPublicationPreview";
import { TrackScreenView, type TrackScreenViewProps } from "./TrackScreenView";

const publicationPreviewModel: ContextPublicationPreviewModel = {
  locale: "en",
  sourceGroupName: "test-publication-source",
};

let controllerMounts = 0;

function PreviewController({ children }: { children: ReactNode }) {
  useEffect(() => {
    controllerMounts += 1;
  }, []);
  return (
    <ContextPublicationPreviewDataBinder model={publicationPreviewModel}>
      {children}
    </ContextPublicationPreviewDataBinder>
  );
}

function DeletionProbe() {
  const [count, setCount] = useState(0);
  return (
    <button type="button" onClick={() => setCount((value) => value + 1)}>
      Deletion draft {count}
    </button>
  );
}

function DraftEditor({ label }: { label: string }) {
  const [value, setValue] = useState(0);
  return (
    <button type="button" onClick={() => setValue((current) => current + 1)}>
      {label} draft {value}
    </button>
  );
}

function TagDrawerHarness({
  current = true,
  onOpenChange,
}: {
  current?: boolean;
  onOpenChange?: (open: boolean) => void;
}) {
  const [sourceId, setSourceId] = useState<string>();
  const [open, setOpen] = useState(false);
  return (
    <TrackScreenView
      locale="en"
      initialTab="publication"
      heading={{
        displayNumber: "05",
        title: "Static",
        artist: "Forma",
        releaseTitle: "Signals",
        libraryHref: "#/library",
      }}
      sources={{
        position: "05",
        items: [
          {
            id: "source-a",
            label: "05 - Static.flac",
            summary: "FLAC · available",
            selected: true,
            tags: { TITLE: ["Static"], ALBUM: ["Signals"] },
          },
          {
            id: "source-b",
            label: "05 - Static alternate.flac",
            summary: "FLAC · available",
            selected: false,
            tags: { TITLE: ["Alternate"], COMMENT: ["second"] },
          },
        ],
        inspection: {
          value: sourceId,
          onChange: (id) => {
            setSourceId(id);
            setOpen(true);
          },
        },
      }}
      publication={{
        published: current,
        status: current ? "Metadata matches the publication" : undefined,
        ...(current
          ? {
              current: {
                details: { container: "MKA", codec: "FLAC" },
                tags: { TITLE: ["Static"] },
              },
            }
          : {}),
      }}
      tags={{
        open,
        onOpenChange: (next) => {
          setOpen(next);
          onOpenChange?.(next);
        },
        sourceId,
        comparison: <p>Comparison for {sourceId}</p>,
      }}
    />
  );
}

const editors: TrackScreenViewProps["editors"] = {
  metadata: {
    position: { bind: () => <DraftEditor label="Position" /> },
    recording: { bind: () => <DraftEditor label="Recording" /> },
  },
};

function renderTrack(
  props: Partial<TrackScreenViewProps<Record<never, never>>> = {},
) {
  return render(
    <TrackScreenView
      locale="en"
      heading={{
        displayNumber: "05",
        title: "Static",
        artist: "Forma",
        releaseTitle: "Signals",
        libraryHref: "#/library",
      }}
      sources={{
        position: "05",
        items: [
          {
            id: "source-a",
            label: "05 - Static.flac",
            summary: "FLAC · available",
            selected: true,
          },
        ],
      }}
      publication={{
        published: true,
        status: "Metadata matches the publication",
        output: { label: "05 - Static.mka", summary: "MKA · FLAC" },
        outputStatus: "No deviations reported",
        current: {
          details: { container: "MKA", codec: "FLAC" },
          tags: {},
        },
      }}
      technical={{
        position: "05",
        title: "Static",
        durationMs: 214000,
        recordingTitle: "Static",
      }}
      editors={editors}
      deletion={<DeletionProbe />}
      contextualPublication={{
        key: "position-05",
        Component: PreviewController,
        props: {},
      }}
      {...props}
    />,
  );
}

describe("TrackScreenView tabs", () => {
  it("opens the publication tab from a deep link and keeps the queue issue", () => {
    renderTrack({
      initialTab: "publication",
      publicationIssue: {
        message: "The output filename is unsafe.",
        backHref: "#/incoming-groups?tab=publication",
      },
    });

    expect(
      screen.getByRole("tab", { name: "Files and publication" }),
    ).toHaveAttribute("aria-selected", "true");
    expect(screen.getByRole("region", { name: "Publication" })).toBeVisible();
    expect(screen.getByRole("alert")).toHaveTextContent(
      "The output filename is unsafe.",
    );
    expect(
      screen.getByRole("link", { name: "Back to publication queue" }),
    ).toHaveAttribute("href", "#/incoming-groups?tab=publication");
  });

  it("keeps the overview to source, output and one contextual action", () => {
    renderTrack();
    const overview = screen.getByRole("tabpanel", { name: "Overview" });

    expect(within(overview).getByText("05 - Static.flac")).toBeVisible();
    expect(within(overview).getByText(/05 - Static\.mka/)).toBeVisible();
    expect(within(overview).getByText("No deviations reported")).toBeVisible();
    expect(
      within(overview).getByRole("button", {
        name: "Open Files and publication",
      }),
    ).toBeVisible();
    expect(
      within(overview).queryByRole("button", { name: /publish/i }),
    ).toBeNull();

    fireEvent.click(
      within(overview).getByRole("button", {
        name: "Open Files and publication",
      }),
    );
    expect(
      screen.getByRole("tab", { name: "Files and publication" }),
    ).toHaveAttribute("aria-selected", "true");
  });

  it("keeps publication controllers mounted across tab switches", () => {
    controllerMounts = 0;
    renderTrack({ initialTab: "publication" });
    expect(controllerMounts).toBe(1);

    fireEvent.click(screen.getByRole("tab", { name: "Overview" }));
    expect(screen.queryByRole("region", { name: "Publication" })).toBeNull();
    expect(controllerMounts).toBe(1);

    fireEvent.click(screen.getByRole("tab", { name: "Files and publication" }));
    expect(controllerMounts).toBe(1);

    const publication = screen.getByRole("region", { name: "Publication" });
    fireEvent.click(within(publication).getByText("More actions"));
    fireEvent.click(
      within(publication).getByRole("button", { name: "Deletion draft 0" }),
    );
    expect(
      within(publication).getByRole("button", { name: "Deletion draft 1" }),
    ).toBeVisible();

    fireEvent.click(screen.getByRole("tab", { name: "Overview" }));
    expect(
      screen.queryByRole("button", { name: "Deletion draft 1" }),
    ).toBeNull();
    fireEvent.click(screen.getByRole("tab", { name: "Files and publication" }));
    const reopened = screen.getByRole("region", { name: "Publication" });
    fireEvent.click(within(reopened).getByText("More actions"));
    expect(
      within(reopened).getByRole("button", { name: "Deletion draft 0" }),
    ).toBeVisible();
    expect(controllerMounts).toBe(1);
  });

  it("keeps metadata drafts and shows only one editor scope at a time", () => {
    renderTrack();
    fireEvent.click(screen.getByRole("tab", { name: "Metadata" }));

    expect(
      screen.getByRole("button", { name: "Position draft 0" }),
    ).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: "Position draft 0" }));
    expect(
      screen.getByRole("button", { name: "Position draft 1" }),
    ).toBeVisible();
    expect(
      screen.queryByRole("button", { name: "Recording draft 0" }),
    ).toBeNull();

    fireEvent.click(
      screen.getByRole("radio", { name: "Recording across linked editions" }),
    );
    expect(
      screen.queryByRole("button", { name: "Position draft 1" }),
    ).toBeNull();
    expect(
      screen.getByRole("button", { name: "Recording draft 0" }),
    ).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: "Recording draft 0" }));
    expect(
      screen.getByRole("button", { name: "Recording draft 1" }),
    ).toBeVisible();

    fireEvent.click(
      screen.getByRole("radio", { name: "Only this position in the edition" }),
    );
    expect(
      screen.getByRole("button", { name: "Position draft 1" }),
    ).toBeVisible();

    fireEvent.click(screen.getByRole("tab", { name: "Overview" }));
    fireEvent.click(screen.getByRole("tab", { name: "Metadata" }));
    expect(
      screen.getByRole("button", { name: "Position draft 1" }),
    ).toBeVisible();
    fireEvent.click(
      screen.getByRole("radio", { name: "Recording across linked editions" }),
    );
    expect(
      screen.getByRole("button", { name: "Recording draft 1" }),
    ).toBeVisible();
  });

  it("does not mount metadata editors before the tab is opened", () => {
    renderTrack();
    expect(
      screen.queryByRole("button", { name: "Position draft 0" }),
    ).toBeNull();
  });
});

describe("TrackScreenView source tag drawer", () => {
  it("opens the file tags of the exact clicked source without a portal", () => {
    const view = render(<TagDrawerHarness />);
    expect(
      screen.queryByText("Compare source and publication tags"),
    ).toBeNull();

    fireEvent.click(screen.getAllByRole("button", { name: "Inspect tags" })[1]);

    const dialog = screen.getByRole("dialog", { name: "Source tags" });
    expect(view.container.contains(dialog)).toBe(true);
    expect(
      within(dialog).getByRole("heading", { name: "Source tags" }),
    ).toHaveFocus();
    expect(
      within(dialog).getByRole("tab", { name: "File tags" }),
    ).toHaveAttribute("aria-selected", "true");
    expect(
      within(dialog).getByText("05 - Static alternate.flac"),
    ).toBeVisible();
    expect(within(dialog).getByText("Alternate")).toBeVisible();
    expect(within(dialog).getByText("second")).toBeVisible();
    expect(within(dialog).queryByText("Signals")).toBeNull();
  });

  it("shows comparison only with a reported CURRENT and returns focus on Escape", () => {
    render(<TagDrawerHarness />);
    const triggers = screen.getAllByRole("button", { name: "Inspect tags" });
    fireEvent.click(triggers[1]);

    const dialog = screen.getByRole("dialog", { name: "Source tags" });
    fireEvent.click(
      within(dialog).getByRole("tab", { name: "Compare with publication" }),
    );
    expect(within(dialog).getByText("Comparison for source-b")).toBeVisible();

    fireEvent.keyDown(dialog, { key: "Escape" });
    expect(screen.queryByRole("dialog", { name: "Source tags" })).toBeNull();
    expect(triggers[1]).toHaveFocus();
  });

  it("returns focus to the source button when the drawer is closed", () => {
    render(<TagDrawerHarness />);
    const triggers = screen.getAllByRole("button", { name: "Inspect tags" });
    fireEvent.click(triggers[0]);

    const dialog = screen.getByRole("dialog", { name: "Source tags" });
    fireEvent.click(within(dialog).getByRole("button", { name: "Close" }));

    expect(screen.queryByRole("dialog", { name: "Source tags" })).toBeNull();
    expect(triggers[0]).toHaveFocus();
  });

  it("hides comparison without a reported CURRENT", () => {
    render(<TagDrawerHarness current={false} />);
    fireEvent.click(screen.getAllByRole("button", { name: "Inspect tags" })[0]);

    const dialog = screen.getByRole("dialog", { name: "Source tags" });
    expect(
      within(dialog).queryByRole("tab", {
        name: "Compare with publication",
      }),
    ).toBeNull();
    expect(within(dialog).queryByText("Comparison for source-a")).toBeNull();
  });

  it("closes the drawer when the position tab changes", () => {
    render(<TagDrawerHarness />);
    fireEvent.click(screen.getAllByRole("button", { name: "Inspect tags" })[0]);
    expect(
      screen.getByRole("dialog", { name: "Source tags" }),
    ).toBeInTheDocument();

    fireEvent.click(screen.getByRole("tab", { name: "Metadata" }));

    expect(screen.queryByRole("dialog", { name: "Source tags" })).toBeNull();
  });
});

describe("TrackScreenView publication queue links", () => {
  it("renders the issue action before the queue back link", () => {
    renderTrack({
      publicationIssue: {
        message: "The output filename is unsafe.",
        backHref: "#/incoming-groups?tab=publication",
        action: {
          href: "#/library/releases/r/positions/05?tab=metadata",
          label: "Choose another source",
        },
      },
    });

    const banner = screen.getByRole("alert");
    const links = within(banner).getAllByRole("link");
    expect(links[0]).toHaveTextContent("Choose another source");
    expect(links[0]).toHaveAttribute(
      "href",
      "#/library/releases/r/positions/05?tab=metadata",
    );
    expect(links[1]).toHaveTextContent("Back to publication queue");
  });

  it("keeps the queue return link near the heading", () => {
    renderTrack({ queueBackHref: "#/incoming-groups?tab=publication" });
    expect(
      screen.getByRole("link", { name: "Back to publication queue" }),
    ).toHaveAttribute("href", "#/incoming-groups?tab=publication");
  });

  it("does not duplicate the queue link when the issue already has one", () => {
    renderTrack({
      queueBackHref: "#/incoming-groups?tab=publication",
      publicationIssue: {
        message: "The output filename is unsafe.",
        backHref: "#/incoming-groups?tab=attention",
      },
    });
    const links = screen.getAllByRole("link", {
      name: "Back to publication queue",
    });
    expect(links).toHaveLength(1);
    expect(links[0]).toHaveAttribute("href", "#/incoming-groups?tab=attention");
  });
});
