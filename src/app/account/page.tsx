import AuthGate from "../auth-gate";

export default function AccountPage() {
  return <AuthGate initialTab="account" />;
}
