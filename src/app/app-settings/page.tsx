import AuthGate from "../auth-gate";

export default function AppSettingsPage() {
  return <AuthGate initialTab="app" />;
}
