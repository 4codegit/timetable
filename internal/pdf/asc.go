package pdf

import (
	"strings"

	"github.com/signintech/gopdf"
)

// ascTheme holds the aSc Timetables print palette. The colour version
// mimics the classic aSc printout: light steel-blue caption row, navy
// titles, white lesson cells with black-ish grid, red outlined conflicts.
// The BW variant keeps the same geometry but strips the hues.
type ascTheme struct {
	headBg    string // caption row (weekdays) background
	headText  string // caption row text
	bellBg    string // left "Урок" column background
	bellNum   string // period number colour
	bellTime  string // bell time colour
	cellBg    string // empty cell fill
	line      string // inner grid line
	frame     string // outer table frame
	cardText  string // subject name in a card
	cardMuted string // teacher / room lines in a card
	conflictBg, conflictText, conflictMuted, conflictLine string
	grayText  string // header/footer meta text
	ruleColor string // rule under the page title
	bwFill    string // lesson-cell fill in BW mode
}

func newTheme(bw bool) ascTheme {
	if bw {
		return ascTheme{
			headBg: "#e8e8e8", headText: "#111111",
			bellBg: "#f3f4f6", bellNum: "#111111", bellTime: "#444444",
			cellBg: "#ffffff", line: "#333333", frame: "#000000",
			cardText: "#111111", cardMuted: "#333333",
			conflictBg: "#d1d5db", conflictText: "#7f1d1d", conflictMuted: "#7f1d1d", conflictLine: "#7f1d1d",
			grayText: "#555555", ruleColor: "#333333", bwFill: "#ececec",
		}
	}
	return ascTheme{
		headBg: "#c9d7ee", headText: "#1f3864",
		bellBg: "#eef2fa", bellNum: "#1f3864", bellTime: "#5b6b8c",
		cellBg: "#ffffff", line: "#8a93a6", frame: "#2f3b52",
		cardText: "#17335e", cardMuted: "#4a5568",
		conflictBg: "#fdecea", conflictText: "#991b1b", conflictMuted: "#b91c1c", conflictLine: "#c0392b",
		grayText: "#6b7280", ruleColor: "#1f3864", bwFill: "#ececec",
	}
}

// ascLine is one line of text inside a centered text block.
type ascLine struct {
	Text  string
	Size  float64
	Bold  bool
	Color string
}

// ascFillRect paints a filled rectangle with an outline in one primitive.
func ascFillRect(pdf *gopdf.GoPdf, x, y, w, h float64, bg, lineColor string, lineW float64) {
	r, g, b := hexToRGB(bg)
	pdf.SetFillColor(r, g, b)
	lr, lg, lb := hexToRGB(lineColor)
	pdf.SetStrokeColor(lr, lg, lb)
	pdf.SetLineWidth(lineW)
	pdf.RectFromUpperLeftWithStyle(x, y, w, h, "DF")
}

// ascRectOutline strokes a rectangle without filling it.
func ascRectOutline(pdf *gopdf.GoPdf, x, y, w, h float64, color string, lineW float64) {
	r, g, b := hexToRGB(color)
	pdf.SetStrokeColor(r, g, b)
	pdf.SetLineWidth(lineW)
	pdf.RectFromUpperLeftWithStyle(x, y, w, h, "D")
}

func ascHLine(pdf *gopdf.GoPdf, x1, x2, y float64, color string, lineW float64) {
	r, g, b := hexToRGB(color)
	pdf.SetStrokeColor(r, g, b)
	pdf.SetLineWidth(lineW)
	pdf.Line(x1, y, x2, y)
}

func ascVLine(pdf *gopdf.GoPdf, x, y1, y2 float64, color string, lineW float64) {
	r, g, b := hexToRGB(color)
	pdf.SetStrokeColor(r, g, b)
	pdf.SetLineWidth(lineW)
	pdf.Line(x, y1, x, y2)
}

