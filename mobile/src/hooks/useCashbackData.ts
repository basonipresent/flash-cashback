import { useCallback, useEffect, useState } from "react";
import { ApiError } from "../api/client";
import { getBalance, getCampaign, getDaily, getHistory, CampaignStatus, DailyUsage, HistoryEntry } from "../api/cashback";

export type CashbackData = {
  balance: number;
  daily: DailyUsage;
  campaign: CampaignStatus;
  history: HistoryEntry[];
};

type State = { kind: "loading" } | { kind: "ok"; data: CashbackData } | { kind: "error"; message: string };

// Loads balance + daily usage + campaign status + history together, and
// exposes separate "initial load" vs "pull-to-refresh" states so a refresh
// doesn't blank the screen while it's in flight.
export function useCashbackData(userId: string) {
  const [state, setState] = useState<State>({ kind: "loading" });
  const [refreshing, setRefreshing] = useState(false);

  const load = useCallback(
    async (isRefresh: boolean) => {
      if (isRefresh) {
        setRefreshing(true);
      } else {
        setState({ kind: "loading" });
      }

      try {
        const [balance, daily, campaign, history] = await Promise.all([
          getBalance(userId),
          getDaily(userId),
          getCampaign(),
          getHistory(userId),
        ]);
        setState({ kind: "ok", data: { balance: balance.balance, daily, campaign, history } });
      } catch (err) {
        setState({ kind: "error", message: err instanceof ApiError ? err.message : "Unknown error" });
      } finally {
        if (isRefresh) {
          setRefreshing(false);
        }
      }
    },
    [userId]
  );

  useEffect(() => {
    load(false);
  }, [load]);

  return { state, refreshing, refresh: () => load(true), reload: () => load(false) };
}
