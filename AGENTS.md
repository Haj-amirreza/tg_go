# Working on tg_go

- This standalone repository contains the Go/PostgreSQL bot and its Vue admin panel. Keep PHP application code in the original tg_bot repository.
- Read STATE.md and relevant code before editing. Batch independent reads, use focused changes and meaningful checks.
- Never commit tokens, passwords, host configuration, database dumps or private uploads.
- Use only disposable databases ending in _test for tests. Preserve existing releases, volumes and APP_KEY during upgrades.
- Keep Persian UI clear, RTL and responsive. Preserve Telegram menu behavior, permissions, pagination and queued-delivery safeguards.
- Save completed checkpoints and a concise handoff. Published source is not a deployment; report actual checks and remaining blockers.
