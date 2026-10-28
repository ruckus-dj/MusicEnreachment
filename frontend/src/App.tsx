import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { useState } from "react";
import { AppShell } from "./routes/AppShell";

export function App() {
  const [queryClient] = useState(() => new QueryClient());

  return (
    <QueryClientProvider client={queryClient}>
      <AppShell />
    </QueryClientProvider>
  );
}
