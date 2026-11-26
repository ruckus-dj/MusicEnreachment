import { fireEvent, render, screen, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import type { GenrePickerCatalog } from "../../../ui/features/library/GenrePicker";
import {
  LocalReleaseEditor,
  type LocalReleaseEditorProps,
} from "../../../ui/features/local-release/LocalReleaseEditor";
import {
  LocalReleaseGenreCatalogBinder,
  type LocalReleaseGenreCatalogProviderProps,
} from "../../../ui/features/local-release/LocalReleaseGenreCatalogOutlet";

const genreCatalog: GenrePickerCatalog = {
  options: [],
  query: "",
  onQueryChange: () => undefined,
  loading: false,
  catalogError: "",
  onClearCatalogError: () => undefined,
  onRetryCatalog: () => undefined,
  catalogStatusLoading: false,
  catalogStatusError: "",
  onOpenCatalog: () => undefined,
  onRetryStatus: () => undefined,
};

function TestGenreCatalogProvider({
  children,
}: LocalReleaseGenreCatalogProviderProps) {
  return (
    <LocalReleaseGenreCatalogBinder catalog={genreCatalog}>
      {children}
    </LocalReleaseGenreCatalogBinder>
  );
}

it("requires a successful search and saves a missing recording separately from the release", async () => {
  const onCancel = vi.fn();
  const onSave = vi.fn();
  const onUseRelease = vi.fn();
  const saveRecording = vi.fn<LocalReleaseEditorProps["saveRecording"]>(
    (data) => ({
      ...data,
      id: "new-recording",
      key: "recording-key",
      provider: "Local",
    }),
  );
  const checkRelease = vi
    .fn<LocalReleaseEditorProps["checkRelease"]>()
    .mockRejectedValueOnce(new Error("offline"))
    .mockResolvedValue([]);
  render(
    <LocalReleaseEditor
      locale="en"
      incomingLabel="Northern Lines"
      suggestedArtist="Mira Vale"
      suggestedTitle="Northern Lines"
      artists={[]}
      recordings={[]}
      checkRelease={checkRelease}
      findRecordings={async () => []}
      findArtists={async () => []}
      genreCatalogProvider={TestGenreCatalogProvider}
      saveArtist={(data) => ({
        id: "new-artist",
        name: data.name,
        provider: "Local",
      })}
      saveRecording={saveRecording}
      onUseRelease={onUseRelease}
      onSave={onSave}
      onCancel={onCancel}
    />,
  );
  const check = screen.getByRole("button", { name: "Check editions" });
  const next = "No suitable edition — continue";
  fireEvent.click(check);
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "Search is unavailable",
  );
  expect(screen.queryByRole("button", { name: next })).not.toBeInTheDocument();
  fireEvent.click(check);
  fireEvent.click(await screen.findByRole("button", { name: next }));

  fireEvent.click(screen.getByRole("button", { name: "Add a recording" }));
  const picker = screen.getByRole("region", { name: "Add a recording" });
  fireEvent.change(
    within(picker).getByRole("textbox", { name: "Recording title" }),
    {
      target: { value: "Still Water" },
    },
  );
  fireEvent.click(within(picker).getByRole("button", { name: "Search" }));
  fireEvent.click(
    await within(picker).findByRole("button", {
      name: "No suitable recording — create one",
    }),
  );
  fireEvent.click(
    within(picker).getByRole("button", { name: "Save recording" }),
  );
  expect(saveRecording).toHaveBeenCalledExactlyOnceWith({
    title: "Still Water",
    duration_ms: undefined,
  });
  expect(
    screen.queryByRole("list", { name: "Tracklist" }),
  ).not.toBeInTheDocument();
  expect(onSave).not.toHaveBeenCalled();

  await screen.findByRole("button", {
    name: "Add to tracklist: Still Water",
  });
  fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
  const dialog = await screen.findByRole("dialog", {
    name: "Cancel this release?",
  });
  expect(dialog).toHaveTextContent("These recordings persist");
  expect(dialog).toHaveTextContent("Still Water");
  fireEvent.click(within(dialog).getByRole("button", { name: "Keep editing" }));
  fireEvent.click(
    await screen.findByRole("button", {
      name: "Add to tracklist: Still Water",
    }),
  );
  checkRelease.mockResolvedValueOnce([
    {
      id: "other",
      title: "Another edition",
      artist: "Mira Vale",
      provider: "Local",
    },
  ]);
  fireEvent.click(
    screen.getByRole("button", { name: "Back to the edition check" }),
  );
  fireEvent.click(screen.getByRole("button", { name: "Check editions" }));
  fireEvent.click(
    await screen.findByRole("button", { name: "Return with this edition" }),
  );
  expect(onUseRelease).not.toHaveBeenCalled();
  fireEvent.click(
    within(await screen.findByRole("dialog")).getByRole("button", {
      name: "Keep editing",
    }),
  );
  fireEvent.click(screen.getByRole("button", { name: next }));
  expect(screen.getByRole("list", { name: "Tracklist" })).toHaveTextContent(
    "Still Water",
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Save release and return" }),
  );
  expect(onSave).toHaveBeenCalledExactlyOnceWith(
    expect.objectContaining({
      title: "Northern Lines",
      tracks: [expect.objectContaining({ recordingId: "new-recording" })],
    }),
  );
  expect(onCancel).not.toHaveBeenCalled();
});

