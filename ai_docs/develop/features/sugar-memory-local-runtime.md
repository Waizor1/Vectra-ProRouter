# Sugar Memory local runtime note

Purpose: document the user-local Sugar Memory workaround that keeps the MCP memory tools responsive on this workstation class.

## What failed

- `sugar-memory_get_project_context`, `search_memory`, and related calls could time out even though the MCP server started correctly.
- The local Sugar log under `.sugar/sugar.log` showed the real failure point:
  - `Vector search failed: near "->": syntax error`
- The failure happens in the user-local pipx installation of `sugarai`, not in this git repository.

## Current local workaround

- The active Sugar runtime under `~/.local/pipx/venvs/sugarai/.../sugar/memory/store.py` is patched so that semantic/vector search is probed at runtime.
- If sqlite-vec query syntax is unsupported, Sugar now disables semantic search and falls back to FTS5 keyword search instead of repeatedly timing out.
- This keeps the following MCP tools usable:
  - `get_project_context`
  - `search_memory`
  - `store_learning`
  - `list_recent_memories`

## Important boundary

- This is a **user-local runtime fix outside git**.
- Reinstalling or upgrading the local `sugarai` pipx environment can overwrite the patched file.
- Repository docs should therefore describe the workaround, but must not imply that the repo itself contains the runtime fix.

## Reapply or verify after a workstation rebuild

Use the tracked helper:

```bash
python3 ./scripts/ensure-sugar-memory-local-fallback.py --check
python3 ./scripts/ensure-sugar-memory-local-fallback.py
```

What it does:

- resolves the installed `sugar` launcher
- finds the live `sugar/memory/store.py`
- checks whether the fallback patch is already present
- reapplies it if needed and writes a `.bak` backup next to the original file

## Preferred durable end state

- Upstream Sugar should treat this as a package/runtime capability issue and automatically downgrade to FTS5 when vector search operators are unsupported.
- Until that lands, keep the local patch reproducible with the helper above and record any future reinstall/repatch in the daily session note.
