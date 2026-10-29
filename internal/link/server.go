// Package link lets a paired phone share its clipboard and send files to this
// PC over the local network.
//
// Protocol (all JSON over HTTPS, bearer-token auth after pairing):
//
//	POST /v1/pair       {code,name}        -> {token}          (no auth; one-time code)
//	GET  /v1/ping                          -> {ok:true}
//	GET  /v1/clipboard?since=N&wait=S      -> {seq,text}       (long-poll up to 60s)
//	PUT  /v1/clipboard  {text}             -> {seq}
//	POST /v1/files      X-Filename: <pct>  -> {name,size}      (raw body)
//	POST /v1/open       {url}              -> {ok:true}        (http/https only)
//	POST /v1/media      {action}           -> {ok:true}        (see MediaActions)
//	GET  /v1/drive/credentials             -> {cert,key,token} (phone WebDAV server setup; see drive.go)
//	GET  /v1/bluetooth                     -> {state}          (on|off|none)
//	PUT  /v1/bluetooth  {on}               -> {state}          (switch the PC radio)
//
// The phone authenticates the server by pinning the certificate SHA-256
// fingerprint that is delivered in the pairing QR code.
package link

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	DefaultPort    = 48765
	pairCodeTTL    = 5 * time.Minute
	maxClipBytes   = 1 << 20
	maxLongPoll    = 60 * time.Second
	clipPollPeriod = 500 * time.Millisecond
)

// Clipboard abstracts the PC clipboard. Implementations must be safe to call
// from any goroutine.
type Clipboard interface {
	Text() string
	SetText(string)
}

type Server struct {
	dir      string
	inbox    string
	port     int
	clip     Clipboard
	cert     tls.Certificate
	fp       string
	devices  *deviceStore
	drive    *driveConfig
	driveFP  string
	proxySrv *http.Server
	seenMu   sync.Mutex
	seen     map[string]phoneSeen
	OnFile   func(name string, size int64) // optional notification hook
	OnDevice func()                        // optional: device list changed
	OpenURL  func(rawURL string) error     // opens a validated http(s) URL; nil disables /v1/open
	Media    func(action string) error     // performs a validated media action; nil disables /v1/media
	Shares   func() []Share                // PC folders visible to phones; nil disables /v1/pc/*
	// Bluetooth returns the PC radio state, first switching it when set != nil.
	Bluetooth func(set *bool) (string, error)

	mu         sync.Mutex
	pairCode   string
	pairExpiry time.Time
	seq        int64
	lastText   string
	changed    chan struct{} // closed and replaced on every clipboard change

	httpSrv *http.Server
	cancel  context.CancelFunc
}

// New prepares a server. dir holds the cert and device list; inbox receives
// uploaded files.
func New(dir, inbox string, port int, clip Clipboard) (*Server, error) {
	if port == 0 {
		port = DefaultPort
	}
	cert, fp, err := loadOrCreateCert(dir)
	if err != nil {
		return nil, fmt.Errorf("tls cert: %w", err)
	}
	ds, err := loadDevices(dir)
	if err != nil {
		return nil, fmt.Errorf("devices: %w", err)
	}
	if err := os.MkdirAll(inbox, 0755); err != nil {
		return nil, fmt.Errorf("inbox: %w", err)
	}
	dc, dfp, err := loadOrCreateDrive(dir)
	if err != nil {
		return nil, fmt.Errorf("drive config: %w", err)
	}
	return &Server{
		drive: dc, driveFP: dfp, seen: map[string]phoneSeen{},
		dir: dir, inbox: inbox, port: port, clip: clip,
		cert: cert, fp: fp, devices: ds,
		changed: make(chan struct{}),
	}, nil
}

func (s *Server) Inbox() string       { return s.inbox }
func (s *Server) Devices() []Device   { return s.devices.list() }
func (s *Server) Fingerprint() string { return s.fp }

func (s *Server) RemoveDevice(id string) error {
	err := s.devices.remove(id)
	if s.OnDevice != nil {
		s.OnDevice()
	}
	return err
}

// Start begins listening and watching the clipboard.
func (s *Server) Start() error {
	// Read once here (not in New) so callers can construct the server before
	// the UI event loop runs, which Fyne clipboard access requires.
	initial := s.clip.Text()
	s.mu.Lock()
	s.lastText = initial
	s.mu.Unlock()

	ln, err := tls.Listen("tcp", fmt.Sprintf(":%d", s.port), &tls.Config{
		Certificates: []tls.Certificate{s.cert},
		MinVersion:   tls.VersionTLS12,
	})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.httpSrv = &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		if err := s.httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("link: serve: %v", err)
		}
	}()
	go s.watchClipboard(ctx)
	s.startProxy()
	log.Printf("link: listening on :%d", s.port)
	return nil
}

