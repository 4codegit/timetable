// Package pdf renders the school timetable to a PDF using a pure-Go PDF
// library and an embedded Unicode (Cyrillic-capable) font. Moving PDF
// generation off the frontend (which used jsPDF inside the Wails WebKit
// webview, where datauristring output was unreliable and the default
// fonts lacked Cyrillic glyphs) eliminates a whole class of export bugs.
//
// v1.8.0 quality improvements:
//   - slot header row (П1..Пn + bell times) in the "school" poster mode,
//     which previously had NO column captions at all;
//   - full grid borders on every cell (filled cells used to float on the
//     page with no outline);
//   - measured word-wrapping (MeasureTextWidth) instead of the "~1.6 mm per
//     character" guess, with a clean ellipsis when a cell label is cut;
//   - vertically centered cell text and an adaptive font size, so posters
//     with 35+ classes stay readable instead of overflowing;
//   - page numbers ("стр. 3 из 35") and a print-date footer on every page
//     of the per-class / per-teacher / per-room modes;
//   - a tidier two-dimensional legend flow at the bottom of the poster.
package pdf

import (
	"bytes"
	_ "embed"
	"fmt"
	"sort"
	"strings"

	"github.com/signintech/gopdf"
)

//go:embed fonts/DejaVuSans.ttf
var fontRegular []byte

//go:embed fonts/DejaVuSans-Bold.ttf
var fontBold []byte

// Row is one "row" of the timetable grid: a class, a teacher, or a room.
// The Label is what gets printed at the left of the row.
type Row struct {
	ID    int
	Label string
}

// Cell is the contents of one timetable cell.
type Cell struct {
	SubjectID int
	TeacherID int
	RoomID    int
	Conflict  bool
}

// Options is the full description of one PDF export request.
//
// All strings are already user-facing (the caller is responsible for any
// localization). The PDF library only does layout.
type Options struct {
	SchoolName string
	Title      string // e.g. "по классам", "вся школа"

	// Grid geometry.
	Days  int // total days in week (1..7)
	Slots int // total slots per day

	// Bell schedule (length == Slots). Empty start/end means "no label".
	Periods []Period

	// What to render.
	Mode string // "school" (all rows on poster), "class", "teacher", "room"
	Rows []Row

	// Lookup of a cell by (rowID, day, slot) -> Cell. Return ok=false for empty.
	CellAt func(rowID, day, slot int) (Cell, bool)

	// Display flags (mirror the frontend checkboxes).
	ShowTeacher  bool
	ShowRoom     bool
	WeekdaysOnly bool // hide Sat/Sun even if Days > 5
	BW           bool // black & white: gray fill for any subject, red for conflicts

	// Page setup.
	PageSize    string // "A0".."A4"
	Orientation string // "landscape" or "portrait"

	// Label resolvers — the caller passes these in so the PDF package
	// does not depend on the domain layer.
	SubjectName  func(id int) string
	TeacherName  func(id int) string // short name preferred by caller
	RoomName     func(id int) string
	SubjectColor func(id int) string // hex like "#dbeafe"

	// Legend: subjects that actually appear on the schedule. Caller
	// precomputes this list so the PDF package does not have to scan the
	// whole schedule again.
	LegendSubjects []LegendItem

	// Conflict list (cell label + day + slot), pre-computed by the caller.
	// Rendered at the bottom of the "school" poster mode.
	Conflicts []ConflictLine

	// GeneratedOn is a human-readable date (e.g. "05.09.2026") printed in
	// the page footer. Empty string = no footer date.
	GeneratedOn string
}

// Period is one bell slot.
type Period struct {
	Start string // "08:00" or ""
	End   string // "08:45" or ""
}

// LegendItem is one entry in the colour legend.
type LegendItem struct {
	SubjectID int
	Name      string
}

// ConflictLine is one printed conflict entry.
type ConflictLine struct {
	Text string
}

