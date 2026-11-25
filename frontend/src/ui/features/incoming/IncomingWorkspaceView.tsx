import type { ReactNode } from "react";
import { incomingScreenClass } from "./incomingStyles";

export function IncomingWorkspaceView({ children }: { children: ReactNode }) {
  return <section className={incomingScreenClass}>{children}</section>;
}

export function IncomingCreationFallbackView({
  backHref,
  backLabel,
  feedback,
}: {
  backHref: string;
  backLabel: string;
  feedback: { kind: "loading" | "error"; message: string };
}) {
  return (
    <section className="space-y-2">
      <a href={backHref} className="underline">
        {backLabel}
      </a>
      <p role={feedback.kind === "loading" ? "status" : "alert"}>
        {feedback.message}
      </p>
    </section>
  );
}
