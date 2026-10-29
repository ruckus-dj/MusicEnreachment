import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { expect } from "vitest";
import { server } from "./server";

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
    within(container).getByRole("button", { name: "Выбрать папку" }),
  );

  const dialog = await screen.findByRole("dialog", {
    name: "Папка на сервере",
  });
  await waitFor(() => {
    expect(
      within(dialog).getByRole("button", { name: /^Изменить путь:/ }),
    ).toBeInTheDocument();
  });
  fireEvent.click(
    within(dialog).getByRole("button", { name: /^Изменить путь:/ }),
  );
  fireEvent.change(
    within(dialog).getByRole("textbox", { name: "Путь к папке" }),
    {
      target: { value: path },
    },
  );
  fireEvent.keyDown(
    within(dialog).getByRole("textbox", { name: "Путь к папке" }),
    {
      key: "Enter",
    },
  );
  await waitFor(() => {
    expect(
      within(dialog).getByRole("button", { name: "Выбрать папку" }),
    ).toBeEnabled();
  });
  fireEvent.click(
    within(dialog).getByRole("button", { name: "Выбрать папку" }),
  );
  await waitFor(() => expect(dialog).not.toHaveAttribute("open"));
}
