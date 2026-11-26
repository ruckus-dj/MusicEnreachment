const documentRootClass = `min-w-[320px] bg-[var(--canvas)] font-[system-ui,-apple-system,'Segoe UI',sans-serif] text-[14px]/[1.5] text-[var(--ink)]
  [color-scheme:light] [--canvas:#fafafa] [--surface:#ffffff] [--raised:#f5f5f5] [--ink:#171717]
  [--muted:#525252] [--line:#d4d4d4] [--control:#8a8a8a] [--accent:#2563eb] [--accent-strong:#1d4ed8]
  [--accent-soft:#dbeafe] [--button:#2563eb] [--button-hover:#1d4ed8] [--button-text:#ffffff]
  [--warning:#92400e] [--warning-soft:#fef3c7] [--error:#b91c1c] [--error-soft:#fee2e2]
  [--info:#1d4ed8] [--info-soft:#dbeafe] [--success:#047857] [--success-soft:#d1fae5]
  [--focus:#2563eb] [--radius:16px] [--control-radius:8px] [--popover-radius:12px] [--table-radius:8px]
  [--mono:ui-monospace,SFMono-Regular,Consolas,monospace] [--shadow:0_1px_2px_rgb(0_0_0/0.04)]
  [accent-color:var(--accent)] [scrollbar-color:var(--control)_transparent] [scrollbar-width:thin]
  dark:[color-scheme:dark] dark:[--canvas:#0a0a0a] dark:[--surface:#171717] dark:[--raised:#262626]
  dark:[--ink:#f5f5f5] dark:[--muted:#a3a3a3] dark:[--line:#404040] dark:[--control:#737373]
  dark:[--accent:#60a5fa] dark:[--accent-strong:#93c5fd] dark:[--accent-soft:#1e3a8a4d]
  dark:[--button:#2563eb] dark:[--button-hover:#1d4ed8] dark:[--button-text:#ffffff]
  dark:[--warning:#fbbf24] dark:[--warning-soft:#451a03] dark:[--error:#f87171]
  dark:[--error-soft:#450a0a] dark:[--info:#93c5fd] dark:[--info-soft:#172554]
  dark:[--success:#34d399] dark:[--success-soft:#022c22] dark:[--focus:#60a5fa]`;

const documentBodyClass = `m-0 min-h-dvh bg-[var(--canvas)] font-[system-ui,-apple-system,'Segoe UI',sans-serif] text-[14px]/[1.5] text-[var(--ink)]
  [&_*:focus-visible]:outline-2 [&_*:focus-visible]:outline-[var(--focus)] [&_*:focus-visible]:outline-offset-2
  [&_h1[tabindex='-1']:focus]:outline-none [&_h2[tabindex='-1']:focus]:outline-none selection:bg-[var(--accent-soft)] selection:text-[var(--ink)]
  caret-[var(--accent)] [padding-bottom:env(safe-area-inset-bottom)]
  motion-reduce:[&_*]:transition-none motion-reduce:[&_*::before]:transition-none motion-reduce:[&_*::after]:transition-none`;

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
