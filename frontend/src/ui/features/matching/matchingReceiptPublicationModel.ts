import type { ComponentProps } from "react";
import type { MatchingReceiptPublicationOutcomeView } from "./MatchingReceiptPublicationOutcomeView";

const copy = {
  en: {
    sourceLegend: "Linked file for publication",
    sourceNone: "No file selected",
  },
  ru: {
    sourceLegend: "Связанный файл для публикации",
    sourceNone: "Файл не выбран",
  },
} as const;

export function matchingReceiptFileChoiceLabels(locale: keyof typeof copy) {
  const { sourceLegend: legend, sourceNone: emptyLabel } = copy[locale];
  return { legend, emptyLabel };
}

/** Outcome summary for the last confirmation attempt, without the locale. */
export type MatchingReceiptPublicationOutcomeModel = Omit<
  ComponentProps<typeof MatchingReceiptPublicationOutcomeView>,
  "locale"
>;
