package ui

import (
	"path/filepath"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"wintp/internal/link"
)

// pcSharesCard lists the PC folders a paired phone may browse (read-only).
func pcSharesCard(ctrl PhoneController, win fyne.Window) fyne.CanvasObject {
	shares := ctrl.PCShares()
	list := container.NewVBox()
	var rebuild func()

	save := func() {
		if err := ctrl.SetPCShares(shares); err != nil {
			dialog.ShowError(err, win)
		}
	}
	rebuild = func() {
		list.Objects = nil
		if len(shares) == 0 {
			empty := widget.NewLabel("No folders shared.")
			empty.Importance = widget.LowImportance
			list.Add(empty)
		}
		for i := range shares {
			i := i
			name := newText(shares[i].Name, 13, colText, true)
			path := widget.NewLabel(shares[i].Path)
			path.Importance = widget.LowImportance
			path.Truncation = fyne.TextTruncateEllipsis
			path.SizeName = theme.SizeNameCaptionText
			rm := widget.NewButtonWithIcon("", theme.DeleteIcon(), func() {
				shares = append(shares[:i], shares[i+1:]...)
				save()
				rebuild()
			})
			rm.Importance = widget.LowImportance
			list.Add(container.NewBorder(nil, nil,
				inset(0, 0, 0, 10, container.NewCenter(widget.NewIcon(theme.FolderIcon()))),
				container.NewCenter(rm), vbox(3, name, unpadded(path))))
		}
		list.Refresh()
	}
	rebuild()

	add := widget.NewButtonWithIcon("Share a folder", theme.FolderNewIcon(), func() {
		dialog.NewFolderOpen(func(u fyne.ListableURI, err error) {
			if err != nil || u == nil {
				return
			}
			p := filepath.FromSlash(u.Path())
			for _, s := range shares {
				if s.Path == p {
					return
				}
			}
			shares = append(shares, link.Share{Name: filepath.Base(p), Path: p})
			save()
			rebuild()
		}, win).Show()
	})

	return card("PC folders on the phone",
		"Paired phones can browse these folders and open or save their files. Read-only; nothing else on the PC is visible.",
		list,
		container.NewHBox(add),
	)
}