// Render produces the PDF bytes for the given options.
func Render(opts Options) ([]byte, error) {
	if err := validate(&opts); err != nil {
		return nil, err
	}

	wMM := pageWidthMM(opts.PageSize, opts.Orientation)
	hMM := pageHeightMM(opts.PageSize, opts.Orientation)

	pdf := gopdf.GoPdf{}
	pdf.Start(gopdf.Config{
		Unit:     gopdf.UnitMM,
		PageSize: gopdf.Rect{W: wMM, H: hMM},
	})

	// Add the regular and bold faces. TTF bytes are embedded into the
	// binary via go:embed above, so the .exe/AppImage has no external
	// font dependency.
	if err := pdf.AddTTFFontDataWithOption("DejaVu", fontRegular, gopdf.TtfOption{Style: gopdf.Regular}); err != nil {
		return nil, fmt.Errorf("add regular font: %w", err)
	}
	// Register the bold face as its OWN family with the default (regular)
	// style flag. gopdf matches SetFont(family, "", size) against fonts whose
	// TtfOption.Style == Regular — a bold face registered with Style: Bold can
	// never be selected through SetFont and SetFont silently fails with
	// ErrMissingFontFamily. (The old renderer loaded the bold face with
	// Style: Bold and never actually used it, which is why this never blew up
	// before.)
	if err := pdf.AddTTFFontDataWithOption("DejaVu-Bold", fontBold, gopdf.TtfOption{}); err != nil {
		return nil, fmt.Errorf("add bold font: %w", err)
	}
	// Create the first page so SetFont / Text have a current page to
	// write into. (renderOnePerPage calls AddPage itself for each row,
	// but it skips the FIRST row; so we add the first page here once
	// and let renderOnePerPage reuse it.)
	pdf.AddPage()
	if err := pdf.SetFont("DejaVu", "", 11); err != nil {
		return nil, fmt.Errorf("set regular font: %w", err)
	}

	daysN := opts.Days
	if opts.WeekdaysOnly && daysN > 5 {
		daysN = 5
	}

	switch opts.Mode {
	case "school":
		renderSchoolPoster(&pdf, opts, daysN, wMM, hMM)
	case "class", "teacher", "room":
		renderOnePerPage(&pdf, opts, daysN, wMM, hMM)
	default:
		return nil, fmt.Errorf("unknown mode %q", opts.Mode)
	}

	var buf bytes.Buffer
	if err := pdf.Write(&buf); err != nil {
		return nil, fmt.Errorf("write pdf: %w", err)
	}
	return buf.Bytes(), nil
}

func validate(o *Options) error {
	if o.Days <= 0 || o.Days > 7 {
		return fmt.Errorf("Days must be 1..7, got %d", o.Days)
	}
	if o.Slots <= 0 || o.Slots > 14 {
		return fmt.Errorf("Slots must be 1..14, got %d", o.Slots)
	}
	if o.CellAt == nil {
		return fmt.Errorf("CellAt is required")
	}
	if o.SubjectName == nil || o.TeacherName == nil || o.RoomName == nil {
		return fmt.Errorf("SubjectName/TeacherName/RoomName are required")
	}
	if o.SubjectColor == nil {
		o.SubjectColor = func(int) string { return "#e5e7eb" }
	}
	if len(o.Periods) != o.Slots {
		// Tolerate missing periods; just don't print bell labels.
		o.Periods = make([]Period, o.Slots)
	}
	return nil
}

// pageWidthMM/pageHeightMM return the W/H pair for the chosen page in mm.
// gopdf uses millimeters as the unit (UnitMM) throughout.
func pageWidthMM(size, orient string) float64 {
	w, h := paperSizeMM(size)
	if orient == "landscape" {
		w, h = h, w
	}
	return w
}

func pageHeightMM(size, orient string) float64 {
	w, h := paperSizeMM(size)
	if orient == "landscape" {
		w, h = h, w
	}
	return h
}

// paperSizeMM returns (W, H) in millimeters for the given ISO A-series page.
func paperSizeMM(size string) (float64, float64) {
	switch strings.ToUpper(size) {
	case "A0":
		return 841, 1189
	case "A1":
		return 594, 841
	case "A2":
		return 420, 594
	case "A3":
		return 297, 420
	case "A4":
		return 210, 297
	default:
		return 297, 420 // A3 default — same as the old jsPDF default
	}
}

// mmToPt converts millimeters to PDF points (1 pt = 1/72 inch; 1 inch = 25.4 mm).
// Only used when we need a point measurement (e.g. MeasureTextWidth returns pt).
func mmToPt(mm float64) float64 {
	return mm * 72.0 / 25.4
}

// ptToMM converts points to millimeters.
func ptToMM(pt float64) float64 {
	return pt * 25.4 / 72.0
}

