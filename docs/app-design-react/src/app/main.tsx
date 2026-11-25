import { StrictMode, useState } from "react";
import { createRoot } from "react-dom/client";
import { CatalogDemo } from "../demo/CatalogDemo";
import { I18nProvider } from "./i18n";
import { DesignShell } from "./routes/DesignShell";
import "./styles.css";

const root = document.getElementById("root");
function PrototypeApp() {
	const [incomingCount, setIncomingCount] = useState<number>();
	return (
		<DesignShell incomingCount={incomingCount}>
			<CatalogDemo onIncomingCount={setIncomingCount} />
		</DesignShell>
	);
}
if (!root) throw new Error("Missing design root");
createRoot(root).render(
	<StrictMode>
		<I18nProvider>
			<PrototypeApp />
		</I18nProvider>
	</StrictMode>,
);
