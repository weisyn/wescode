type ProviderType = 'openai_compat' | 'anthropic'
type ProviderStatus = 'ok' | 'error' | 'unknown' | 'testing'

export interface LLMProviderItem {
  name: string
  type: ProviderType
  baseUrl: string
  model: string
  hasApiKey: boolean
  apiKeyPreview: string
  isDefault: boolean
  noStreamUsage?: boolean
}
