"use client";

export interface JoinAddressProps {
  label: string;
  address: string;
  onCopy: () => void;
  /** wrap in a bordered card surface */
  card?: boolean;
  className?: string;
}

export function JoinAddress({
  label,
  address,
  onCopy,
  card = false,
  className = "",
}: JoinAddressProps) {
  return (
    <div className={`overview-hero ${card ? "join-address-card" : ""} ${className}`.trim()}>
      <span className="overview-hero-label">{label}</span>
      <button className="overview-hero-address" type="button" onClick={onCopy} aria-label={`Copy ${address}`}>
        <strong>{address}</strong>
        <em>Copy</em>
      </button>
    </div>
  );
}
