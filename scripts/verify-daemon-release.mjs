import { createHash } from "node:crypto";
import { existsSync, readFileSync } from "node:fs";
import { readFile, stat } from "node:fs/promises";
import path from "node:path";

const root = process.cwd();
const manifestPath = path.resolve(readArg("--manifest") || path.join(root, "dist", "cliff-release.json"));

function readArg(name) {
  const arg = process.argv.find((item) => item === name || item.startsWith(`${name}=`));
  if (!arg) return "";
  if (arg.includes("=")) return arg.split("=").slice(1).join("=");
  const index = process.argv.indexOf(arg);
  const value = process.argv[index + 1] ?? "";
  if (!value || value.startsWith("-")) {
    console.error(`Missing value for ${name}`);
    process.exit(1);
  }
  return value;
}

function fail(message) {
  console.error(message);
  process.exit(1);
}

function readJSON(filePath, label) {
  try {
    return JSON.parse(readFileSync(filePath, "utf8"));
  } catch (error) {
    fail(`${label} is not valid JSON: ${error instanceof Error ? error.message : String(error)}`);
  }
}

async function fileSHA256(filePath) {
  const data = await readFile(filePath);
  return createHash("sha256").update(data).digest("hex");
}

function validateInstaller(name, value) {
  if (value?.file !== name) fail(`Release manifest installer ${name} has invalid file.`);
  if (!Number.isFinite(value?.sizeBytes) || value.sizeBytes <= 0) fail(`Release manifest installer ${name} sizeBytes is invalid.`);
  if (!/^[a-f0-9]{64}$/.test(value?.sha256 ?? "")) fail(`Release manifest installer ${name} sha256 is invalid.`);
}

function validateCommand(path, platform, expected) {
  const group = path.split(".").reduce((value, key) => value?.[key], manifest);
  const actual = group?.[platform];
  if (actual !== expected) {
    fail(`Release manifest ${path} ${platform} command is invalid. Expected "${expected}", got "${actual ?? ""}".`);
  }
}

if (!existsSync(manifestPath)) fail(`Release manifest not found: ${manifestPath}`);

const manifest = readJSON(manifestPath, "Release manifest");
const manifestDir = path.dirname(manifestPath);
if (manifest.schemaVersion !== 1) fail("Release manifest schemaVersion must be 1.");
if (manifest.name !== "cliff") fail("Release manifest name must be cliff.");
if (!manifest.version || !manifest.commit || !manifest.builtAt || !manifest.generatedAt) {
  fail("Release manifest build metadata is incomplete.");
}
if (manifest.platform?.os !== process.platform || manifest.platform?.arch !== process.arch) {
  fail(`Release manifest platform ${manifest.platform?.os}/${manifest.platform?.arch} does not match ${process.platform}/${process.arch}.`);
}
if (!manifest.platform?.binary) fail("Release manifest does not include platform.binary.");
if (manifest.package?.directory !== "cliff") fail("Release manifest package.directory must be cliff.");
if (manifest.package?.manifest !== "cliff/package-manifest.json") fail("Release manifest package.manifest is invalid.");
if (!Number.isFinite(manifest.package?.sizeBytes) || manifest.package.sizeBytes <= 0) fail("Release manifest package.sizeBytes is invalid.");
if (!/^[a-f0-9]{64}$/.test(manifest.package?.manifestSHA256 ?? "")) fail("Release manifest package.manifestSHA256 is invalid.");
if (!manifest.archive?.file || !manifest.archive.file.endsWith(".zip")) fail("Release manifest archive.file is invalid.");
if (manifest.archive?.checksumFile !== `${manifest.archive.file}.sha256`) fail("Release manifest archive.checksumFile is invalid.");
if (manifest.archive?.metadataFile !== `${manifest.archive.file}.json`) fail("Release manifest archive.metadataFile is invalid.");
if (!Number.isFinite(manifest.archive?.sizeBytes) || manifest.archive.sizeBytes <= 0) fail("Release manifest archive.sizeBytes is invalid.");
if (!/^[a-f0-9]{64}$/.test(manifest.archive?.sha256 ?? "")) fail("Release manifest archive.sha256 is invalid.");
validateInstaller("install.ps1", manifest.installers?.bootstrap?.windows);
validateInstaller("install.sh", manifest.installers?.bootstrap?.unix);
validateInstaller("install-package.ps1", manifest.installers?.package?.windows);
validateInstaller("install-package.sh", manifest.installers?.package?.unix);
validateCommand("commands.install", "windows", "irm getcliff.dev/install.ps1 | iex");
validateCommand("commands.install", "unix", "curl -fsSL getcliff.dev/install.sh | sh");
validateCommand("commands.run", "windows", "powershell -ExecutionPolicy Bypass -File .\\run.ps1");
validateCommand("commands.run", "unix", "sh ./run.sh");
validateCommand("commands.status", "windows", "powershell -ExecutionPolicy Bypass -File .\\status.ps1");
validateCommand("commands.status", "unix", "sh ./status.sh");
validateCommand("commands.stop", "windows", "powershell -ExecutionPolicy Bypass -File .\\stop.ps1");
validateCommand("commands.stop", "unix", "sh ./stop.sh");

