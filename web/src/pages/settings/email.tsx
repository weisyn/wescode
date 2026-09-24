import { useMemo } from 'react'
import { EmailSettingsPanel, type EmailSettingsAdapter } from '@wesui/connections'
import { request } from '@/bridge'

function useBridgeAdapter(): EmailSettingsAdapter {
  return useMemo(() => ({
    list: () => request('sidebar/listEmailAccounts'),
    get: (id) => request('sidebar/getEmailAccount', { account_id: id }),
    save: (id, data) => request('sidebar/saveEmailAccount', { ...data, account_id: id }),
    delete: (id) => request('sidebar/deleteEmailAccount', { account_id: id }),
    test: (id) => request('sidebar/testEmailAccount', { account_id: id }),
  }), [])
}

export function EmailSettings() {
  const adapter = useBridgeAdapter()
  return <EmailSettingsPanel adapter={adapter} />
}
