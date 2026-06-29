import * as React from "react";

export declare function Table(props: React.HTMLAttributes<HTMLTableElement>): React.JSX.Element;
export declare function THead(props: React.HTMLAttributes<HTMLTableSectionElement>): React.JSX.Element;
export declare function TBody(props: React.HTMLAttributes<HTMLTableSectionElement>): React.JSX.Element;

export interface TRProps extends React.HTMLAttributes<HTMLTableRowElement> {
  /** Highlight on hover (default true). Turn off for header rows. */
  hover?: boolean;
}
export declare function TR(props: TRProps): React.JSX.Element;
export declare function TH(props: React.ThHTMLAttributes<HTMLTableCellElement>): React.JSX.Element;

export interface TDProps extends React.TdHTMLAttributes<HTMLTableCellElement> {
  /** Monospace + tabular numbers — for IDs, concurrency, timestamps. */
  mono?: boolean;
}
export declare function TD(props: TDProps): React.JSX.Element;
