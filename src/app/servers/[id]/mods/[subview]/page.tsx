import AuthGate from "../../../../auth-gate";
import { redirect } from "next/navigation";

const subviews = new Set(["installed", "discover"]);

export const dynamicParams = false;

export async function generateStaticParams() {
  return Array.from(subviews).map((subview) => ({ id: "__server__", subview }));
}

export default async function ModsSubviewPage(props: { params: Promise<{ id: string; subview: string }> }) {
  const { id, subview } = await props.params;
  if (!subviews.has(subview)) redirect(`/servers/${encodeURIComponent(id)}/mods/installed`);
  return <AuthGate initialServerId={id} initialTab={`mods/${subview}`} />;
}
