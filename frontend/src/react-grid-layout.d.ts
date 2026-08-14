// Declaração do subpath /legacy do react-grid-layout (o ESM principal não
// exporta WidthProvider; o legacy tem). Tipado minimamente — o runtime é o
// que importa.
declare module "react-grid-layout/legacy" {
  import type { ComponentType } from "react";
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  export function WidthProvider<P>(component: ComponentType<P>): ComponentType<any>;
}
