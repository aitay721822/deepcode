import { createFileRoute } from '@tanstack/react-router'
import { TokensPage } from '../../session/tokens/TokensPage'

/** /tokens: how agents connect over MCP, and the user's access tokens and authorized apps. */
export const Route = createFileRoute('/(session)/tokens')({ component: TokensPage })
