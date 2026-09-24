/** Provider fetch retry delay (after initial failure). */
export const PROVIDER_RETRY_DELAY_MS = 3_000

/** Max backoff cap for provider fetch retries (exponential: 3s → 6s → 12s → 30s cap). */
export const PROVIDER_RETRY_MAX_DELAY_MS = 30_000
