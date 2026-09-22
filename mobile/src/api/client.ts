const DEFAULT_TIMEOUT_MS = 5000;

export class ApiError extends Error {}

type ApiFetchOptions = RequestInit & { timeoutMs?: number };

// Thin fetch wrapper with a timeout. The mobile app is an untrusted client:
// it only displays whatever the server returns, never computes cashback.
export async function apiFetch<T>(path: string, options: ApiFetchOptions = {}): Promise<T> {
  const baseUrl = process.env.EXPO_PUBLIC_API_URL;
  if (!baseUrl) {
    throw new ApiError("EXPO_PUBLIC_API_URL is not set (see mobile/.env.example)");
  }

  const { timeoutMs = DEFAULT_TIMEOUT_MS, ...init } = options;
  const controller = new AbortController();
  const timeoutId = setTimeout(() => controller.abort(), timeoutMs);

  try {
    const response = await fetch(`${baseUrl}${path}`, {
      ...init,
      signal: controller.signal,
    });

    if (!response.ok) {
      throw new ApiError(`${path} responded with HTTP ${response.status}`);
    }

    return (await response.json()) as T;
  } catch (err) {
    if (err instanceof Error && err.name === "AbortError") {
      throw new ApiError(`${path} timed out after ${timeoutMs}ms`);
    }
    throw err;
  } finally {
    clearTimeout(timeoutId);
  }
}
