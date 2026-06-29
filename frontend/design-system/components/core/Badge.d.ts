import * as React from "react";

export type BadgeVariant =
  | "default"
  | "secondary"
  | "destructive"
  | "outline"
  | "success"
  | "warning"
  | "info";

export interface BadgeProps extends React.HTMLAttributes<HTMLSpanElement> {
  /** Tone of the pill. Map account health to success/warning/destructive. */
  variant?: BadgeVariant;
}

/**
 * Rounded status/label pill — the console's primary way to show account health & schedule state.
 */
export declare function Badge(props: BadgeProps): React.JSX.Element;
