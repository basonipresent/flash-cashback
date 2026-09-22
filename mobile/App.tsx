import { useCallback, useEffect, useState } from "react";
import { ActivityIndicator, Pressable, StyleSheet, Text, View } from "react-native";
import { StatusBar } from "expo-status-bar";
import { apiFetch, ApiError } from "./src/api/client";

type ReadyzResponse = {
  postgres?: string;
  redis?: string;
};

type Status =
  | { kind: "loading" }
  | { kind: "ok"; body: ReadyzResponse }
  | { kind: "error"; message: string };

export default function App() {
  const [status, setStatus] = useState<Status>({ kind: "loading" });

  const checkReadyz = useCallback(async () => {
    setStatus({ kind: "loading" });
    try {
      const body = await apiFetch<ReadyzResponse>("/readyz");
      setStatus({ kind: "ok", body });
    } catch (err) {
      setStatus({ kind: "error", message: err instanceof ApiError ? err.message : "Unknown error" });
    }
  }, []);

  useEffect(() => {
    checkReadyz();
  }, [checkReadyz]);

  return (
    <View style={styles.container}>
      <Text style={styles.title}>Flash Cashback</Text>
      <Text style={styles.subtitle}>Backend connectivity</Text>

      {status.kind === "loading" && <ActivityIndicator />}

      {status.kind === "ok" && (
        <Text style={styles.ok}>
          Reachable — postgres: {status.body.postgres}, redis: {status.body.redis}
        </Text>
      )}

      {status.kind === "error" && <Text style={styles.error}>Unreachable — {status.message}</Text>}

      <Pressable style={styles.button} onPress={checkReadyz}>
        <Text style={styles.buttonText}>Retry</Text>
      </Pressable>

      <StatusBar style="auto" />
    </View>
  );
}

const styles = StyleSheet.create({
  container: {
    flex: 1,
    backgroundColor: "#fff",
    alignItems: "center",
    justifyContent: "center",
    gap: 12,
    padding: 24,
  },
  title: {
    fontSize: 22,
    fontWeight: "700",
  },
  subtitle: {
    fontSize: 14,
    color: "#666",
  },
  ok: {
    color: "#0a7d32",
    textAlign: "center",
  },
  error: {
    color: "#b00020",
    textAlign: "center",
  },
  button: {
    marginTop: 8,
    paddingVertical: 10,
    paddingHorizontal: 20,
    backgroundColor: "#1a1a1a",
    borderRadius: 8,
  },
  buttonText: {
    color: "#fff",
    fontWeight: "600",
  },
});
