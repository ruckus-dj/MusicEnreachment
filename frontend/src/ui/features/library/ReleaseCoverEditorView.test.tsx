import "@testing-library/jest-dom/vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { createRef } from "react";
import { expect, it, vi } from "vitest";
import {
  type ReleaseCoverCandidatesModel,
  ReleaseCoverEditorView,
  type ReleaseCoverEditorViewProps,
} from "./ReleaseCoverEditorView";

function candidates(
  overrides: Partial<ReleaseCoverCandidatesModel> = {},
): ReleaseCoverCandidatesModel {
  return {
    status: "idle",
    items: [],
    onReveal: vi.fn(),
    onRetry: vi.fn(),
    onSelect: vi.fn(),
    ...overrides,
  };
}

function editor(
  overrides: Partial<ReleaseCoverEditorViewProps> = {},
): ReleaseCoverEditorViewProps {
  return {
    locale: "en",
    releaseId: "release-1",
    fileInput: createRef<HTMLInputElement>(),
    busy: false,
    savePendingDisabled: true,
    applyDisabled: true,
    retryDisabled: true,
    onFileChange: vi.fn(),
    onSavePending: vi.fn(),
    onApply: vi.fn(),
    onRetry: vi.fn(),
    ...overrides,
  };
}

it("reveals Cover Art Archive choices on demand with lazy no-referrer thumbnails", () => {
  const model = candidates();
  const view = render(
    <ReleaseCoverEditorView {...editor({ candidates: model })} />,
  );

  expect(screen.queryByRole("button", { name: /Select artwork/ })).toBeNull();
  fireEvent.click(
    screen.getByRole("button", { name: "Choose Cover Art Archive artwork" }),
  );
  expect(model.onReveal).toHaveBeenCalledTimes(1);
  expect(screen.getByRole("status")).toHaveTextContent(
    "Loading Cover Art Archive artwork…",
  );

  const loaded = candidates({
    status: "loaded",
    items: [
      {
        id: "front",
        front: true,
        back: false,
        types: ["Front", "Booklet"],
        comment: "Front cover",
        thumbnailSrc: "/api/cover/front.jpg",
      },
      {
        id: "back",
        front: false,
        back: true,
        types: ["Back"],
        comment: "",
        thumbnailSrc: "/api/cover/back.jpg",
      },
    ],
  });
  view.rerender(<ReleaseCoverEditorView {...editor({ candidates: loaded })} />);

  const thumbnail = view.container.querySelector("img");
  expect(thumbnail).toHaveAttribute("src", "/api/cover/front.jpg");
  expect(thumbnail).toHaveAttribute("loading", "lazy");
  expect(thumbnail).toHaveAttribute("referrerpolicy", "no-referrer");

  const front = screen.getByRole("button", {
    name: "Select artwork: Front, Booklet, Front cover",
  });
  expect(front).toHaveAttribute("type", "button");
  expect(front).not.toBeDisabled();
  fireEvent.click(front);
  expect(loaded.onSelect).toHaveBeenCalledWith("front");
  expect(
    screen.getByRole("button", { name: "Select artwork: Back" }),
  ).toBeVisible();
  expect(screen.getByLabelText("Choose PNG or JPEG")).toBeVisible();
});

it("shows a pending artwork state with retry", () => {
  const pending = candidates({ status: "pending" });
  render(<ReleaseCoverEditorView {...editor({ candidates: pending })} />);

  fireEvent.click(
    screen.getByRole("button", { name: "Choose Cover Art Archive artwork" }),
  );
  expect(screen.getByRole("status")).toHaveTextContent(
    "Cover Art Archive artwork is still being prepared. Retry in a moment.",
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Retry loading artwork" }),
  );
  expect(pending.onRetry).toHaveBeenCalledTimes(1);
});

it("shows loading, empty, and retryable error states for the artwork choices", () => {
  const error = candidates({
    status: "error",
    error: "Could not load Cover Art Archive artwork.",
  });
  const view = render(
    <ReleaseCoverEditorView {...editor({ candidates: error })} />,
  );

  fireEvent.click(
    screen.getByRole("button", { name: "Choose Cover Art Archive artwork" }),
  );
  expect(screen.getByRole("alert")).toHaveTextContent(
    "Could not load Cover Art Archive artwork.",
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Retry loading artwork" }),
  );
  expect(error.onRetry).toHaveBeenCalledTimes(1);

  const empty = candidates({ status: "loaded", items: [] });
  view.rerender(<ReleaseCoverEditorView {...editor({ candidates: empty })} />);
  expect(
    screen.getByText(
      "No Cover Art Archive artwork was found for this edition.",
    ),
  ).toBeVisible();
  expect(screen.queryByRole("button", { name: /Select artwork/ })).toBeNull();
});

it("localizes the artwork disclosure and chips", () => {
  const model = candidates({
    status: "loaded",
    items: [
      {
        id: "front",
        front: true,
        back: false,
        types: ["Front"],
        comment: "Обложка",
        thumbnailSrc: "/api/cover/front.jpg",
      },
    ],
  });
  render(
    <ReleaseCoverEditorView {...editor({ locale: "ru", candidates: model })} />,
  );

  fireEvent.click(
    screen.getByRole("button", {
      name: "Выбрать обложку из Cover Art Archive",
    }),
  );
  expect(
    screen.getByRole("button", {
      name: "Выбрать изображение: Лицевая, Обложка",
    }),
  ).toBeVisible();
});
