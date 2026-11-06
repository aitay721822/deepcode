import type { Provider } from '../../../types/repo/repo'

export const providerName = (p: Provider) => (p === 'gitlab' ? 'GitLab' : 'GitHub')
