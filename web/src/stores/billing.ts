import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import type { WesBillingState } from '@wesui/billing'
import { request } from '@/bridge'

const DEFAULT_WES_BILLING: WesBillingState = { enabled: false }

/** Snake_case shape returned by RPC `sidebar/getWesBilling`. */
interface RawBillingResponse {
  enabled?: boolean
  active?: boolean
  reason?: string
  balance_mills?: number
  balanceMills?: number
  unpaid_mills?: number
  unpaid_count?: number
  debts_url?: string
}

function mapBilling(raw: RawBillingResponse): WesBillingState {
  return {
    enabled: raw.enabled ?? false,
    active: raw.active,
    reason: raw.reason,
    balanceMills: raw.balance_mills ?? raw.balanceMills,
    unpaidMills: raw.unpaid_mills,
    unpaidCount: raw.unpaid_count,
    debtsUrl: raw.debts_url,
  }
}

interface BillingState {
  wesBilling: WesBillingState
  wesBillingLoaded: boolean
  billingDebtBannerDismissKey: string | null
  loadWesBilling: () => Promise<void>
  dismissBillingDebtBanner: (key: string) => void
}

export const useBillingStore = create<BillingState>()(
  persist(
    (set) => ({
      wesBilling: DEFAULT_WES_BILLING,
      wesBillingLoaded: false,
      billingDebtBannerDismissKey: null,

      loadWesBilling: async () => {
        try {
          // Method name matches the webview RPC dispatch in chatViewPane.ts
          // (no `sidebar/` prefix — that's the Go-backend internal route,
          // exposed to webview through chatViewPane._handleWebviewMessage).
          const data = await request<RawBillingResponse>('getWesBilling')
          set({ wesBilling: mapBilling(data || {}), wesBillingLoaded: true })
        } catch {
          set({ wesBillingLoaded: true })
        }
      },

      dismissBillingDebtBanner: (key) => set({ billingDebtBannerDismissKey: key }),
    }),
    {
      name: 'wescode-billing',
      // Only persist dismiss fingerprint; reload billing fresh on each start
      // so the user never sees stale balance/debts.
      partialize: (s) => ({ billingDebtBannerDismissKey: s.billingDebtBannerDismissKey }),
    },
  ),
)
