package main

import (
	"path/filepath"
	"testing"

	"timetable/internal/db"
)

func newTestApp(t *testing.T) *App {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	store, err := db.New(path)
	if err != nil {
		t.Fatalf("db.New: %v", err)
	}
	return &App{store: store}
}

func TestRefsCSVImportExport(t *testing.T) {
	a := newTestApp(t)
	sc, err := a.CreateSchool("Тест")
	if err != nil {
		t.Fatalf("CreateSchool: %v", err)
	}
	id := sc.ID

	// teachers
	n, err := a.ImportRefsCSV(id, "teachers", "name,short_name,max_hours_per_week\nИванов,Ив,30\nПетрова,Пт,25\n")
	if err != nil {
		t.Fatalf("import teachers: %v", err)
	}
	if n != 2 {
		t.Fatalf("expected 2 teachers, got %d", n)
	}

	// classes + subjects
	if _, err := a.ImportRefsCSV(id, "classes", "name,grade,student_count,subgroup_of\n10А,10,25,\n"); err != nil {
		t.Fatalf("import classes: %v", err)
	}
	if _, err := a.ImportRefsCSV(id, "subjects", "name,short_name,requires_room_type\nМатем,Мат,any\n"); err != nil {
		t.Fatalf("import subjects: %v", err)
	}

	// lessons (resolved by name)
	n, err = a.ImportRefsCSV(id, "lessons", "class,subject,teacher,hours_per_week,min_gap_days\n10А,Матем,Иванов,5,1\n")
	if err != nil {
		t.Fatalf("import lessons: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 lesson, got %d", n)
	}

	// export lessons and verify round-trip content
	csv, err := a.ExportRefsCSV(id, "lessons")
	if err != nil {
		t.Fatalf("export lessons: %v", err)
	}
	want := "10А,Матем,Иванов,5,1"
	if !containsLine(csv, want) {
		t.Fatalf("exported lessons missing %q, got:\n%s", want, csv)
	}

	// export teachers keeps the data
	tcsv, err := a.ExportRefsCSV(id, "teachers")
	if err != nil {
		t.Fatalf("export teachers: %v", err)
	}
	if !containsLine(tcsv, "Иванов,Ив,30") {
		t.Fatalf("exported teachers missing row, got:\n%s", tcsv)
	}
}

func containsLine(s, sub string) bool {
	for _, line := range splitLines(s) {
		if line == sub {
			return true
		}
	}
	return false
}

func splitLines(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == '\n' {
			out = append(out, cur)
			cur = ""
		} else if r != '\r' {
			cur += string(r)
		}
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}
