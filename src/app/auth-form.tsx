"use client";

import { useState, type FormEvent } from "react";
import { Eye, EyeOff } from "lucide-react";
import { externalApiUrl } from "./dashboard/lib/utils";
import type { User } from "./dashboard/lib/types";
import { Button } from "./dashboard/components/ui/button";
import { Input } from "./dashboard/components/ui/input";

export default function AuthForm({ needsSetup, initialError = "", onAuthenticated }: { needsSetup: boolean; initialError?: string; onAuthenticated: (user: User) => void }) {
  const [message, setMessage] = useState(initialError);
  const [pending, setPending] = useState(false);
  const [showPasswords, setShowPasswords] = useState(false);
  const [confirmPassword, setConfirmPassword] = useState("");

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
        }),
      });
      const data = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(data.error || (needsSetup ? "Setup failed" : "Login failed"));
      onAuthenticated(data.user);
    } catch (error) {
      setMessage(error instanceof Error ? error.message : needsSetup ? "Setup failed" : "Login failed");
    } finally {
      setPending(false);
    }
  }

  const passwordToggle = (
    <button
      type="button"
      className="password-toggle"
      onClick={() => setShowPasswords(!showPasswords)}
      aria-label={showPasswords ? "Hide passwords" : "Show passwords"}
      tabIndex={-1}
    >
      {showPasswords ? <EyeOff size={18} /> : <Eye size={18} />}
    </button>
  );

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
        <Input
          label="Password"
          name="password"
          type={showPasswords ? "text" : "password"}
          autoComplete={needsSetup ? "new-password" : "current-password"}
          required
          suffix={passwordToggle}
        />
        {needsSetup && (
          <Input
            label="Confirm password"
            name="confirm-password"
            type={showPasswords ? "text" : "password"}
            autoComplete="new-password"
            required
            value={confirmPassword}
            onChange={(e) => setConfirmPassword(e.target.value)}
            suffix={passwordToggle}
          />
        )}
        <Button variant="primary" type="submit" disabled={pending}>
          {pending ? "Working..." : needsSetup ? "Create account" : "Login"}
        </Button>
        {message && <p className="error">{message}</p>}
      </form>
    </main>
  );
}
