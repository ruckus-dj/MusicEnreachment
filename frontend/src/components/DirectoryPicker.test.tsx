import "@testing-library/jest-dom/vitest";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import type { FormEvent } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { DirectoryPicker } from "./DirectoryPicker";

const browse = vi.hoisted(() => vi.fn());
vi.mock("../api/generated/client", () => ({ browseServerDirectories: browse }));

function listing(
  current_path: string,
  parent_path = current_path,
  roots: string[] = ["/"],
  directories: string[] = [],
  next_offset?: number,
) {
  return {
    status: 200,
    data: {
      current_path,
      parent_path,
      roots,
      directories,
      offset: 0,
      next_offset,
    },
  };
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((yes, no) => {
    resolve = yes;
    reject = no;
  });
  return { promise, resolve, reject };
}

beforeEach(() => {
  browse.mockReset();
  Object.defineProperty(HTMLDialogElement.prototype, "showModal", {
    configurable: true,
    value: function (this: HTMLDialogElement) {
      this.setAttribute("open", "");
    },
  });
  Object.defineProperty(HTMLDialogElement.prototype, "close", {
    configurable: true,
    value: function (this: HTMLDialogElement) {
      this.removeAttribute("open");
    },
  });
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("DirectoryPicker", () => {
  it("navigates to a root and its parent", async () => {
    browse.mockResolvedValueOnce(
      listing("/music", "/", ["/", "/mnt"], ["/music/album"]),
    );
    browse.mockResolvedValueOnce(listing("/", "/", ["/", "/mnt"]));
    browse.mockResolvedValueOnce(listing("/mnt", "/", ["/", "/mnt"]));
    render(
      <DirectoryPicker
        open
        initialPath="/music"
        onSelect={vi.fn()}
        onClose={vi.fn()}
      />,
    );

    expect(
      await screen.findByRole("button", { name: "Изменить путь: /music" }),
    ).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: "На уровень выше" }));
    expect(
      await screen.findByRole("button", { name: "Изменить путь: /" }),
    ).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: "/mnt" }));
    expect(
      await screen.findByRole("button", { name: "Изменить путь: /mnt" }),
    ).toBeVisible();
    expect(browse.mock.calls.map(([params]) => params.path)).toEqual([
      "/music",
      "/",
      "/mnt",
    ]);
  });

  it("cannot select until listing succeeds, and cancellation never selects", async () => {
    const pending = deferred<Awaited<ReturnType<typeof browse>>>();
    browse.mockReturnValueOnce(pending.promise);
    const onSelect = vi.fn();
    const onClose = vi.fn();
    render(
      <DirectoryPicker
        open
        initialPath="/unlisted"
        onSelect={onSelect}
        onClose={onClose}
      />,
    );
    expect(
      screen.getByRole("button", { name: "Выбрать папку" }),
    ).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: "Отмена" }));
    expect(onSelect).not.toHaveBeenCalled();
    expect(onClose).toHaveBeenCalledOnce();
    await act(async () => {
      pending.resolve(listing("/unlisted"));
      await pending.promise;
    });
    expect(
      screen.queryByRole("button", { name: "Изменить путь: /unlisted" }),
    ).not.toBeInTheDocument();
  });

  it("keeps an invalid path unselectable after navigation failure", async () => {
    browse.mockResolvedValueOnce(listing("/valid"));
    browse.mockRejectedValueOnce(new Error("bad path"));
    const onSelect = vi.fn();
    render(
      <DirectoryPicker
        open
        initialPath="/valid"
        onSelect={onSelect}
        onClose={vi.fn()}
      />,
    );
    await screen.findByRole("button", { name: "Изменить путь: /valid" });
    fireEvent.click(
      screen.getByRole("button", { name: "Изменить путь: /valid" }),
    );
    fireEvent.change(screen.getByRole("textbox", { name: "Путь к папке" }), {
      target: { value: "/invalid" },
    });
    fireEvent.keyDown(screen.getByRole("textbox", { name: "Путь к папке" }), {
      key: "Enter",
    });
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Не удалось прочитать каталог сервера.",
    );
    expect(
      screen.getByRole("button", { name: "Выбрать папку" }),
    ).toBeDisabled();
    expect(onSelect).not.toHaveBeenCalled();
  });

  it("offers server-root recovery when the initial path is invalid", async () => {
    browse
      .mockRejectedValueOnce(new Error("missing path"))
      .mockResolvedValueOnce(listing("/"));
    const onSelect = vi.fn();
    render(
      <DirectoryPicker
        open
        initialPath="/missing"
        onSelect={onSelect}
        onClose={vi.fn()}
      />,
    );

    expect(await screen.findByRole("alert")).toBeVisible();
    expect(
      screen.getByRole("button", { name: "Выбрать папку" }),
    ).toBeDisabled();
    fireEvent.click(
      screen.getByRole("button", { name: "Изменить путь: /missing" }),
    );
    fireEvent.change(screen.getByRole("textbox", { name: "Путь к папке" }), {
      target: { value: "/" },
    });
    fireEvent.keyDown(screen.getByRole("textbox", { name: "Путь к папке" }), {
      key: "Enter",
    });
    expect(
      await screen.findByRole("button", { name: "Изменить путь: /" }),
    ).toBeVisible();
    expect(browse.mock.calls[1][0]).toEqual({ path: "/", offset: 0 });
    expect(onSelect).not.toHaveBeenCalled();
    expect(
      screen.queryByRole("textbox", { name: "Путь к папке" }),
    ).not.toBeInTheDocument();
  });

  it("restores focus to the opener when the picker closes", async () => {
    browse.mockResolvedValue(listing("/music"));
    const opener = document.createElement("button");
    document.body.append(opener);
    opener.focus();
    const view = render(
      <DirectoryPicker
        open
        initialPath="/music"
        onSelect={vi.fn()}
        onClose={vi.fn()}
      />,
    );
    await screen.findByRole("button", { name: "Изменить путь: /music" });
    view.rerender(
      <DirectoryPicker
        open={false}
        initialPath="/music"
        onSelect={vi.fn()}
        onClose={vi.fn()}
      />,
    );
    expect(document.activeElement).toBe(opener);
    opener.remove();
  });

  it("ignores a stale response after close and reopen", async () => {
    const oldRequest = deferred<Awaited<ReturnType<typeof browse>>>();
    browse
      .mockReturnValueOnce(oldRequest.promise)
      .mockResolvedValueOnce(listing("/fresh"));
    const onSelect = vi.fn();
    const props = { initialPath: "/old", onSelect, onClose: vi.fn() };
    const view = render(<DirectoryPicker {...props} open />);
    await waitFor(() => expect(browse).toHaveBeenCalledOnce());
    view.rerender(<DirectoryPicker {...props} open={false} />);
    view.rerender(<DirectoryPicker {...props} open />);
    expect(
      await screen.findByRole("button", { name: "Изменить путь: /fresh" }),
    ).toBeVisible();
    oldRequest.resolve(listing("/stale"));
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "Изменить путь: /fresh" }),
      ).toBeVisible(),
    );
    expect(
      screen.queryByRole("button", { name: "Изменить путь: /stale" }),
    ).not.toBeInTheDocument();
  });

  it("loads directory pages sequentially and appends every folder", async () => {
    browse
      .mockResolvedValueOnce(listing("/music", "/", ["/"], ["/music/a"], 3))
      .mockResolvedValueOnce({
        ...listing("/music", "/", ["/"], ["/music/b"], 9),
        data: {
          ...listing("/music", "/", ["/"], ["/music/b"], 9).data,
          offset: 3,
          next_offset: 6,
        },
      })
      .mockResolvedValueOnce({
        ...listing("/music", "/", ["/"], ["/music/c"]),
        data: {
          ...listing("/music", "/", ["/"], ["/music/c"]).data,
          offset: 6,
        },
      });
    render(
      <DirectoryPicker
        open
        initialPath="/music"
        onSelect={vi.fn()}
        onClose={vi.fn()}
      />,
    );

    expect(await screen.findByRole("button", { name: "a" })).toBeVisible();
    expect(await screen.findByRole("button", { name: "b" })).toBeVisible();
    expect(await screen.findByRole("button", { name: "c" })).toBeVisible();
    expect(browse.mock.calls.map(([params]) => params.offset)).toEqual([
      0, 3, 6,
    ]);
  });

  it("aborts an in-flight page request when switching to a typed path", async () => {
    const pendingPage = deferred<Awaited<ReturnType<typeof browse>>>();
    browse
      .mockResolvedValueOnce(listing("/music", "/", ["/"], ["/music/a"], 3))
      .mockReturnValueOnce(pendingPage.promise)
      .mockResolvedValueOnce(listing("/other"));
    render(
      <DirectoryPicker
        open
        initialPath="/music"
        onSelect={vi.fn()}
        onClose={vi.fn()}
      />,
    );

    await screen.findByRole("button", { name: "a" });
    await waitFor(() => expect(browse).toHaveBeenCalledTimes(2));
    expect(browse.mock.calls[1][1].signal.aborted).toBe(false);
    fireEvent.click(
      screen.getByRole("button", { name: "Изменить путь: /music" }),
    );
    fireEvent.change(screen.getByRole("textbox", { name: "Путь к папке" }), {
      target: { value: "/other" },
    });
    fireEvent.keyDown(screen.getByRole("textbox", { name: "Путь к папке" }), {
      key: "Enter",
    });
    expect(
      await screen.findByRole("button", { name: "Изменить путь: /other" }),
    ).toBeVisible();
    expect(browse.mock.calls[1][1].signal.aborted).toBe(true);

    pendingPage.resolve(listing("/music", "/", ["/"], ["/music/b"]));
    await waitFor(() =>
      expect(
        screen.queryByRole("button", { name: "Изменить путь: /music" }),
      ).not.toBeInTheDocument(),
    );
    expect(screen.queryByRole("button", { name: "b" })).not.toBeInTheDocument();
  });

  it("keeps drive and UNC roots intact in breadcrumbs", async () => {
    browse
      .mockResolvedValueOnce(
        listing("C:\\Music", "C:\\", ["C:\\"], ["C:\\Music\\Album"]),
      )
      .mockResolvedValueOnce(listing("C:\\", "C:\\", ["C:\\"]))
      .mockResolvedValueOnce(
        listing("\\\\server\\share\\Music", "\\\\server\\share\\", [
          "\\\\server\\share\\",
        ]),
      )
      .mockResolvedValueOnce(
        listing("\\\\server\\share\\", "\\\\server\\share\\", [
          "\\\\server\\share\\",
        ]),
      );
    const view = render(
      <DirectoryPicker
        open
        initialPath="C:\\Music"
        onSelect={vi.fn()}
        onClose={vi.fn()}
      />,
    );

    expect(
      await screen.findByRole("button", { name: "Изменить путь: C:\\Music" }),
    ).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: "C:\\" }));
    expect(
      await screen.findByRole("button", { name: "Изменить путь: C:\\" }),
    ).toBeVisible();
    expect(browse.mock.calls[1][0].path).toBe("C:\\");

    view.rerender(
      <DirectoryPicker
        open={false}
        initialPath="C:\\Music"
        onSelect={vi.fn()}
        onClose={vi.fn()}
      />,
    );
    view.rerender(
      <DirectoryPicker
        open
        initialPath="\\\\server\\share\\Music"
        onSelect={vi.fn()}
        onClose={vi.fn()}
      />,
    );
    expect(
      await screen.findByRole("button", {
        name: "Изменить путь: \\\\server\\share\\Music",
      }),
    ).toBeVisible();
    fireEvent.click(
      screen.getByRole("button", { name: "\\\\server\\share\\" }),
    );
    expect(
      await screen.findByRole("button", {
        name: "Изменить путь: \\\\server\\share\\",
      }),
    ).toBeVisible();
    expect(browse.mock.calls[3][0].path).toBe("\\\\server\\share\\");
  });

  it("shows other Windows roots when browsing a drive root", async () => {
    browse
      .mockResolvedValueOnce(listing("C:\\", "C:\\", ["C:\\", "D:\\"]))
      .mockResolvedValueOnce(listing("D:\\", "D:\\", ["C:\\", "D:\\"]));
    render(
      <DirectoryPicker
        open
        initialPath="C:\\"
        onSelect={vi.fn()}
        onClose={vi.fn()}
      />,
    );

    expect(
      await screen.findByRole("heading", { name: "Корни сервера" }),
    ).toBeVisible();
    expect(screen.getByRole("button", { name: "D:\\" })).toBeVisible();
    expect(
      screen.queryByRole("button", { name: "C:\\" }),
    ).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "D:\\" }));
    expect(
      await screen.findByRole("button", { name: "Изменить путь: D:\\" }),
    ).toBeVisible();
    expect(browse.mock.calls[1][0].path).toBe("D:\\");
  });

  it("preserves backslashes as literal Unix filename characters", async () => {
    browse.mockResolvedValueOnce(
      listing("/music/a\\b", "/music", ["/"], ["/music/a\\b/c\\d"]),
    );
    render(
      <DirectoryPicker
        open
        initialPath="/music/a\\b"
        onSelect={vi.fn()}
        onClose={vi.fn()}
      />,
    );

    expect(
      await screen.findByRole("button", { name: "Изменить путь: /music/a\\b" }),
    ).toBeVisible();
    expect(
      screen.getByRole("navigation", { name: "Путь к папке" }),
    ).toHaveTextContent("a\\b");
    expect(screen.getByRole("button", { name: "c\\d" })).toBeVisible();
  });

  it("allows entering a path when the initial path is empty and unavailable", async () => {
    browse
      .mockRejectedValueOnce(new Error("missing path"))
      .mockResolvedValueOnce(listing("/"));
    render(
      <DirectoryPicker
        open
        initialPath=""
        onSelect={vi.fn()}
        onClose={vi.fn()}
      />,
    );

    expect(await screen.findByRole("alert")).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: "Ввести путь" }));
    const pathInput = screen.getByRole("textbox", { name: "Путь к папке" });
    expect(pathInput).toBeVisible();
    fireEvent.change(pathInput, { target: { value: "/" } });
    fireEvent.keyDown(pathInput, { key: "Enter" });
    expect(
      await screen.findByRole("button", { name: "Изменить путь: /" }),
    ).toBeVisible();
  });

  it("uses Enter to browse without submitting an enclosing form", async () => {
    browse.mockResolvedValueOnce(listing("/music"));
    const onSubmit = vi.fn((event: FormEvent) => event.preventDefault());
    render(
      <form onSubmit={onSubmit}>
        <DirectoryPicker
          open
          initialPath="/music"
          onSelect={vi.fn()}
          onClose={vi.fn()}
        />
      </form>,
    );
    await screen.findByRole("button", { name: "Изменить путь: /music" });
    fireEvent.click(
      screen.getByRole("button", { name: "Изменить путь: /music" }),
    );
    fireEvent.change(screen.getByRole("textbox", { name: "Путь к папке" }), {
      target: { value: "/other" },
    });
    fireEvent.keyDown(screen.getByRole("textbox", { name: "Путь к папке" }), {
      key: "Enter",
    });
    await waitFor(() => expect(browse).toHaveBeenCalledTimes(2));
    expect(onSubmit).not.toHaveBeenCalled();
    expect(browse.mock.calls[1][0].path).toBe("/other");
  });

  it("disables selection while the edited path differs from the listing", async () => {
    browse.mockResolvedValueOnce(listing("/music"));
    render(
      <DirectoryPicker
        open
        initialPath="/music"
        onSelect={vi.fn()}
        onClose={vi.fn()}
      />,
    );
    await screen.findByRole("button", { name: "Изменить путь: /music" });
    fireEvent.click(
      screen.getByRole("button", { name: "Изменить путь: /music" }),
    );
    fireEvent.change(screen.getByRole("textbox", { name: "Путь к папке" }), {
      target: { value: "/unresolved" },
    });
    expect(
      screen.getByRole("button", { name: "Выбрать папку" }),
    ).toBeDisabled();
  });

  it("Escape exits path editing without closing the picker", async () => {
    browse.mockResolvedValueOnce(listing("/music"));
    const onClose = vi.fn();
    render(
      <DirectoryPicker
        open
        initialPath="/music"
        onSelect={vi.fn()}
        onClose={onClose}
      />,
    );
    await screen.findByRole("button", { name: "Изменить путь: /music" });
    fireEvent.click(
      screen.getByRole("button", { name: "Изменить путь: /music" }),
    );
    fireEvent.change(screen.getByRole("textbox", { name: "Путь к папке" }), {
      target: { value: "/changed" },
    });
    fireEvent.keyDown(screen.getByRole("textbox", { name: "Путь к папке" }), {
      key: "Escape",
    });
    expect(
      screen.queryByRole("textbox", { name: "Путь к папке" }),
    ).not.toBeInTheDocument();
    expect(screen.getByRole("dialog")).toBeVisible();
    expect(onClose).not.toHaveBeenCalled();
    expect(
      screen.getByRole("button", { name: "Изменить путь: /music" }),
    ).toBeVisible();
  });
});
