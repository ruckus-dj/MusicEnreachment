import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { useState } from "react";
import { I18nProvider } from "./i18n/I18nProvider";
import { AppShell } from "./routes/AppShell";

export function App() {
  const [queryClient] = useState(() => new QueryClient());

  return (
    <QueryClientProvider client={queryClient}>
      <I18nProvider>
        <AppShell />
      </I18nProvider>
    </QueryClientProvider>
  );
}
