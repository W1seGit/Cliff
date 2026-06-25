import type { MetadataRoute } from "next";

export const dynamic = "force-static";

export default function manifest(): MetadataRoute.Manifest {
  return {
    name: "Cliff",
    short_name: "Cliff",
    description: "Local Minecraft Java server management dashboard.",
    start_url: "/",
    display: "standalone",
    background_color: "#111315",
    theme_color: "#111315",
    icons: [
      {
        src: "/icon.svg",
        sizes: "any",
        type: "image/svg+xml",
      },
      {
        src: "/apple-icon.svg",
        sizes: "180x180",
        type: "image/svg+xml",
      },
    ],
  };
}
