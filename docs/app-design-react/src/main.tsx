import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { CatalogDemo } from "./CatalogDemo";
import { Comparison } from "./Comparison";
import "./design.css";

const root = document.getElementById("root");
if (!root) throw new Error("Missing design root");
createRoot(root).render(
	<StrictMode>
		{window.location.pathname === "/compare" ? <Comparison /> : <CatalogDemo />}
	</StrictMode>,
);