// ascCenterLines draws a block of lines centered horizontally within
// [x, x+w] and vertically within [y, y+h]. gopdf positions text by its
// top-left corner, so vertical centering uses the summed line heights.
func ascCenterLines(pdf *gopdf.GoPdf, x, y, w, h float64, lines []ascLine) {
	total := 0.0
	for _, ln := range lines {
		total += lineHeightMM(ln.Size)
	}
	ty := y + (h-total)/2
	if ty < y+0.3 {
		ty = y + 0.3
	}
	for _, ln := range lines {
		if ln.Text == "" {
			continue
		}
		fam := "DejaVu"
		if ln.Bold {
			fam = "DejaVu-Bold"
		}
		setFont(pdf, fam, ln.Size)
		r, g, b := hexToRGB(ln.Color)
		pdf.SetTextColor(r, g, b)
		lw := textWidthMM(pdf, ln.Text)
		if lw > w-0.4 {
			// Should not happen (wrapping ellipsizes to fit), but if it
			// does, left-anchor instead of spilling out on both sides.
			pdf.SetX(x + 0.2)
		} else {
			pdf.SetX(x + (w-lw)/2)
		}
		pdf.SetY(ty)
		_ = pdf.Text(ln.Text)
		ty += lineHeightMM(ln.Size)
	}
}

// ascWrapFit word-wraps text to maxW at the given font/size (the font is
// set here, because wrapping is measured with the current font).
func ascWrapFit(pdf *gopdf.GoPdf, text string, maxW float64, maxLines int, fam string, size float64) []string {
	setFont(pdf, fam, size)
	return wrapTextMM(pdf, text, maxW, maxLines)
}

// ascFitOne fits a single line into maxW, ellipsizing if needed.
func ascFitOne(pdf *gopdf.GoPdf, s string, maxW float64, fam string, size float64) string {
	setFont(pdf, fam, size)
	return truncate(pdf, s, maxW)
}

// ascCardLines builds the centered text block of one lesson card in the
// aSc manner: subject (bold) on top, teacher and room in a smaller muted
// face below. The font size is chosen by shrinking until the whole block
// fits boxH; if nothing fits, the subject alone is printed ellipsized.
func ascCardLines(pdf *gopdf.GoPdf, opts Options, th ascTheme, cell Cell, boxW, boxH float64, maxSubjLines int) []ascLine {
	subj := opts.SubjectName(cell.SubjectID)
	if subj == "" {
		subj = "?"
	}
	teacher, room := "", ""
	if opts.ShowTeacher {
		teacher = opts.TeacherName(cell.TeacherID)
	}
	if opts.ShowRoom {
		if rn := opts.RoomName(cell.RoomID); rn != "" && rn != "?" {
			room = rn
		}
	}
	subjColor, mutColor := th.cardText, th.cardMuted
	if cell.Conflict {
		subjColor, mutColor = th.conflictText, th.conflictMuted
	}

	padW := boxW - 2.0
	// Generous starting size: the shrink loop below (drop room line, then
	// teacher, then shrink) is what actually finds the best fit, so the
	// ceiling only has to keep single lines from being absurd.
	hi := clampF(minF(10.5, boxH*0.75, boxW*0.45), 4.5, 10.5)
	lo := 4.5
	var fallback *[]ascLine

	var cand [][]string
	if teacher != "" && room != "" {
		cand = append(cand, []string{teacher, room})
	}
	if teacher != "" {
		cand = append(cand, []string{teacher})
	}
	if room != "" {
		cand = append(cand, []string{room})
	}
	cand = append(cand, nil)

	for size := hi; size >= lo; size -= 0.5 {
		subLines := ascWrapFit(pdf, subj, padW, maxSubjLines, "DejaVu-Bold", size)
		small := clampF(size*0.72, 4.0, 8.0)
		for _, extra := range cand {
			extras := make([]ascLine, 0, len(extra))
			for _, e := range extra {
				extras = append(extras, ascLine{Text: ascFitOne(pdf, e, padW, "DejaVu", small), Size: small, Color: mutColor})
			}
			total := float64(len(subLines))*lineHeightMM(size) + float64(len(extras))*lineHeightMM(small)
			if total <= boxH-1.0 {
				out := make([]ascLine, 0, len(subLines)+len(extras))
				for _, sl := range subLines {
					out = append(out, ascLine{Text: sl, Size: size, Bold: true, Color: subjColor})
				}
				out = append(out, extras...)
				// An ellipsized subject at a big size reads worse than the
				// whole word slightly smaller, so if this size had to cut
				// the subject mid-word, keep scanning for a size where it
				// fits whole (but only a little smaller — 75% of the start).
				if !strings.Contains(subLines[len(subLines)-1], "…") || size < maxF(lo, hi*0.75) {
					return out
				}
				if fallback == nil {
					cp := append([]ascLine(nil), out...)
					fallback = &cp
				}
			}
		}
	}
	if fallback != nil {
		return *fallback
	}
	return []ascLine{{Text: ascFitOne(pdf, subj, padW, "DejaVu-Bold", lo), Size: lo, Bold: true, Color: subjColor}}
}

