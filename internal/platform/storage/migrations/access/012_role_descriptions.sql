-- The built-in roles' descriptions in Traditional Chinese, as the rest of the
-- interface; one an administrator has since rewritten stays as it is.
UPDATE roles SET description = '所有 Repository 的所有權限'
  WHERE id = 1 AND builtin AND description = 'Every permission on every repository';
UPDATE roles SET description = '閱讀授予此角色的 Repository 並提問'
  WHERE id = 2 AND builtin AND description = 'Read and ask about the repositories granted to this role';