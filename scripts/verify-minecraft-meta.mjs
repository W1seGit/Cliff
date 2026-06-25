const userAgent = "cliff/0.1.0 metadata-verifier";
const recentReleaseLimit = 12;

async function fetchJson(url) {
  const response = await fetch(url, {
    headers: { "User-Agent": userAgent },
    cache: "no-store",
  });
  if (!response.ok) throw new Error(`${url} returned ${response.status}`);
  return response.json();
}

async function fetchText(url) {
  const response = await fetch(url, {
    headers: { "User-Agent": userAgent },
    cache: "no-store",
  });
  if (!response.ok) throw new Error(`${url} returned ${response.status}`);
  return response.text();
}

function parseMavenVersions(xml) {
  return [...xml.matchAll(/<version>([^<]+)<\/version>/g)].map((match) => match[1]).reverse();
}

function forgeVersionsForMinecraft(versions, minecraftVersion) {
  return versions
    .filter((version) => version.startsWith(`${minecraftVersion}-`))
    .map((version) => version.slice(minecraftVersion.length + 1));
}

function neoforgeVersionsForMinecraft(versions, minecraftVersion) {
  const prefixes = [minecraftVersion];
  const [, minor, patch = "0"] = minecraftVersion.match(/^1\.(\d+)(?:\.(\d+))?/) ?? [];
  if (minor) prefixes.push(`${Number(minor)}.${patch}`);
  return versions.filter((version) => prefixes.some((prefix) => version === prefix || version.startsWith(`${prefix}.`)));
}

function forgePromotedVersionsForMinecraft(promotions, minecraftVersion) {
  const promos = promotions?.promos ?? {};
  return [...new Set([
    promos[`${minecraftVersion}-recommended`],
    promos[`${minecraftVersion}-latest`],
  ].filter(Boolean))];
}

function recentForgeLoaderVersions(versions) {
  const seen = new Set();
  const loaders = [];
  for (const version of versions) {
    const separator = version.indexOf("-");
    if (separator === -1) continue;
    const loaderVersion = version.slice(separator + 1);
    if (seen.has(loaderVersion)) continue;
    seen.add(loaderVersion);
    loaders.push(loaderVersion);
    if (loaders.length >= 50) break;
  }
  return loaders;
}

function assert(condition, message) {
  if (!condition) throw new Error(message);
}

const manifest = await fetchJson("https://piston-meta.mojang.com/mc/game/version_manifest_v2.json");
assert(manifest.latest?.release, "Mojang manifest did not include latest.release");
assert(manifest.latest?.snapshot, "Mojang manifest did not include latest.snapshot");

const releases = manifest.versions.filter((version) => version.type === "release").slice(0, recentReleaseLimit);
const latestRelease = releases.find((version) => version.id === manifest.latest.release);
assert(latestRelease, `Latest release ${manifest.latest.release} was not found in the release list`);

const latestDetails = await fetchJson(latestRelease.url);
assert(latestDetails.downloads?.server?.url, `Latest release ${manifest.latest.release} does not expose a server jar download`);

const [fabricLatestLoaders, forgePromotions, forgeXml, neoforgeXml] = await Promise.all([
  fetchJson(`https://meta.fabricmc.net/v2/versions/loader/${encodeURIComponent(manifest.latest.release)}`),
  fetchJson("https://files.minecraftforge.net/net/minecraftforge/forge/promotions_slim.json"),
  fetchText("https://maven.minecraftforge.net/net/minecraftforge/forge/maven-metadata.xml"),
  fetchText("https://maven.neoforged.net/releases/net/neoforged/neoforge/maven-metadata.xml"),
]);
assert(Array.isArray(fabricLatestLoaders) && fabricLatestLoaders.length > 0, `Fabric returned no loaders for ${manifest.latest.release}`);

const forgeAll = parseMavenVersions(forgeXml);
const neoforgeAll = parseMavenVersions(neoforgeXml);
const forgeCatalog = recentForgeLoaderVersions(forgeAll);
const neoforgeCatalog = neoforgeAll.slice(0, 50);
const forgeSupported = releases
  .map((version) => ({ minecraftVersion: version.id, loaders: [...forgePromotedVersionsForMinecraft(forgePromotions, version.id), ...forgeVersionsForMinecraft(forgeAll, version.id)] }))
  .find((entry) => entry.loaders.length > 0);
const neoforgeSupported = releases
  .map((version) => ({ minecraftVersion: version.id, loaders: neoforgeVersionsForMinecraft(neoforgeAll, version.id) }))
  .find((entry) => entry.loaders.length > 0);

assert(forgeSupported, `Forge returned no loaders for the latest ${recentReleaseLimit} Minecraft releases`);
assert(neoforgeSupported, `NeoForge returned no loaders for the latest ${recentReleaseLimit} Minecraft releases`);
assert(forgeCatalog.length > 10, "Forge Maven metadata did not provide a recent loader catalog");
assert(neoforgeCatalog.length > 10, "NeoForge Maven metadata did not provide a recent loader catalog");

console.log(`Mojang latest release: ${manifest.latest.release}`);
console.log(`Mojang latest snapshot: ${manifest.latest.snapshot}`);
console.log(`Latest release server jar: ${latestDetails.downloads.server.url}`);
console.log(`Fabric loaders for ${manifest.latest.release}: ${fabricLatestLoaders.length}`);
console.log(`Forge latest supported release in recent list: ${forgeSupported.minecraftVersion} (${forgeSupported.loaders[0]})`);
console.log(`NeoForge latest supported release in recent list: ${neoforgeSupported.minecraftVersion} (${neoforgeSupported.loaders[0]})`);
console.log(`Forge catalog loaders: ${forgeCatalog.length} (${forgeCatalog[0]})`);
console.log(`NeoForge catalog loaders: ${neoforgeCatalog.length} (${neoforgeCatalog[0]})`);
