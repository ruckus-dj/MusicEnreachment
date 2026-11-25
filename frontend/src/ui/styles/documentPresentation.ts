const documentRootClass = `min-w-[320px] font-[system-ui,sans-serif] text-[13px]/[1.4] text-[var(--ink)] bg-[var(--canvas)]
  [color-scheme:light] [--canvas:#f2f3ef] [--surface:#fff] [--raised:#f7f8f5] [--ink:#26332b]
  [--muted:#59675d] [--line:#d7ded5] [--control:#7c8a7e] [--accent:#226740] [--accent-soft:#e5f1e7]
  [--button-text:#fff] [--warning:#845a12] [--warning-soft:#fff2d7] [--error:#a13535]
  [--error-soft:#fde9e8] [--info:#315f88] [--info-soft:#e7f0fa] [--success:var(--accent)]
  [--success-soft:var(--accent-soft)] [--focus:var(--accent)] [--button:var(--accent)] [--radius:5px]
  [--control-radius:4px] [--mono:ui-monospace,SFMono-Regular,Consolas,monospace]
  [font-variant-numeric:normal] dark:[color-scheme:dark] dark:[--canvas:#111512]
  dark:[--surface:#191e1a] dark:[--raised:#222923] dark:[--ink:#e3eae3] dark:[--muted:#a7b5a9]
  dark:[--line:#354138] dark:[--control:#718376] dark:[--accent:#83dca0] dark:[--accent-soft:#233e2c]
  dark:[--button-text:#112419] dark:[--warning:#edc474] dark:[--warning-soft:#3a301d]
  dark:[--error:#ffa09b] dark:[--error-soft:#3a2425] dark:[--info:#9fc7ec] dark:[--info-soft:#203246]`;

const documentBodyClass = `m-0 min-h-dvh bg-[var(--canvas)] font-[system-ui,sans-serif] text-[13px]/[1.4] text-[var(--ink)]
  [&_*:focus-visible]:outline-2 [&_*:focus-visible]:outline-[var(--focus)] [&_h1[tabindex='-1']:focus]:outline-none
  [&_*:focus-visible]:outline-offset-2 selection:bg-[var(--accent-soft)] selection:text-[var(--ink)] caret-[var(--accent)]
  [padding-bottom:env(safe-area-inset-bottom)] motion-reduce:[&_*]:transition-none motion-reduce:[&_*::before]:transition-none motion-reduce:[&_*::after]:transition-none`;

export const documentPresentation = {
  name: "portable-document-presentation",
  transformIndexHtml: {
    order: "pre" as const,
    handler(html: string) {
      const root = "__DOCUMENT_ROOT_CLASS__";
      const body = "__DOCUMENT_BODY_CLASS__";
      if (!html.includes(root) || !html.includes(body))
        throw new Error("Missing portable document presentation placeholders");
      return html
        .replace(root, documentRootClass)
        .replace(body, documentBodyClass);
    },
  },
};
