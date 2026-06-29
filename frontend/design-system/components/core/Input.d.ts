import * as React from "react";

export interface InputProps extends React.InputHTMLAttributes<HTMLInputElement> {
  /** Red border for validation errors. */
  invalid?: boolean;
  /** Monospace text — for API keys, IDs, tokens. */
  mono?: boolean;
}

/** Dark-surface text field. */
export declare function Input(props: InputProps): React.JSX.Element;
export declare function Label(props: React.LabelHTMLAttributes<HTMLLabelElement>): React.JSX.Element;
