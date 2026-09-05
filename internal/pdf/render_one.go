package pdf

import (
	"fmt"

	"github.com/signintech/gopdf"
)

// renderOnePerPage prints each row (class, teacher or room) on its own
// page in the aSc Timetables layout: periods are table rows with a
// bell-time left column, weekdays are columns under a light-blue caption
// row, and every lesson is a centered card (subject / teacher / room).
func renderOnePerPage(pdf *gopdf.GoPdf, opts Options, th ascTheme, daysN int, pageW, pageH float64) {
	const (
		margin   = 10.0
		leftCol  = 15.0
		footerH  = 7.0
	)

	total := len(opts.Rows)
	for pageNo, row := range opts.Rows {
		if pageNo > 0 {
			pdf.AddPage()
		}
		tableTop := ascPageHeader(pdf, opts, th, row.Label, opts.Title, pageW, margin)

		gridW := pageW - margin*2 - leftCol
		tableH := pageH - tableTop - margin - footerH
		hdrH := clampF(tableH/(float64(opts.Slots)+3), 6.0, 9.0)
		rowH := (tableH - hdrH) / float64(opts.Slots)
		colW := gridW / float64(daysN)

		gridX := margin + leftCol
		gridY := tableTop + hdrH
		bottomY := gridY + rowH*float64(opts.Slots)

		// Caption row: the "Урок" corner cell plus weekday cells.
		ascFillRect(pdf, margin, tableTop, leftCol+gridW, hdrH, th.headBg, th.line, 0.2)
		ascCenterLines(pdf, margin, tableTop, leftCol, hdrH, []ascLine{
			{Text: "Урок", Size: clampF(minF(8, hdrH*0.7), 5.5, 8), Bold: true, Color: th.headText},
		})
		daySize := clampF(minF(10, hdrH*0.75, colW*0.35), 6, 10)
		for di := 0; di < daysN; di++ {
			ascCenterLines(pdf, gridX+float64(di)*colW, tableTop, colW, hdrH, []ascLine{
				{Text: dayName(di), Size: daySize, Bold: true, Color: th.headText},
			})
		}

		for si := 0; si < opts.Slots; si++ {
			y := gridY + float64(si)*rowH

			// Bell column: big period number, bell times under it.
			ascFillRect(pdf, margin, y, leftCol, rowH, th.bellBg, th.line, 0.15)
			bell := []ascLine{
				{Text: fmt.Sprintf("%d", si+1), Size: clampF(minF(10, rowH*0.3), 6.5, 10), Bold: true, Color: th.bellNum},
			}
			if si < len(opts.Periods) && opts.Periods[si].Start != "" {
				ts := clampF(minF(6, rowH*0.15), 4, 6)
				bell = append(bell,
					ascLine{Text: opts.Periods[si].Start, Size: ts, Color: th.bellTime},
					ascLine{Text: opts.Periods[si].End, Size: ts, Color: th.bellTime})
			}
			ascCenterLines(pdf, margin, y, leftCol, rowH, bell)

			for di := 0; di < daysN; di++ {
				cell, ok := opts.CellAt(row.ID, di, si)
				drawASCTableCell(pdf, opts, th, cell, ok,
					gridX+float64(di)*colW, y, colW, rowH, 2, 0.55)
			}
		}

		// Inner grid lines over the fills, then the aSc-style heavy frame.
		ascVLine(pdf, gridX, tableTop, bottomY, th.line, 0.15)
		for di := 1; di < daysN; di++ {
			ascVLine(pdf, gridX+float64(di)*colW, tableTop, bottomY, th.line, 0.15)
		}
		ascHLine(pdf, margin, margin+leftCol+gridW, gridY, th.line, 0.15)
		for si := 1; si < opts.Slots; si++ {
			ascHLine(pdf, margin, margin+leftCol+gridW, gridY+float64(si)*rowH, th.line, 0.15)
		}
		ascRectOutline(pdf, margin, tableTop, leftCol+gridW, hdrH+rowH*float64(opts.Slots), th.frame, 0.55)

		ascPageFooter(pdf, opts, th, pageW, pageH, margin, pageNo+1, total)
	}
}

// drawASCTableCell paints one timetable cell: subject-tinted fill (white
// in BW, light red for conflicts), the centered card block, and for
// conflicts an extra red outline just inside the cell border — the aSc
// conflict marker.
func drawASCTableCell(pdf *gopdf.GoPdf, opts Options, th ascTheme, cell Cell, ok bool, x, y, w, h float64, maxSubjLines int, conflictLW float64) {
	if !ok {
		ascFillRect(pdf, x, y, w, h, th.cellBg, th.line, 0.15)
		return
	}
	bg := opts.SubjectColor(cell.SubjectID)
	if opts.BW {
		bg = th.bwFill
	}
	if cell.Conflict {
		bg = th.conflictBg
	}
	ascFillRect(pdf, x, y, w, h, bg, th.line, 0.15)
	lines := ascCardLines(pdf, opts, th, cell, w, h, maxSubjLines)
	ascCenterLines(pdf, x, y, w, h, lines)
	if cell.Conflict {
		ascRectOutline(pdf, x+0.35, y+0.35, w-0.7, h-0.7, th.conflictLine, conflictLW)
	}
}
