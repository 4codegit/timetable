package pdf

import (
	"github.com/signintech/gopdf"
)

// renderSchoolPoster prints the whole school as a sheet of per-class
// mini-tables — the aSc Timetables "timetable for all classes" look —
// with the subject-colour legend and the conflict list under the last
// page of tables.
func renderSchoolPoster(pdf *gopdf.GoPdf, opts Options, th ascTheme, daysN int, pageW, pageH float64) {
	const (
		margin    = 10.0
		footerH   = 7.0
		gap       = 6.0
		miniLeft  = 9.0
		captionH  = 6.0
		miniHdr   = 6.5
	)

	n := len(opts.Rows)
	subtitle := "классов: " + itoa(n) + " · дней: " + itoa(daysN) + " · уроков в день: " + itoa(opts.Slots)
	if len(opts.Conflicts) > 0 {
		subtitle += " · конфликтов: " + itoa(len(opts.Conflicts))
	}

	// The legend/conflict zone is measured up front so every page leaves
	// room for it (it is drawn only under the last page of tables).
	legendH := flowLegendAndConflicts(pdf, opts, th, margin, 0, pageW, false) + 2.0

	if n == 0 {
		tableTop := ascPageHeader(pdf, opts, th, opts.Title, subtitle, pageW, margin)
		flowLegendAndConflicts(pdf, opts, th, margin, tableTop, pageW, true)
		return
	}

	tableTop := ascPageHeader(pdf, opts, th, opts.Title, subtitle, pageW, margin)
	availW := pageW - margin*2
	availH := pageH - tableTop - margin - footerH - legendH

	// Pick the mini-table grid (cols × tables-per-column). Priority:
	// (1) fit every class on as few pages as possible — the export is a
	// poster, one sheet if at all feasible; (2) among grids with that
	// page count, maximize the smaller of (day column width, period row
	// height); (3) ties prefer more columns, i.e. a denser aSc-like sheet.
	type gridCand struct {
		cols, perCol, pages int
		score               float64
	}
	var cands []gridCand
	for cols := 1; cols <= 4; cols++ {
		dayW := (availW - float64(cols)*miniLeft - float64(cols-1)*gap) / float64(cols*daysN)
		if dayW < 10 {
			break
		}
		for perCol := 1; perCol <= n; perCol++ {
			// Subtract the inter-table vertical gap so a full column of
			// tables (perCol × miniH + (perCol-1) × gap) never overflows
			// the available height.
			rowH := (availH/float64(perCol) - captionH - miniHdr - gap) / float64(opts.Slots)
			if rowH < 3.5 {
				continue
			}
			capacity := cols * perCol
			cands = append(cands, gridCand{
				cols:   cols,
				perCol: perCol,
				pages:  (n + capacity - 1) / capacity,
				score:  minF(dayW, rowH),
			})
		}
	}
	if len(cands) == 0 {
		// Degenerate page (huge slot count): stack tables single-file and
		// let rows shrink as needed rather than draw nothing.
		cands = append(cands, gridCand{cols: 1, perCol: 1, pages: n, score: 3.5})
	}
	best := cands[0]
	for _, c := range cands[1:] {
		if c.pages < best.pages ||
			(c.pages == best.pages && c.score > best.score+1e-9) ||
			(c.pages == best.pages && c.score > best.score-1e-9 && c.cols > best.cols) {
			best = c
		}
	}

	cols, perCol := best.cols, best.perCol
	dayW := (availW - float64(cols)*miniLeft - float64(cols-1)*gap) / float64(cols*daysN)
	rowH := (availH/float64(perCol) - captionH - miniHdr - gap) / float64(opts.Slots)
	miniW := miniLeft + dayW*float64(daysN)
	miniH := captionH + miniHdr + rowH*float64(opts.Slots)
	perPage := cols * perCol
	totalPages := (n + perPage - 1) / perPage

	tableW := float64(cols)*miniW + float64(cols-1)*gap
	startX := margin + (availW-tableW)/2

	maxSubj := 2
	if rowH < 7 {
		maxSubj = 1
	}

	for tp := 0; tp < totalPages; tp++ {
		if tp > 0 {
			pdf.AddPage()
		}
		for i := 0; i < perPage; i++ {
			idx := tp*perPage + i
			if idx >= n {
				break
			}
			tc, tr := i%cols, i/cols
			x0 := startX + float64(tc)*(miniW+gap)
			y0 := tableTop + float64(tr)*(miniH+gap)
			drawASCMiniTable(pdf, opts, th, opts.Rows[idx], x0, y0, miniW, dayW, rowH, daysN, opts.Slots, maxSubj)
		}
		if tp == totalPages-1 {
			zoneTop := pageH - margin - footerH - legendH
			flowLegendAndConflicts(pdf, opts, th, margin, zoneTop, pageW, true)
		}
		ascPageFooter(pdf, opts, th, pageW, pageH, margin, tp+1, totalPages)
	}
}

