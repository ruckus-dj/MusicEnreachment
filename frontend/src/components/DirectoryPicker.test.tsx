import "@testing-library/jest-dom/vitest";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { DirectoryPicker } from "./DirectoryPicker";

const browse = vi.hoisted(() => vi.fn());
vi.mock("../api/generated/client", () => ({ browseServerDirectories: browse }));

function listing(
  current_path: string,
  parent_path = current_path,
  roots: string[] = ["/"],
  directories: string[] = [],
) {
  return {
    status: 200,
    data: { current_path, parent_path, roots, directories, offset: 0 },
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

    expect(await screen.findByText("Текущий каталог: /music")).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: "На уровень выше" }));
    expect(await screen.findByText("Текущий каталог: /")).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: "/mnt" }));
    expect(await screen.findByText("Текущий каталог: /mnt")).toBeVisible();
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
      screen.getByRole("button", { name: "Выбрать текущий каталог" }),
    ).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: "Отмена" }));
    expect(onSelect).not.toHaveBeenCalled();
    expect(onClose).toHaveBeenCalledOnce();
    await act(async () => {
      pending.resolve(listing("/unlisted"));
      await pending.promise;
    });
    expect(
      screen.queryByText("Текущий каталог: /unlisted"),
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
    await screen.findByText("Текущий каталог: /valid");
    fireEvent.change(screen.getByLabelText("Путь"), {
      target: { value: "/invalid" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Перейти" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Не удалось прочитать каталог сервера.",
    );
    expect(
      screen.getByRole("button", { name: "Выбрать текущий каталог" }),
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
      screen.getByRole("button", { name: "Выбрать текущий каталог" }),
    ).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: "Корень сервера" }));
    expect(await screen.findByText("Текущий каталог: /")).toBeVisible();
    expect(browse.mock.calls[1][0]).toEqual({ path: "", offset: 0 });
    expect(onSelect).not.toHaveBeenCalled();
    expect(screen.getByLabelText("Путь")).toHaveValue("/");
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
    await screen.findByText("Текущий каталог: /music");
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
    expect(await screen.findByText("Текущий каталог: /fresh")).toBeVisible();
    oldRequest.resolve(listing("/stale"));
    await waitFor(() =>
      expect(screen.getByText("Текущий каталог: /fresh")).toBeVisible(),
    );
    expect(
      screen.queryByText("Текущий каталог: /stale"),
    ).not.toBeInTheDocument();
  });
});
