import type { ReactNode } from "react";
import {
  MatchingScreenView,
  type MatchingScreenViewModel,
} from "../matching/MatchingScreenView";
import {
  IncomingScreenView,
  type IncomingScreenViewProps,
} from "./IncomingScreenView";
import { incomingScreenClass } from "./incomingStyles";

export function IncomingWorkspaceView({
  creation,
  list,
  matching,
}: {
  creation?: ReactNode;
  list?: IncomingScreenViewProps | null;
  matching: MatchingScreenViewModel | null;
}) {
  return (
    <section className={incomingScreenClass}>
      {creation}
      {list ? <IncomingScreenView {...list} /> : null}
      {matching ? <MatchingScreenView model={matching} /> : null}
    </section>
  );
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
