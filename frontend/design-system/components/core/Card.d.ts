import * as React from "react";

export interface CardProps extends React.HTMLAttributes<HTMLDivElement> {
  /** Lift + warm the border to teal on hover (for clickable nav cards). */
  interactive?: boolean;
}

/**
 * Primary surface for the console — wraps stats, tables, panels.
 * @startingPoint section="Core" subtitle="Surface with header/title/content slots" viewport="700x240"
 */
export declare function Card(props: CardProps): React.JSX.Element;
export declare function CardHeader(props: React.HTMLAttributes<HTMLDivElement>): React.JSX.Element;
export declare function CardTitle(props: React.HTMLAttributes<HTMLDivElement>): React.JSX.Element;
export declare function CardDescription(props: React.HTMLAttributes<HTMLDivElement>): React.JSX.Element;
export declare function CardContent(props: React.HTMLAttributes<HTMLDivElement>): React.JSX.Element;
export declare function CardFooter(props: React.HTMLAttributes<HTMLDivElement>): React.JSX.Element;
