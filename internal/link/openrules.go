package link

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// OpenRule says which program opens a received file of certain types. Files
// are only ever opened with a program the user configured on the PC, never
// with the Windows default handler, so a phone cannot make the PC run an
// arbitrary file just by sending it.
type OpenRule struct {
	Exts    []string `json:"exts"`           // lower-case, without the dot
	Program string   `json:"program"`        // full path to the executable
	Args    string   `json:"args,omitempty"` // extra arguments; "{file}" marks where the path goes
}

// NormalizeExts turns "MP4, .mkv  avi" into ["mp4" "mkv" "avi"].
func NormalizeExts(s string) []string {
	var out []string
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' || r == ' ' }) {
		f = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(f), "."))
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

// Validate checks a rule is usable.
func (r OpenRule) Validate() error {
	if len(r.Exts) == 0 {
		return errors.New("add at least one file type")
	}
	st, err := os.Stat(r.Program)
	if err != nil || st.IsDir() {
		return fmt.Errorf("program not found: %s", r.Program)
	}
	return nil
}

// MatchRule returns the first rule whose types include the file's extension.
func MatchRule(rules []OpenRule, name string) (OpenRule, bool) {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
	if ext == "" {
		return OpenRule{}, false
	}
	for _, r := range rules {
		for _, e := range r.Exts {
			if e == ext {
				return r, true
			}
		}
	}
	return OpenRule{}, false
}

// commandArgs builds the argument list; the file is always a single argument.
func (r OpenRule) commandArgs(file string) []string {
	var args []string
	placed := false
	for _, a := range strings.Fields(r.Args) {
		if a == "{file}" {
			args = append(args, file)
			placed = true
		} else {
			args = append(args, a)
		}
	}
	if !placed {
		args = append(args, file)
	}
	return args
}

// Open starts the rule's program on file without waiting for it.
func (r OpenRule) Open(file string) error {
	if err := r.Validate(); err != nil {
		return err
	}
	cmd := exec.Command(r.Program, r.commandArgs(file)...)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