func (s *Server) Stop() {
	s.stopProxy()
	if s.cancel != nil {
		s.cancel()
	}
	if s.httpSrv != nil {
		ctx, c := context.WithTimeout(context.Background(), 2*time.Second)
		defer c()
		_ = s.httpSrv.Shutdown(ctx)
	}
}

// NewPairing issues a one-time pairing code and returns the URI to render as a
// QR code.
func (s *Server) NewPairing() string {
	buf := make([]byte, 9)
	_, _ = rand.Read(buf)
	code := base64.RawURLEncoding.EncodeToString(buf)

	s.mu.Lock()
	s.pairCode = code
	s.pairExpiry = time.Now().Add(pairCodeTTL)
	s.mu.Unlock()

	var hosts []string
	for _, ip := range lanIPs() {
		hosts = append(hosts, ip.String())
	}
	q := url.Values{}
	q.Set("h", strings.Join(hosts, ","))
	q.Set("p", strconv.Itoa(s.port))
	q.Set("fp", s.fp)
	q.Set("c", code)
	return "wintp://pair?" + q.Encode()
}

func (s *Server) consumePairCode(code string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	ok := s.pairCode != "" && time.Now().Before(s.pairExpiry) &&
		subtle.ConstantTimeCompare([]byte(code), []byte(s.pairCode)) == 1
	if ok {
		s.pairCode = "" // single use
	}
	return ok
}

// --- clipboard sync ---

func (s *Server) watchClipboard(ctx context.Context) {
	t := time.NewTicker(clipPollPeriod)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			cur := s.clip.Text()
			s.mu.Lock()
			if cur != s.lastText {
				s.bumpLocked(cur)
			}
			s.mu.Unlock()
		}
	}
}

// bumpLocked records new clipboard text and wakes long-pollers.
func (s *Server) bumpLocked(text string) {
	s.lastText = text
	s.seq++
	close(s.changed)
	s.changed = make(chan struct{})
}

func (s *Server) snapshot() (int64, string, <-chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.seq, s.lastText, s.changed
}

// --- HTTP ---

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/pair", s.handlePair)
	mux.HandleFunc("GET /v1/ping", s.auth(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}))
	mux.HandleFunc("GET /v1/clipboard", s.auth(s.handleClipGet))
	mux.HandleFunc("PUT /v1/clipboard", s.auth(s.handleClipPut))
	mux.HandleFunc("POST /v1/files", s.auth(s.handleFile))
	mux.HandleFunc("POST /v1/open", s.auth(s.handleOpen))
	mux.HandleFunc("GET /v1/pc/list", s.auth(s.handlePCList))
	mux.HandleFunc("GET /v1/pc/file", s.auth(s.handlePCFile))
	mux.HandleFunc("POST /v1/media", s.auth(s.handleMedia))
	mux.HandleFunc("GET /v1/drive/credentials", s.auth(s.handleDriveCredentials))
	mux.HandleFunc("GET /v1/bluetooth", s.auth(s.handleBluetooth))
	mux.HandleFunc("PUT /v1/bluetooth", s.auth(s.handleBluetooth))
	return mux
}

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || tok == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		dev, ok := s.devices.lookup(tok)
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		s.noteDevice(dev.ID, r)
		next(w, r)
	}
}

