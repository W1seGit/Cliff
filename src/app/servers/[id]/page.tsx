import AuthGate from "../../auth-gate";

export const dynamicParams = false;

export async function generateStaticParams() {
  return [{ id: "__server__" }];
}

export default async function ServerPage(props: PageProps<"/servers/[id]">) {
  const { id } = await props.params;
  return <AuthGate initialServerId={id} initialTab="overview" />;
}
