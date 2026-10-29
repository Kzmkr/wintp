package link

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeClip struct {
	mu sync.Mutex
	t  string
}

func (f *fakeClip) Text() string     { f.mu.Lock(); defer f.mu.Unlock(); return f.t }
func (f *fakeClip) SetText(s string) { f.mu.Lock(); defer f.mu.Unlock(); f.t = s }

func newTestServer(t *testing.T) (*Server, *fakeClip, *httptest.Server) {
	t.Helper()
	clip := &fakeClip{}
	s, err := New(t.TempDir(), filepath.Join(t.TempDir(), "inbox"), 0, clip)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return s, clip, ts
}

func pair(t *testing.T, s *Server, ts *httptest.Server) string {
	t.Helper()
	uri := s.NewPairing()
	u, err := url.Parse(uri)
	if err != nil {
		t.Fatal(err)
	}
	if u.Scheme != "wintp" || u.Query().Get("fp") != s.Fingerprint() {
		t.Fatalf("bad pairing uri %q", uri)
	}
	body, _ := json.Marshal(map[string]string{"code": u.Query().Get("c"), "name": "Pixel"})
	resp, err := http.Post(ts.URL+"/v1/pair", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("pair status %d", resp.StatusCode)
	}
	var out struct{ Token string }
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if out.Token == "" {
		t.Fatal("no token")
	}
	return out.Token
}

