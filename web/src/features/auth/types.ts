export interface User {
  id: number
  username: string
  nickname: string
  email: string
  has_avatar: boolean
  avatar_url?: string
  two_factor_enabled: boolean
}