func (s *Server) handlePair(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code string `json:"code"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if !s.consumePairCode(req.Code) {
		http.Error(w, "invalid or expired code", http.StatusForbidden)
		return
	}
	buf := make([]byte, 32)
	_, _ = rand.Read(buf)
	token := base64.RawURLEncoding.EncodeToString(buf)
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "Phone"
	}
	err := s.devices.add(Device{
		ID: uuid.NewString(), Name: name,
		Hash: hashToken(token), AddedAt: time.Now(),
	})
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	log.Printf("link: paired device %q", name)
	if s.OnDevice != nil {
		s.OnDevice()
	}
	writeJSON(w, http.StatusOK, map[string]string{"token": token})
}

type clipMsg struct {
	Seq  int64  `json:"seq"`
	Text string `json:"text"`
}

func (s *Server) handleClipGet(w http.ResponseWriter, r *http.Request) {
	since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
	waitSec, _ := strconv.Atoi(r.URL.Query().Get("wait"))
	wait := time.Duration(waitSec) * time.Second
	if wait > maxLongPoll {
		wait = maxLongPoll
	}

	seq, text, ch := s.snapshot()
	if seq == since && wait > 0 {
		select {
		case <-ch:
		case <-time.After(wait):
		case <-r.Context().Done():
			return
		}
		seq, text, _ = s.snapshot()
	}
	writeJSON(w, http.StatusOK, clipMsg{Seq: seq, Text: text})
}

func (s *Server) handleClipPut(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxClipBytes)).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	s.clip.SetText(req.Text)
	s.mu.Lock()
	if req.Text != s.lastText {
		s.bumpLocked(req.Text)
	}
	seq := s.seq
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]int64{"seq": seq})
}

func (s *Server) handleFile(w http.ResponseWriter, r *http.Request) {
	raw := r.Header.Get("X-Filename")
	name, err := url.PathUnescape(raw)
	if err != nil || strings.TrimSpace(name) == "" {
		http.Error(w, "missing or invalid X-Filename", http.StatusBadRequest)
		return
	}
	name = sanitizeName(name)

	f, finalName, err := createUnique(s.inbox, name)
	if err != nil {
		http.Error(w, "cannot create file", http.StatusInternalServerError)
		return
	}
	n, copyErr := io.Copy(f, r.Body)
	closeErr := f.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(filepath.Join(s.inbox, finalName))
		http.Error(w, "upload failed", http.StatusBadRequest)
		return
	}
	log.Printf("link: received %s (%d bytes)", finalName, n)
	if s.OnFile != nil {
		s.OnFile(finalName, n)
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": finalName, "size": n})
}

const maxURLLen = 8192

func (s *Server) handleBluetooth(w http.ResponseWriter, r *http.Request) {
	if s.Bluetooth == nil {
		http.Error(w, "not supported", http.StatusNotImplemented)
		return
	}
	var set *bool
	if r.Method == http.MethodPut {
		var req struct {
			On *bool `json:"on"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&req); err != nil || req.On == nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		set = req.On
	}
	state, err := s.Bluetooth(set)
	if err != nil {
		log.Printf("link: bluetooth: %v", err)
		http.Error(w, "bluetooth unavailable", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"state": state})
}

// MediaActions is the closed set of actions the phone may request.
var MediaActions = map[string]bool{
	"playpause": true, "next": true, "prev": true, "stop": true,
	"volup": true, "voldown": true, "mute": true,
}

func (s *Server) handleMedia(w http.ResponseWriter, r *http.Request) {
	if s.Media == nil {
		http.Error(w, "not supported", http.StatusNotImplemented)
		return
	}
	var req struct {
		Action string `json:"action"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&req); err != nil || !MediaActions[req.Action] {
		http.Error(w, "unknown action", http.StatusBadRequest)
		return
	}
	if err := s.Media(req.Action); err != nil {
		log.Printf("link: media %s: %v", req.Action, err)
		http.Error(w, "failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleOpen opens a URL from the phone in the PC's default browser. Only
// http and https are accepted: anything else (file:, ms-settings:, custom
// protocol handlers) could launch programs on the PC.
func (s *Server) handleOpen(w http.ResponseWriter, r *http.Request) {
	if s.OpenURL == nil {
		http.Error(w, "not supported", http.StatusNotImplemented)
		return
	}
	var req struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxURLLen+1024)).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	u, err := url.Parse(strings.TrimSpace(req.URL))
	if err != nil || len(req.URL) > maxURLLen || u.Host == "" ||
		(u.Scheme != "http" && u.Scheme != "https") {
		http.Error(w, "only http and https URLs are allowed", http.StatusBadRequest)
		return
	}
	if err := s.OpenURL(u.String()); err != nil {
		log.Printf("link: open url: %v", err)
		http.Error(w, "could not open", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// sanitizeName reduces an untrusted filename to a safe single path element.
func sanitizeName(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	name = name[strings.LastIndex(name, "/")+1:]
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || strings.ContainsRune(`<>:"|?*`, r) {
			return '_'
		}
		return r
	}, name)
	name = strings.TrimRight(name, ". ")
	if name == "" || name == ".." {
		name = "file"
	}
	base := strings.ToUpper(strings.TrimSuffix(name, filepath.Ext(name)))
	switch base {
	case "CON", "PRN", "AUX", "NUL",
		"COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
		"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		name = "_" + name
	}
	return name
}

// createUnique exclusively creates dir/name, appending " (n)" on collision.
func createUnique(dir, name string) (*os.File, string, error) {
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 0; i < 1000; i++ {
		cand := name
		if i > 0 {
			cand = fmt.Sprintf("%s (%d)%s", stem, i, ext)
		}
		f, err := os.OpenFile(filepath.Join(dir, cand), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
		if err == nil {
			return f, cand, nil
		}
		if !os.IsExist(err) {
			return nil, "", err
		}
	}
	return nil, "", errors.New("too many name collisions")
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
