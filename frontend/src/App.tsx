import { AppShell } from "./app/AppShell";
import { useAppController } from "./app/useAppController";

export function App() {
  const controller = useAppController();
  return <AppShell controller={controller} />;
}
