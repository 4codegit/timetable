package main

import (
	"fmt"
	"path/filepath"
	"testing"

	"timetable/internal/db"
	"timetable/internal/domain"
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

func TestGenerate(t *testing.T) {
	a := newTestApp(t)
	sc, err := a.CreateSchool("Тест Генерация")
	if err != nil {
		t.Fatalf("CreateSchool: %v", err)
	}
	id := sc.ID

	// Create reference data
	if _, err := a.CreateTeacher(domain.Teacher{SchoolID: id, Name: "Иванов И.И.", ShortName: "Иванов", MaxHoursPerWeek: 30}); err != nil {
		t.Fatalf("CreateTeacher: %v", err)
	}
	if _, err := a.CreateTeacher(domain.Teacher{SchoolID: id, Name: "Петрова П.П.", ShortName: "Петрова", MaxHoursPerWeek: 25}); err != nil {
		t.Fatalf("CreateTeacher: %v", err)
	}
	if _, err := a.CreateSubject(domain.Subject{SchoolID: id, Name: "Математика", ShortName: "Мат", RequiresRoomType: "any"}); err != nil {
		t.Fatalf("CreateSubject: %v", err)
	}
	if _, err := a.CreateSubject(domain.Subject{SchoolID: id, Name: "Физика", ShortName: "Физ", RequiresRoomType: "any"}); err != nil {
		t.Fatalf("CreateSubject: %v", err)
	}
	if _, err := a.CreateClass(domain.SchoolClass{SchoolID: id, Name: "10А", Grade: 10, StudentCount: 25}); err != nil {
		t.Fatalf("CreateClass: %v", err)
	}
	if _, err := a.CreateClass(domain.SchoolClass{SchoolID: id, Name: "10Б", Grade: 10, StudentCount: 28}); err != nil {
		t.Fatalf("CreateClass: %v", err)
	}
	if _, err := a.CreateRoom(domain.Room{SchoolID: id, Name: "301", Capacity: 30, RoomType: "any"}); err != nil {
		t.Fatalf("CreateRoom: %v", err)
	}
	if _, err := a.CreateRoom(domain.Room{SchoolID: id, Name: "302", Capacity: 30, RoomType: "any"}); err != nil {
		t.Fatalf("CreateRoom: %v", err)
	}

	// Create lessons
	teachers, _ := a.ListTeachers(id)
	subjects, _ := a.ListSubjects(id)
	classes, _ := a.ListClasses(id)

	lessonDefs := []struct {
		cls, subj, teach string
		hours            int
	}{
		{"10А", "Математика", "Иванов", 5},
		{"10А", "Физика", "Петрова", 3},
		{"10Б", "Математика", "Иванов", 4},
		{"10Б", "Физика", "Петрова", 3},
	}

	tMap := map[string]int{}
	for _, tc := range teachers {
		tMap[tc.Name] = tc.ID
	}
	sMap := map[string]int{}
	for _, s := range subjects {
		sMap[s.Name] = s.ID
	}
	cMap := map[string]int{}
	for _, c := range classes {
		cMap[c.Name] = c.ID
	}

	for _, ld := range lessonDefs {
		if _, err := a.CreateLesson(domain.Lesson{
			SchoolID: id, ClassID: cMap[ld.cls], SubjectID: sMap[ld.subj],
			TeacherID: tMap[ld.teach], HoursPerWeek: ld.hours, MinGapDays: 1,
		}); err != nil {
			t.Fatalf("CreateLesson %v: %v", ld, err)
		}
	}

	// Generate schedule
	res, err := a.Generate(id, 6, 8)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if res.Placed != res.Total {
		t.Fatalf("not all lessons placed: %d/%d", res.Placed, res.Total)
	}
	t.Logf("Generate: placed %d/%d, violations %d", res.Placed, res.Total, res.Violations)

	// Verify schedule was persisted
	entries, err := a.ListSchedule(id)
	if err != nil {
		t.Fatalf("ListSchedule: %v", err)
	}
	if len(entries) != res.Placed {
		t.Fatalf("expected %d entries in DB, got %d", res.Placed, len(entries))
	}

	// Verify no hard conflicts (teacher/class/room double-booked)
	teacherBusy := map[int]map[int]bool{}
	classBusy := map[int]map[int]bool{}
	roomBusy := map[int]map[int]bool{}
	for _, e := range entries {
		key := e.DayOfWeek*1000 + e.Timeslot
		if teacherBusy[e.TeacherID] == nil {
			teacherBusy[e.TeacherID] = map[int]bool{}
		}
		if classBusy[e.ClassID] == nil {
			classBusy[e.ClassID] = map[int]bool{}
		}
		if roomBusy[e.RoomID] == nil {
			roomBusy[e.RoomID] = map[int]bool{}
		}
		if teacherBusy[e.TeacherID][key] {
			t.Errorf("teacher %d double-booked at day=%d slot=%d", e.TeacherID, e.DayOfWeek, e.Timeslot)
		}
		if classBusy[e.ClassID][key] {
			t.Errorf("class %d double-booked at day=%d slot=%d", e.ClassID, e.DayOfWeek, e.Timeslot)
		}
		if roomBusy[e.RoomID][key] {
			t.Errorf("room %d double-booked at day=%d slot=%d", e.RoomID, e.DayOfWeek, e.Timeslot)
		}
		teacherBusy[e.TeacherID][key] = true
		classBusy[e.ClassID][key] = true
		roomBusy[e.RoomID][key] = true
	}
}

func TestImportAllClearsExistingData(t *testing.T) {
	a := newTestApp(t)
	sc, err := a.CreateSchool("Импорт Тест")
	if err != nil {
		t.Fatalf("CreateSchool: %v", err)
	}
	id := sc.ID

	// Add initial data
	a.CreateTeacher(domain.Teacher{SchoolID: id, Name: "Старый Учитель", MaxHoursPerWeek: 30})
	a.CreateSubject(domain.Subject{SchoolID: id, Name: "Старый Предмет"})
	teachers1, _ := a.ListTeachers(id)
	if len(teachers1) != 1 {
		t.Fatalf("expected 1 teacher, got %d", len(teachers1))
	}

	// Import snapshot into existing school (should clear old data)
	snap := `{
                "school": {"id": ` + fmt.Sprintf("%d", id) + `, "name": "Импорт Тест"},
                "teachers": [{"name": "Новый Учитель", "max_hours_per_week": 25}],
                "subjects": [{"name": "Новый Предмет"}],
                "classes": [],
                "rooms": [],
                "lessons": [],
                "constraints": []
        }`
	err = a.ImportAll(snap)
	if err != nil {
		t.Fatalf("ImportAll: %v", err)
	}

	// Verify old data was cleared and new data imported
	teachers2, _ := a.ListTeachers(id)
	if len(teachers2) != 1 {
		t.Fatalf("expected 1 teacher after import, got %d", len(teachers2))
	}
	if teachers2[0].Name != "Новый Учитель" {
		t.Fatalf("expected 'Новый Учитель', got %q", teachers2[0].Name)
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
