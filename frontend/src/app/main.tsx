import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { productionMeloTroveProvider } from "../providers/meloTroveProvider";
import { MeloTroveUI } from "../ui/app/MeloTroveUI";
import "../ui/styles/index.css";
import { I18nProvider } from "./i18n/host";

if (import.meta.env.DEV) {
  void import("react-grab");
  void import("react-scan").then(({ scan }) => scan());
}

const root = document.getElementById("root");
if (!root) throw new Error("Missing application root");

const queryClient = new QueryClient();

createRoot(root).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <I18nProvider>
        <MeloTroveUI provider={productionMeloTroveProvider} />
      </I18nProvider>
    </QueryClientProvider>
  </StrictMode>,
);
