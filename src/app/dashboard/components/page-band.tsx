"use client";

import type { ReactNode } from "react";
import { Menu } from "lucide-react";

/**
 * Header band for pages that are not about one server (App settings, Account,
 * Create, Import). It reuses the server header's layout so every page in the
 * shell has the same top edge: an icon tile, a title and a subtitle.
 */
export function PageBand({
  icon,
  title,
  subtitle,
  onOpenSidebar,
}: {
  icon: ReactNode;
  title: string;
  subtitle: string;
  onOpenSidebar: () => void;
}) {
  return (
    <header className="server-header" aria-label="Page context">
      <button className="mobile-sidebar-button" aria-label="Open sidebar" onClick={onOpenSidebar}>
        <Menu size={18} />
      </button>
      <div className="server-header-id">
        <span className="page-band-icon" aria-hidden="true">{icon}</span>
        <div className="server-header-meta">
          <div className="server-header-title">
            <h1>{title}</h1>
          </div>
          <div className="server-header-sub">
            <span>{subtitle}</span>
          </div>
        </div>
      </div>
    </header>
  );
}
