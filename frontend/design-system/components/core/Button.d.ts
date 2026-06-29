import * as React from "react";

export type ButtonVariant =
  | "default"
  | "destructive"
  | "outline"
  | "secondary"
  | "ghost"
  | "link";
export type ButtonSize = "sm" | "md" | "lg" | "icon";

export interface ButtonProps
  extends React.ButtonHTMLAttributes<HTMLButtonElement> {
  /** Visual style. `default` is the brand teal fill. */
  variant?: ButtonVariant;
  /** Control height/padding preset. `icon` is a square 40px button. */
  size?: ButtonSize;
}

/**
 * Primary action control for the HUAKAI console.
 * @startingPoint section="Core" subtitle="Brand teal button with 6 variants" viewport="700x200"
 */
export declare function Button(props: ButtonProps): React.JSX.Element;