it("preserves provider credits and uses commas for new Local recording artists", async () => {
  const onSave = vi.fn();
  const saveArtist = vi.fn();
  const saveRecording = vi.fn<LocalReleaseEditorProps["saveRecording"]>(
    (data) => ({ ...data, id: "duet", provider: "Local" }),
  );
  const artists = [
    { id: "mira", name: "Mira Vale", provider: "Local" },
    { id: "acdc", name: "AC/DC", provider: "MusicBrainz" },
  ];
  render(
    <LocalReleaseEditor
      locale="en"
      incomingLabel="Northern Lines"
      suggestedArtist="Various Artists"
      genreCatalogProvider={TestGenreCatalogProvider}
      suggestedTitle="Northern Lines"
      artists={artists}
      recordings={[]}
      checkRelease={async () => []}
      findRecordings={async () => [
        {
          id: "provider-duet",
          title: "Another duet",
          provider: "MusicBrainz",
          credits: [
            { artistId: "first", creditName: "First", joinPhrase: " feat. " },
            { artistId: "second", creditName: "Second", joinPhrase: " and " },
            { artistId: "third", creditName: "Third", joinPhrase: "" },
          ],
        },
      ]}
      findArtists={async (query) =>
        artists.filter((artist) => artist.name === query)
      }
      saveArtist={saveArtist}
      saveRecording={saveRecording}
      onUseRelease={vi.fn()}
      onSave={onSave}
      onCancel={vi.fn()}
    />,
  );
  fireEvent.click(screen.getByRole("button", { name: "Check editions" }));
  fireEvent.click(
    await screen.findByRole("button", {
      name: "No suitable edition — continue",
    }),
  );
  fireEvent.click(screen.getByRole("button", { name: "Add a recording" }));
  const picker = within(
    screen.getByRole("region", { name: "Add a recording" }),
  );
  fireEvent.change(picker.getByRole("textbox", { name: "Recording title" }), {
    target: { value: "Duet" },
  });
  fireEvent.click(picker.getByRole("button", { name: "Search" }));
  expect(
    await picker.findByText(/First feat\. Second and Third/),
  ).toBeInTheDocument();
  fireEvent.click(
    await picker.findByRole("button", {
      name: "No suitable recording — create one",
    }),
  );
  for (const artist of artists) {
    fireEvent.change(picker.getByRole("textbox", { name: "Artist name" }), {
      target: { value: artist.name },
    });
    fireEvent.click(picker.getByRole("button", { name: "Find artist" }));
    const add = await picker.findByRole("button", {
      name: `Add artist: ${artist.name} · ${artist.provider}`,
    });
    fireEvent.click(add);
    expect(add).toBeDisabled();
  }
  expect(picker.queryByRole("textbox", { name: /separator/i })).toBeNull();
  fireEvent.click(picker.getByRole("button", { name: "Move artist up 2" }));
  expect(picker.getByText("AC/DC, Mira Vale")).toBeInTheDocument();
  expect(saveRecording).not.toHaveBeenCalled();
  expect(saveArtist).not.toHaveBeenCalled();
  fireEvent.click(picker.getByRole("button", { name: "Save recording" }));
  expect(saveRecording).toHaveBeenCalledExactlyOnceWith({
    title: "Duet",
    duration_ms: undefined,
    credits: [
      { artistId: "acdc", creditName: "AC/DC", joinPhrase: ", " },
      { artistId: "mira", creditName: "Mira Vale", joinPhrase: "" },
    ],
  });
  expect(onSave).not.toHaveBeenCalled();
  expect(
    screen.queryByRole("list", { name: "Tracklist" }),
  ).not.toBeInTheDocument();
});
