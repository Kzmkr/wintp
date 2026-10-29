package link

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestPCFiles(t *testing.T) {
	s, _, ts := newTestServer(t)
	tok := pair(t, s, ts)

	share := t.TempDir()
	secret := filepath.Join(filepath.Dir(share), "secret.txt")
	_ = os.WriteFile(secret, []byte("nope"), 0644)
	_ = os.MkdirAll(filepath.Join(share, "sub dir"), 0755)
	_ = os.WriteFile(filepath.Join(share, "a.txt"), []byte("0123456789"), 0644)
	_ = os.WriteFile(filepath.Join(share, "sub dir", "b.txt"), []byte("bee"), 0644)
	s.Shares = func() []Share { return []Share{{Name: "Docs", Path: share}} }

	get := func(p string, hdr map[string]string) (*http.Response, string) {
		req, _ := http.NewRequest("GET", ts.URL+p, nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp, string(b)
	}
	list := func(p string) (int, []pcEntry) {
		resp, body := get("/v1/pc/list?path="+url.QueryEscape(p), nil)
		var out struct{ Entries []pcEntry }
		_ = json.Unmarshal([]byte(body), &out)
		return resp.StatusCode, out.Entries
	}

	if c, e := list(""); c != 200 || len(e) != 1 || e[0].Name != "Docs" || !e[0].Dir {
		t.Fatalf("roots: %d %+v", c, e)
	}
	if c, e := list("Docs"); c != 200 || len(e) != 2 || e[0].Name != "sub dir" || !e[0].Dir || e[1].Size != 10 {
		t.Fatalf("docs: %d %+v", c, e)
	}
	if c, e := list("Docs/sub dir"); c != 200 || len(e) != 1 || e[0].Name != "b.txt" {
		t.Fatalf("sub: %d %+v", c, e)
	}

	if resp, body := get("/v1/pc/file?path=Docs/a.txt", nil); resp.StatusCode != 200 || body != "0123456789" {
		t.Fatalf("file: %d %q", resp.StatusCode, body)
	}
	if resp, body := get("/v1/pc/file?path=Docs/a.txt", map[string]string{"Range": "bytes=2-4"}); resp.StatusCode != 206 || body != "234" {
		t.Fatalf("range: %d %q", resp.StatusCode, body)
	}

	for _, p := range []string{"Docs/../secret.txt", "Docs/%2e%2e/secret.txt", "Docs/sub dir/../../secret.txt", `Docs\..\secret.txt`, "Other/a.txt", "Docs/missing", "C:/Windows/win.ini", "Docs"} {
		if resp, body := get("/v1/pc/file?path="+url.QueryEscape(p), nil); resp.StatusCode == 200 || body == "nope" {
			t.Errorf("file %q leaked: %d", p, resp.StatusCode)
		}
	}
	for _, p := range []string{"Docs/..", "../", "Nope", `Docs\..`} {
		if c, _ := list(p); c == 200 {
			t.Errorf("list %q allowed", p)
		}
	}

	// A symlink inside the share that points outside must not be followed.
	if err := os.Symlink(secret, filepath.Join(share, "link.txt")); err == nil {
		if resp, body := get("/v1/pc/file?path=Docs/link.txt", nil); resp.StatusCode == 200 || body == "nope" {
			t.Errorf("symlink escape: %d %q", resp.StatusCode, body)
		}
	}

	// Without a token nothing is served.
	resp, _ := http.Get(ts.URL + "/v1/pc/list")
	if resp.StatusCode != 401 {
		t.Errorf("unauthenticated: %d", resp.StatusCode)
	}
	resp.Body.Close()
}
