export type PasswordManagement = 'config_file' | 'environment' | 'database'

export type PasswordCredentialStatus = {
  change_required: boolean
  management: PasswordManagement
  environment_variable?: string
}

export type PasswordChangeResponse = {
  status: 'ok'
  message: string
  token: string
  expires_at: string
  credential: PasswordCredentialStatus
}
