import { createNamespace } from "../ui/i18n/createNamespace";

/**
 * Prototype-host copy. The portable UI must not own demo-only labels, so the
 * demo provider supplies the footer and document-title suffix from here.
 */
export const demoMeloTroveMessages = createNamespace({
  en: {
    footer: "Prototype",
    documentTitleSuffix: "MeloTrove · React prototype",
  },
  ru: {
    footer: "Прототип",
    documentTitleSuffix: "MeloTrove · React-макет",
  },
});
