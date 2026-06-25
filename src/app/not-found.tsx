import Link from "next/link";
import { Home } from "lucide-react";

export default function NotFound() {
  return (
    <main className="not-found-page">
      <section className="not-found-copy" aria-labelledby="not-found-title">
        {/* eslint-disable-next-line @next/next/no-img-element */}
        <img className="not-found-logo" src="/assets/cliff-logo.svg" alt="Cliff" />
        <h1 id="not-found-title">404</h1>
        <p className="not-found-heading">Page not found</p>
        <div className="not-found-divider" aria-hidden="true">
          <span />
          <i />
          <span />
        </div>
        <p className="not-found-message">Looks like this server wandered off the edge.</p>
        <Link className="not-found-action" href="/">
          <Home size={22} />
          <span>Go Home</span>
        </Link>
      </section>
    </main>
  );
}
