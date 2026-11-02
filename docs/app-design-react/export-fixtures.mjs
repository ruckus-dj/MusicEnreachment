import { readFile, writeFile } from "node:fs/promises";
import { createHash } from "node:crypto";
import vm from "node:vm";

const source = await readFile(new URL("../app-design/catalog-data.js", import.meta.url), "utf8");
const data = JSON.parse(vm.runInNewContext(`${source}\nJSON.stringify({ artists: demoArtists, releases: localReleases, files: demoFiles, providerReleases, incomingGroups, absentGroups: absentProviderGroups, recoveryFixture })`, { structuredClone }));
const output = `${JSON.stringify({ sourceSha256: createHash("sha256").update(source).digest("hex"), ...data }, null, 2)}\n`;
const destination = new URL("./src/original-fixtures.json", import.meta.url);
if (process.argv.includes("--check")) {
  if (await readFile(destination, "utf8") !== output) throw new Error("Original prototype fixtures drifted; regenerate explicitly before reviewing parity");
} else {
  await writeFile(destination, output);
}
