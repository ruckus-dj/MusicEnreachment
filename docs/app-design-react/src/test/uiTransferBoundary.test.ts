import { readdirSync, readFileSync } from "node:fs";
import { dirname, isAbsolute, join, relative, resolve, sep } from "node:path";
import { fileURLToPath } from "node:url";
import ts from "typescript";
import { expect, it } from "vitest";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "../ui");
const portableFiles = readdirSync(root, { recursive: true })
  .map(String)
  .filter((file) => /\.tsx?$/.test(file));

it("keeps relative imports and re-exports inside the portable tree", () => {
  const escaped: string[] = [];

  for (const file of portableFiles) {
    const path = join(root, file);
    const source = ts.createSourceFile(
      file,
      readFileSync(path, "utf8"),
      ts.ScriptTarget.Latest,
    );

    function check(specifier: ts.Node | undefined) {
      if (!specifier || !ts.isStringLiteralLike(specifier)) return;
      const target = specifier.text;
      if (isAbsolute(target)) {
        escaped.push(`${file}: ${target}`);
        return;
      }
      if (!target.startsWith(".")) return;
      const location = relative(root, resolve(dirname(path), target));
      if (location === ".." || location.startsWith(`..${sep}`)) {
        escaped.push(`${file}: ${target}`);
      }
    }

    function visit(node: ts.Node) {
      if (ts.isImportDeclaration(node) || ts.isExportDeclaration(node))
        check(node.moduleSpecifier);
      if (ts.isExternalModuleReference(node)) check(node.expression);
      if (
        ts.isCallExpression(node) &&
        node.expression.kind === ts.SyntaxKind.ImportKeyword
      )
        check(node.arguments[0]);
      if (ts.isImportTypeNode(node) && ts.isLiteralTypeNode(node.argument))
        check(node.argument.literal);
      ts.forEachChild(node, visit);
    }

    visit(source);
  }

  expect(escaped).toEqual([]);
});

it("does not reintroduce known prototype-only labels in shared UI", () => {
  const demoCopy =
    /^(?:Demo|Демо|Prototype|Прототип)$|(?:^|\s)(?:Demo|Демо):|Sample publication diagnoses|демо-данных|Макет не публикует|published in this demo|React[- ](?:prototype|макет)|MOCK · IN MEMORY|МАКЕТ · В ПАМЯТИ|Simulated token is configured|prototype data in memory|данные макета в памяти/u;
  const leaked: string[] = [];
  for (const file of portableFiles.filter(
    (file) => !/\.test\.tsx?$/.test(file),
  )) {
    const source = ts.createSourceFile(
      file,
      readFileSync(join(root, file), "utf8"),
      ts.ScriptTarget.Latest,
    );
    function visit(node: ts.Node) {
      if (
        (ts.isStringLiteralLike(node) || ts.isJsxText(node)) &&
        demoCopy.test(node.text.trim())
      )
        leaked.push(`${file}: ${node.text.trim()}`);
      ts.forEachChild(node, visit);
    }
    visit(source);
  }
  expect(leaked).toEqual([]);
});
