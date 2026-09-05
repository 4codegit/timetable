// Package pdf renders the school timetable to a PDF using a pure-Go PDF
// library and an embedded Unicode (Cyrillic-capable) font. Moving PDF
// generation off the frontend (which used jsPDF inside the Wails WebKit
// webview, where datauristring output was unreliable and the default
// fonts lacked Cyrillic glyphs) eliminates a whole class of export bugs.
//
// The frontend now calls App.ExportPDF, which returns a base64 string the
// existing SaveExport flow writes to ~/Downloads — so the user-facing
// dialog is unchanged, but the bytes come from a reliable Go renderer.
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
	if err := pdf.AddTTFFontDataWithOption("DejaVu-Bold", fontBold, gopdf.TtfOption{Style: gopdf.Bold}); err != nil {
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

// renderSchoolPoster lays every row (typically every class) on ONE big
// page with a colour legend at the bottom. This matches the existing
// "вся школа (плакат)" export mode.
func renderSchoolPoster(pdf *gopdf.GoPdf, opts Options, daysN int, pageW, pageH float64) {
	margin := 8.0
	labelW := 22.0
	gridX := margin + labelW
	topY := margin + 12.0

	// Reserve bottom space for the legend (and conflict list if present).
	legendH := 14.0
	conflictsH := 0.0
	if len(opts.Conflicts) > 0 {
		conflictsH = 6.0 + float64(len(opts.Conflicts))*3.5
	}
	gridH := pageH - topY - margin - legendH - conflictsH

	rowsCount := len(opts.Rows)
	if rowsCount <= 0 {
		return
	}
	rowH := gridH / float64(rowsCount)
	colW := (pageW - gridX - margin) / float64(opts.Slots)
	dayH := rowH / float64(daysN)

	// Title + meta line.
	setText(pdf, 20, 20, 20, 13)
	pdf.SetX(margin)
	pdf.SetY(margin)
	_ = pdf.Text(opts.SchoolName + " · " + opts.Title)

	// Per-row content.
	for ri, row := range opts.Rows {
		rowTop := topY + float64(ri)*rowH
		// Row label (left).
		setText(pdf, 20, 20, 20, 9)
		pdf.SetX(margin)
		pdf.SetY(rowTop + 1)
		_ = pdf.Text(truncate(pdf, row.Label, labelW-2))

		// Day label (left of each day row).
		for di := 0; di < daysN; di++ {
			dayTop := rowTop + float64(di)*dayH
			setText(pdf, 20, 20, 20, 7)
			pdf.SetX(margin)
			pdf.SetY(dayTop + dayH/2)
			_ = pdf.Text(dayName(di))
		}

		// Cells.
		for si := 0; si < opts.Slots; si++ {
			cellX := gridX + float64(si)*colW
			for di := 0; di < daysN; di++ {
				cellTop := rowTop + float64(di)*dayH
				cell, ok := opts.CellAt(row.ID, di, si)
				if !ok {
					// Empty cell — just a faint border.
					pdf.SetStrokeColor(220, 220, 220)
					pdf.SetLineWidth(0.1)
					pdf.RectFromUpperLeftWithStyle(cellX, cellTop, colW, dayH, "D")
					continue
				}
				bg := opts.SubjectColor(cell.SubjectID)
				if cell.Conflict {
					bg = "#b91c1c"
				}
				if opts.BW && !cell.Conflict {
					bg = "#e5e7eb"
				}
				r, g, b := hexToRGB(bg)
				pdf.SetFillColor(r, g, b)
				pdf.RectFromUpperLeftWithStyle(cellX, cellTop, colW, dayH, "F")

				// Text. White on red (conflict), black otherwise.
				if cell.Conflict {
					setText(pdf, 255, 255, 255, 6)
				} else {
					setText(pdf, 20, 20, 20, 6)
				}
				lines := wrapCellText(opts, cell, colW-1.5)
				y := cellTop + 2
				for i, ln := range lines {
					if i >= 3 {
						break
					}
					pdf.SetX(cellX + 0.8)
					pdf.SetY(y)
					_ = pdf.Text(ln)
					y += 2.4
				}
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
		bg := opts.SubjectColor(item.SubjectID)
		if opts.BW {
			bg = "#e5e7eb"
		}
		r, g, b := hexToRGB(bg)
		pdf.SetFillColor(r, g, b)
		pdf.RectFromUpperLeftWithStyle(x, legendY-2.5, 3.5, 3.5, "F")
		pdf.SetX(x + 4.5)
		pdf.SetY(legendY)
		_ = pdf.Text(truncate(pdf, item.Name, 50))
		x += 4.5 + textWidthMM(pdf, item.Name) + 4
		if x > pageW-30 {
			x = margin
			legendY -= 5
		}
	}

	// Conflict list (if any) below the legend.
	if len(opts.Conflicts) > 0 {
		cy := legendY + 5
		setText(pdf, 0x80, 0x10, 0x10, 8)
		pdf.SetX(margin)
		pdf.SetY(cy)
		_ = pdf.Text(fmt.Sprintf("Конфликты (%d):", len(opts.Conflicts)))
		cy += 3.5
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
	margin := 8.0
	labelW := 22.0
	gridX := margin + labelW
	topY := margin + 12.0
	gridW := pageW - gridX - margin
	gridH := pageH - topY - margin
	colW := gridW / float64(opts.Slots)
	rowH := gridH / float64(daysN)

	first := true
	for _, row := range opts.Rows {
		if !first {
			pdf.AddPage()
		}
		first = false

		// Header.
		setText(pdf, 20, 20, 20, 13)
		pdf.SetX(margin)
		pdf.SetY(margin)
		_ = pdf.Text(row.Label)

		setText(pdf, 20, 20, 20, 8)
		pdf.SetX(margin)
		pdf.SetY(margin + 4.5)
		_ = pdf.Text(opts.SchoolName + " · " + opts.Title)

		// Slot header row.
		for si := 0; si < opts.Slots; si++ {
			x := gridX + float64(si)*colW
			setText(pdf, 20, 20, 20, 7)
			pdf.SetX(x + 0.5)
			pdf.SetY(topY - 4)
			_ = pdf.Text(slotLabel(si, opts.Periods))
		}

		// Day rows.
		for di := 0; di < daysN; di++ {
			y := topY + float64(di)*rowH
			// Day label.
			setText(pdf, 20, 20, 20, 8)
			pdf.SetX(margin)
			pdf.SetY(y + rowH/2)
			_ = pdf.Text(dayName(di))
			for si := 0; si < opts.Slots; si++ {
				x := gridX + float64(si)*colW
				cell, ok := opts.CellAt(row.ID, di, si)
				if !ok {
					pdf.SetStrokeColor(220, 220, 220)
					pdf.SetLineWidth(0.1)
					pdf.RectFromUpperLeftWithStyle(x, y, colW, rowH, "D")
					continue
				}
				bg := opts.SubjectColor(cell.SubjectID)
				if cell.Conflict {
					bg = "#b91c1c"
				}
				if opts.BW && !cell.Conflict {
					bg = "#e5e7eb"
				}
				r, g, b := hexToRGB(bg)
				pdf.SetFillColor(r, g, b)
				pdf.RectFromUpperLeftWithStyle(x, y, colW, rowH, "F")
				if cell.Conflict {
					setText(pdf, 255, 255, 255, 7)
				} else {
					setText(pdf, 20, 20, 20, 7)
				}
				lines := wrapCellText(opts, cell, colW-1.5)
				yy := y + 2.5
				for i, ln := range lines {
					if i >= 4 {
						break
					}
					pdf.SetX(x + 0.8)
					pdf.SetY(yy)
					_ = pdf.Text(ln)
					yy += 2.8
				}
			}
		}
	}
}

// setText sets the text colour and font size in one call.
func setText(pdf *gopdf.GoPdf, r, g, b uint8, size float64) {
	pdf.SetTextColor(r, g, b)
	_ = pdf.SetFontSize(size)
}

// wrapCellText mirrors the frontend cellLabel and splits long lines so
// the PDF cell can fit up to ~3-4 lines of text without overflowing the
// cell box. gopdf does not have splitTextToSize so we approximate by
// character count based on the column width.
func wrapCellText(opts Options, cell Cell, maxWidthMM float64) []string {
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
	full := strings.Join(parts, " ")
	// Approximate fit: ~2 chars per mm at 7pt DejaVu. Wrap on spaces.
	approxCharsPerLine := int(maxWidthMM / 1.6)
	if approxCharsPerLine < 6 {
		approxCharsPerLine = 6
	}
	return wrapWords(full, approxCharsPerLine)
}

// wrapWords does a simple greedy word-wrap on `text` with the given line
// length in characters. It is good enough for cell labels.
func wrapWords(text string, lineLen int) []string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return []string{}
	}
	var out []string
	cur := words[0]
	for _, w := range words[1:] {
		if len(cur)+1+len(w) > lineLen {
			out = append(out, cur)
			cur = w
		} else {
			cur += " " + w
		}
	}
	if cur != "" {
		out = append(out, cur)
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
	// Binary-search-ish chop: take progressively shorter prefixes until it fits.
	for n := len(s); n > 1; n-- {
		candidate := s[:n-1] + "…"
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
		return float64(len(s)) * 1.5
	}
	return wPt / (72.0 / 25.4)
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
