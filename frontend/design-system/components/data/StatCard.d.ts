import * as React from "react";

export type StatTone = "primary" | "blue" | "emerald" | "amber" | "red" | "slate";

export interface StatCardProps extends React.HTMLAttributes<HTMLDivElement> {
  /** Metric label, e.g. 今日 Token 用量. */
  title: string;
  /** Big formatted value. Render numbers with tabular spacing. */
  value: React.ReactNode;
  /** A lucide icon element shown in the tone-colored chip. */
  icon?: React.ReactNode;
  /** Sub-label under the title. */
  description?: string;
  /** Secondary breakdown line under the value. */
  detail?: React.ReactNode;
  /** Icon-chip colour. */
  tone?: StatTone;
}

/**
 * KPI tile for the operations dashboard.
 * @startingPoint section="Data" subtitle="Dashboard KPI tile with icon chip" viewport="700x180"
 */
export declare function StatCard(props: StatCardProps): React.JSX.Element;
