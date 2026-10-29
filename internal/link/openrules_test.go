package link

import (
	"reflect"
	"testing"
)

func TestNormalizeExts(t *testing.T) {
	got := NormalizeExts("MP4, .mkv;  avi,,")
	if want := []string{"mp4", "mkv", "avi"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestMatchRule(t *testing.T) {
	rules := []OpenRule{{Exts: []string{"mp4", "mkv"}, Program: "vlc"}, {Exts: []string{"pdf"}, Program: "x"}}
	if r, ok := MatchRule(rules, "Movie.MKV"); !ok || r.Program != "vlc" {
		t.Fatalf("mkv: %v %v", r, ok)
	}
	if _, ok := MatchRule(rules, "a.exe"); ok {
		t.Fatal("exe must not match")
	}
	if _, ok := MatchRule(rules, "noext"); ok {
		t.Fatal("no extension must not match")
	}
}

func TestCommandArgs(t *testing.T) {
	f := `C:\in box\a b.mp4`
	if got := (OpenRule{}).commandArgs(f); !reflect.DeepEqual(got, []string{f}) {
		t.Fatalf("%v", got)
	}
	got := OpenRule{Args: "--fullscreen {file} --play-and-exit"}.commandArgs(f)
	if want := []string{"--fullscreen", f, "--play-and-exit"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("%v", got)
	}
}

func TestValidate(t *testing.T) {
	if (OpenRule{Exts: []string{"a"}, Program: `C:\nope\x.exe`}).Validate() == nil {
		t.Fatal("missing program accepted")
	}
	if (OpenRule{Program: "."}).Validate() == nil {
		t.Fatal("no exts accepted")
	}
}