// lineHeightMM is the vertical advance (in mm) for one line of `size` pt
// text, including comfortable leading.
func lineHeightMM(size float64) float64 {
	return float64(size) * 0.3528 * 1.18
}

// drawCell paints one timetable cell: fill (if bg != ""), border, and a
// vertically centered, left-padded block of pre-wrapped lines. textColor is
// used for every line. The font must already be sized appropriately; `size`
// here is only used to compute the line height.
func drawCell(pdf *gopdf.GoPdf, x, y, w, h float64, bg string, lines []string, size float64, tr, tg, tb uint8) {
	// Fill + border in one primitive when the cell has a background.
	if bg != "" {
		r, g, b := hexToRGB(bg)
		pdf.SetFillColor(r, g, b)
		pdf.SetStrokeColor(120, 130, 145)
		pdf.SetLineWidth(0.12)
		pdf.RectFromUpperLeftWithStyle(x, y, w, h, "DF")
	} else {
		pdf.SetStrokeColor(120, 130, 145)
		pdf.SetLineWidth(0.12)
		pdf.RectFromUpperLeftWithStyle(x, y, w, h, "D")
	}
	if len(lines) == 0 {
		return
	}
	lineH := lineHeightMM(size)
	totalH := lineH * float64(len(lines))
	// Vertically center; gopdf draws text with Y = top of the glyphs.
	ty := y + (h-totalH)/2
	setText(pdf, tr, tg, tb, size)
	for _, ln := range lines {
		pdf.SetX(x + 0.8)
		pdf.SetY(ty)
		_ = pdf.Text(ln)
		ty += lineH
	}
}

