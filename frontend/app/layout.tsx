import type { Metadata } from "next";
import "./globals.css";
import AppLayout from "@/components/layout/AppLayout";

export const metadata: Metadata = {
  title: "HUAKAI 控制台",
  description: "HUAKAI AI Gateway、账号池与运营管理总览",
};

export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  return (
    <html lang="zh-CN" className="dark">
      <body>
        <AppLayout>{children}</AppLayout>
      </body>
    </html>
  );
}
