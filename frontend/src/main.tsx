import { createRoot } from "react-dom/client";
import { AppShell } from "./routes/AppShell";
import "./styles/index.css";

if (import.meta.env.DEV) {
  void import("react-grab");
  void import("react-scan").then(({ scan }) => scan());
}

const root = document.getElementById("root");
if (!root) throw new Error("Missing application root");

createRoot(root).render(<AppShell />);
