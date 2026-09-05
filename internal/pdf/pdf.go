// Package pdf renders the school timetable to a PDF using a pure-Go PDF
// library and an embedded Unicode (Cyrillic-capable) font. Moving PDF
// generation off the frontend (which used jsPDF inside the Wails WebKit
// webview, where datauristring output was unreliable and the default
// fonts lacked Cyrillic glyphs) eliminates a whole class of export bugs.
//
// v1.9.0 — aSc Timetable print style:
//   - page header in the aSc manner: school name top-left, print date in
//     the footer, the row name as the centered bold title over a thin rule;
//   - the table runs periods × days: rows are lesson periods (№ + bell
//     times in the left column), columns are weekdays under a light-blue
//     caption row — the layout every aSc Timetables printout uses;
//   - lesson cards are centered blocks: subject (bold) / teacher / room,
//     adaptively sized with an ellipsis, conflicts outlined in red;
//   - the "school" export is a sheet of per-class mini-tables (the aSc
//     "timetable for all classes" look) with a colour legend and the
//     conflict list under the last page of tables;
//   - every page carries a footer: print date left, "стр. N из M" right.
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
	Mode string // "school" (per-class mini-tables), "class", "teacher", "room"
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
	// Rendered under the last page of the "school" mode.
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

	th := newTheme(opts.BW)

	switch opts.Mode {
	case "school":
		renderSchoolPoster(&pdf, opts, th, daysN, wMM, hMM)
	case "class", "teacher", "room":
		renderOnePerPage(&pdf, opts, th, daysN, wMM, hMM)
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

// maxF returns the largest of its arguments.
func maxF(vals ...float64) float64 {
	m := vals[0]
	for _, v := range vals[1:] {
		if v > m {
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
	// A single long word ("Физкультура") cannot wrap, so make sure EVERY
	// returned line actually fits the width — ellipsize the ones that do
	// not instead of letting them spill over the cell border.
	for i, ln := range out {
		if textWidthMM(pdf, ln) > maxWidthMM {
			runes := []rune(ln)
			for len(runes) > 1 && textWidthMM(pdf, string(runes)+"…") > maxWidthMM {
				runes = runes[:len(runes)-1]
			}
			out[i] = string(runes) + "…"
		}
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
// the currently-set font. gopdf's MeasureTextWidth already converts its
// internal PDF-point measurement into the configured unit (UnitMM here),
// so the value comes back in millimeters directly — converting it again
// (the v1.8.0 behaviour) under-measured every string by ~2.8x, which
// made wrapped lines, truncation and the legend flow all overflow.
func textWidthMM(pdf *gopdf.GoPdf, s string) float64 {
	wMM, err := pdf.MeasureTextWidth(s)
	if err != nil || wMM == 0 {
		return float64(len([]rune(s))) * 1.5
	}
	return wMM
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
