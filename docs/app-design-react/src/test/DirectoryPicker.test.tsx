import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { useState } from "react";
import { afterEach, expect, it, vi } from "vitest";
import { browseDemoDirectory } from "../demo/fixtures/directoryBrowseDemo";
import { directoryPickerMessages } from "../ui/components/DirectoryPickerMessages";
import {
  type BrowseDirectory,
  type DirectoryPage,
  DirectoryPickerView,
} from "../ui/components/DirectoryPickerView";

afterEach(() => {
  cleanup();
  localStorage.clear();
});

it.each([
  "en",
  "ru",
] as const)("localizes directory controls and associated errors in %s", async (locale) => {
  const copy = directoryPickerMessages[locale];
  const onChoose = vi.fn();
  render(
    <DirectoryPickerView
      open
      initialPath="/"
      browse={browseDemoDirectory}
      locale={locale}
      onSelect={onChoose}
      onClose={vi.fn()}
    />,
  );
  expect(screen.getByRole("heading", { name: copy.title })).toBeInTheDocument();
  const picker = screen.getByRole("button", { name: `${copy.editPath}: /` });
  fireEvent.click(picker);
  const field = screen.getByRole("textbox", { name: copy.pathLabel });
  fireEvent.change(field, { target: { value: "/not-in-the-demo-tree" } });
  fireEvent.keyDown(field, { key: "Enter" });
  expect(await screen.findByRole("alert")).toHaveTextContent(copy.notFound);
  expect(field).toHaveAttribute("aria-invalid", "true");
  expect(field).toHaveAccessibleDescription(copy.notFound);
  expect(field).toHaveValue("/not-in-the-demo-tree");
  expect(screen.getByRole("button", { name: copy.choose })).toBeDisabled();
  expect(onChoose).not.toHaveBeenCalled();
});

it("closes on Escape and returns focus to the opener", async () => {
  const copy = directoryPickerMessages.en;
  const onClose = vi.fn();

  function Harness() {
    const [open, setOpen] = useState(false);
    return (
      <>
        <button type="button" onClick={() => setOpen(true)}>
          Open
        </button>
        <DirectoryPickerView
          open={open}
          initialPath="/"
          browse={browseDemoDirectory}
          locale="en"
          onSelect={vi.fn()}
          onClose={() => {
            onClose();
            setOpen(false);
          }}
        />
      </>
    );
  }

  render(<Harness />);
  const opener = screen.getByRole("button", { name: "Open" });
  opener.focus();
  fireEvent.click(opener);
  await screen.findByRole("heading", { name: copy.title });

  fireEvent.keyDown(screen.getByRole("dialog"), { key: "Escape" });

  await waitFor(() => expect(onClose).toHaveBeenCalledOnce());
  await waitFor(() =>
    expect(
      screen.queryByRole("heading", { name: copy.title }),
    ).not.toBeInTheDocument(),
  );
  await waitFor(() => expect(opener).toHaveFocus());
});

it("leaves the dialog open when Escape cancels path editing", async () => {
  const copy = directoryPickerMessages.en;
  const onClose = vi.fn();
  render(
    <DirectoryPickerView
      open
      initialPath="/"
      browse={browseDemoDirectory}
      locale="en"
      onSelect={vi.fn()}
      onClose={onClose}
    />,
  );
  await screen.findByRole("heading", { name: copy.title });

  fireEvent.click(screen.getByRole("button", { name: `${copy.editPath}: /` }));
  const field = screen.getByRole("textbox", { name: copy.pathLabel });
  fireEvent.keyDown(field, { key: "Escape" });

  expect(
    screen.queryByRole("textbox", { name: copy.pathLabel }),
  ).not.toBeInTheDocument();
  expect(screen.getByRole("heading", { name: copy.title })).toBeInTheDocument();
  expect(onClose).not.toHaveBeenCalled();
});

it("drops the Escape handler when closed while editing", async () => {
  const copy = directoryPickerMessages.en;
  const onClose = vi.fn();
  const browseMock = vi.fn(
    async (
      path: string,
      offset: number,
      signal: AbortSignal,
    ): Promise<DirectoryPage> => {
      signal.throwIfAborted();
      if (path === "/") {
        return {
          currentPath: "/",
          parentPath: "/",
          directories: ["/mnt"],
          offset,
        };
      }
      return new Promise<DirectoryPage>(() => {});
    },
  );
  const browse: BrowseDirectory = browseMock;

  function Harness() {
    const [open, setOpen] = useState(true);
    return (
      <>
        <button type="button" onClick={() => setOpen(false)}>
          Close externally
        </button>
        <DirectoryPickerView
          open={open}
          initialPath="/"
          browse={browse}
          locale="en"
          onSelect={vi.fn()}
          onClose={onClose}
        />
      </>
    );
  }

  render(<Harness />);
  await screen.findByRole("button", { name: "mnt" });
  fireEvent.click(screen.getByRole("button", { name: `${copy.editPath}: /` }));
  fireEvent.click(screen.getByRole("button", { name: "mnt" }));
  await screen.findByText(copy.loading);

  fireEvent.click(
    screen.getByRole("button", { name: "Close externally", hidden: true }),
  );
  await waitFor(() =>
    expect(
      screen.queryByRole("heading", { name: copy.title }),
    ).not.toBeInTheDocument(),
  );
  expect(browseMock).toHaveBeenCalledTimes(2);

  const escape = new KeyboardEvent("keydown", {
    key: "Escape",
    bubbles: true,
    cancelable: true,
  });
  document.dispatchEvent(escape);

  expect(escape.defaultPrevented).toBe(false);
  expect(browseMock).toHaveBeenCalledTimes(2);
  expect(onClose).not.toHaveBeenCalled();
});

it("chooses the current server path after breadcrumb navigation", async () => {
  const copy = directoryPickerMessages.en;
  const onChoose = vi.fn();
  render(
    <DirectoryPickerView
      open
      initialPath="/"
      browse={browseDemoDirectory}
      locale="en"
      onSelect={onChoose}
      onClose={vi.fn()}
    />,
  );
  await screen.findByRole("heading", { name: copy.title });

  fireEvent.click(screen.getByRole("button", { name: "mnt" }));
  await screen.findByRole("button", { name: `${copy.editPath}: /mnt` });
  const choose = screen.getByRole("button", { name: copy.choose });
  expect(choose).toBeEnabled();
  fireEvent.click(choose);

  expect(onChoose).toHaveBeenCalledWith("/mnt");
});
