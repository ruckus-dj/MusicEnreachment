import "@testing-library/jest-dom/vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  within,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { DirectoryPathField } from "./DirectoryPathField";

const browse = vi.hoisted(() => vi.fn());
vi.mock("../api/generated/client", () => ({ browseServerDirectories: browse }));

beforeEach(() => {
  browse.mockReset();
  browse.mockResolvedValue({
    status: 200,
    data: {
      current_path: "/srv/music",
      parent_path: "/srv",
      roots: ["/"],
      directories: [],
      offset: 0,
    },
  });
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

describe("DirectoryPathField", () => {
  it("renders a readonly path and updates only after selecting a folder", async () => {
    const onSelect = vi.fn();
    render(
      <DirectoryPathField
        id="output-path"
        label="Каталог публикации"
        value="/srv/music"
        onSelect={onSelect}
        aria-describedby="output-hint"
      />,
    );

    const input = screen.getByLabelText("Каталог публикации");
    expect(input).toHaveValue("/srv/music");
    expect(input).toHaveAttribute("readonly");
    expect(input).toHaveAttribute("aria-describedby", "output-hint");

    fireEvent.click(screen.getByRole("button", { name: "Выбрать папку" }));
    const dialog = await screen.findByRole("dialog", {
      name: "Папка на сервере",
    });
    expect(
      within(dialog).getByRole("heading", { name: "Папка на сервере" }),
    ).toBeVisible();
    fireEvent.click(
      within(dialog).getByRole("button", { name: "Выбрать папку" }),
    );
    expect(onSelect).toHaveBeenCalledWith("/srv/music");
  });

  it("disables both the displayed path and picker action", () => {
    render(
      <DirectoryPathField
        id="output-path"
        label="Каталог публикации"
        value="/srv/music"
        onSelect={vi.fn()}
        disabled
      />,
    );
    expect(screen.getByLabelText("Каталог публикации")).toBeDisabled();
    expect(
      screen.getByRole("button", { name: "Выбрать папку" }),
    ).toBeDisabled();
  });
});
