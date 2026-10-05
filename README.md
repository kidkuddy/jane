# jane

A Q&A chat between you and Claude Code. When Claude needs to ask you things, it opens a
jane thread in your browser; you answer one question at a time in a big, calm UI, and each
answer goes straight back to the Claude session, which can reflect and ask follow-ups in the
same thread or close it.

One Go binary: CLI + local server (127.0.0.1 only) + embedded page and fonts. No dependencies.

```bash
go build -o ~/.local/bin/jane .
ln -s "$PWD/skill" ~/.claude/skills/jane   # Claude Code skill
```

```
jane new                 stdin: {"title": "...", "questions": [...]}  → {id, url}, opens the page
jane ask <id>            stdin: {"questions": [...]} or [...]          → next round, same thread
jane wait <id> [--all]   block until new answers / all answered / closed
jane close <id> [note]   the note is the last thing the user sees
jane list [--all] | show <id> | serve | stop
```

Questions: `"plain text"` or `{"text", "context", "options", "default", "multi", "other"}`.

- `context`: background shown above the question, for a reader who hasn't seen the terminal.
- `options`: click or press 1–6 (⌥+number while typing); `default` is what Enter sends.
- Every thread ends with an optional "Anything else?" that stays last across rounds.
- Limits: 20 questions per round (no cap per thread), 300 chars per question, 600 chars of
  context, 6 options.

The page: dark, lilac-only, a slowly moving blurred gradient, Instrument Serif questions that
shrink with their length and reveal word by word, a frosted header and answer box.

Threads persist in `~/.jane/threads/*.json` (private to your user), so a later Claude session
can `jane list` and pick an unfinished thread back up. Drafts you're typing are kept in the
browser's localStorage until you send them. The server rejects requests from other websites
(Host and Origin checks), so a page you visit can't post answers into a Claude session.

`go test ./...` runs the end-to-end check.