const archivePath = path.join(manifestDir, manifest.archive.file);
const archiveChecksumPath = path.join(manifestDir, manifest.archive.checksumFile);
const archiveMetadataPath = path.join(manifestDir, manifest.archive.metadataFile);
const packageManifestPath = path.join(manifestDir, manifest.package.manifest);
const installerPaths = [
  { path: path.join(manifestDir, manifest.installers.bootstrap.windows.file), metadata: manifest.installers.bootstrap.windows },
  { path: path.join(manifestDir, manifest.installers.bootstrap.unix.file), metadata: manifest.installers.bootstrap.unix },
  { path: path.join(manifestDir, manifest.installers.package.windows.file), metadata: manifest.installers.package.windows },
  { path: path.join(manifestDir, manifest.installers.package.unix.file), metadata: manifest.installers.package.unix },
];
if (!existsSync(archivePath)) fail(`Release manifest archive is missing: ${archivePath}`);
if (!existsSync(archiveChecksumPath)) fail(`Release manifest checksum is missing: ${archiveChecksumPath}`);
if (!existsSync(archiveMetadataPath)) fail(`Release manifest archive metadata is missing: ${archiveMetadataPath}`);
if (!existsSync(packageManifestPath)) fail(`Release manifest package manifest is missing: ${packageManifestPath}`);
for (const installer of installerPaths) {
  if (!existsSync(installer.path)) fail(`Release manifest installer is missing: ${installer.path}`);
  const info = await stat(installer.path);
  if (info.size !== installer.metadata.sizeBytes) fail(`Release manifest installer size does not match file: ${installer.metadata.file}`);
  const hash = await fileSHA256(installer.path);
  if (hash !== installer.metadata.sha256) fail(`Release manifest installer hash does not match file: ${installer.metadata.file}`);
}

const unixBootstrapInstaller = readFileSync(path.join(manifestDir, manifest.installers.bootstrap.unix.file), "utf8");
const unixPackageInstaller = readFileSync(path.join(manifestDir, manifest.installers.package.unix.file), "utf8");
for (const [name, text] of [
  ["install.sh", unixBootstrapInstaller],
  ["install-package.sh", unixPackageInstaller],
]) {
  if (!text.includes("require_arg()") || !text.includes("Missing value for $option")) {
    fail(`Release installer ${name} does not validate missing option values.`);
  }
}

const archiveInfo = await stat(archivePath);
if (archiveInfo.size !== manifest.archive.sizeBytes) fail("Release manifest archive size does not match archive file.");

const archiveMetadata = readJSON(archiveMetadataPath, "Archive metadata");
const checksumText = readFileSync(archiveChecksumPath, "utf8").trim();
const checksumParts = checksumText.split(/\s+/);
const checksumHash = checksumParts[0] || "";
const checksumName = checksumParts.at(-1) || "";
if (checksumHash !== manifest.archive.sha256) fail("Release manifest archive hash does not match checksum file.");
if (checksumName !== manifest.archive.file) fail("Release manifest checksum file does not reference the archive file.");
if (archiveMetadata.archive !== manifest.archive.file) fail("Release manifest archive file does not match archive metadata.");
if (archiveMetadata.version !== manifest.version || archiveMetadata.commit !== manifest.commit || archiveMetadata.builtAt !== manifest.builtAt) {
  fail("Release manifest build metadata does not match archive metadata.");
}
if (archiveMetadata.archiveSHA256 !== manifest.archive.sha256) fail("Release manifest archive hash does not match archive metadata.");
if (archiveMetadata.archiveSizeBytes !== manifest.archive.sizeBytes) fail("Release manifest archive size does not match archive metadata.");
if (archiveMetadata.packageSizeBytes !== manifest.package.sizeBytes) fail("Release manifest package size does not match archive metadata.");
if (archiveMetadata.packageManifestSHA256 !== manifest.package.manifestSHA256) fail("Release manifest package manifest hash does not match archive metadata.");

const packageManifest = readJSON(packageManifestPath, "Package manifest");
if (packageManifest.binary !== manifest.platform.binary) fail("Release manifest binary does not match package manifest.");
if (packageManifest.version !== manifest.version || packageManifest.commit !== manifest.commit) fail("Release manifest build metadata does not match package manifest.");
if (packageManifest.packageSizeBytes !== manifest.package.sizeBytes) fail("Release manifest package size does not match package manifest.");
if (packageManifest.manifestSHA256 !== manifest.package.manifestSHA256) fail("Release manifest package manifest hash does not match package manifest.");

console.log(`Release manifest verified: ${path.relative(root, manifestPath) || manifestPath}`);
console.log(`Version: ${manifest.version} (${manifest.commit})`);
console.log(`Archive: ${manifest.archive.file}`);
