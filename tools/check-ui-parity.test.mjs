import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import test from "node:test";
import {
  checkHostBoundaries,
  checkUiBoundaries,
  compareDependencyVersions,
  compareUiDirectories,
  compareUiSources,
  enumerateUiPairs,
  SHARED_DEPENDENCY_PACKAGES,
} from "./check-ui-parity.mjs";

test("a prototype lead permits only the named source versions and expires on port", () => {
  const app = Buffer.from("approved UI");
  const design = Buffer.from("next design");
  const sha256 = (bytes) => createHash("sha256").update(bytes).digest("hex");
  const pairs = [["View.tsx", "View.tsx"]];
  const lead = {
    from: "View.tsx",
    reason: "Next screen revision",
    prototypeSHA256: sha256(design),
    applicationSHA256: sha256(app),
  };
  const check = (source, copy, receipts) =>
    compareUiSources(pairs, receipts, () => source, () => copy);

  assert.match(check(design, app, []).errors[0], /Different source bytes/);
  const approved = check(design, app, [lead]);
  assert.deepEqual(approved.errors, []);
  assert.equal(approved.pending.length, 1);
  assert.match(
    check(design, Buffer.from("unapproved"), [lead]).errors[0],
    /Different source bytes/,
  );
  assert.match(
    check(app, app, [lead]).errors[0],
    /Remove the completed prototype lead/,
  );
  const checkBeforeFirstPort = (receipt) => compareUiSources(
    pairs,
    [receipt],
    () => design,
    () => { throw Object.assign(new Error("missing"), { code: "ENOENT" }); },
  );
  assert.equal(
    checkBeforeFirstPort({ ...lead, applicationSHA256: null }).pending.length,
    1,
  );
  assert.match(checkBeforeFirstPort(lead).errors[0], /Cannot compare/);
});

test("UI directories pair by relative path and reject application-only sources", () => {
  const sha256 = (bytes) => createHash("sha256").update(bytes).digest("hex");
  const design = Buffer.from("next design");
  assert.deepEqual(
    enumerateUiPairs(
      ["components/Shared.tsx", "features/native/View.tsx"],
      ["components/Shared.tsx", "features/application-only/View.tsx"],
    ),
    {
      pairs: [
        ["components/Shared.tsx", "components/Shared.tsx"],
        ["features/native/View.tsx", "features/native/View.tsx"],
      ],
      sourceOnly: ["features/native/View.tsx"],
      copyOnly: ["features/application-only/View.tsx"],
    },
  );
  const copies = new Map([["components/Shared.tsx", design]]);
  const result = compareUiDirectories(
    ["components/Shared.tsx", "features/native/View.tsx"],
    ["components/Shared.tsx", "features/application-only/View.tsx"],
    [{
      from: "features/native/View.tsx",
      reason: "Next screen.",
      prototypeSHA256: sha256(design),
      applicationSHA256: null,
    }],
    () => design,
    (path) => {
      if (!copies.has(path)) throw Object.assign(new Error("missing"), { code: "ENOENT" });
      return copies.get(path);
    },
  );
  assert.equal(result.identical, 1);
  assert.equal(result.pending.length, 1);
  assert.match(result.errors[0], /Application-only UI source/);
});

test("UI boundary rejects escapes, host aliases and unapproved dependencies", () => {
  const errors = checkUiBoundaries("frontend/src/ui", [
    {
      path: "features/view/View.tsx",
      text:
        'import { client } from "../../../api/client";\n' +
        'import { Button } from "react-aria-components";',
    },
    {
      path: "features/view/Alias.tsx",
      text: 'import { api } from "@/api/generated";',
    },
    {
      path: "features/view/Query.tsx",
      text: 'import { useQuery } from "@tanstack/react-query";',
    },
    {
      path: "features/view/View.test.tsx",
      text: 'import { describe } from "vitest";',
    },
    {
      path: "styles/index.css",
      text: '@import "tailwindcss";\n@import "../../outside.css";',
    },
  ]);
  assert.equal(errors.length, 4);
  const text = errors.join("\n");
  assert.match(text, /escapes the UI boundary/);
  assert.match(text, /uses a host\/API alias/);
  assert.match(text, /Nonapproved UI dependency: @tanstack\/react-query/);
  assert.match(text, /styles\/index.css: @import escapes the UI boundary/);
});

