import AuthGate from "../../../auth-gate";

const serverTabs = new Set(["overview", "console", "mods", "worlds", "players", "backups", "files", "public-access", "settings"]);

export const dynamicParams = false;

export async function generateStaticParams() {
  return Array.from(serverTabs).map((tab) => ({ id: "__server__", tab }));
}

export default async function ServerTabPage(props: PageProps<"/servers/[id]/[tab]">) {
  const { id, tab } = await props.params;
  return <AuthGate initialServerId={id} initialTab={serverTabs.has(tab) ? tab : "overview"} />;
}
