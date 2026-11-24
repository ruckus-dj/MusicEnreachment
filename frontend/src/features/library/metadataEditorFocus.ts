type ElementRef = { current: HTMLElement | null };

function afterRender(
  containerRef: ElementRef,
  applyFocus: (container: HTMLElement) => void,
) {
  const container = containerRef.current;
  if (!container) return;
  requestAnimationFrame(() => {
    if (containerRef.current !== container || !container.isConnected) return;
    if (container.closest("[hidden], [inert], [aria-hidden='true']")) return;
    applyFocus(container);
  });
}

export function focusMetadataAction(
  actionsRef: ElementRef,
  editorRef?: ElementRef,
) {
  afterRender(actionsRef, (actions) => {
    const button = actions.querySelector("button");
    if (button && !button.matches(":disabled, [aria-disabled='true']")) {
      button.focus();
    } else if (editorRef?.current?.contains(actions)) {
      editorRef.current.focus();
    }
  });
}

export function focusMetadataKey(
  editorRef: ElementRef,
  key: string | undefined,
) {
  if (!key) return;
  afterRender(editorRef, (editor) => {
    const candidates = [
      ...editor.querySelectorAll<HTMLElement>("[data-metadata-keys]"),
    ].filter((node) =>
      node.getAttribute("data-metadata-keys")?.split(",").includes(key),
    );
    const marked =
      candidates.find((node) => node.matches("input, select, textarea")) ??
      candidates[0];
    if (!marked || marked.closest("[hidden], [inert], [aria-hidden='true']"))
      return;
    let ancestor = marked.parentElement;
    while (ancestor && ancestor !== editor) {
      if (ancestor instanceof HTMLDetailsElement) ancestor.open = true;
      ancestor = ancestor.parentElement;
    }
    marked.focus();
  });
}