test("UI boundary resolves extensionless modules and rejects host discovery", () => {
  const paths = new Set(["components/Button.tsx", "styles/reset.css"]);
  const sources = [
    { path: "features/view.tsx", text: 'import { Button } from "../components/Button";' },
    {
      path: "styles/index.css",
      text: '@source "../**/*.{ts,tsx}"; @reference "./reset.css"; @source "../../providers/**/*.tsx";',
    },
    { path: "loader.ts", text: 'import(modulePath); import.meta.glob("../providers/*");' },
  ];
  const errors = checkUiBoundaries("ui", sources, { exists: (path) => paths.has(path) });
  assert.equal(errors.length, 3);
  assert.match(errors.join("\n"), /@source escapes the UI boundary/);
  assert.match(errors.join("\n"), /computed module reference/);
  assert.match(errors.join("\n"), /module loader on import\.meta/);
});

test("host boundary permits only checked JSX wiring and bootstrap CSS", () => {
  const allowed = checkHostBoundaries("host", [
    {
      path: "app/App.tsx",
      text:
        'import { QueryClientProvider } from "@tanstack/react-query";\n' +
        'import { StrictMode } from "react";\n' +
        'import { LocalList, LocalRow } from "./components";\n' +
        'import { AppShell } from "./routes/AppShell";\n' +
        'import { I18nProvider } from "./i18n/host";\n' +
        "export function App({ client }: { client: unknown }) {\n" +
        "  return (\n" +
        "    <StrictMode>\n" +
        "      <QueryClientProvider client={client}>\n" +
        "        <I18nProvider>\n" +
        "          <AppShell />\n" +
        "        </I18nProvider>\n" +
        "        <LocalList\n" +
        "          renderItem={(item) => <LocalRow item={item} />}\n" +
        "        />\n" +
        "      </QueryClientProvider>\n" +
        "    </StrictMode>\n" +
        "  );\n" +
        "}\n",
    },
    {
      path: "app/styles.css",
      text: '@import "../ui/styles/index.css";\n',
    },
  ]);
  assert.deepEqual(allowed, []);

  const errors = checkHostBoundaries("host", [
    {
      path: "app/Visual.tsx",
      text:
        'import { Button as HostButton } from "react-aria-components";\n' +
        "export function Visual({ title }: { title: string }) {\n" +
        '  return <div className="shell">{title}Hello</div>;\n' +
        "}\n" +
        "export function Outside() { return <HostButton />; }\n",
    },
    {
      path: "app/Factory.tsx",
      text:
        'import { createElement } from "react";\n' +
        'import { LocalRow } from "./components";\n' +
        "export const row = createElement(LocalRow, { item });\n",
    },
    {
      path: "app/Callback.tsx",
      text:
        'import { LocalList, LocalRow } from "./components";\n' +
        "export function Callback() {\n" +
        "  return (\n" +
        "    <LocalList\n" +
        "      renderItem={(item) => {\n" +
        "        const label = item;\n" +
        "        return <LocalRow label={label} />;\n" +
        "      }}\n" +
        "    />\n" +
        "  );\n" +
        "}\n",
    },
    {
      path: "app/Unknown.tsx",
      text: "export function MysteryView() { return <Mystery />; }\n",
    },
    {
      path: "app/Glyph.tsx",
      text: 'export function Glyph() { return <svg><path d="M0 0" /></svg>; }\n',
    },
    {
      path: "app/Barrel.ts",
      text: 'export { Button as HostButton } from "react-aria-components";',
    },
    {
      path: "app/styles.css",
      text: '@source inline("text-red-500");',
    },
    {
      path: "styles/theme.css",
      text:
        '@import "tailwindcss";\n' +
        '@source "../**/*.tsx";\n' +
        "body { color: red; }\n",
    },
  ]);
  const text = errors.join("\n");
  assert.match(text, /Native or SVG JSX element is not allowed: <div>/);
  assert.match(text, /Host JSX must not set className/);
  assert.match(text, /Host JSX must not contain copy/);
  assert.match(text, /Visual third-party JSX is not allowed: <HostButton>/);
  assert.match(text, /element factory: createElement/);
  assert.doesNotMatch(text, /app\/Callback\.tsx/);
  assert.match(text, /Custom JSX element is not a checked local component: <Mystery>/);
  assert.match(text, /Host CSS may only import portable UI styles: tailwindcss/);
  assert.match(text, /Host CSS may only bootstrap UI styles, not declare styles: body/);
  assert.match(text, /Host must not re-export external runtime components/);
  assert.match(text, /unapproved @source: inline/);
});

