import { render, screen, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import {
  type LibraryArtistCard,
  LibraryArtistCards,
} from "../ui/features/library/LibraryArtistCards";

function artist(overrides: Partial<LibraryArtistCard> = {}): LibraryArtistCard {
  return {
    id: "mira",
    name: "Mira Vale",
    href: "#/library/artists/mira",
    monogram: "MV",
    palette: "forest",
    genres: ["ambient", "folk"],
    releaseCount: 3,
    positionCount: 9,
    availableCount: 6,
    publishedCount: 4,
    selection: {
      isSelected: false,
      isIndeterminate: false,
      isDisabled: false,
      onChange: vi.fn(),
    },
    ...overrides,
  };
}

const locales = [
  {
    locale: "en",
    audio: "Audio",
    published: "Published",
    help: "Selection applies to positions, not artist records.",
    providerWarning: "Provider counts cover the known cache only.",
  },
  {
    locale: "ru",
    audio: "Аудио",
    published: "Опубликовано",
    help: "Выбор относится к позициям, а не к записям артистов.",
    providerWarning: "Данные провайдера ограничены известным кэшем.",
  },
] as const;

it.each(
  locales,
)("keeps only the two audio and publication counts and explains position selection in $locale", ({
  locale,
  audio,
  published,
  help,
  providerWarning,
}) => {
  render(
    <LibraryArtistCards
      locale={locale}
      artists={[artist({ partialProviderScope: true })]}
      pageSelectionControl={{
        isSelected: false,
        isIndeterminate: false,
        isDisabled: false,
        onChange: vi.fn(),
      }}
    />,
  );

  const row = screen.getByRole("row");
  expect(within(row).getByText(audio)).toBeInTheDocument();
  expect(within(row).getByText(published)).toBeInTheDocument();
  expect(within(row).getByText("6 / 9")).toBeInTheDocument();
  expect(within(row).getByText("4 / 9")).toBeInTheDocument();
  expect(within(row).queryByText("Releases")).toBeNull();
  expect(within(row).queryByText("Positions")).toBeNull();
  expect(within(row).queryByText("Without an edition")).toBeNull();
  expect(within(row).getByText(providerWarning)).toBeInTheDocument();
  expect(screen.getByText(help)).toBeInTheDocument();
});

it("gives every card checkbox a position-specific description and tooltip", () => {
  render(
    <LibraryArtistCards
      locale="en"
      artists={[
        artist({ id: "mira", name: "Mira Vale" }),
        artist({
          id: "arden",
          name: "Arden Trio",
          href: "#/library/artists/arden",
        }),
      ]}
    />,
  );

  const mira = screen.getByRole("row", { name: /Mira Vale/ });
  const miraCheckbox = within(mira).getByRole("checkbox", {
    name: "Select positions by Mira Vale",
  });
  expect(miraCheckbox).toHaveAccessibleDescription(
    "Selects every position by this artist, not the artist record.",
  );
  expect(miraCheckbox.closest("label")?.parentElement).toHaveAttribute(
    "title",
    "Selects every position by this artist, not the artist record.",
  );

  const arden = screen.getByRole("row", { name: /Arden Trio/ });
  expect(
    within(arden).getByRole("checkbox", {
      name: "Select positions by Arden Trio",
    }),
  ).toHaveAccessibleDescription(
    "Selects every position by this artist, not the artist record.",
  );
});

it("never renders an unknown missing-edition count as zero", () => {
  render(
    <LibraryArtistCards
      locale="en"
      artists={[artist({ missingEditionCount: undefined })]}
    />,
  );

  const row = screen.getByRole("row");
  expect(within(row).queryByText("Without an edition")).toBeNull();
  expect(within(row).queryByText("0")).toBeNull();
});
