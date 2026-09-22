import { useState } from "react";
import { Pressable, SafeAreaView, StyleSheet, Text, TextInput, View } from "react-native";
import { StatusBar } from "expo-status-bar";
import Dashboard from "./src/screens/Dashboard";

// No auth in this MVP (requirements.md §4) - the app just asks for a user
// id rather than logging in. Users are seeded directly via SQL (decisions.md
// "User identity"), not created through this app - see scripts/seed_users.sql
// for the known demo ids. Not persisted across app restarts; kept in memory
// for the session, which is enough for a reviewer/demo tool.
export default function App() {
  const [userId, setUserId] = useState<string | null>(null);

  return (
    <SafeAreaView style={styles.safeArea}>
      {userId ? (
        <Dashboard userId={userId} onSwitchUser={() => setUserId(null)} />
      ) : (
        <UserIdPrompt onSubmit={setUserId} />
      )}
      <StatusBar style="auto" />
    </SafeAreaView>
  );
}

function UserIdPrompt({ onSubmit }: { onSubmit: (userId: string) => void }) {
  const [text, setText] = useState("");

  return (
    <View style={styles.promptContainer}>
      <Text style={styles.title}>Flash Cashback</Text>
      <Text style={styles.subtitle}>Enter a user id to view their cashback</Text>
      <TextInput
        style={styles.input}
        placeholder="e.g. 11111111-1111-1111-1111-111111111111"
        autoCapitalize="none"
        autoCorrect={false}
        value={text}
        onChangeText={setText}
        onSubmitEditing={() => text.trim() && onSubmit(text.trim())}
      />
      <Pressable
        style={[styles.button, !text.trim() && styles.buttonDisabled]}
        onPress={() => text.trim() && onSubmit(text.trim())}
        disabled={!text.trim()}
      >
        <Text style={styles.buttonText}>Continue</Text>
      </Pressable>
    </View>
  );
}

const styles = StyleSheet.create({
  safeArea: {
    flex: 1,
    backgroundColor: "#fff",
  },
  promptContainer: {
    flex: 1,
    alignItems: "center",
    justifyContent: "center",
    padding: 24,
    gap: 12,
  },
  title: {
    fontSize: 22,
    fontWeight: "700",
  },
  subtitle: {
    fontSize: 14,
    color: "#666",
    marginBottom: 8,
  },
  input: {
    width: "100%",
    borderWidth: 1,
    borderColor: "#ddd",
    borderRadius: 8,
    paddingHorizontal: 12,
    paddingVertical: 10,
  },
  button: {
    marginTop: 8,
    paddingVertical: 10,
    paddingHorizontal: 24,
    backgroundColor: "#1a1a1a",
    borderRadius: 8,
  },
  buttonDisabled: {
    opacity: 0.4,
  },
  buttonText: {
    color: "#fff",
    fontWeight: "600",
  },
});
