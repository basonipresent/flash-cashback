import { apiFetch } from "./client";

// Mirrors spec/api.yaml exactly - field names as the API sends them.

export type ReasonCode =
  | "AWARDED"
  | "PARTIAL_DAILY_CAP"
  | "PARTIAL_BUDGET"
  | "BELOW_MINIMUM"
  | "DAILY_CAP_REACHED"
  | "CAMPAIGN_ENDED"
  | ""; // REDEEM entries have no reason code.

export type Balance = {
  balance: number;
};

export type DailyUsage = {
  earned_today: number;
  remaining_today: number;
};

export type CampaignStatus = {
  status: "active" | "ended";
};

export type HistoryEntry = {
  entry_type: "AWARD" | "REDEEM";
  amount: number;
  ref_type: "PAYMENT" | "REDEMPTION";
  ref_id: string;
  reason_code: ReasonCode;
  created_at: string;
};

export type RedemptionResult = {
  redemption_id: string;
  new_balance: number;
};

export function getBalance(userId: string): Promise<Balance> {
  return apiFetch<Balance>("/cashback/balance", {
    headers: { "X-User-Id": userId },
  });
}

export function getDaily(userId: string): Promise<DailyUsage> {
  return apiFetch<DailyUsage>("/cashback/daily", {
    headers: { "X-User-Id": userId },
  });
}

export function getHistory(userId: string): Promise<HistoryEntry[]> {
  return apiFetch<HistoryEntry[]>("/cashback/history", {
    headers: { "X-User-Id": userId },
  });
}

export function getCampaign(): Promise<CampaignStatus> {
  return apiFetch<CampaignStatus>("/campaign");
}

export function redeem(userId: string, amount: number, idempotencyKey: string): Promise<RedemptionResult> {
  return apiFetch<RedemptionResult>("/cashback/redemptions", {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "X-User-Id": userId,
      "Idempotency-Key": idempotencyKey,
    },
    body: JSON.stringify({ amount }),
  });
}
