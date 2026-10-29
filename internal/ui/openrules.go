package ui

import (
	"os"
	"path/filepath"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"wintp/internal/link"
)

const defaultVideoExts = "mp4, mkv, avi, mov, webm, wmv, flv, m4v, mpg, ts"

// ruleRow is one editable "file types -> program" line.
type ruleRow struct {
	exts, program, args *widget.Entry
}

// openRulesCard lets the user choose which program opens received files.
func openRulesCard(ctrl PhoneController, win fyne.Window) fyne.CanvasObject {
	var rows []*ruleRow
	list := container.NewVBox()
	var rebuild func()

	addRow := func(r link.OpenRule) {
		row := &ruleRow{exts: widget.NewEntry(), program: widget.NewEntry(), args: widget.NewEntry()}
		row.exts.SetText(strings.Join(r.Exts, ", "))
		row.exts.SetPlaceHolder("File types, e.g. mp4, mkv")
		row.program.SetText(r.Program)
		row.program.SetPlaceHolder(`Program, e.g. C:\Program Files\VideoLAN\VLC\vlc.exe`)
		row.args.SetText(r.Args)
		row.args.SetPlaceHolder("Extra arguments (optional)")
		rows = append(rows, row)
	}

	rebuild = func() {
		list.Objects = nil
		for _, row := range rows {
			row := row
			browse := widget.NewButtonWithIcon("", theme.FolderOpenIcon(), func() {
				d := dialog.NewFileOpen(func(rc fyne.URIReadCloser, err error) {
					if err != nil || rc == nil {
						return
					}
					row.program.SetText(filepath.FromSlash(rc.URI().Path()))
					_ = rc.Close()
				}, win)
				d.SetFilter(exeFilter{})
				d.Show()
			})
			remove := widget.NewButtonWithIcon("", theme.DeleteIcon(), func() {
				for i, r := range rows {
					if r == row {
						rows = append(rows[:i], rows[i+1:]...)
						break
					}
				}
				rebuild()
			})
			list.Add(container.NewVBox(
				row.exts,
				container.NewBorder(nil, nil, nil, container.NewHBox(browse, remove), row.program),
				row.args,
				widget.NewSeparator(),
			))
		}
		list.Refresh()
	}

	for _, r := range ctrl.OpenRules() {
		addRow(r)
	}
	rebuild()

	add := widget.NewButtonWithIcon("Add rule", theme.ContentAddIcon(), func() {
		addRow(link.OpenRule{})
		rebuild()
	})
	vlc := widget.NewButton("Videos in VLC", func() {
		prog := ""
		for _, p := range []string{
			filepath.Join(os.Getenv("ProgramFiles"), `VideoLAN\VLC\vlc.exe`),
			filepath.Join(os.Getenv("ProgramFiles(x86)"), `VideoLAN\VLC\vlc.exe`),
		} {
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				prog = p
				break
			}
		}
		addRow(link.OpenRule{Exts: link.NormalizeExts(defaultVideoExts), Program: prog})
		rebuild()
		if prog == "" {
			dialog.ShowInformation("VLC not found", "Pick vlc.exe with the folder button.", win)
		}
	})
	save := widget.NewButtonWithIcon("Save", theme.DocumentSaveIcon(), func() {
		var out []link.OpenRule
		for _, row := range rows {
			if strings.TrimSpace(row.exts.Text) == "" && strings.TrimSpace(row.program.Text) == "" {
				continue // blank row
			}
			r := link.OpenRule{
				Exts:    link.NormalizeExts(row.exts.Text),
				Program: strings.TrimSpace(row.program.Text),
				Args:    strings.TrimSpace(row.args.Text),
			}
			if err := r.Validate(); err != nil {
				dialog.ShowError(err, win)
				return
			}
			out = append(out, r)
		}
		if err := ctrl.SetOpenRules(out); err != nil {
			dialog.ShowError(err, win)
			return
		}
		dialog.ShowInformation("Saved", "Received files are opened with these rules.", win)
	})
	save.Importance = widget.HighImportance

	return widget.NewCard("", "", container.NewVBox(
		sectionHeading("Open received files",
			"Files sent from the phone open automatically with the program set for their type. Types with no rule are only saved."),
		list,
		container.NewHBox(add, vlc, save),
	))
}

// exeFilter shows programs and shortcuts in the file picker.
type exeFilter struct{}

func (exeFilter) Matches(u fyne.URI) bool {
	e := strings.ToLower(u.Extension())
	return e == ".exe" || e == ".bat" || e == ".cmd"
}

