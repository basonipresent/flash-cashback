import { useState } from "react";
import {
  ActivityIndicator,
  FlatList,
  Pressable,
  RefreshControl,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import { ApiError } from "../api/client";
import { redeem, HistoryEntry, ReasonCode } from "../api/cashback";
import { generateIdempotencyKey } from "../lib/id";
import { useCashbackData } from "../hooks/useCashbackData";

const REASON_LABELS: Record<ReasonCode, string> = {
  AWARDED: "Awarded",
  PARTIAL_DAILY_CAP: "Partial - daily cap reached",
  PARTIAL_BUDGET: "Partial - campaign budget low",
  BELOW_MINIMUM: "Below minimum payment amount",
  DAILY_CAP_REACHED: "Daily cap reached",
  CAMPAIGN_ENDED: "Campaign ended",
  "": "",
};

// Rp thousands separator, e.g. 1234567 -> "Rp 1.234.567". Display-only
// formatting of a number the server already computed - not a cashback
// calculation (NFR-07).
function formatIDR(amount: number): string {
  const sign = amount < 0 ? "-" : "";
  const digits = Math.abs(amount).toString();
  const withSeparators = digits.replace(/\B(?=(\d{3})+(?!\d))/g, ".");
  return `${sign}Rp ${withSeparators}`;
}

function formatDate(iso: string): string {
  const d = new Date(iso);
  return d.toLocaleString();
}

type Props = {
  userId: string;
  onSwitchUser: () => void;
};

export default function Dashboard({ userId, onSwitchUser }: Props) {
  const { state, refreshing, refresh, reload } = useCashbackData(userId);

  if (state.kind === "loading") {
    return (
      <View style={styles.centered}>
        <ActivityIndicator size="large" />
      </View>
    );
  }

  if (state.kind === "error") {
    return (
      <View style={styles.centered}>
        <Text style={styles.errorText}>Couldn't load your cashback data — {state.message}</Text>
        <Pressable style={styles.button} onPress={reload}>
          <Text style={styles.buttonText}>Retry</Text>
        </Pressable>
      </View>
    );
  }

  const { data } = state;

  return (
    <FlatList
      style={styles.list}
      contentContainerStyle={styles.listContent}
      data={data.history}
      keyExtractor={(item) => `${item.entry_type}-${item.ref_id}`}
      refreshControl={<RefreshControl refreshing={refreshing} onRefresh={refresh} />}
      ListHeaderComponent={
        <Header userId={userId} onSwitchUser={onSwitchUser} balance={data.balance} campaignActive={data.campaign.status === "active"} earnedToday={data.daily.earned_today} remainingToday={data.daily.remaining_today} onRedeemed={refresh} />
      }
      ListEmptyComponent={<Text style={styles.emptyText}>No history yet.</Text>}
      renderItem={({ item }) => <HistoryRow entry={item} />}
    />
  );
}

function Header(props: {
  userId: string;
  onSwitchUser: () => void;
  balance: number;
  campaignActive: boolean;
  earnedToday: number;
  remainingToday: number;
  onRedeemed: () => void;
}) {
  return (
    <View>
      <View style={styles.topBar}>
        <View>
          <Text style={styles.title}>Flash Cashback</Text>
          <Text style={styles.subtitle}>{props.userId}</Text>
        </View>
        <Pressable onPress={props.onSwitchUser}>
          <Text style={styles.switchUser}>Switch user</Text>
        </Pressable>
      </View>

      <View style={[styles.badge, props.campaignActive ? styles.badgeActive : styles.badgeEnded]}>
        <Text style={styles.badgeText}>Campaign {props.campaignActive ? "Active" : "Ended"}</Text>
      </View>

      <View style={styles.card}>
        <Text style={styles.cardLabel}>Balance</Text>
        <Text style={styles.balanceValue}>{formatIDR(props.balance)}</Text>
      </View>

      <View style={styles.card}>
        <Text style={styles.cardLabel}>Today</Text>
        <Text style={styles.cardValue}>
          Earned {formatIDR(props.earnedToday)} · Remaining {formatIDR(props.remainingToday)}
        </Text>
      </View>

      <RedeemForm userId={props.userId} balance={props.balance} onRedeemed={props.onRedeemed} />

      <Text style={styles.sectionTitle}>History</Text>
    </View>
  );
}

function RedeemForm(props: { userId: string; balance: number; onRedeemed: () => void }) {
  const [amountText, setAmountText] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [feedback, setFeedback] = useState<{ kind: "ok" | "error"; message: string } | null>(null);

  const onSubmit = async () => {
    setFeedback(null);

    const amount = Number(amountText);
    if (!amountText || !Number.isFinite(amount) || amount <= 0) {
      setFeedback({ kind: "error", message: "Enter a valid amount." });
      return;
    }

    setSubmitting(true);
    try {
      const result = await redeem(props.userId, Math.trunc(amount), generateIdempotencyKey());
      setFeedback({ kind: "ok", message: `Redeemed ${formatIDR(amount)}. New balance: ${formatIDR(result.new_balance)}.` });
      setAmountText("");
      props.onRedeemed();
    } catch (err) {
      setFeedback({ kind: "error", message: err instanceof ApiError ? err.message : "Redemption failed." });
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <View style={styles.card}>
      <Text style={styles.cardLabel}>Redeem</Text>
      <View style={styles.redeemRow}>
        <TextInput
          style={styles.input}
          placeholder="Amount (IDR)"
          keyboardType="numeric"
          value={amountText}
          onChangeText={setAmountText}
          editable={!submitting}
        />
        <Pressable style={styles.button} onPress={onSubmit} disabled={submitting}>
          <Text style={styles.buttonText}>{submitting ? "..." : "Redeem"}</Text>
        </Pressable>
      </View>
      {feedback && (
        <Text style={feedback.kind === "ok" ? styles.ok : styles.errorText}>{feedback.message}</Text>
      )}
    </View>
  );
}

function HistoryRow({ entry }: { entry: HistoryEntry }) {
  const isRedeem = entry.entry_type === "REDEEM";
  const label = isRedeem ? "Redeemed" : entry.amount > 0 ? "Earned" : "No cashback";
  const amountStyle = isRedeem ? styles.amountRedeem : entry.amount > 0 ? styles.amountAward : styles.amountZero;
  const reasonLabel = REASON_LABELS[entry.reason_code];

  return (
    <View style={styles.row}>
      <View style={styles.rowLeft}>
        <Text style={styles.rowLabel}>{label}</Text>
        <Text style={styles.rowDate}>{formatDate(entry.created_at)}</Text>
        {reasonLabel ? <Text style={styles.rowReason}>{reasonLabel}</Text> : null}
      </View>
      <Text style={amountStyle}>
        {isRedeem ? "-" : "+"}
        {formatIDR(Math.abs(entry.amount))}
      </Text>
    </View>
  );
}

const styles = StyleSheet.create({
  centered: {
    flex: 1,
    alignItems: "center",
    justifyContent: "center",
    padding: 24,
    gap: 12,
  },
  list: {
    flex: 1,
    backgroundColor: "#fff",
  },
  listContent: {
    padding: 16,
    paddingBottom: 32,
  },
  topBar: {
    flexDirection: "row",
    justifyContent: "space-between",
    alignItems: "center",
    marginBottom: 12,
  },
  title: {
    fontSize: 22,
    fontWeight: "700",
  },
  subtitle: {
    fontSize: 13,
    color: "#666",
  },
  switchUser: {
    fontSize: 13,
    color: "#1a1a1a",
    textDecorationLine: "underline",
  },
  badge: {
    alignSelf: "flex-start",
    paddingVertical: 4,
    paddingHorizontal: 10,
    borderRadius: 999,
    marginBottom: 12,
  },
  badgeActive: {
    backgroundColor: "#e6f4ea",
  },
  badgeEnded: {
    backgroundColor: "#f1f1f1",
  },
  badgeText: {
    fontSize: 12,
    fontWeight: "600",
    color: "#333",
  },
  card: {
    backgroundColor: "#f7f7f7",
    borderRadius: 12,
    padding: 14,
    marginBottom: 12,
  },
  cardLabel: {
    fontSize: 12,
    color: "#666",
    marginBottom: 4,
  },
  cardValue: {
    fontSize: 16,
    fontWeight: "600",
  },
  balanceValue: {
    fontSize: 28,
    fontWeight: "700",
  },
  redeemRow: {
    flexDirection: "row",
    gap: 8,
    alignItems: "center",
  },
  input: {
    flex: 1,
    borderWidth: 1,
    borderColor: "#ddd",
    borderRadius: 8,
    paddingHorizontal: 10,
    paddingVertical: 8,
    backgroundColor: "#fff",
  },
  button: {
    paddingVertical: 10,
    paddingHorizontal: 16,
    backgroundColor: "#1a1a1a",
    borderRadius: 8,
  },
  buttonText: {
    color: "#fff",
    fontWeight: "600",
  },
  ok: {
    color: "#0a7d32",
    marginTop: 8,
  },
  errorText: {
    color: "#b00020",
    textAlign: "center",
    marginTop: 8,
  },
  sectionTitle: {
    fontSize: 16,
    fontWeight: "700",
    marginBottom: 8,
    marginTop: 4,
  },
  emptyText: {
    color: "#999",
    textAlign: "center",
    marginTop: 12,
  },
  row: {
    flexDirection: "row",
    justifyContent: "space-between",
    alignItems: "center",
    paddingVertical: 10,
    borderBottomWidth: 1,
    borderBottomColor: "#eee",
  },
  rowLeft: {
    flexShrink: 1,
    paddingRight: 8,
  },
  rowLabel: {
    fontSize: 14,
    fontWeight: "600",
  },
  rowDate: {
    fontSize: 12,
    color: "#888",
  },
  rowReason: {
    fontSize: 12,
    color: "#a56b00",
  },
  amountAward: {
    fontSize: 14,
    fontWeight: "700",
    color: "#0a7d32",
  },
  amountRedeem: {
    fontSize: 14,
    fontWeight: "700",
    color: "#b00020",
  },
  amountZero: {
    fontSize: 14,
    fontWeight: "700",
    color: "#999",
  },
});
