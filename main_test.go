package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// End to end: new → wait blocks → answer → wait returns → --all → close.
func TestThreadFlow(t *testing.T) {
	dir = t.TempDir()
	srv := httptest.NewServer(routes())
	defer srv.Close()

	do := func(method, path, body string) (int, map[string]any) {
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var m map[string]any
		json.NewDecoder(res.Body).Decode(&m)
		return res.StatusCode, m
	}

	code, th := do("POST", "/api/threads", `{"title":"t","questions":["name?",{"text":"color?","options":["red","blue"],"default":"blue","other":false}]}`)
	if code != 201 || th["pending"] != 2.0 {
		t.Fatalf("create: %d %v", code, th)
	}
	id := th["id"].(string)
	if _, full := do("GET", "/api/threads/"+id, ""); full["questions"].([]any)[0].(map[string]any)["options"] == nil {
		t.Fatal("options must encode as [], not null")
	}

	if code, m := do("POST", "/api/threads", `{"questions":["`+strings.Repeat("x", maxText+1)+`"]}`); code != 400 {
		t.Fatalf("over-long question accepted: %d %v", code, m)
	}

	got := make(chan map[string]any)
	go func() { _, m := do("GET", "/api/threads/"+id+"/wait", ""); got <- m }()
	select {
	case m := <-got:
		t.Fatalf("wait returned before any answer: %v", m)
	case <-time.After(200 * time.Millisecond):
	}

	if code, m := do("POST", "/api/threads/"+id+"/answer", `{"qid":"q1","value":"jane"}`); code != 200 {
		t.Fatalf("answer: %d %v", code, m)
	}
	m := <-got
	if a := m["answers"].([]any); len(a) != 1 || a[0].(map[string]any)["answer"] != "jane" || m["pending"] != 1.0 {
		t.Fatalf("wait result: %v", m)
	}

	if code, _ := do("POST", "/api/threads/"+id+"/answer", `{"qid":"q2","value":"green"}`); code != 400 {
		t.Fatalf("off-list answer accepted with other=false: %d", code)
	}
	do("POST", "/api/threads/"+id+"/answer", `{"qid":"q2","value":""}`) // empty → default
	if _, m := do("GET", "/api/threads/"+id+"/wait?all", ""); m["answers"].([]any)[0].(map[string]any)["answer"] != "blue" {
		t.Fatalf("default not applied / not delivered: %v", m)
	}

	do("POST", "/api/threads/"+id+"/close", `{"note":"bye"}`)
	if _, m := do("GET", "/api/threads/"+id+"/wait", ""); m["status"] != "closed" || len(m["answers"].([]any)) != 0 {
		t.Fatalf("closed wait: %v", m)
	}
	if code, _ := do("POST", "/api/threads/"+id+"/questions", `{"questions":["more?"]}`); code != 409 {
		t.Fatalf("asked on closed thread: %d", code)
	}
	evil, _ := http.NewRequest("POST", srv.URL+"/api/threads", strings.NewReader(`{"questions":["x"]}`))
	evil.Header.Set("Origin", "https://evil.example")
	if res, _ := http.DefaultClient.Do(evil); res.StatusCode != 403 {
		t.Fatalf("cross-origin POST allowed: %d", res.StatusCode)
	}
	rebind, _ := http.NewRequest("GET", srv.URL+"/api/threads", nil)
	rebind.Host = "evil.example"
	if res, _ := http.DefaultClient.Do(rebind); res.StatusCode != 403 {
		t.Fatalf("foreign Host allowed: %d", res.StatusCode)
	}
	if code, _ := do("GET", "/api/threads/..%2f..%2fetc/wait", ""); code != 404 {
		t.Fatalf("bad id: %d", code)
	}
}