// ascPageHeader draws the aSc-style page header: school name top-left,
// print date top-right, the big bold title centered, an optional muted
// subtitle under it, and a rule across the page. Returns the Y where the
// table may start.
func ascPageHeader(pdf *gopdf.GoPdf, opts Options, th ascTheme, bigTitle, subtitle string, pageW, margin float64) float64 {
	y := margin
	setFont(pdf, "DejaVu", 8)
	r, g, b := hexToRGB(th.grayText)
	pdf.SetTextColor(r, g, b)
	pdf.SetX(margin)
	pdf.SetY(y)
	_ = pdf.Text(truncate(pdf, opts.SchoolName, pageW*0.55))

	tSize := 15.0
	setFont(pdf, "DejaVu-Bold", tSize)
	tr, tg, tb := hexToRGB(th.headText)
	pdf.SetTextColor(tr, tg, tb)
	tw := textWidthMM(pdf, bigTitle)
	pdf.SetX((pageW - tw) / 2)
	pdf.SetY(y + 4.4)
	_ = pdf.Text(bigTitle)

	if subtitle != "" {
		setFont(pdf, "DejaVu", 8.5)
		g2r, g2g, g2b := hexToRGB(th.grayText)
		pdf.SetTextColor(g2r, g2g, g2b)
		sw := textWidthMM(pdf, subtitle)
		pdf.SetX((pageW - sw) / 2)
		pdf.SetY(y + 10.9)
		_ = pdf.Text(subtitle)
	}

	ruleY := y + 15.4
	ascHLine(pdf, margin, pageW-margin, ruleY, th.ruleColor, 0.45)
	return ruleY + 2.8
}

// ascPageFooter prints the print date at the left and "стр. N из M" at
// the right, below the content area.
func ascPageFooter(pdf *gopdf.GoPdf, opts Options, th ascTheme, pageW, pageH, margin float64, pageNo, total int) {
	y := pageH - margin + 1.6
	setFont(pdf, "DejaVu", 8)
	r, g, b := hexToRGB(th.grayText)
	pdf.SetTextColor(r, g, b)
	if opts.GeneratedOn != "" {
		pdf.SetX(margin)
		pdf.SetY(y)
		_ = pdf.Text("Отпечатано: " + opts.GeneratedOn)
	}
	if total > 0 {
		right := "стр. " + itoa(pageNo) + " из " + itoa(total)
		w := textWidthMM(pdf, right)
		pdf.SetX(pageW - margin - w)
		pdf.SetY(y)
		_ = pdf.Text(right)
	}
}

// itoa is a tiny helper to keep footer formatting allocation-light.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var digits [20]byte
	i := len(digits)
	for n > 0 {
		i--
		digits[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		digits[i] = '-'
	}
	return string(digits[i:])
}
