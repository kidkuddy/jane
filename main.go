// jane — a pastel chat between you and Claude Code. One binary: CLI + local server.
package main

import (
	"bytes"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

//go:embed index.html
var indexHTML []byte

const (
	maxQuestions = 20
	maxText      = 300
	maxOptions   = 6
	maxOption    = 80
	maxAnswer    = 4000
	maxTitle     = 80
	maxNote      = 600
	waitFor      = 240 * time.Second // the CLI re-polls; keeps idle connections from living forever
)

var (
	port = envOr("JANE_PORT", "7337")
	home = envOr("JANE_HOME", filepath.Join(os.Getenv("HOME"), ".jane"))
	dir  = filepath.Join(home, "threads")
	base = "http://localhost:" + port
	idRe = regexp.MustCompile(`^[a-f0-9]{8}$`)
)

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

// ---------- model ----------

type Answer struct {
	Value any   `json:"value"` // string, or []string for multi
	At    int64 `json:"at"`
}

type Question struct {
	ID        string   `json:"id"`
	Text      string   `json:"text"`
	Options   []string `json:"options"`
	Multi     bool     `json:"multi"`
	Other     bool     `json:"other"`
	Default   any      `json:"default"`
	Answer    *Answer  `json:"answer"`
	Delivered bool     `json:"delivered"`
}

type Thread struct {
	ID        string      `json:"id"`
	Title     string      `json:"title"`
	Status    string      `json:"status"` // open | closed
	ClosedBy  string      `json:"closedBy,omitempty"`
	Note      string      `json:"note,omitempty"`
	CreatedAt int64       `json:"createdAt"`
	UpdatedAt int64       `json:"updatedAt"`
	Questions []*Question `json:"questions"`
}

type httpErr struct {
	code int
	msg  string
}

func (e httpErr) Error() string               { return e.msg }
func fail(code int, f string, a ...any) error { return httpErr{code, fmt.Sprintf(f, a...)} }

func now() int64 { return time.Now().UnixMilli() }

func toStrings(v any) []string {
	var out []string
	switch x := v.(type) {
	case string:
		out = []string{x}
	case []any:
		for _, e := range x {
			out = append(out, fmt.Sprint(e))
		}
	case []string:
		out = x
	case nil:
	default:
		out = []string{fmt.Sprint(x)}
	}
	clean := []string{} // never nil: encodes as [] not null
	for _, s := range out {
		if s = strings.TrimSpace(s); s != "" {
			clean = append(clean, s)
		}
	}
	return clean
}

// A question arrives as "plain text" or {"text","options","default","multi","other"}.
func addQuestions(t *Thread, raw []json.RawMessage) error {
	if t.Status != "open" {
		return fail(409, "thread is closed")
	}
	if len(raw) == 0 {
		return fail(400, "questions must be a non-empty array")
	}
	if len(t.Questions)+len(raw) > maxQuestions {
		return fail(400, "max %d questions per thread", maxQuestions)
	}
	var add []*Question
	for _, r := range raw {
		var in struct {
			Text    string `json:"text"`
			Options []any  `json:"options"`
			Default any    `json:"default"`
			Multi   bool   `json:"multi"`
			Other   *bool  `json:"other"`
		}
		if json.Unmarshal(r, &in.Text) != nil {
			if err := json.Unmarshal(r, &in); err != nil {
				return fail(400, "bad question: %s", r)
			}
		}
		text := strings.TrimSpace(in.Text)
		if text == "" {
			return fail(400, "every question needs text")
		}
		if n := len([]rune(text)); n > maxText {
			return fail(400, "question over %d chars (%d): %q", maxText, n, string([]rune(text)[:40])+"…")
		}
		opts := toStrings(in.Options)
		if len(opts) > maxOptions {
			return fail(400, "max %d options per question", maxOptions)
		}
		for _, o := range opts {
			if len([]rune(o)) > maxOption {
				return fail(400, "options max %d chars: %q", maxOption, o)
			}
		}
		if in.Multi && len(opts) == 0 {
			return fail(400, "multi needs options")
		}
		q := &Question{
			ID: fmt.Sprintf("q%d", len(t.Questions)+len(add)+1), Text: text, Options: opts, Multi: in.Multi,
			Other: in.Other == nil || *in.Other || len(opts) == 0,
		}
		if d := toStrings(in.Default); len(d) > 0 {
			if q.Multi {
				q.Default = d
			} else {
				q.Default = d[0]
			}
		}
		add = append(add, q)
	}
	t.Questions = append(t.Questions, add...)
	return nil
}

func answer(t *Thread, qid string, value any) error {
	if t.Status != "open" {
		return fail(409, "thread is closed")
	}
	var q *Question
	for _, x := range t.Questions {
		if x.ID == qid {
			q = x
		}
	}
	if q == nil {
		return fail(404, "no question %s", qid)
	}
	if q.Answer != nil {
		return fail(409, "already answered")
	}
	vals := toStrings(value)
	if len(vals) == 0 {
		vals = toStrings(q.Default)
	}
	if len(vals) == 0 {
		return fail(400, "empty answer")
	}
	if !q.Multi {
		vals = vals[:1]
	}
	if n := len([]rune(strings.Join(vals, ""))); n > maxAnswer {
		return fail(400, "answer over %d chars", maxAnswer)
	}
	if !q.Other {
		for _, v := range vals {
			if !contains(q.Options, v) {
				return fail(400, "pick one of the options")
			}
		}
	}
	var v any = vals[0]
	if q.Multi {
		v = vals
	}
	q.Answer = &Answer{v, now()}
	return nil
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

type delivery struct {
	ID       string           `json:"id"`
	Status   string           `json:"status"`
	ClosedBy string           `json:"closedBy,omitempty"`
	Note     string           `json:"note,omitempty"`
	Pending  int              `json:"pending"`
	Answers  []map[string]any `json:"answers"`
}

// Answers Claude hasn't seen yet. Ready when: closed, nothing pending, or (not all) anything new.
// Marks them delivered; caller saves.
func takeNew(t *Thread, all bool) *delivery {
	pending, fresh := 0, []*Question{}
	for _, q := range t.Questions {
		if q.Answer == nil {
			pending++
		} else if !q.Delivered {
			fresh = append(fresh, q)
		}
	}
	if !(t.Status != "open" || pending == 0 || (!all && len(fresh) > 0)) {
		return nil
	}
	d := &delivery{ID: t.ID, Status: t.Status, ClosedBy: t.ClosedBy, Note: t.Note, Pending: pending, Answers: []map[string]any{}}
	for _, q := range fresh {
		q.Delivered = true
		d.Answers = append(d.Answers, map[string]any{"id": q.ID, "question": q.Text, "answer": q.Answer.Value})
	}
	return d
}

type summary struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Status    string `json:"status"`
	URL       string `json:"url"`
	UpdatedAt int64  `json:"updatedAt"`
	Asked     int    `json:"asked"`
	Pending   int    `json:"pending"`
}

func summarize(t *Thread) summary {
	p := 0
	for _, q := range t.Questions {
		if q.Answer == nil {
			p++
		}
	}
	return summary{t.ID, t.Title, t.Status, base + "/t/" + t.ID, t.UpdatedAt, len(t.Questions), p}
}

// ---------- store ----------

var mu sync.Mutex // ponytail: one lock for all threads, it's a single-user local tool

func file(id string) string { return filepath.Join(dir, id+".json") }

func load(id string) (*Thread, error) {
	if !idRe.MatchString(id) {
		return nil, fail(404, "no thread %s", id)
	}
	b, err := os.ReadFile(file(id))
	if err != nil {
		return nil, fail(404, "no thread %s", id)
	}
	var t Thread
	return &t, json.Unmarshal(b, &t)
}

func save(t *Thread) error {
	t.UpdatedAt = now()
	b, _ := json.MarshalIndent(t, "", "  ")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(file(t.ID)+".tmp", b, 0o600); err != nil {
		return err
	}
	if err := os.Rename(file(t.ID)+".tmp", file(t.ID)); err != nil {
		return err
	}
	notify(t.ID)
	return nil
}

// load → fn → save under the lock.
func mutate(id string, fn func(*Thread) error) (*Thread, error) {
	mu.Lock()
	defer mu.Unlock()
	t, err := load(id)
	if err != nil {
		return nil, err
	}
	if err := fn(t); err != nil {
		return nil, err
	}
	return t, save(t)
}

var (
	subMu sync.Mutex
	subs  = map[string]map[chan struct{}]bool{}
)

func subscribe(id string) (chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	subMu.Lock()
	if subs[id] == nil {
		subs[id] = map[chan struct{}]bool{}
	}
	subs[id][ch] = true
	subMu.Unlock()
	return ch, func() { subMu.Lock(); delete(subs[id], ch); subMu.Unlock() }
}

func notify(id string) {
	subMu.Lock()
	defer subMu.Unlock()
	for ch := range subs[id] {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// ---------- server ----------

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, err error) {
	var he httpErr
	if errors.As(err, &he) {
		writeJSON(w, he.code, map[string]string{"error": he.msg})
		return
	}
	writeJSON(w, 500, map[string]string{"error": err.Error()})
}

func readBody(r *http.Request, v any) error {
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(v); err != nil {
		return fail(400, "bad json: %v", err)
	}
	return nil
}

func newID() string {
	b := make([]byte, 4)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func routes() http.Handler {
	m := http.NewServeMux()
	page := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/html; charset=utf-8")
		w.Write(indexHTML)
	}
	m.HandleFunc("GET /{$}", page)
	m.HandleFunc("GET /t/{id}", page)
	m.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"ok": true, "pid": os.Getpid()})
	})
	m.HandleFunc("GET /api/threads", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
		out := []summary{}
		for _, f := range files {
			if t, err := load(strings.TrimSuffix(filepath.Base(f), ".json")); err == nil {
				out = append(out, summarize(t))
			}
		}
		sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt > out[j].UpdatedAt })
		writeJSON(w, 200, out)
	})
	m.HandleFunc("POST /api/threads", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Title     string            `json:"title"`
			Questions []json.RawMessage `json:"questions"`
		}
		if err := readBody(r, &b); err != nil {
			writeErr(w, err)
			return
		}
		title := strings.TrimSpace(b.Title)
		if title == "" {
			title = "a few questions"
		}
		if len([]rune(title)) > maxTitle {
			title = string([]rune(title)[:maxTitle])
		}
		t := &Thread{ID: newID(), Title: title, Status: "open", CreatedAt: now(), Questions: []*Question{}}
		mu.Lock()
		defer mu.Unlock()
		if err := addQuestions(t, b.Questions); err != nil {
			writeErr(w, err)
			return
		}
		if err := save(t); err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 201, summarize(t))
	})
	m.HandleFunc("GET /api/threads/{id}", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		t, err := load(r.PathValue("id"))
		mu.Unlock()
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, t)
	})
	m.HandleFunc("GET /api/threads/{id}/events", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		ch, unsub := subscribe(id)
		defer unsub()
		w.Header().Set("content-type", "text/event-stream")
		w.Header().Set("cache-control", "no-cache")
		flush := w.(http.Flusher).Flush
		ping := time.NewTicker(25 * time.Second)
		defer ping.Stop()
		for {
			mu.Lock()
			t, err := load(id)
			mu.Unlock()
			if err != nil {
				return
			}
			b, _ := json.Marshal(t)
			fmt.Fprintf(w, "data: %s\n\n", b)
			flush()
		idle:
			select {
			case <-ch:
			case <-ping.C:
				fmt.Fprint(w, ": ping\n\n")
				flush()
				goto idle
			case <-r.Context().Done():
				return
			}
		}
	})
	m.HandleFunc("GET /api/threads/{id}/wait", func(w http.ResponseWriter, r *http.Request) {
		id, all := r.PathValue("id"), r.URL.Query().Has("all")
		ch, unsub := subscribe(id) // before the first check, so nothing slips between check and wait
		defer unsub()
		deadline := time.After(waitFor)
		for {
			var d *delivery
			_, err := mutate(id, func(t *Thread) error {
				if d = takeNew(t, all); d == nil {
					return errNothing
				}
				return nil
			})
			if err == nil {
				writeJSON(w, 200, d)
				return
			}
			if err != errNothing {
				writeErr(w, err)
				return
			}
			select {
			case <-ch:
			case <-deadline:
				writeJSON(w, 200, map[string]string{"id": id, "status": "waiting"})
				return
			case <-r.Context().Done():
				return
			}
		}
	})
	act := func(fn func(t *Thread, r *http.Request) error) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			t, err := mutate(r.PathValue("id"), func(t *Thread) error { return fn(t, r) })
			if err != nil {
				writeErr(w, err)
				return
			}
			writeJSON(w, 200, summarize(t))
		}
	}
	m.HandleFunc("POST /api/threads/{id}/questions", act(func(t *Thread, r *http.Request) error {
		var b struct {
			Questions []json.RawMessage `json:"questions"`
		}
		if err := readBody(r, &b); err != nil {
			return err
		}
		return addQuestions(t, b.Questions)
	}))
	m.HandleFunc("POST /api/threads/{id}/answer", act(func(t *Thread, r *http.Request) error {
		var b struct {
			QID   string `json:"qid"`
			Value any    `json:"value"`
		}
		if err := readBody(r, &b); err != nil {
			return err
		}
		return answer(t, b.QID, b.Value)
	}))
	m.HandleFunc("POST /api/threads/{id}/close", act(func(t *Thread, r *http.Request) error {
		var b struct {
			By   string `json:"by"`
			Note string `json:"note"`
		}
		if err := readBody(r, &b); err != nil {
			return err
		}
		if t.Status != "open" {
			return nil
		}
		t.Status, t.ClosedBy = "closed", "claude"
		if b.By == "user" {
			t.ClosedBy = "user"
		}
		if n := []rune(strings.TrimSpace(b.Note)); len(n) > maxNote {
			t.Note = string(n[:maxNote])
		} else {
			t.Note = string(n)
		}
		return nil
	}))
	return guard(m)
}

