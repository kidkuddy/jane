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
{"title": "login options for the admin dashboard", "questions": [
  {"context": "The admin dashboard (dashboard/) has no login yet; anyone with the URL gets in. I'm adding auth before it goes public.", "text": "Which login methods should the admin dashboard support?", "options": ["email + password", "Google", "magic link"], "multi": true, "default": ["email + password"]},
  {"context": "After logging in, the dashboard keeps a session cookie. If it outlives the browser, admins stay logged in for 30 days; if not, they log in again every time they reopen the browser.", "text": "Should admins stay logged in after closing the browser?", "options": ["yes, for 30 days", "no, log in every time"], "default": "yes, for 30 days"}
]}
EOF
```

### Write for someone who hasn't seen the terminal

The user reads jane cold, often minutes or hours later, without the conversation in front
of them. Every question must stand on its own:

- Name the thing. Not "what should we do with X?" but what X is, where it lives, and what
  is wrong or undecided about it.
- Say what's at stake: what each option changes, what breaks if they pick wrong.
- Put that background in `context` (up to 600 chars, shown as a paragraph above the
  question) and keep `text` to the actual question.
- No pronouns or labels that only make sense in the terminal ("the second approach",
  "that bug", "option B", "the file I mentioned").
- Options are full phrases a newcomer understands, not shorthand.

Test before sending: could someone who opened only this URL answer it? If not, add context.

Bad:  `{"text": "Should I keep the retry in the wrapper?"}`
Good: `{"context": "Uploads to S3 sometimes fail with a timeout. I added a retry around the upload call in storage/upload.go. It retries 3 times with a 2s wait, so a dead connection now takes 6s to report an error.", "text": "Keep the automatic retry on S3 uploads?", "options": ["keep it, 6s is fine", "keep it but only 1 retry", "remove it, fail fast"]}`

### Question format

A question is a plain string or `{"text", "context", "options", "default", "multi", "other"}`.
- `options`: up to 6 choices, 80 chars each. Clicking one answers immediately.
- `default`: what Enter on an empty answer sends. Give one whenever a sensible default exists.
- `multi`: pick several (needs `options`). Answer comes back as an array.
- `other: false`: only the listed options are accepted. Default `true` (free text allowed).

Limits: 20 questions per thread in total, 300 chars per question, 600 chars of context. One idea per question;
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
