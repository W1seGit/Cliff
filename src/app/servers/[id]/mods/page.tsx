import { redirect } from "next/navigation";

export const dynamicParams = false;

export async function generateStaticParams() {
  return [{ id: "__server__" }];
}

export default async function ModsPage(props: { params: Promise<{ id: string }> }) {
  const { id } = await props.params;
  redirect(`/servers/${encodeURIComponent(id)}/mods/installed`);
}
