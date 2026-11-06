import { useState } from 'react'

/**
 * What a dialog is about and whether it shows. show() opens it under a new key, so the dialog mounts fresh;
 * hide() keeps the item, so the dialog still has it while it animates out.
 */
export function useDialog<T>() {
  const [state, setState] = useState<{ item?: T; open: boolean; key: number }>({ open: false, key: 0 })
  return {
    ...state,
    show: (item: T) => setState((s) => ({ item, open: true, key: s.key + 1 })),
    hide: () => setState((s) => ({ ...s, open: false })),
  }
}