// drawASCMiniTable draws one class table: bold caption, light-blue
// weekday caption row, numbered period column and lesson cards.
func drawASCMiniTable(pdf *gopdf.GoPdf, opts Options, th ascTheme, row Row, x0, y0, miniW, dayW, rowH float64, daysN, slots, maxSubj int) {
	const (
		miniLeft = 9.0
		captionH = 6.0
		miniHdr  = 6.5
	)

	setFont(pdf, "DejaVu-Bold", clampF(minF(10, captionH*1.1), 7, 10))
	r, g, b := hexToRGB(th.headText)
	pdf.SetTextColor(r, g, b)
	pdf.SetX(x0)
	pdf.SetY(y0)
	_ = pdf.Text(truncate(pdf, row.Label, miniW))

	tableY := y0 + captionH
	ascFillRect(pdf, x0, tableY, miniW, miniHdr, th.headBg, th.line, 0.15)
	daySize := clampF(minF(7.5, miniHdr*0.8, dayW*0.4), 4.5, 7.5)
	for di := 0; di < daysN; di++ {
		ascCenterLines(pdf, x0+miniLeft+float64(di)*dayW, tableY, dayW, miniHdr, []ascLine{
			{Text: dayName(di), Size: daySize, Bold: true, Color: th.headText},
		})
	}

	gridY := tableY + miniHdr
	bottomY := gridY + rowH*float64(slots)
	for si := 0; si < slots; si++ {
		y := gridY + float64(si)*rowH
		ascFillRect(pdf, x0, y, miniLeft, rowH, th.bellBg, th.line, 0.12)
		bell := []ascLine{
			{Text: itoa(si+1), Size: clampF(minF(8, rowH*0.4), 4.5, 8), Bold: true, Color: th.bellNum},
		}
		if rowH >= 9.5 && si < len(opts.Periods) && opts.Periods[si].Start != "" {
			bell = append(bell, ascLine{Text: opts.Periods[si].Start, Size: clampF(minF(5, rowH*0.16), 3.5, 5), Color: th.bellTime})
		}
		ascCenterLines(pdf, x0, y, miniLeft, rowH, bell)

		for di := 0; di < daysN; di++ {
			cell, ok := opts.CellAt(row.ID, di, si)
			drawASCTableCell(pdf, opts, th, cell, ok,
				x0+miniLeft+float64(di)*dayW, y, dayW, rowH, maxSubj, 0.4)
		}
	}

	ascVLine(pdf, x0+miniLeft, tableY, bottomY, th.line, 0.12)
	for di := 1; di < daysN; di++ {
		ascVLine(pdf, x0+miniLeft+float64(di)*dayW, tableY, bottomY, th.line, 0.12)
	}
	ascHLine(pdf, x0, x0+miniW, gridY, th.line, 0.12)
	for si := 1; si < slots; si++ {
		ascHLine(pdf, x0, x0+miniW, gridY+float64(si)*rowH, th.line, 0.12)
	}
	ascRectOutline(pdf, x0, tableY, miniW, miniHdr+rowH*float64(slots), th.frame, 0.45)
}

// flowLegendAndConflicts lays out (and, with draw=true, prints) the
// colour legend followed by the conflict list. Running it with draw=false
// measures the zone height, so the poster layout can reserve space.
func flowLegendAndConflicts(pdf *gopdf.GoPdf, opts Options, th ascTheme, margin, yStart, pageW float64, draw bool) float64 {
	y := yStart
	txtR, txtG, txtB := hexToRGB(th.cardText)
	grayR, grayG, grayB := hexToRGB(th.grayText)

	if len(opts.LegendSubjects) > 0 {
		if draw {
			setFont(pdf, "DejaVu", 8)
			pdf.SetTextColor(txtR, txtG, txtB)
			pdf.SetX(margin)
			pdf.SetY(y)
			_ = pdf.Text("Легенда:")
		}
		x := margin + 16
		ly := y
		for _, item := range opts.LegendSubjects {
			setFont(pdf, "DejaVu", 7)
			name := truncate(pdf, item.Name, 45)
			nw := textWidthMM(pdf, name)
			if x+4.0+nw > pageW-margin-4 && x > margin+16 {
				x = margin + 16
				ly += 4.6
			}
			if draw {
				bg := opts.SubjectColor(item.SubjectID)
				if opts.BW {
					bg = th.bwFill
				}
				cr, cg, cb := hexToRGB(bg)
				pdf.SetFillColor(cr, cg, cb)
				lr, lg, lb := hexToRGB(th.line)
				pdf.SetStrokeColor(lr, lg, lb)
				pdf.SetLineWidth(0.12)
				pdf.RectFromUpperLeftWithStyle(x, ly+0.8, 3.2, 3.2, "DF")
				setFont(pdf, "DejaVu", 7)
				pdf.SetTextColor(txtR, txtG, txtB)
				pdf.SetX(x + 4.0)
				pdf.SetY(ly)
				_ = pdf.Text(name)
			}
			x += 4.0 + nw + 4.5
		}
		y = ly + 4.6
	}

	if len(opts.Conflicts) > 0 {
		y += 1.5
		if draw {
			setFont(pdf, "DejaVu", 8)
			cr, cg, cb := hexToRGB(th.conflictText)
			pdf.SetTextColor(cr, cg, cb)
			pdf.SetX(margin)
			pdf.SetY(y)
			_ = pdf.Text("Конфликты (" + itoa(len(opts.Conflicts)) + "):")
		}
		y += 3.6
		for i, c := range opts.Conflicts {
			if i >= 24 {
				if draw {
					setFont(pdf, "DejaVu", 7)
					pdf.SetTextColor(grayR, grayG, grayB)
					pdf.SetX(margin)
					pdf.SetY(y)
					_ = pdf.Text("…")
				}
				y += 3.4
				break
			}
			if draw {
				setFont(pdf, "DejaVu", 7)
				mr, mg, mb := hexToRGB(th.conflictMuted)
				pdf.SetTextColor(mr, mg, mb)
				pdf.SetX(margin)
				pdf.SetY(y)
				_ = pdf.Text(truncate(pdf, c.Text, pageW*0.6))
			}
			y += 3.4
		}
	}
	return y - yStart
}
