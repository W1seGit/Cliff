/** Inline validation for server.properties numbers. Returns "" when valid. */
export function rangeError(value: number, min: number, max: number) {
  return Number.isFinite(value) && value >= min && value <= max ? "" : `Enter a number from ${min} to ${max}.`;
}
