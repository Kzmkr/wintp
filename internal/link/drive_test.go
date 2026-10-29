package link

import (
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/net/webdav"
)

// fakePhone serves WebDAV over TLS with the PC-issued drive certificate and
// requires the PC's bearer token, like the Android app does.
func fakePhone(t *testing.T, s *Server, certPEM, keyPEM string) (*httptest.Server, int) {
	t.Helper()
	cert, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
	if err != nil {
		t.Fatal(err)
	}
	dav := &webdav.Handler{FileSystem: webdav.NewMemFS(), LockSystem: webdav.NewMemLS()}
	ps := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+s.drive.PhoneToken {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		dav.ServeHTTP(w, r)
	}))
	ps.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	ps.StartTLS()
	t.Cleanup(ps.Close)
	u, _ := url.Parse(ps.URL)
	port, _ := strconv.Atoi(u.Port())
	return ps, port
}

func announce(t *testing.T, ts *httptest.Server, tok string, port int) {
	t.Helper()
	r := do(t, "GET", ts.URL+"/v1/ping", tok, nil, map[string]string{"X-Drive-Port": strconv.Itoa(port)})
	if r.StatusCode != 200 {
		t.Fatalf("announce = %d", r.StatusCode)
	}
}

func proxyReq(t *testing.T, method, url string, pass string, body string, hdr map[string]string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	if pass != "" {
		req.SetBasicAuth(DriveUser, pass)
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

func TestDriveProxyEndToEnd(t *testing.T) {
	s, _, ts := newTestServer(t)
	tok := pair(t, s, ts)
	_, port := fakePhone(t, s, s.drive.CertPEM, s.drive.KeyPEM)
	announce(t, ts, tok, port)

	px := httptest.NewServer(s.ProxyHandler())
	defer px.Close()
	pass := s.drive.LoopSecret

	if r := proxyReq(t, "PUT", px.URL+"/a.txt", pass, "hello phone", nil); r.StatusCode/100 != 2 {
		t.Fatalf("PUT = %d", r.StatusCode)
	}
	r := proxyReq(t, "GET", px.URL+"/a.txt", pass, "", nil)
	if b, _ := io.ReadAll(r.Body); string(b) != "hello phone" {
		t.Fatalf("GET body = %q (status %d)", b, r.StatusCode)
	}
	if r := proxyReq(t, "MKCOL", px.URL+"/dir", pass, "", nil); r.StatusCode/100 != 2 {
		t.Fatalf("MKCOL = %d", r.StatusCode)
	}
	// MOVE carries an absolute Destination pointing at the proxy; it must be
	// rewritten to the phone or the phone rejects it.
	r = proxyReq(t, "MOVE", px.URL+"/a.txt", pass, "", map[string]string{"Destination": px.URL + "/dir/b.txt"})
	if r.StatusCode/100 != 2 {
		t.Fatalf("MOVE = %d", r.StatusCode)
	}
	if r := proxyReq(t, "GET", px.URL+"/dir/b.txt", pass, "", nil); r.StatusCode != 200 {
		t.Fatalf("moved file GET = %d", r.StatusCode)
	}
	if r := proxyReq(t, "PROPFIND", px.URL+"/", pass, "", map[string]string{"Depth": "1"}); r.StatusCode != 207 {
		t.Fatalf("PROPFIND = %d", r.StatusCode)
	}
	if r := proxyReq(t, "DELETE", px.URL+"/dir/b.txt", pass, "", nil); r.StatusCode/100 != 2 {
		t.Fatalf("DELETE = %d", r.StatusCode)
	}
}

func TestDriveProxyRejectsUnauthenticated(t *testing.T) {
	s, _, ts := newTestServer(t)
	tok := pair(t, s, ts)
	_, port := fakePhone(t, s, s.drive.CertPEM, s.drive.KeyPEM)
	announce(t, ts, tok, port)
	px := httptest.NewServer(s.ProxyHandler())
	defer px.Close()

	if r := proxyReq(t, "GET", px.URL+"/a.txt", "", "", nil); r.StatusCode != 401 {
		t.Fatalf("no creds = %d", r.StatusCode)
	}
	if r := proxyReq(t, "GET", px.URL+"/a.txt", "wrong", "", nil); r.StatusCode != 401 {
		t.Fatalf("wrong pass = %d", r.StatusCode)
	}
}

func TestDriveProxyOfflineAndSharingStopped(t *testing.T) {
	s, _, ts := newTestServer(t)
	tok := pair(t, s, ts)
	px := httptest.NewServer(s.ProxyHandler())
	defer px.Close()

	if r := proxyReq(t, "GET", px.URL+"/", s.drive.LoopSecret, "", nil); r.StatusCode != 503 {
		t.Fatalf("never announced = %d", r.StatusCode)
	}
	_, port := fakePhone(t, s, s.drive.CertPEM, s.drive.KeyPEM)
	announce(t, ts, tok, port)
	if _, ok := s.DriveOnline(); !ok {
		t.Fatal("phone should be online after announcing")
	}
	// A request without the header means the phone stopped sharing.
	if r := do(t, "GET", ts.URL+"/v1/ping", tok, nil, nil); r.StatusCode != 200 {
		t.Fatal("ping failed")
	}
	if _, ok := s.DriveOnline(); ok {
		t.Fatal("phone should be offline once it stops announcing a drive port")
	}
}

// A different machine answering on the phone's address must not receive the
// access token or any traffic: the certificate pin has to reject it.
func TestDriveProxyPinsCertificate(t *testing.T) {
	s, _, ts := newTestServer(t)
	tok := pair(t, s, ts)
	otherCert, otherKey, err := newSelfSigned("impostor")
	if err != nil {
		t.Fatal(err)
	}
	_, port := fakePhone(t, s, string(otherCert), string(otherKey))
	announce(t, ts, tok, port)
	px := httptest.NewServer(s.ProxyHandler())
	defer px.Close()

	if r := proxyReq(t, "GET", px.URL+"/", s.drive.LoopSecret, "", nil); r.StatusCode != 502 {
		t.Fatalf("impostor accepted: status %d", r.StatusCode)
	}
}

func TestDriveCredentialsRequireAuthAndAreStable(t *testing.T) {
	dir := t.TempDir()
	dc1, fp1, err := loadOrCreateDrive(dir)
	if err != nil {
		t.Fatal(err)
	}
	dc2, fp2, err := loadOrCreateDrive(dir)
	if err != nil || fp1 != fp2 || dc1.LoopSecret != dc2.LoopSecret || dc1.PhoneToken != dc2.PhoneToken {
		t.Fatal("drive config must persist across restarts")
	}

	s, _, ts := newTestServer(t)
	if r := do(t, "GET", ts.URL+"/v1/drive/credentials", "", nil, nil); r.StatusCode != 401 {
		t.Fatalf("unauth credentials = %d", r.StatusCode)
	}
	tok := pair(t, s, ts)
	r := do(t, "GET", ts.URL+"/v1/drive/credentials", tok, nil, nil)
	body, _ := io.ReadAll(r.Body)
	if r.StatusCode != 200 || !strings.Contains(string(body), "BEGIN CERTIFICATE") || !strings.Contains(string(body), s.drive.PhoneToken) {
		t.Fatalf("credentials = %d %s", r.StatusCode, body)
	}
}