// renderSchoolPoster lays every row (typically every class) on ONE big
// page with a colour legend at the bottom. This matches the existing
// "вся школа (плакат)" export mode.
func renderSchoolPoster(pdf *gopdf.GoPdf, opts Options, daysN int, pageW, pageH float64) {
	margin := 8.0
	labelW := 24.0
	gridX := margin + labelW
	titleH := 12.0
	slotHdrH := 5.0
	topY := margin + titleH + slotHdrH

	// Reserve bottom space for the legend (and conflict list if present).
	legendH := 14.0
	conflictsH := 0.0
	if len(opts.Conflicts) > 0 {
		conflictsH = 6.0 + float64(min(len(opts.Conflicts), 40))*3.2
	}
	gridH := pageH - topY - margin - legendH - conflictsH

	rowsCount := len(opts.Rows)
	if rowsCount <= 0 {
		return
	}
	rowH := gridH / float64(rowsCount)
	colW := (pageW - gridX - margin) / float64(opts.Slots)
	dayH := rowH / float64(daysN)

	// Adaptive cell font: keep text inside the cells for both big posters
	// (A0, 5 classes) and dense ones (A2, 35 classes).
	cellFont := clampF(minF(6.5, dayH*1.05, colW*0.42), 3.0, 6.5)
	lineH := lineHeightMM(cellFont)
	maxLines := int(dayH / lineH)
	if maxLines < 1 {
		maxLines = 1
	}

	// Title + meta line.
	setFont(pdf, "DejaVu-Bold", 14)
	setText(pdf, 20, 20, 20, 14)
	pdf.SetX(margin)
	pdf.SetY(margin)
	_ = pdf.Text(opts.SchoolName + " · " + opts.Title)
	setFont(pdf, "DejaVu", 8)
	setText(pdf, 100, 106, 120, 8)
	pdf.SetX(margin)
	pdf.SetY(margin + 5.2)
	meta := fmt.Sprintf("дней: %d · уроков в день: %d", daysN, opts.Slots)
	if opts.GeneratedOn != "" {
		meta += " · напечатано: " + opts.GeneratedOn
	}
	_ = pdf.Text(meta)

	// Slot header row (П1..Пn with bell times) above the grid.
	setText(pdf, 90, 96, 112, clampF(minF(7.5, colW*0.45), 4.5, 7.5))
	for si := 0; si < opts.Slots; si++ {
		x := gridX + float64(si)*colW
		lbl := slotLabel(si, opts.Periods)
		pdf.SetX(x + 0.5)
		pdf.SetY(topY - 3.8)
		_ = pdf.Text(truncate(pdf, lbl, colW-1))
	}

	for ri, row := range opts.Rows {
		rowTop := topY + float64(ri)*rowH

		// Row label (left column, rotated text is not supported by gopdf
		// so we print the label horizontally at the top of the row block).
		setText(pdf, 25, 28, 38, clampF(minF(9, rowH*0.6), 5, 9))
		pdf.SetX(margin)
		pdf.SetY(rowTop + 0.5)
		_ = pdf.Text(truncate(pdf, row.Label, labelW-1.5))

		for di := 0; di < daysN; di++ {
			dayTop := rowTop + float64(di)*dayH
			// Day label (left of each day row).
			setText(pdf, 120, 126, 140, clampF(minF(7, dayH*0.85), 3.4, 7))
			pdf.SetX(margin)
			pdf.SetY(dayTop + dayH/2 - lineHeightMM(minF(7, dayH*0.85))/2)
			_ = pdf.Text(dayName(di))

			for si := 0; si < opts.Slots; si++ {
				cellX := gridX + float64(si)*colW
				cell, ok := opts.CellAt(row.ID, di, si)
				if !ok {
					// Empty cell — border only.
					drawCell(pdf, cellX, dayTop, colW, dayH, "", nil, cellFont, 20, 20, 20)
					continue
				}
				bg := opts.SubjectColor(cell.SubjectID)
				textRGB := [3]uint8{20, 20, 20}
				if cell.Conflict {
					bg = "#b91c1c"
					textRGB = [3]uint8{255, 255, 255}
				}
				if opts.BW && !cell.Conflict {
					bg = "#e5e7eb"
				}
				// Font must be set before measuring the wrap.
				setText(pdf, textRGB[0], textRGB[1], textRGB[2], cellFont)
				lines := wrapCellText(pdf, opts, cell, colW-1.6, maxLines)
				drawCell(pdf, cellX, dayTop, colW, dayH, bg, lines, cellFont, textRGB[0], textRGB[1], textRGB[2])
			}
		}
	}

	// Legend at the bottom.
	legendY := pageH - margin - conflictsH - 2
	setText(pdf, 20, 20, 20, 8)
	pdf.SetX(margin)
	pdf.SetY(legendY)
	_ = pdf.Text("Легенда:")
	x := margin + 16
	for _, item := range opts.LegendSubjects {
		name := item.Name
		bg := opts.SubjectColor(item.SubjectID)
		if opts.BW {
			bg = "#e5e7eb"
		}
		r, g, b := hexToRGB(bg)
		pdf.SetFillColor(r, g, b)
		pdf.SetStrokeColor(120, 130, 145)
		pdf.SetLineWidth(0.1)
		pdf.RectFromUpperLeftWithStyle(x, legendY-2.2, 3.2, 3.2, "DF")
		setText(pdf, 20, 20, 20, 7)
		name = truncate(pdf, name, 45)
		pdf.SetX(x + 4.0)
		pdf.SetY(legendY)
		_ = pdf.Text(name)
		x += 4.0 + textWidthMM(pdf, name) + 4.5
		if x > pageW-24 {
			x = margin + 16
			legendY -= 4.6
		}
	}

	// Conflict list (if any) below the legend.
	if len(opts.Conflicts) > 0 {
		cy := legendY + 5
		setText(pdf, 0x80, 0x10, 0x10, 8)
		pdf.SetX(margin)
		pdf.SetY(cy)
		_ = pdf.Text(fmt.Sprintf("Конфликты (%d):", len(opts.Conflicts)))
		cy += 3.4
		setText(pdf, 20, 20, 20, 7)
		for i, c := range opts.Conflicts {
			if i >= 40 {
				pdf.SetX(margin)
				pdf.SetY(cy)
				_ = pdf.Text("…")
				break
			}
			pdf.SetX(margin)
			pdf.SetY(cy)
			_ = pdf.Text(truncate(pdf, c.Text, pageW/2))
			cy += 3.2
		}
	}
}

