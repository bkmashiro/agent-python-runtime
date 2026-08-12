import { createHash } from "node:crypto";
import { lstat, mkdir, readFile, readdir, rm, writeFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

const APP_ROOT = fileURLToPath(new URL("..", import.meta.url));
const DIST_ROOT = path.join(APP_ROOT, "dist");
const REQUIRED_ROOT_FILES = Object.freeze(["index.html", "styles.css"]);
const SOURCE_DIRECTORY = "src";
const SOURCE_EXTENSIONS = new Set([".css", ".html", ".js", ".json", ".mjs", ".svg"]);

function stablePathOrder(left, right) {
  return left < right ? -1 : left > right ? 1 : 0;
}

function portablePath(filePath) {
  return filePath.split(path.sep).join("/");
}

async function collectSourceFiles(directory, relativeDirectory = "") {
  const entries = await readdir(directory, { withFileTypes: true });
  const files = [];

  for (const entry of entries.sort((left, right) => stablePathOrder(left.name, right.name))) {
    const relativePath = path.join(relativeDirectory, entry.name);
    const absolutePath = path.join(directory, entry.name);

    if (entry.isSymbolicLink()) {
      throw new Error(`refusing to build through symbolic link: ${portablePath(relativePath)}`);
    }
    if (entry.isDirectory()) {
      files.push(...await collectSourceFiles(absolutePath, relativePath));
      continue;
    }
    if (entry.isFile() && SOURCE_EXTENSIONS.has(path.extname(entry.name))) {
      files.push(relativePath);
    }
  }

  return files;
}

export async function build() {
  for (const relativePath of REQUIRED_ROOT_FILES) {
    const metadata = await lstat(path.join(APP_ROOT, relativePath));
    if (!metadata.isFile() || metadata.isSymbolicLink()) {
      throw new Error(`required source must be a regular file: ${relativePath}`);
    }
  }

  const sourceFiles = [
    ...REQUIRED_ROOT_FILES,
    ...(await collectSourceFiles(path.join(APP_ROOT, SOURCE_DIRECTORY), SOURCE_DIRECTORY)),
  ].sort(stablePathOrder);

  await rm(DIST_ROOT, { recursive: true, force: true });
  await mkdir(DIST_ROOT, { recursive: true });

  const manifestLines = [];
  for (const relativePath of sourceFiles) {
    const contents = await readFile(path.join(APP_ROOT, relativePath));
    const destination = path.join(DIST_ROOT, relativePath);
    await mkdir(path.dirname(destination), { recursive: true });
    await writeFile(destination, contents, { mode: 0o644 });

    const digest = createHash("sha256").update(contents).digest("hex");
    manifestLines.push(`${digest}  ${portablePath(relativePath)}`);
  }

  await writeFile(
    path.join(DIST_ROOT, "manifest.sha256"),
    `${manifestLines.join("\n")}\n`,
    { encoding: "utf8", mode: 0o644 },
  );

  return { files: sourceFiles, manifest: manifestLines };
}

const invokedPath = process.argv[1] ? path.resolve(process.argv[1]) : null;
if (invokedPath === fileURLToPath(import.meta.url)) {
  build()
    .then(({ files }) => {
      process.stdout.write(`Built ${files.length} files into dist.\n`);
    })
    .catch((error) => {
      process.stderr.write(`Build failed: ${error.message}\n`);
      process.exitCode = 1;
    });
}
