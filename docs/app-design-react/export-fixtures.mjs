import { readFile, writeFile } from "node:fs/promises";
import { createHash } from "node:crypto";
import vm from "node:vm";
import ts from "typescript";

const TIMEOUT_MS = 5000;
const sha256 = (text) => createHash("sha256").update(text).digest("hex");
const readInput = (name) => readFile(new URL(`../app-design/${name}`, import.meta.url), "utf8");

const [catalogSource, manualReleaseSource, localProviderSource] = await Promise.all([
  readInput("catalog-data.js"),
  readInput("manual-release-demo-data.js"),
  readInput("local-provider-prototype.js"),
]);

// Static data scripts run in one shared context; the IIFE in local-provider-prototype.js is
// never executed because its fixture arrays are read through the TypeScript AST below.
const sandbox = { structuredClone, window: {} };
const context = vm.createContext(sandbox);
vm.runInContext(catalogSource, context, { filename: "catalog-data.js", timeout: TIMEOUT_MS });
vm.runInContext(manualReleaseSource, context, { filename: "manual-release-demo-data.js", timeout: TIMEOUT_MS });
const data = JSON.parse(vm.runInContext("JSON.stringify({ artists: demoArtists, releases: localReleases, files: demoFiles, providerReleases, incomingGroups, absentGroups: absentProviderGroups, recoveryFixture })", context, { filename: "catalog-fixtures.js", timeout: TIMEOUT_MS }));

const sourceFile = ts.createSourceFile("local-provider-prototype.js", localProviderSource, ts.ScriptTarget.Latest, true);

const unwrapParentheses = (node) => {
  let current = node;
  while (ts.isParenthesizedExpression(current)) current = current.expression;
  return current;
};

function findLocalProviderBody(file) {
  for (const statement of file.statements) {
    if (!ts.isExpressionStatement(statement)) continue;
    const assignment = statement.expression;
    if (!ts.isBinaryExpression(assignment) || assignment.operatorToken.kind !== ts.SyntaxKind.EqualsToken) continue;
    const target = assignment.left;
    if (!ts.isPropertyAccessExpression(target) || !ts.isIdentifier(target.expression) || target.expression.text !== "window") continue;
    if (target.name.text !== "LocalProviderPrototype") continue;
    const call = unwrapParentheses(assignment.right);
    if (!ts.isCallExpression(call)) continue;
    const callee = unwrapParentheses(call.expression);
    if (!ts.isArrowFunction(callee) && !ts.isFunctionExpression(callee)) continue;
    if (!ts.isBlock(callee.body)) continue;
    return callee.body;
  }
  throw new Error("LocalProviderPrototype IIFE was not found in local-provider-prototype.js");
}

function collectConstInitializers(body) {
  const initializers = new Map();
  for (const statement of body.statements) {
    if (!ts.isVariableStatement(statement) || (statement.declarationList.flags & ts.NodeFlags.Const) === 0) continue;
    for (const declaration of statement.declarationList.declarations) {
      if (!ts.isIdentifier(declaration.name)) continue;
      const name = declaration.name.text;
      if (initializers.has(name)) throw new Error(`localProvider.${name} is declared more than once`);
      if (declaration.initializer) initializers.set(name, declaration.initializer);
    }
  }
  return initializers;
}

function assertLiteral(node, path) {
  if (ts.isArrayLiteralExpression(node)) {
    if (node.elements.some(ts.isSpreadElement)) throw new Error(`${path} uses a spread element; only literal data is allowed`);
    node.elements.forEach((element, index) => assertLiteral(element, `${path}[${index}]`));
    return;
  }
  if (ts.isObjectLiteralExpression(node)) {
    for (const property of node.properties) {
      if (!ts.isPropertyAssignment(property)) throw new Error(`${path} uses a non-literal or shorthand property; only literal data is allowed`);
      if (!ts.isIdentifier(property.name) && !ts.isStringLiteral(property.name) && !ts.isNumericLiteral(property.name)) throw new Error(`${path} uses a computed property name; only literal data is allowed`);
      assertLiteral(property.initializer, `${path}.${property.name.text}`);
    }
    return;
  }
  if (ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node) || ts.isNumericLiteral(node)) return;
  if (node.kind === ts.SyntaxKind.TrueKeyword || node.kind === ts.SyntaxKind.FalseKeyword || node.kind === ts.SyntaxKind.NullKeyword) return;
  if (ts.isPrefixUnaryExpression(node) && node.operator === ts.SyntaxKind.MinusToken && ts.isNumericLiteral(node.operand)) return;
  throw new Error(`${path} contains ${ts.SyntaxKind[node.kind]}; only literal data is allowed`);
}

const body = findLocalProviderBody(sourceFile);
const initializers = collectConstInitializers(body);
const localProvider = {};
for (const name of ["artists", "releases", "recordings"]) {
  const initializer = initializers.get(name);
  if (!initializer) throw new Error(`localProvider.${name} initializer was not found in the LocalProviderPrototype IIFE`);
  if (!ts.isArrayLiteralExpression(initializer)) throw new Error(`localProvider.${name} must be an array literal`);
  assertLiteral(initializer, `localProvider.${name}`);
  localProvider[name] = vm.runInNewContext(`(${initializer.getText(sourceFile)})`, {}, { filename: `local-provider-${name}.js`, timeout: TIMEOUT_MS });
}

const output = `${JSON.stringify({
  sourceSha256: sha256(catalogSource),
  ...data,
  manualReleaseDemoData: sandbox.window.manualReleaseDemoData,
  localProvider,
  sourceSha256s: {
    "catalog-data.js": sha256(catalogSource),
    "manual-release-demo-data.js": sha256(manualReleaseSource),
    "local-provider-prototype.js": sha256(localProviderSource),
  },
}, null, 2)}\n`;
const destination = new URL("./src/original-fixtures.json", import.meta.url);
if (process.argv.includes("--check")) {
  if (await readFile(destination, "utf8") !== output) throw new Error("Original prototype fixtures drifted; regenerate explicitly before reviewing parity");
} else {
  await writeFile(destination, output);
}
