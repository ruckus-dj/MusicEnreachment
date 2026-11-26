import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { demoMeloTroveProvider } from "../demo/demoMeloTroveProvider";
import { I18nProvider } from "../demo/providers/demoPreferences";
import { MeloTroveUI } from "../ui/app/MeloTroveUI";
import "../ui/styles/index.css";

const root = document.getElementById("root");
if (!root) throw new Error("Missing design root");
createRoot(root).render(
  <StrictMode>
    <I18nProvider>
      <MeloTroveUI provider={demoMeloTroveProvider} />
    </I18nProvider>
  </StrictMode>,
);