func do(t *testing.T, method, url, token string, body []byte, hdr map[string]string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(method, url, bytes.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestPairingIsSingleUseAndAuthRequired(t *testing.T) {
	s, _, ts := newTestServer(t)

	if r := do(t, "GET", ts.URL+"/v1/ping", "", nil, nil); r.StatusCode != 401 {
		t.Fatalf("unauthenticated ping = %d", r.StatusCode)
	}
	if r := do(t, "GET", ts.URL+"/v1/ping", "bogus", nil, nil); r.StatusCode != 401 {
		t.Fatalf("bad token ping = %d", r.StatusCode)
	}

	uri := s.NewPairing()
	u, _ := url.Parse(uri)
	code := u.Query().Get("c")
	body, _ := json.Marshal(map[string]string{"code": code, "name": "A"})
	for i, want := range []int{200, 403} {
		r := do(t, "POST", ts.URL+"/v1/pair", "", body, nil)
		if r.StatusCode != want {
			t.Fatalf("pair attempt %d = %d, want %d", i, r.StatusCode, want)
		}
	}
	if len(s.Devices()) != 1 {
		t.Fatalf("devices = %d", len(s.Devices()))
	}
}

func TestPairCodeExpires(t *testing.T) {
	s, _, ts := newTestServer(t)
	u, _ := url.Parse(s.NewPairing())
	s.mu.Lock()
	s.pairExpiry = time.Now().Add(-time.Second)
	s.mu.Unlock()
	body, _ := json.Marshal(map[string]string{"code": u.Query().Get("c")})
	if r := do(t, "POST", ts.URL+"/v1/pair", "", body, nil); r.StatusCode != 403 {
		t.Fatalf("expired code = %d", r.StatusCode)
	}
}

func TestClipboardPhoneToPC(t *testing.T) {
	s, clip, ts := newTestServer(t)
	tok := pair(t, s, ts)

	r := do(t, "PUT", ts.URL+"/v1/clipboard", tok, []byte(`{"text":"hello"}`), nil)
	if r.StatusCode != 200 {
		t.Fatalf("put = %d", r.StatusCode)
	}
	if clip.Text() != "hello" {
		t.Fatalf("pc clipboard = %q", clip.Text())
	}
}

func TestClipboardLongPollPCToPhone(t *testing.T) {
	s, clip, ts := newTestServer(t)
	tok := pair(t, s, ts)

	r := do(t, "GET", ts.URL+"/v1/clipboard", tok, nil, nil)
	var first clipMsg
	_ = json.NewDecoder(r.Body).Decode(&first)

	go func() {
		time.Sleep(100 * time.Millisecond)
		clip.SetText("from pc")
		s.mu.Lock()
		s.bumpLocked(clip.Text()) // what the watcher does on its next tick
		s.mu.Unlock()
	}()

	start := time.Now()
	r = do(t, "GET", ts.URL+"/v1/clipboard?wait=10&since="+itoa(first.Seq), tok, nil, nil)
	var got clipMsg
	_ = json.NewDecoder(r.Body).Decode(&got)
	if got.Text != "from pc" || got.Seq <= first.Seq {
		t.Fatalf("got %+v after %v", got, time.Since(start))
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("long poll did not wake promptly")
	}
}

func TestWatcherPicksUpPCClipboard(t *testing.T) {
	s, clip, _ := newTestServer(t)
	if err := s.Start(); err != nil {
		t.Skipf("cannot listen: %v", err)
	}
	defer s.Stop()
	seq0, _, _ := s.snapshot()
	clip.SetText("copied on pc")
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if seq, text, _ := s.snapshot(); seq > seq0 && text == "copied on pc" {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("watcher never noticed clipboard change")
}

func TestFileUpload(t *testing.T) {
	s, _, ts := newTestServer(t)
	tok := pair(t, s, ts)

	up := func(name string, data string) string {
		r := do(t, "POST", ts.URL+"/v1/files", tok, []byte(data),
			map[string]string{"X-Filename": url.PathEscape(name)})
		if r.StatusCode != 200 {
			t.Fatalf("upload %q = %d", name, r.StatusCode)
		}
		var out struct{ Name string }
		_ = json.NewDecoder(r.Body).Decode(&out)
		return out.Name
	}

	if got := up("photo.jpg", "AAA"); got != "photo.jpg" {
		t.Fatalf("name = %q", got)
	}
	if got := up("photo.jpg", "BBB"); got != "photo (1).jpg" {
		t.Fatalf("collision name = %q", got)
	}
	data, err := os.ReadFile(filepath.Join(s.Inbox(), "photo (1).jpg"))
	if err != nil || string(data) != "BBB" {
		t.Fatalf("content = %q err=%v", data, err)
	}
}

func TestFileUploadCannotEscapeInbox(t *testing.T) {
	s, _, ts := newTestServer(t)
	tok := pair(t, s, ts)
	for _, evil := range []string{`..\..\evil.txt`, `../../evil.txt`, `C:\Windows\evil.txt`, `..`, `con.txt`} {
		r := do(t, "POST", ts.URL+"/v1/files", tok, []byte("x"),
			map[string]string{"X-Filename": url.PathEscape(evil)})
		if r.StatusCode != 200 {
			continue // rejecting is fine too
		}
		var out struct{ Name string }
		_ = json.NewDecoder(r.Body).Decode(&out)
		if strings.ContainsAny(out.Name, `/\:`) {
			t.Fatalf("%q produced unsafe name %q", evil, out.Name)
		}
		if _, err := os.Stat(filepath.Join(s.Inbox(), out.Name)); err != nil {
			t.Fatalf("%q: file not inside inbox: %v", evil, err)
		}
	}
	if _, err := os.Stat(filepath.Join(s.Inbox(), "..", "evil.txt")); err == nil {
		t.Fatal("file escaped inbox")
	}
}

func TestOpenURL(t *testing.T) {
	s, _, ts := newTestServer(t)
	tok := pair(t, s, ts)
	var opened []string
	s.OpenURL = func(u string) error { opened = append(opened, u); return nil }

	post := func(body string) int {
		return do(t, "POST", ts.URL+"/v1/open", tok, []byte(body), nil).StatusCode
	}
	if c := post(`{"url":"https://example.com/a?b=1"}`); c != 200 {
		t.Fatalf("https = %d", c)
	}
	for _, bad := range []string{
		`{"url":"file:///C:/Windows/System32/calc.exe"}`,
		`{"url":"ms-settings:"}`,
		`{"url":"javascript:alert(1)"}`,
		`{"url":"calc.exe"}`,
		`{"url":"https://"}`,
		`{"url":""}`,
		`not json`,
	} {
		if c := post(bad); c != 400 {
			t.Fatalf("%s = %d, want 400", bad, c)
		}
	}
	if len(opened) != 1 || opened[0] != "https://example.com/a?b=1" {
		t.Fatalf("opened = %v", opened)
	}
	// Unauthenticated callers must never reach the opener.
	if r := do(t, "POST", ts.URL+"/v1/open", "", []byte(`{"url":"https://x.com"}`), nil); r.StatusCode != 401 {
		t.Fatalf("unauth = %d", r.StatusCode)
	}
	s.OpenURL = nil
	if c := post(`{"url":"https://example.com"}`); c != 501 {
		t.Fatalf("disabled = %d", c)
	}
}

func TestMedia(t *testing.T) {
	s, _, ts := newTestServer(t)
	tok := pair(t, s, ts)
	var got []string
	s.Media = func(a string) error { got = append(got, a); return nil }
	post := func(body string) int {
		return do(t, "POST", ts.URL+"/v1/media", tok, []byte(body), nil).StatusCode
	}
	for a := range MediaActions {
		if c := post(`{"action":"` + a + `"}`); c != 200 {
			t.Fatalf("%s = %d", a, c)
		}
	}
	if len(got) != len(MediaActions) {
		t.Fatalf("performed %d of %d", len(got), len(MediaActions))
	}
	for _, bad := range []string{`{"action":"shutdown"}`, `{"action":""}`, `{}`, `x`} {
		if c := post(bad); c != 400 {
			t.Fatalf("%s = %d, want 400", bad, c)
		}
	}
	if r := do(t, "POST", ts.URL+"/v1/media", "", []byte(`{"action":"mute"}`), nil); r.StatusCode != 401 {
		t.Fatalf("unauth = %d", r.StatusCode)
	}
}

func TestBluetooth(t *testing.T) {
	s, _, ts := newTestServer(t)
	tok := pair(t, s, ts)
	state := "on"
	s.Bluetooth = func(set *bool) (string, error) {
		if set != nil {
			state = map[bool]string{true: "on", false: "off"}[*set]
		}
		return state, nil
	}
	read := func(r *http.Response) string {
		var out struct{ State string }
		_ = json.NewDecoder(r.Body).Decode(&out)
		return out.State
	}
	if got := read(do(t, "GET", ts.URL+"/v1/bluetooth", tok, nil, nil)); got != "on" {
		t.Fatalf("get = %q", got)
	}
	if got := read(do(t, "PUT", ts.URL+"/v1/bluetooth", tok, []byte(`{"on":false}`), nil)); got != "off" {
		t.Fatalf("put off = %q", got)
	}
	if r := do(t, "PUT", ts.URL+"/v1/bluetooth", tok, []byte(`{}`), nil); r.StatusCode != 400 {
		t.Fatalf("missing field = %d, want 400 (must not default to switching)", r.StatusCode)
	}
	if state != "off" {
		t.Fatalf("bad request changed state to %q", state)
	}
	if r := do(t, "PUT", ts.URL+"/v1/bluetooth", "", []byte(`{"on":true}`), nil); r.StatusCode != 401 {
		t.Fatalf("unauth = %d", r.StatusCode)
	}
}

func TestMissingFilenameRejected(t *testing.T) {
	s, _, ts := newTestServer(t)
	tok := pair(t, s, ts)
	if r := do(t, "POST", ts.URL+"/v1/files", tok, []byte("x"), nil); r.StatusCode != 400 {
		t.Fatalf("status = %d", r.StatusCode)
	}
}

func TestCertIsStableAcrossRestarts(t *testing.T) {
	dir := t.TempDir()
	_, fp1, err := loadOrCreateCert(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, fp2, err := loadOrCreateCert(dir)
	if err != nil {
		t.Fatal(err)
	}
	if fp1 != fp2 {
		t.Fatal("fingerprint changed; paired phones would break")
	}
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }
