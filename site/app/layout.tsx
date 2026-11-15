import type { Metadata } from "next";
import "./globals.css";
export const metadata: Metadata = {title: "GPT-Home", description: "Private home hub tools for ChatGPT"};
export default function RootLayout({children}: Readonly<{children: React.ReactNode}>) {
  return <html lang="en"><body>{children}</body></html>;
}
