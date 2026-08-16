// shared/cn.ts — composição de classes (padrão extraído do react-redux-boilerplate,
// sem o twMerge — não usamos Tailwind). clsx resolve condicionais e arrays.

import { clsx, type ClassValue } from "clsx";

export function cn(...inputs: ClassValue[]): string {
  return clsx(inputs);
}
