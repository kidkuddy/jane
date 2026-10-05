---
name: jane
description: Ask the user questions through jane, a local chat UI, instead of in the terminal. Use whenever you need a Q&A round with the user to reach a goal (clarifying requirements, picking between options, gathering preferences) — prefer it over AskUserQuestion and over lists of questions in chat. Also use when the user says "jane", "ask me in jane", "jane thread", and at the start of a session that may have unfinished jane threads.
---

# jane — Q&A threads with the user

`jane` is a CLI (`~/.local/bin/jane`, source `~/Desktop/github/jane`). It starts its own
local server on first use and stores threads in `~/.jane/threads/`. The user answers one
question at a time at `http://localhost:7337/t/<id>`.

## 1. Look for unfinished threads first

```bash
jane list
```

If an open thread covers the current goal, resume it (`jane show <id>` for the full
transcript, then `jane wait <id>`) instead of opening a new one.

## 2. Open a thread

```bash
jane new <<'EOF'
{"title": "auth flow for the dashboard", "questions": [
  {"text": "Which login methods do we need?", "options": ["email + password", "Google", "magic link"], "multi": true, "default": ["email + password"]},
  {"text": "Should sessions survive a browser restart?", "options": ["yes", "no"], "default": "yes"},
  "Anything about the current login that annoys you?"
]}
EOF
```

A question is a plain string or `{"text", "options", "default", "multi", "other"}`.
- `options`: up to 6 choices, 80 chars each. Clicking one answers immediately.
- `default`: what Enter on an empty answer sends. Give one whenever a sensible default exists.
- `multi`: pick several (needs `options`). Answer comes back as an array.
- `other: false`: only the listed options are accepted. Default `true` (free text allowed).

Limits: 20 questions per thread in total, 300 chars per question. One idea per question;
lead with the question that decides the most. Don't add your own "anything else?": jane
appends one (id `x<n>`, default "nothing else") and keeps it last when you `ask` more.

Send the user the `url` from the output in one line. Do not open it yourself.

## 3. Wait for answers

Run with the Bash tool's `run_in_background: true` — you are re-invoked when it exits:

```bash
jane wait <id>          # returns as soon as there are new answers (question by question)
jane wait <id> --all    # returns once every pending question is answered
```

Output: `{"status", "pending", "answers": [{"id", "question", "answer"}], "closedBy", "note"}`.
Each answer is delivered once. While waiting, do not ask the same things in the terminal.

## 4. Reflect, then continue or close

- Answers raise new questions → `jane ask <id> <<<'["follow-up?"]'` (same format; array or
  `{"questions": [...]}`), then wait again. The user sees "claude is thinking" until it lands.
- Got what you need → `jane close <id> "one-line summary of what you'll do"`. The note is
  shown to the user as the last bubble.
- `status: "closed"` with `closedBy: "user"` → the user ended it. Stop asking and work with
  what was answered; unanswered questions use your best default and you say which ones.

## Troubleshooting

- `jane stop` then any command restarts the server (needed after rebuilding the binary).
- Server log: `~/.jane/server.log`. Port/home: `JANE_PORT`, `JANE_HOME`.
