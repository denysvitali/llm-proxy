// Account-backed providers sign in through the proxy's web login flow; this
// table is the single source for label + login path in card, drawer, and the
// Setup page's account-connections list. grok mounts at /login, the others
// at /login/<name>.
export const ACCOUNT_AUTH: Record<string, { label: string; login: string; account: string }> = {
  grok: { label: 'xAI', login: '/login', account: 'xAI account' },
  workbuddy: { label: 'WorkBuddy', login: '/login/workbuddy', account: 'WorkBuddy account' },
  codex: { label: 'ChatGPT', login: '/login/codex', account: 'ChatGPT account' },
  zcode: { label: 'ZCode', login: '/login/zcode', account: 'ZCode account' },
  'minimax-code': { label: 'MiniMax', login: '/login/minimax-code', account: 'MiniMax account' },
}
