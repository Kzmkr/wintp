package link

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// Share is a PC folder the user chose to make visible (read-only) to paired phones.
type Share struct {
	Name string `json:"name"` // shown on the phone
	Path string `json:"path"`
}

// shareNames returns shares keyed by a unique display name.
func shareMap(shares []Share) map[string]string {
	m := map[string]string{}
	for _, sh := range shares {
		name := strings.TrimSpace(sh.Name)
		if name == "" {
			name = filepath.Base(sh.Path)
		}
		name = strings.NewReplacer("/", "_", "\\", "_").Replace(name)
		base, n := name, 2
		for _, taken := m[name]; taken; _, taken = m[name] {
			name = fmt.Sprintf("%s (%d)", base, n)
			n++
		}
		m[name] = sh.Path
	}
	return m
}

type pcEntry struct {
	Name  string `json:"name"`
	Dir   bool   `json:"dir"`
	Size  int64  `json:"size"`
	MTime int64  `json:"mtime"` // unix milliseconds
}

// splitShare splits "share/a/b" into the share's folder and the path below it.
// Anything that could climb out of the folder is rejected; os.Root enforces
// the same boundary again, including through symlinks.
func (s *Server) splitShare(p string) (root, rel string, err error) {
	p = strings.Trim(p, "/")
	if strings.ContainsAny(p, `\:`) {
		return "", "", errors.New("bad path")
	}
	parts := strings.Split(p, "/")
	for _, seg := range parts {
		if seg == ".." || seg == "." {
			return "", "", errors.New("bad path")
		}
	}
	if s.Shares == nil {
		return "", "", errors.New("not shared")
	}
	dir, ok := shareMap(s.Shares())[parts[0]]
	if !ok {
		return "", "", errors.New("not shared")
	}
	rel = path.Join(parts[1:]...)
	if rel == "" {
		rel = "."
	}
	return dir, rel, nil
}

func (s *Server) handlePCList(w http.ResponseWriter, r *http.Request) {
	if s.Shares == nil {
		http.Error(w, "not supported", http.StatusNotImplemented)
		return
	}
	p := strings.Trim(r.URL.Query().Get("path"), "/")
	var out []pcEntry
	if p == "" {
		for name := range shareMap(s.Shares()) {
			out = append(out, pcEntry{Name: name, Dir: true})
		}
	} else {
		dir, rel, err := s.splitShare(p)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		root, err := os.OpenRoot(dir)
		if err != nil {
			http.Error(w, "folder unavailable", http.StatusNotFound)
			return
		}
		defer root.Close()
		f, err := root.Open(rel)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		defer f.Close()
		ents, err := f.ReadDir(-1)
		if err != nil {
			http.Error(w, "not a folder", http.StatusBadRequest)
			return
		}
		for _, e := range ents {
			info, err := e.Info()
			if err != nil {
				continue
			}
			out = append(out, pcEntry{Name: e.Name(), Dir: info.IsDir(), Size: info.Size(), MTime: info.ModTime().UnixMilli()})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Dir != out[j].Dir {
			return out[i].Dir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	if out == nil {
		out = []pcEntry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": out})
}

func (s *Server) handlePCFile(w http.ResponseWriter, r *http.Request) {
	if s.Shares == nil {
		http.Error(w, "not supported", http.StatusNotImplemented)
		return
	}
	dir, rel, err := s.splitShare(r.URL.Query().Get("path"))
	if err != nil || rel == "." {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		http.Error(w, "folder unavailable", http.StatusNotFound)
		return
	}
	defer root.Close()
	f, err := root.Open(rel)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		http.Error(w, "not a file", http.StatusBadRequest)
		return
	}
	http.ServeContent(w, r, info.Name(), info.ModTime(), f)
}

