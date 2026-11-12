import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { expect } from "vitest";
import { server } from "./server";

const button = (dialog: HTMLElement, name: RegExp) =>
  within(dialog).getByRole("button", { name });

export async function selectDirectoryPath(label: string, path: string) {
  server.use(
    http.get("/api/setup/directories", ({ request }) => {
      const requestedPath =
        new URL(request.url).searchParams.get("path") || "/";
      return HttpResponse.json({
        current_path: requestedPath,
        parent_path: requestedPath,
        directories: [],
        offset: 0,
        roots: ["/"],
      });
    }),
  );

  const field = await screen.findByLabelText(label);
  const container = field.parentElement;
  if (!container)
    throw new Error(`Missing directory field container for ${label}`);
  fireEvent.click(
    within(container).getByRole("button", {
      name: /^(?:Выбрать папку|Choose folder)$/,
    }),
  );

  const dialog = await screen.findByRole("dialog", {
    name: /^(?:Папка на сервере|Server folder)$/,
  });
  await waitFor(() => {
    expect(button(dialog, /^(?:Изменить путь|Edit path):/)).toBeInTheDocument();
  });
  fireEvent.click(button(dialog, /^(?:Изменить путь|Edit path):/));
  const pathField = within(dialog).getByRole("textbox", {
    name: /^(?:Путь к папке|Folder path)$/,
  });
  fireEvent.change(pathField, {
    target: { value: path },
  });
  fireEvent.keyDown(pathField, {
    key: "Enter",
  });
  await waitFor(() => {
    expect(button(dialog, /^(?:Выбрать папку|Choose folder)$/)).toBeEnabled();
  });
  fireEvent.click(button(dialog, /^(?:Выбрать папку|Choose folder)$/));
  await waitFor(() => expect(dialog).not.toHaveAttribute("open"));
}
