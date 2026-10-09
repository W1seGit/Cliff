"use client";

import { useState, type FormEvent } from "react";
import { externalApiUrl } from "./dashboard/lib/utils";
import type { User } from "./dashboard/lib/types";
import { Button } from "./dashboard/components/ui/button";
import { Input } from "./dashboard/components/ui/input";
import { PasswordInput } from "./dashboard/components/ui/password-input";

export default function AuthForm({ needsSetup, initialError = "", onAuthenticated }: { needsSetup: boolean; initialError?: string; onAuthenticated: (user: User) => void }) {
  const [message, setMessage] = useState(initialError);
  const [pending, setPending] = useState(false);
  const [confirmPassword, setConfirmPassword] = useState("");
  const [needsCode, setNeedsCode] = useState(false);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (pending) return;
    if (needsSetup) {
      const formData = new FormData(event.currentTarget);
      const password = String(formData.get("password") ?? "");
      if (password !== confirmPassword) {
        setMessage("Passwords do not match");
        return;
      }
    }
    setPending(true);
    setMessage("");
    const formData = new FormData(event.currentTarget);
    try {
      const response = await fetch(externalApiUrl(needsSetup ? "/api/auth/setup" : "/api/auth/login"), {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          username: String(formData.get("username") ?? ""),
          password: String(formData.get("password") ?? ""),
          ...(needsCode ? { code: String(formData.get("code") ?? "").trim() } : {}),
        }),
      });
      const data = await response.json().catch(() => ({}));
      if (!response.ok && response.status === 401 && data.totpRequired) {
        setNeedsCode(true);
        setMessage(needsCode ? data.error || "That code did not work" : "");
        return;
      }
      if (!response.ok) throw new Error(data.error || (needsSetup ? "Setup failed" : "Login failed"));
      onAuthenticated(data.user);
    } catch (error) {
      setMessage(error instanceof Error ? error.message : needsSetup ? "Setup failed" : "Login failed");
    } finally {
      setPending(false);
    }
  }

  return (
    <main className="center-panel">
      <form className="auth-card" onSubmit={submit}>
        {/* eslint-disable-next-line @next/next/no-img-element */}
        <img className="auth-logo" src="/assets/cliff-logo.svg" alt="Cliff" />
        <h1>{needsSetup ? "Create local admin" : "Login"}</h1>
        <p>
          {needsSetup
            ? "This dashboard controls server files, console commands, and system processes. Create a strong password to protect it — even on a trusted home network."
            : "Sign in to manage your Minecraft servers, console, and files. This dashboard runs locally on your network."}
        </p>
        <Input
          label="Username"
          name="username"
          defaultValue={needsSetup ? "" : "admin"}
          autoComplete="username"
          required
        />
        <PasswordInput
          label="Password"
          name="password"
          autoComplete={needsSetup ? "new-password" : "current-password"}
          required
        />
        {needsSetup && (
          <PasswordInput
            label="Confirm password"
            name="confirm-password"
            autoComplete="new-password"
            required
            value={confirmPassword}
            onChange={(e) => setConfirmPassword(e.target.value)}
          />
        )}
        {needsCode && !needsSetup && (
          <Input
            label="Two-factor code"
            name="code"
            autoComplete="one-time-code"
            autoFocus
            required
          />
        )}
        {needsCode && !needsSetup && (
          <p>6-digit code from your authenticator app, or a recovery code</p>
        )}
        <Button variant="primary" type="submit" disabled={pending}>
          {pending ? "Working..." : needsSetup ? "Create account" : "Login"}
        </Button>
        {message && <p className="error">{message}</p>}
      </form>
    </main>
  );
}
