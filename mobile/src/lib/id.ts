// Client-generated idempotency key for a redemption request (FR-16). Doesn't
// need to be a real UUID, just unique per redemption attempt - avoids
// pulling in expo-crypto for one call site.
export function generateIdempotencyKey(): string {
  return `${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`;
}
