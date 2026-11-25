import {
  type ComponentProps,
  type ComponentType,
  createContext,
  type ReactNode,
  useContext,
} from "react";
import {
  LocalReleaseEditor,
  type LocalReleaseEditorProps,
} from "../local-release/LocalReleaseEditor";

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

/** Canonical Local release editor data, as the creation slot expects it. */
export type IncomingCreationEditorModel = LocalReleaseEditorProps;

/**
 * A typed creation fallback: the selected group or its files are not ready, so
 * the slot shows back navigation and loading/error feedback instead of an editor.
 */
export type IncomingCreationFallbackModel = ComponentProps<
  typeof IncomingCreationFallbackView
>;

/**
 * Pure creation data owned by a host. `key` is the incoming group identity, so a
 * different group remounts the editor; the same group keeps its drafts mounted.
 */
export type IncomingCreationModel =
  | {
      kind: "editor";
      key: string;
      editor: IncomingCreationEditorModel;
    }
  | {
      kind: "fallback";
      fallback: IncomingCreationFallbackModel;
    };

/**
 * Host-owned live creation controller. It publishes the editor model through
 * {@link IncomingCreationDataBinder}; the portable tree mounts it only while the
 * creation slot is rendered, so its queries and operations live exactly that long.
 */
export type IncomingCreationProvider<Props extends object> = {
  kind: "editor";
  /** Incoming group identity; a different group remounts the controller. */
  key: string;
  Component: ComponentType<Props & { children: ReactNode }>;
  props: Props;
};

/** Pure creation model, or a mounted host controller that supplies one. */
export type IncomingCreationSource<
  Props extends object = Record<never, never>,
> = IncomingCreationModel | IncomingCreationProvider<Props>;

const IncomingCreationEditorContext =
  createContext<IncomingCreationEditorModel | null>(null);

/** Host adapter: binds creation editor data without rendering the portable editor. */
export function IncomingCreationDataBinder({
  model,
  children,
}: {
  model: IncomingCreationEditorModel;
  children: ReactNode;
}) {
  return (
    <IncomingCreationEditorContext.Provider value={model}>
      {children}
    </IncomingCreationEditorContext.Provider>
  );
}

function BoundIncomingCreationEditor() {
  const model = useContext(IncomingCreationEditorContext);
  if (!model) throw new Error("missing incoming creation editor model");
  return <LocalReleaseEditor {...model} />;
}

/** The creation slot: pure editor data, a live controller, or a typed fallback. */
export function IncomingCreationOutlet<
  Props extends object = Record<never, never>,
>({ source }: { source: IncomingCreationSource<Props> }) {
  if (source.kind === "fallback") {
    return <IncomingCreationFallbackView {...source.fallback} />;
  }
  if ("Component" in source) {
    const { Component, key, props } = source;
    return (
      <Component key={key} {...props}>
        <BoundIncomingCreationEditor />
      </Component>
    );
  }
  return <LocalReleaseEditor key={source.key} {...source.editor} />;
}
