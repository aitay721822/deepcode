import { act, cleanup, renderHook, waitFor } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { useApi } from '../useApi'

afterEach(cleanup)

it('reports loading when a settled key is re-entered after being disabled', async () => {
  let resolveReload!: (data: string) => void
  const load = vi
    .fn<() => Promise<string>>()
    .mockResolvedValueOnce('cached')
    .mockReturnValueOnce(
      new Promise((resolve) => {
        resolveReload = resolve
      }),
    )
  const initialProps: { key: string | null } = { key: 'symbols' }
  const { result, rerender } = renderHook(({ key }) => useApi(key, load), { initialProps })
  await waitFor(() => expect(result.current).toMatchObject({ data: 'cached', loading: false }))

  rerender({ key: null })
  expect(result.current.data).toBeUndefined()
  expect(result.current.loading).toBe(false)

  rerender({ key: 'symbols' })
  expect(load).toHaveBeenCalledTimes(2)
  expect(result.current).toMatchObject({ data: 'cached', loading: true })

  await act(async () => resolveReload('fresh'))
  expect(result.current).toMatchObject({ data: 'fresh', loading: false })
})