// guard keeps other websites in your browser out: the Host must be ours (blocks DNS
// rebinding) and a browser request must come from our own page (blocks CSRF, which could
// otherwise post fake answers into a Claude session). The CLI sends no Origin.
func guard(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, p, _ := net.SplitHostPort(r.Context().Value(http.LocalAddrContextKey).(net.Addr).String())
		ours := map[string]bool{"localhost:" + p: true, "127.0.0.1:" + p: true}
		o := r.Header.Get("Origin")
		if !ours[r.Host] || (o != "" && !ours[strings.TrimPrefix(o, "http://")]) {
			writeJSON(w, 403, map[string]string{"error": "forbidden"})
			return
		}
		h.ServeHTTP(w, r)
	})
}

func serve() {
	fmt.Println("jane on", base)
	if err := http.ListenAndServe("127.0.0.1:"+port, routes()); err != nil {
		fmt.Fprintln(os.Stderr, "jane:", err)
		os.Exit(1)
	}
}

var errNothing = errors.New("nothing new")

// ---------- cli ----------

func die(f string, a ...any) {
	fmt.Fprintf(os.Stderr, "jane: "+f+"\n", a...)
	os.Exit(1)
}

func call(method, path string, body any) ([]byte, error) {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, base+path, rd)
	req.Header.Set("content-type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		var e struct{ Error string }
		json.Unmarshal(b, &e)
		die("%s", e.Error)
	}
	return b, nil
}

