# Bugs

Postmortems for defects that locked a player out, corrupted state, or
otherwise needed a root-cause writeup beyond a one-line evidence note.

Each file should cover:

1. **现象** — what the operator / player saw
2. **出现原因** — the invariant that broke and why it looked intentional
3. **排查方法** — the log / code path that led to the diagnosis
4. **解决** — the fix, the regression test, and how to verify

These notes are reference material. Current capability still lives in
`context/CURRENT.md` and dated `evidence/` files.
