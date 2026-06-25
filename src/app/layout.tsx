import type { Metadata, Viewport } from "next";
import "./globals.css";

export const metadata: Metadata = {
  title: {
    default: "Cliff",
    template: "%s / Cliff",
  },
  applicationName: "Cliff",
  description: "A local web dashboard for importing, creating, modding, and operating Minecraft Java servers.",
  manifest: "/manifest.webmanifest",
  icons: {
    icon: "/icon.svg",
    shortcut: "/icon.svg",
    apple: "/apple-icon.svg",
  },
};

export const viewport: Viewport = {
  themeColor: "#111315",
  colorScheme: "dark",
};

export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  return (
    <html lang="en">
      <body>
        <script
          dangerouslySetInnerHTML={{
            __html: "window.__cliffPushState=History.prototype.pushState;",
          }}
        />
        {children}
      </body>
    </html>
  );
}
