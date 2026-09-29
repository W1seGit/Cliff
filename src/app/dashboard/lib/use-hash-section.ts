"use client";

import { useCallback, useEffect, useState } from "react";

function readHash<T extends string>(valid: readonly T[], fallback: T): T {
  if (typeof window === "undefined") return fallback;
  const hash = window.location.hash.replace("#", "");
  return (valid as readonly string[]).includes(hash) ? (hash as T) : fallback;
}

/**
 * Tracks the active settings section in the URL hash so sections are
 * linkable and Back/Forward move between them. The fallback section has no hash.
 */
export function useHashSection<T extends string>(valid: readonly T[], fallback: T): [T, (id: string) => void] {
  const [active, setActive] = useState<T>(() => readHash(valid, fallback));

  useEffect(() => {
    const onHashChange = () => setActive(readHash(valid, fallback));
    window.addEventListener("hashchange", onHashChange);
    return () => window.removeEventListener("hashchange", onHashChange);
  }, [valid, fallback]);

  const select = useCallback(
    (id: string) => {
      if (!(valid as readonly string[]).includes(id)) return;
      setActive(id as T);
      window.history.replaceState(null, "", id === fallback ? window.location.pathname : `${window.location.pathname}#${id}`);
    },
    [valid, fallback],
  );

  return [active, select];
}