func api(method, path string, body any) []byte {
	b, err := call(method, path, body)
	if err != nil {
		die("%v", err)
	}
	return b
}

func health() (pid int, ok bool) {
	c := http.Client{Timeout: time.Second}
	res, err := c.Get(base + "/api/health")
	if err != nil {
		return 0, false
	}
	defer res.Body.Close()
	var h struct{ Pid int }
	json.NewDecoder(res.Body).Decode(&h)
	return h.Pid, true
}

func ensureServer() {
	if _, ok := health(); ok {
		return
	}
	os.MkdirAll(home, 0o700)
	log, err := os.OpenFile(filepath.Join(home, "server.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		die("%v", err)
	}
	exe, _ := os.Executable()
	cmd := exec.Command(exe, "serve")
	cmd.Stdout, cmd.Stderr = log, log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		die("%v", err)
	}
	cmd.Process.Release()
	for range 50 {
		time.Sleep(100 * time.Millisecond)
		if _, ok := health(); ok {
			return
		}
	}
	die("server did not start, see %s/server.log", home)
}

// stdin: [...questions] or {"title","questions"}
func stdinQuestions() map[string]any {
	b, _ := io.ReadAll(os.Stdin)
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		die("stdin must be JSON: %v", err)
	}
	if arr, ok := v.([]any); ok {
		return map[string]any{"questions": arr}
	}
	if m, ok := v.(map[string]any); ok {
		return m
	}
	die("stdin must be a JSON array or object")
	return nil
}