// renderOnePerPage puts each row on its own page. Matches the existing
// "по классам (отд. стр.)" / "по учителям" / "по кабинетам" modes.
func renderOnePerPage(pdf *gopdf.GoPdf, opts Options, daysN int, pageW, pageH float64) {
	margin := 10.0
	labelW := 20.0
	gridX := margin + labelW
	titleH := 14.0
	slotHdrH := 6.0
	footerH := 6.0
	topY := margin + titleH + slotHdrH
	gridW := pageW - gridX - margin
	gridH := pageH - topY - margin - footerH
	colW := gridW / float64(opts.Slots)
	rowH := gridH / float64(daysN)

	// Adaptive cell font: one row per page gives plenty of room.
	cellFont := clampF(minF(9.5, colW*0.5, rowH*0.32), 5, 9.5)
	lineH := lineHeightMM(cellFont)
	maxLines := int(rowH/lineH) - 1
	if maxLines < 2 {
		maxLines = 2
	}
	total := len(opts.Rows)

	first := true
	pageNo := 0
	for _, row := range opts.Rows {
		if !first {
			pdf.AddPage()
		}
		first = false
		pageNo++

		// Header: the row name is the star of the page.
		setFont(pdf, "DejaVu-Bold", 16)
		setText(pdf, 15, 18, 30, 16)
		pdf.SetX(margin)
		pdf.SetY(margin)
		_ = pdf.Text(row.Label)
		setFont(pdf, "DejaVu", 9)
		setText(pdf, 100, 106, 120, 9)
		pdf.SetX(margin)
		pdf.SetY(margin + 6.5)
		sub := opts.SchoolName + " · " + opts.Title
		if opts.GeneratedOn != "" {
			sub += " · " + opts.GeneratedOn
		}
		_ = pdf.Text(sub)

		// Slot header row.
		for si := 0; si < opts.Slots; si++ {
			x := gridX + float64(si)*colW
			setText(pdf, 70, 76, 95, clampF(minF(9, colW*0.42), 5, 9))
			pdf.SetX(x + 0.5)
			pdf.SetY(topY - 4.5)
			_ = pdf.Text(truncate(pdf, slotLabel(si, opts.Periods), colW-1))
		}

		// Day rows.
		for di := 0; di < daysN; di++ {
			y := topY + float64(di)*rowH
			// Day label.
			setText(pdf, 40, 45, 60, clampF(minF(10, rowH*0.28), 6, 10))
			pdf.SetX(margin)
			pdf.SetY(y + rowH/2 - lineHeightMM(minF(10, rowH*0.28))/2)
			_ = pdf.Text(dayName(di))
			for si := 0; si < opts.Slots; si++ {
				x := gridX + float64(si)*colW
				cell, ok := opts.CellAt(row.ID, di, si)
				if !ok {
					drawCell(pdf, x, y, colW, rowH, "", nil, cellFont, 20, 20, 20)
					continue
				}
				bg := opts.SubjectColor(cell.SubjectID)
				textRGB := [3]uint8{20, 20, 20}
				if cell.Conflict {
					bg = "#b91c1c"
					textRGB = [3]uint8{255, 255, 255}
				}
				if opts.BW && !cell.Conflict {
					bg = "#e5e7eb"
				}
				setText(pdf, textRGB[0], textRGB[1], textRGB[2], cellFont)
				lines := wrapCellText(pdf, opts, cell, colW-1.8, maxLines)
				drawCell(pdf, x, y, colW, rowH, bg, lines, cellFont, textRGB[0], textRGB[1], textRGB[2])
			}
		}

		// Footer: page numbers + date, right-aligned.
		if total > 0 {
			foot := fmt.Sprintf("стр. %d из %d", pageNo, total)
			setText(pdf, 130, 136, 150, 7.5)
			wMM := textWidthMM(pdf, foot)
			pdf.SetX(pageW - margin - wMM)
			pdf.SetY(pageH - margin - 2)
			_ = pdf.Text(foot)
		}
	}
}

// minF returns the smallest of its arguments.
func minF(vals ...float64) float64 {
	m := vals[0]
	for _, v := range vals[1:] {
		if v < m {
			m = v
		}
	}
	return m
}

