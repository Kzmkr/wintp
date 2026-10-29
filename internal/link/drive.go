package link

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// Phone drive: the phone runs a WebDAV server over TLS and the PC mounts it
// through a loopback proxy.
//
//	rclone/WinFsp -> http://127.0.0.1:ProxyPort (basic auth: LoopSecret)
//	              -> https://<phone-ip>:<phone-port> (cert pinned, Bearer PhoneToken)
//
// The proxy exists because the phone's IP changes and a self-signed cert can
// not be validated by hostname; the proxy pins the certificate by fingerprint
// instead. The phone learns it must serve (and on which port) from the
// X-Drive-Port header it sends on every authenticated request, and learns its
// certificate and the PC's access token from GET /v1/drive/credentials.
const (
	ProxyPort      = 48767
	DriveUser      = "wintp"
	driveFreshness = 3 * time.Minute
)

// driveConfig is persisted in drive.json (0600).
type driveConfig struct {
	LoopSecret string `json:"loop_secret"` // rclone -> proxy
	PhoneToken string `json:"phone_token"` // proxy -> phone
	CertPEM    string `json:"cert"`
	KeyPEM     string `json:"key"`
}

func randomToken() string {
	buf := make([]byte, 32)
	_, _ = rand.Read(buf)
	return base64.RawURLEncoding.EncodeToString(buf)
}

func loadOrCreateDrive(dir string) (*driveConfig, string, error) {
	path := filepath.Join(dir, "drive.json")
	var dc driveConfig
	if data, err := os.ReadFile(path); err == nil {
		if json.Unmarshal(data, &dc) == nil && dc.CertPEM != "" && dc.LoopSecret != "" {
			cert, err := tls.X509KeyPair([]byte(dc.CertPEM), []byte(dc.KeyPEM))
			if err == nil {
				return &dc, fingerprint(cert.Certificate[0]), nil
			}
		}
	}
	certPEM, keyPEM, err := newSelfSigned("wintp-phone-drive")
	if err != nil {
		return nil, "", err
	}
	dc = driveConfig{
		LoopSecret: randomToken(), PhoneToken: randomToken(),
		CertPEM: string(certPEM), KeyPEM: string(keyPEM),
	}
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, "", err
	}
	data, _ := json.Marshal(&dc)
	if err := os.WriteFile(path, data, 0600); err != nil {
		return nil, "", err
	}
	return &dc, fingerprint(cert.Certificate[0]), nil
}

type phoneSeen struct {
	ip   string
	port int // 0 = phone is not sharing storage
	at   time.Time
}

// noteDevice records where an authenticated phone is and whether it is sharing.
func (s *Server) noteDevice(id string, r *http.Request) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return
	}
	port, _ := strconv.Atoi(r.Header.Get("X-Drive-Port"))
	if port < 1 || port > 65535 {
		port = 0
	}
	s.seenMu.Lock()
	s.seen[id] = phoneSeen{ip: host, port: port, at: time.Now()}
	s.seenMu.Unlock()
}

// DriveOnline reports the address of the most recently seen phone that is
// sharing its storage.
func (s *Server) DriveOnline() (addr string, ok bool) {
	s.seenMu.Lock()
	defer s.seenMu.Unlock()
	var best phoneSeen
	for _, p := range s.seen {
		if p.port > 0 && time.Since(p.at) < driveFreshness && p.at.After(best.at) {
			best = p
		}
	}
	if best.port == 0 {
		return "", false
	}
	return net.JoinHostPort(best.ip, strconv.Itoa(best.port)), true
}

// DriveConfig returns the rclone webdav settings for mounting the phone.
func (s *Server) DriveConfig() map[string]string {
	return map[string]string{
		"url":    "http://127.0.0.1:" + strconv.Itoa(ProxyPort),
		"vendor": "other",
		"user":   DriveUser,
		"pass":   s.drive.LoopSecret,
	}
}

func (s *Server) handleDriveCredentials(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"cert":  s.drive.CertPEM,
		"key":   s.drive.KeyPEM,
		"token": s.drive.PhoneToken,
	})
}

func (s *Server) startProxy() {
	ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(ProxyPort))
	if err != nil {
		log.Printf("link: drive proxy: %v", err)
		return
	}
	s.proxySrv = &http.Server{Handler: s.ProxyHandler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := s.proxySrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("link: drive proxy serve: %v", err)
		}
	}()
}

func (s *Server) stopProxy() {
	if s.proxySrv != nil {
		ctx, c := context.WithTimeout(context.Background(), 2*time.Second)
		defer c()
		_ = s.proxySrv.Shutdown(ctx)
		s.proxySrv = nil
	}
}

// ProxyHandler is the loopback side: it authenticates rclone, then forwards to
// the phone with the pinned TLS transport.
func (s *Server) ProxyHandler() http.Handler {
	tr := &http.Transport{
		DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
		TLSClientConfig: &tls.Config{
			// Hostname checks cannot work for a phone on a changing IP, so
			// verification is replaced by an exact certificate fingerprint pin.
			InsecureSkipVerify: true,
			MinVersion:         tls.VersionTLS12,
			VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
				if len(raw) == 0 || fingerprint(raw[0]) != s.driveFP {
					return errors.New("phone certificate does not match the paired drive certificate")
				}
				return nil
			},
		},
		ResponseHeaderTimeout: 90 * time.Second,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       60 * time.Second,
	}
	wantUser := sha256.Sum256([]byte(DriveUser))
	wantPass := sha256.Sum256([]byte(s.drive.LoopSecret))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		gotUser, gotPass := sha256.Sum256([]byte(u)), sha256.Sum256([]byte(p))
		if !ok ||
			subtle.ConstantTimeCompare(gotUser[:], wantUser[:]) != 1 ||
			subtle.ConstantTimeCompare(gotPass[:], wantPass[:]) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="wintp"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		addr, online := s.DriveOnline()
		if !online {
			http.Error(w, "phone is not sharing storage", http.StatusServiceUnavailable)
			return
		}
		rp := &httputil.ReverseProxy{
			Transport: tr,
			Director: func(out *http.Request) {
				out.URL.Scheme = "https"
				out.URL.Host = addr
				out.Host = addr
				out.Header.Set("Authorization", "Bearer "+s.drive.PhoneToken)
				out.Header["X-Forwarded-For"] = nil // do not advertise the loopback client
				// COPY/MOVE carry an absolute Destination URL that points at us.
				if d := out.Header.Get("Destination"); d != "" {
					if du, err := url.Parse(d); err == nil {
						du.Scheme, du.Host = "https", addr
						out.Header.Set("Destination", du.String())
					}
				}
			},
			ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
				log.Printf("link: drive proxy: %v", err)
				http.Error(w, fmt.Sprintf("phone unreachable: %v", err), http.StatusBadGateway)
			},
		}
		rp.ServeHTTP(w, r)
	})
}