func out(b []byte) {
	var buf bytes.Buffer
	if json.Indent(&buf, b, "", "  ") != nil {
		buf.Write(b)
	}
	fmt.Println(strings.TrimSpace(buf.String()))
}

const help = `jane — ask the human questions in a pastel chat UI

  jane new                 stdin: {"title": "...", "questions": [...]}  → {id, url}
  jane ask <id>            stdin: {"questions": [...]} or [...]          → add to an open thread
  jane wait <id> [--all]   block until new answers (--all: until none pending) or thread closed
  jane close <id> [note]   close the thread with an optional closing note
  jane list [--all]        open threads (--all: include closed)
  jane show <id>           full thread JSON
  jane serve | stop        run server in foreground | stop background server

question: "plain text" or {"text", "options": [..], "default", "multi": bool, "other": bool}
limits: %d questions/thread, %d chars/question, %d options of %d chars, %d chars/answer
env: JANE_PORT (%s), JANE_HOME (%s)
`

func main() {
	args := append(os.Args[1:], "", "")
	cmd, id := args[0], args[1]
	needID := func() string {
		if id == "" || strings.HasPrefix(id, "-") {
			die("missing thread id")
		}
		return id
	}
	switch cmd {
	case "serve":
		serve()
	case "stop":
		pid, ok := health()
		if ok {
			syscall.Kill(pid, syscall.SIGTERM)
		}
		fmt.Printf("{\"stopped\": %v}\n", ok)
	case "new":
		ensureServer()
		out(api("POST", "/api/threads", stdinQuestions()))
	case "ask":
		ensureServer()
		out(api("POST", "/api/threads/"+needID()+"/questions", stdinQuestions()))
	case "wait":
		ensureServer()
		q := ""
		if contains(os.Args[2:], "--all") {
			q = "?all"
		}
		path := "/api/threads/" + needID() + "/wait" + q
		for {
			b, err := call("GET", path, nil)
			if err != nil { // server went away: bring it back and keep waiting
				time.Sleep(time.Second)
				ensureServer()
				continue
			}
			var s struct{ Status string }
			json.Unmarshal(b, &s)
			if s.Status != "waiting" {
				out(b)
				return
			}
		}
	case "close":
		ensureServer()
		out(api("POST", "/api/threads/"+needID()+"/close", map[string]string{"by": "claude", "note": strings.Join(os.Args[3:], " ")}))
	case "list":
		ensureServer()
		b := api("GET", "/api/threads", nil)
		if id != "--all" {
			var all []summary
			json.Unmarshal(b, &all)
			open := []summary{}
			for _, t := range all {
				if t.Status == "open" {
					open = append(open, t)
				}
			}
			b, _ = json.Marshal(open)
		}
		out(b)
	case "show":
		ensureServer()
		out(api("GET", "/api/threads/"+needID(), nil))
	default:
		fmt.Printf(help, maxQuestions, maxText, maxOptions, maxOption, maxAnswer, port, home)
	}
}
