// DOMPurify in place of the real one, which with no DOM (goja) has no addHook or sanitize. Sanitising changes a
// label's text, never whether the diagram parses, and the parse's output is thrown away.
export default {
  isSupported: true,
  addHook() {},
  removeHook() {},
  removeHooks() {},
  removeAllHooks() {},
  sanitize: (s) => s,
}
