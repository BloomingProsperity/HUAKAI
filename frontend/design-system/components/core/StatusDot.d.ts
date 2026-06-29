import * as React from "react";

export type StatusTone = "online" | "offline" | "pending" | "idle" | "live";

export interface StatusDotProps extends React.HTMLAttributes<HTMLSpanElement> {
  /** Maps to the brand status colours. */
  tone?: StatusTone;
  /** Diameter in px. Default 8. */
  size?: number;
  /** Gently pulse opacity for "live polling" feel. */
  pulse?: boolean;
}

/** A small dot with a soft halo ring — used in the heartbeat/status indicators. */
export declare function StatusDot(props: StatusDotProps): React.JSX.Element;
