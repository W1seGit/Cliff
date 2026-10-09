/** Inline validation for server.properties numbers. Returns "" when valid. */
export function rangeError(value: number, min: number, max: number) {
  return Number.isFinite(value) && value >= min && value <= max ? "" : `Enter a number from ${min} to ${max}.`;
}

export const restartAttemptsRange = { min: 1, max: 20 } as const;
export const restartWindowRange = { min: 1, max: 1440 } as const;

/** Whole-number check for the crash-restart limits. Returns "" when valid. */
export function wholeRangeError(value: number, min: number, max: number) {
  return Number.isInteger(value) && value >= min && value <= max ? "" : `Enter a whole number from ${min} to ${max}.`;
}

export function restartPolicyValid(policy: "off" | "on-crash", attempts: number, windowMinutes: number) {
  if (policy !== "on-crash") return true;
  return !wholeRangeError(attempts, restartAttemptsRange.min, restartAttemptsRange.max) &&
    !wholeRangeError(windowMinutes, restartWindowRange.min, restartWindowRange.max);
}
