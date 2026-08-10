if (import.meta.env.DEV) {
  import("react-grab");
  import("react-scan");
}

import { createRoot } from "react-dom/client";
import { App } from "./App";
import "./styles.css";

const root = document.getElementById("root");
if (root) createRoot(root).render(<App />);
