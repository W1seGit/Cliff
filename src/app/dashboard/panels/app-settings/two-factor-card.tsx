"use client";

import { useState } from "react";
import { ShieldCheck } from "lucide-react";
import { disableTwoFactor, enableTwoFactor, startTwoFactorSetup } from "../../lib/runtime-client";
import type { User } from "../../lib/types";
import { Banner, Button, Card, CopyButton, Input, Modal, PasswordInput, Pill, SettingRow } from "../../components/ui";

type Step = "closed" | "password" | "code" | "recovery" | "disable";

const errorText = (error: unknown, fallback: string) => (error instanceof Error ? error.message : fallback);

/** Group a secret key in blocks of four so it is easier to type. */
function groupSecret(secret: string) {
  return secret.replace(/\s+/g, "").replace(/(.{4})/g, "$1 ").trim();
}

/** Turn two-factor sign-in on or off for the signed-in user. */
export function TwoFactorCard({ user, onMessage }: { user: User; onMessage: (message: string) => void }) {
  const [step, setStep] = useState<Step>("closed");
  const [password, setPassword] = useState("");
  const [code, setCode] = useState("");
  const [secret, setSecret] = useState("");
  const [uri, setUri] = useState("");
  const [recoveryCodes, setRecoveryCodes] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  function reset() {
    setStep("closed");
    setPassword("");
    setCode("");
    setSecret("");
    setUri("");
    setError("");
    setBusy(false);
  }

  function finish() {
    reset();
    setRecoveryCodes([]);
    // The signed-in user is loaded once at startup; reload so it reflects the change.
    window.location.reload();
  }

  async function run(action: () => Promise<void>, failure: string) {
    setBusy(true);
    setError("");
    try {
      await action();
    } catch (err) {
      setError(errorText(err, failure));
    } finally {
      setBusy(false);
    }
  }

  const begin = () =>
    run(async () => {
      const data = await startTwoFactorSetup(password);
      setSecret(data.secret);
      setUri(data.uri);
      setPassword("");
      setCode("");
      setStep("code");
    }, "Could not start setup");

  const enable = () =>
    run(async () => {
      const data = await enableTwoFactor(code.trim());
      setRecoveryCodes(data.recoveryCodes ?? []);
      setCode("");
      setStep("recovery");
    }, "That code did not work");

  const disable = () =>
    run(async () => {
      await disableTwoFactor(password, code.trim());
      onMessage("Two-factor turned off");
      finish();
    }, "Could not turn off two-factor");

  const enabled = Boolean(user.totpEnabled);

  return (
    <Card
      title="Two-factor authentication"
      description="Ask for a 6-digit code from an authenticator app when signing in, on top of your password."
      actions={<Pill variant={enabled ? "success" : "default"}>{enabled ? "On" : "Off"}</Pill>}
    >
      <SettingRow
        label={enabled ? "Two-factor is on" : "Two-factor is off"}
        description={enabled ? "You need your authenticator app (or a recovery code) every time you sign in." : "Recommended if anyone else can reach this dashboard."}
      >
        {enabled ? (
          <Button variant="danger" onClick={() => { reset(); setStep("disable"); }}>Turn off</Button>
        ) : (
          <Button iconLeft={<ShieldCheck size={14} />} onClick={() => { reset(); setStep("password"); }}>Set up</Button>
        )}
      </SettingRow>

      <Modal
        isOpen={step === "password"}
        onClose={reset}
        title="Set up two-factor"
        description="Enter your current password to continue."
        confirmLabel="Continue"
        onConfirm={() => void begin()}
        confirmDisabled={!password}
        busy={busy}
      >
        <PasswordInput label="Current password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="current-password" />
        {error && <Banner variant="danger">{error}</Banner>}
      </Modal>

      <Modal
        isOpen={step === "code"}
        onClose={reset}
        title="Add Cliff to your authenticator"
        description="In your authenticator app choose 'enter a setup key' and type the key below. Then enter the 6-digit code it shows."
        confirmLabel="Turn on"
        onConfirm={() => void enable()}
        confirmDisabled={code.trim().length < 6}
        busy={busy}
      >
        <div className="twofa-secret">
          <code className="twofa-secret-key" aria-label="Setup key">{groupSecret(secret)}</code>
          <CopyButton text={secret} label="Copy setup key" />
        </div>
        <p className="accounts-hint">
          Some apps can open this link directly: <a href={uri}>setup link</a> <CopyButton text={uri} label="Copy setup link" />
        </p>
        <Input label="6-digit code" value={code} onChange={(e) => setCode(e.target.value)} inputMode="numeric" autoComplete="one-time-code" maxLength={8} />
        {error && <Banner variant="danger">{error}</Banner>}
      </Modal>

      <Modal
        isOpen={step === "recovery"}
        onClose={finish}
        title="Save your recovery codes"
        confirmLabel="I have saved them"
        onConfirm={finish}
        cancelLabel="Close"
        onCancel={finish}
        rolePresentationClick={false}
      >
        <Banner variant="warning" title="Save these now, they are shown only once">
          If you lose your phone, each code can be used once to sign in. Keep them somewhere safe.
        </Banner>
        <ul className="twofa-codes">
          {recoveryCodes.map((item) => (
            <li key={item}><code>{item}</code></li>
          ))}
        </ul>
        <div>
          <span className="twofa-copy-all">
            <CopyButton text={recoveryCodes.join("\n")} label="Copy all recovery codes" /> Copy all
          </span>
        </div>
      </Modal>

      <Modal
        isOpen={step === "disable"}
        onClose={reset}
        title="Turn off two-factor"
        description="Enter your password and a current 6-digit code (or one recovery code)."
        confirmLabel="Turn off"
        confirmVariant="danger"
        onConfirm={() => void disable()}
        confirmDisabled={!password || !code.trim()}
        busy={busy}
      >
        <PasswordInput label="Current password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="current-password" />
        <Input label="Code or recovery code" value={code} onChange={(e) => setCode(e.target.value)} autoComplete="one-time-code" />
        {error && <Banner variant="danger">{error}</Banner>}
      </Modal>
    </Card>
  );
}