// clampF constrains v to [lo, hi].
func clampF(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// setFont switches the font family/size, ignoring lookup errors (the
// previous face stays active — a missing family must never truncate the
// whole document mid-render).
func setFont(pdf *gopdf.GoPdf, family string, size float64) {
	_ = pdf.SetFont(family, "", size)
}

// setText sets the text colour and font size in one call.
func setText(pdf *gopdf.GoPdf, r, g, b uint8, size float64) {
	pdf.SetTextColor(r, g, b)
	_ = pdf.SetFontSize(size)
}

// wrapCellText builds the label of a cell ("Математика (Ив) 301") and wraps
// it to the given width using the CURRENT font (set the font before
// calling). Returns at most maxLines lines, the last one ellipsized.
func wrapCellText(pdf *gopdf.GoPdf, opts Options, cell Cell, maxWidthMM float64, maxLines int) []string {
	parts := []string{opts.SubjectName(cell.SubjectID)}
	if opts.ShowTeacher {
		parts = append(parts, "("+opts.TeacherName(cell.TeacherID)+")")
	}
	if opts.ShowRoom {
		rn := opts.RoomName(cell.RoomID)
		if rn != "" && rn != "?" {
			parts = append(parts, rn)
		}
	}
	return wrapTextMM(pdf, strings.Join(parts, " "), maxWidthMM, maxLines)
}

// wrapTextMM does a greedy word-wrap measured with the current font, so the
// result fits maxWidthMM regardless of the actual glyph widths (Cyrillic
// letters are wider than the old "~1.6 mm per char" guess assumed).
// If the text does not fit into maxLines lines, the tail is replaced with
// an ellipsis on the last line.
func wrapTextMM(pdf *gopdf.GoPdf, text string, maxWidthMM float64, maxLines int) []string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}
	if maxLines < 1 {
		maxLines = 1
	}
	var out []string
	cur := words[0]
	for _, w := range words[1:] {
		cand := cur + " " + w
		if textWidthMM(pdf, cand) <= maxWidthMM {
			cur = cand
			continue
		}
		out = append(out, cur)
		cur = w
	}
	if cur != "" {
		out = append(out, cur)
	}
	if len(out) > maxLines {
		out = out[:maxLines]
		last := out[maxLines-1]
		runes := []rune(last)
		for len(runes) > 1 && textWidthMM(pdf, string(runes)+"…") > maxWidthMM {
			runes = runes[:len(runes)-1]
		}
		out[maxLines-1] = string(runes) + "…"
	}
	return out
}

// truncate shortens a string so it fits within maxMM millimeters (using
// the current font). Used for row labels that might otherwise overflow.
func truncate(pdf *gopdf.GoPdf, s string, maxMM float64) string {
	if s == "" {
		return ""
	}
	wMM := textWidthMM(pdf, s)
	if wMM <= maxMM {
		return s
	}
	runes := []rune(s)
	for n := len(runes); n > 1; n-- {
		candidate := string(runes[:n-1]) + "…"
		if textWidthMM(pdf, candidate) <= maxMM {
			return candidate
		}
	}
	return "…"
}

// textWidthMM measures the rendered width of s (in millimeters) using
// the currently-set font. gopdf's MeasureTextWidth returns points; we
// convert back to mm.
func textWidthMM(pdf *gopdf.GoPdf, s string) float64 {
	wPt, err := pdf.MeasureTextWidth(s)
	if err != nil || wPt == 0 {
		return float64(len([]rune(s))) * 1.5
	}
	return ptToMM(wPt)
}

func hexToRGB(hex string) (uint8, uint8, uint8) {
	h := strings.TrimPrefix(hex, "#")
	if len(h) == 3 {
		h = string(h[0]) + string(h[0]) + string(h[1]) + string(h[1]) + string(h[2]) + string(h[2])
	}
	if len(h) != 6 {
		return 229, 231, 235 // #e5e7eb default gray
	}
	var r, g, b int
	fmt.Sscanf(h[:2], "%02x", &r)
	fmt.Sscanf(h[2:4], "%02x", &g)
	fmt.Sscanf(h[4:], "%02x", &b)
	return uint8(r), uint8(g), uint8(b)
}

func dayName(d int) string {
	names := []string{"Пн", "Вт", "Ср", "Чт", "Пт", "Сб", "Вс"}
	if d >= 0 && d < len(names) {
		return names[d]
	}
	return fmt.Sprintf("Д%d", d+1)
}

func slotLabel(si int, periods []Period) string {
	if si >= len(periods) {
		return fmt.Sprintf("П%d", si+1)
	}
	p := periods[si]
	if p.Start == "" {
		return fmt.Sprintf("П%d", si+1)
	}
	return fmt.Sprintf("П%d %s–%s", si+1, p.Start, p.End)
}

// SortRowsByName returns a copy of rows sorted by Label — handy when the
// caller did not pre-sort.
func SortRowsByName(rows []Row) []Row {
	out := append([]Row(nil), rows...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
