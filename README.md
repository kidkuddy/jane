# jane

A pastel chat between you and Claude Code. When Claude needs to ask you things, it opens a
jane thread and hands you a link; you answer one question at a time in a big, calm UI, and
each answer goes straight back to the Claude session, which can reflect and ask follow-ups
or close the thread.

One Go binary: CLI + local server (127.0.0.1 only) + embedded page. No dependencies.

```bash
go build -o ~/.local/bin/jane .
ln -s "$PWD/skill" ~/.claude/skills/jane   # Claude Code skill
```

```
jane new                 stdin: {"title": "...", "questions": [...]}  → {id, url}, opens the page
jane ask <id>            stdin: {"questions": [...]} or [...]
jane wait <id> [--all]   block until new answers / all answered / closed
jane close <id> [note]
jane list [--all] | show <id> | serve | stop
```

Questions: `"plain text"` or `{"text", "context", "options", "default", "multi", "other"}`.
`context` is background shown above the question, for a reader who hasn't seen the terminal.
Limits: 20 questions per round (no cap per thread), 300 chars per question, 600 chars of context, 6 options.

Threads persist in `~/.jane/threads/*.json`, so a later Claude session can `jane list` and
pick an unfinished thread back up. Drafts you're typing are kept in the browser's
localStorage until you send them.

`go test ./...` runs the end-to-end check.