test("shared dependency guard compares resolved lock versions", () => {
  const lock = (versions = {}) => ({
    packages: Object.fromEntries(
      SHARED_DEPENDENCY_PACKAGES.map((name) => [
        `node_modules/${name}`,
        { version: versions[name] ?? "1.0.0" },
      ]),
    ),
  });
  assert.deepEqual(compareDependencyVersions(lock(), lock()), []);
  assert.deepEqual(
    compareDependencyVersions(lock({ vite: "7.3.6" }), lock({ vite: "7.3.7" })),
    ["Shared dependency drift: vite (design 7.3.6, application 7.3.7)"],
  );
  assert.deepEqual(
    compareDependencyVersions(lock(), { packages: {} }),
    SHARED_DEPENDENCY_PACKAGES.map(
      (name) => `Missing shared dependency lock entry: ${name}`,
    ),
  );
});

test("hosts can import route model types but not bypass the portable outlet", () => {
  for (const { view, model, component } of [
    {
      view: "../ui/features/incoming/IncomingScreenView",
      model: "IncomingScreenViewProps",
      component: "IncomingScreenView",
    },
    {
      view: "../ui/features/library/LibraryCatalogPageView",
      model: "LibraryCatalogPageViewProps",
      component: "LibraryCatalogPageView",
    },
    {
      view: "../ui/features/library/ReleaseOverviewView",
      model: "ReleaseOverviewViewProps",
      component: "ReleaseOverviewView",
    },
    {
      view: "../ui/features/library/TrackScreenView",
      model: "TrackScreenViewProps",
      component: "TrackScreenView",
    },
    {
      view: "../ui/features/local-catalog/LocalCatalogScreenView",
      model: "LocalCatalogScreenViewProps",
      component: "LocalCatalogScreenView",
    },
    {
      view: "../ui/features/operations/OperationsScreenView",
      model: "OperationsScreenViewProps",
      component: "OperationsScreenView",
    },
    {
      view: "../ui/features/settings/SettingsScreenView",
      model: "SettingsScreenViewProps",
      component: "SettingsScreenView",
    },
    {
      view: "../ui/features/sources/SourceListPageView",
      model: "SourceListPageViewProps",
      component: "SourceListPageView",
    },
    {
      view: "../ui/features/sources/SourceRootDetailView",
      model: "SourceRootDetailViewProps",
      component: "SourceRootDetailView",
    },
  ]) {
    const typeOnly = {
      path: "app/model.ts",
      text: `import { type ${model} } from "${view}";`,
    };
    assert.deepEqual(checkHostBoundaries("host", [typeOnly]), []);
    for (const source of [
      `import { ${component} } from "${view}";`,
      `export { ${component} } from "${view}.tsx";`,
      `const screen = import("${view}");`,
    ]) {
      assert.match(
        checkHostBoundaries("host", [{ ...typeOnly, text: source }]).join("\n"),
        /Use the portable route outlet/,
      );
    }
  }
});
