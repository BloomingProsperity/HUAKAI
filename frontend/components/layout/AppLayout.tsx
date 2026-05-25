'use client';

import { useState } from 'react';
import Sidebar from '@/components/layout/Sidebar';
import Header from '@/components/layout/Header';
import { cn } from '@/lib/utils';

export default function AppLayout({ children }: { children: React.ReactNode }) {
  const [sidebarCollapsed, setSidebarCollapsed] = useState(false);

  return (
    <div className="min-h-screen bg-accent-50 text-accent-950 dark:bg-accent-950 dark:text-accent-50">
      <Sidebar
        collapsed={sidebarCollapsed}
        onToggle={() => setSidebarCollapsed((value) => !value)}
      />
      <div
        className={cn(
          'flex min-h-screen flex-col transition-[padding] duration-300',
          sidebarCollapsed ? 'lg:pl-[72px]' : 'lg:pl-64'
        )}
      >
        <Header collapsed={sidebarCollapsed} />
        <main className="flex-1 overflow-x-hidden px-4 py-5 md:px-6 lg:px-8 lg:py-7">{children}</main>
      </div>
    </div>
  );
}
