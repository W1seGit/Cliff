import AuthGate from "../auth-gate";

export default function CreatePage() {
  return <AuthGate initialTab="create" />;
}
