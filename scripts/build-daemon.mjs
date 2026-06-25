import { spawnSync } from "node:child_process";
import { readFileSync } from "node:fs";
import path from "node:path";

const root = process.cwd();
const daemonDir = path.join(root, "daemon");
const output = process.platform === "win32" ? "cliff.exe" : "cliff";
const packageJson = JSON.parse(readFileSync(path.join(root, "package.json"), "utf8"));
const buildInfoImport = "github.com/W1seGit/Cliff/daemon/internal/buildinfo";

function commandName(name) {
  return process.platform === "win32" && (name === "npm" || name === "npx") ? `${name}.cmd` : name;
}

function run(command, args, cwd = root) {
  const executable = commandName(command);
  const useShell = process.platform === "win32" && (command === "npm" || command === "npx");
  const result = spawnSync(executable, args, { cwd, stdio: "inherit", shell: useShell });
  if (result.error) {
    console.error(`Failed to run ${executable}: ${result.error.message}`);
    process.exit(1);
  }
  if (result.status !== 0) process.exit(result.status ?? 1);
}

function commandOutput(command, args) {
  const executable = commandName(command);
  const useShell = process.platform === "win32" && (command === "npm" || command === "npx");
  const result = spawnSync(executable, args, { cwd: root, encoding: "utf8", shell: useShell });
  if (result.status !== 0 || result.error) return "unknown";
  return result.stdout.trim() || "unknown";
}

function ldflags() {
  const version = packageJson.version || "dev";
  const commit = commandOutput("git", ["rev-parse", "--short=12", "HEAD"]);
  const builtAt = new Date().toISOString();
  return [
    "-s",
    "-w",
    `-X ${buildInfoImport}.Version=${version}`,
    `-X ${buildInfoImport}.Commit=${commit}`,
    `-X ${buildInfoImport}.BuiltAt=${builtAt}`,
  ].join(" ");
}

run("npm", ["run", "build:daemon-web"]);
run("go", ["build", "-ldflags", ldflags(), "-o", output, "./cmd/cliff"], daemonDir);
