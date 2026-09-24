import { useMemo } from 'react'
import { ChannelSettingsPanel, type ChannelSettingsAdapter, type ChannelEvent } from '@wesui/connections'
import { request, onChannelEvent } from '@/bridge'

function useBridgeAdapter(): ChannelSettingsAdapter {
  return useMemo(() => ({
    list: () => request('sidebar/listChannels'),
    create: (platform, accountId, payload) =>
      request('sidebar/upsertChannel', { platform, account_id: accountId, ...payload }),
    delete: (platform, accountId) =>
      request('sidebar/deleteChannel', { platform, account_id: accountId }),
    connect: (platform, accountId) =>
      request('sidebar/connectChannel', { platform, account_id: accountId }),
    disconnect: (platform, accountId) =>
      request('sidebar/disconnectChannel', { platform, account_id: accountId }),
    subscribe: (onEvent: (ev: ChannelEvent) => void) => {
      return onChannelEvent((raw) => {
        onEvent({
          kind: raw.kind,
          platform: raw.platform,
          account_id: raw.account_id,
          data: raw.data,
        })
      })
    },
  }), [])
}

export function ChannelsSettings() {
  const adapter = useBridgeAdapter()
  return <ChannelSettingsPanel adapter={adapter} />
}
