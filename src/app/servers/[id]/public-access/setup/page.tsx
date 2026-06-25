import AuthGate from "../../../../auth-gate";

export const dynamicParams = false;

export async function generateStaticParams() {
  return [{ id: "__server__" }];
}

export default async function PublicAccessSetupPage(props: PageProps<"/servers/[id]/public-access/setup">) {
  const { id } = await props.params;
  return <AuthGate initialServerId={id} initialTab="public-access/setup" />;
}
