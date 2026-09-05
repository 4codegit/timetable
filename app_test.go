package main

import (
	"context"
	"encoding/json"
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
	return &App{ctx: context.Background(), store: store}
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
		{"10А", "Математика", "Иванов И.И.", 5},
		{"10А", "Физика", "Петрова П.П.", 3},
		{"10Б", "Математика", "Иванов И.И.", 4},
		{"10Б", "Физика", "Петрова П.П.", 3},
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
	_, err = a.ImportAll(snap)
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

// TestExportImportRoundTripPreservesSchedule verifies that a JSON backup is a
// real backup: it can be restored into a different database even though every
// generated ID changes.  It covers the links most likely to be silently lost:
// settings, subgroups, constraints, lessons, and schedule entries.
func TestExportImportRoundTripPreservesSchedule(t *testing.T) {
	source := newTestApp(t)
	// Ensure the exported school is not the first record, as it commonly won't
	// be in the destination database either.
	if _, err := source.CreateSchool("Черновик"); err != nil {
		t.Fatal(err)
	}
	school, err := source.CreateSchool("Лицей №1")
	if err != nil {
		t.Fatal(err)
	}
	if err := source.UpdateSchoolSettings(school.ID, `{"days":5,"slots":7}`); err != nil {
		t.Fatal(err)
	}
	teacher, err := source.CreateTeacher(domain.Teacher{SchoolID: school.ID, Name: "Иванова", MaxHoursPerWeek: 24})
	if err != nil {
		t.Fatal(err)
	}
	subject, err := source.CreateSubject(domain.Subject{SchoolID: school.ID, Name: "Математика"})
	if err != nil {
		t.Fatal(err)
	}
	parent, err := source.CreateClass(domain.SchoolClass{SchoolID: school.ID, Name: "8А"})
	if err != nil {
		t.Fatal(err)
	}
	child, err := source.CreateClass(domain.SchoolClass{SchoolID: school.ID, Name: "8А-1", SubgroupOf: &parent.ID})
	if err != nil {
		t.Fatal(err)
	}
	room, err := source.CreateRoom(domain.Room{SchoolID: school.ID, Name: "204", Capacity: 28})
	if err != nil {
		t.Fatal(err)
	}
	lesson, err := source.CreateLesson(domain.Lesson{SchoolID: school.ID, ClassID: child.ID, SubjectID: subject.ID, TeacherID: teacher.ID, HoursPerWeek: 3, MinGapDays: 1})
	if err != nil {
		t.Fatal(err)
	}
	day, from, to := 2, 1, 2
	if _, err := source.CreateConstraint(domain.Constraint{SchoolID: school.ID, Type: "teacher_unavailable", EntityType: "teacher", EntityID: teacher.ID, DayOfWeek: &day, TimeslotStart: &from, TimeslotEnd: &to, Weight: 100, IsHard: true}); err != nil {
		t.Fatal(err)
	}
	if err := source.ReplaceSchedule(school.ID, []domain.ScheduleEntry{{
		SchoolID: school.ID, LessonID: lesson.ID, ClassID: child.ID, TeacherID: teacher.ID,
		SubjectID: subject.ID, RoomID: room.ID, DayOfWeek: 3, Timeslot: 4,
	}}); err != nil {
		t.Fatal(err)
	}

	snapshot, err := source.ExportAll(school.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Schedule) != 1 {
		t.Fatalf("expected exported schedule entry, got %d", len(snapshot.Schedule))
	}
	// Simulate a backup whose source ID collides with an unrelated local school.
	// Import must create a new school rather than erase that user's data.
	snapshot.School.ID = 1
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}

	destination := newTestApp(t)
	other, err := destination.CreateSchool("Другой лицей")
	if err != nil {
		t.Fatal(err)
	}
	imported, err := destination.ImportAll(string(data))
	if err != nil {
		t.Fatalf("ImportAll: %v", err)
	}
	schools, err := destination.ListSchools()
	if err != nil || len(schools) != 2 {
		t.Fatalf("expected original and imported schools, got %+v (err=%v)", schools, err)
	}
	if imported.ID == other.ID {
		t.Fatal("import overwrote the unrelated school with the same source ID")
	}
	gotSchool := *imported
	if gotSchool.Name != "Лицей №1" || gotSchool.SettingsJSON != `{"days":5,"slots":7}` {
		t.Fatalf("school metadata was not restored: %+v", gotSchool)
	}
	classes, _ := destination.ListClasses(gotSchool.ID)
	if len(classes) != 2 {
		t.Fatalf("expected 2 classes, got %+v", classes)
	}
	var restoredParent, restoredChild domain.SchoolClass
	for _, class := range classes {
		if class.Name == "8А" {
			restoredParent = class
		}
		if class.Name == "8А-1" {
			restoredChild = class
		}
	}
	if restoredChild.SubgroupOf == nil || *restoredChild.SubgroupOf != restoredParent.ID {
		t.Fatalf("subgroup parent was not remapped: parent=%+v child=%+v", restoredParent, restoredChild)
	}
	teachers, _ := destination.ListTeachers(gotSchool.ID)
	constraints, _ := destination.ListConstraints(gotSchool.ID)
	if len(teachers) != 1 || len(constraints) != 1 || constraints[0].EntityID != teachers[0].ID {
		t.Fatalf("constraint entity was not remapped: teachers=%+v constraints=%+v", teachers, constraints)
	}
	entries, err := destination.ListSchedule(gotSchool.ID)
	if err != nil || len(entries) != 1 || entries[0].ClassID != restoredChild.ID || entries[0].DayOfWeek != 3 || entries[0].Timeslot != 4 {
		t.Fatalf("schedule was not restored: %+v (err=%v)", entries, err)
	}
}

// TestSwapEntriesIntegration drives the App-layer SwapEntries exactly the way
// the frontend does after a drag-and-drop: src is at (srcDay, srcSlot), target
// is at (dstDay, dstSlot). After the call the two entries must have exchanged
// their coordinates. This is a regression test for the silent-no-op swap bug
// that went unnoticed because the unit test was missing.
func TestSwapEntriesIntegration(t *testing.T) {
	a := newTestApp(t)
	sc, err := a.CreateSchool("Swap Integration")
	if err != nil {
		t.Fatalf("CreateSchool: %v", err)
	}
	id := sc.ID

	te, _ := a.CreateTeacher(domain.Teacher{SchoolID: id, Name: "Иванов", MaxHoursPerWeek: 30})
	a.CreateSubject(domain.Subject{SchoolID: id, Name: "Математика"})
	a.CreateClass(domain.SchoolClass{SchoolID: id, Name: "10А"})
	a.CreateRoom(domain.Room{SchoolID: id, Name: "301"})
	lesson, err := a.CreateLesson(domain.Lesson{SchoolID: id, ClassID: 1, SubjectID: 1, TeacherID: te.ID, HoursPerWeek: 5, MinGapDays: 1})
	if err != nil {
		t.Fatalf("CreateLesson: %v", err)
	}

	// Seed two schedule entries at distinct cells.
	if err := a.store.ReplaceSchedule(id, []domain.ScheduleEntry{
		{SchoolID: id, LessonID: lesson.ID, ClassID: 1, TeacherID: te.ID, SubjectID: 1, RoomID: 1, DayOfWeek: 1, Timeslot: 2},
		{SchoolID: id, LessonID: lesson.ID, ClassID: 1, TeacherID: te.ID, SubjectID: 1, RoomID: 1, DayOfWeek: 3, Timeslot: 4},
	}); err != nil {
		t.Fatalf("ReplaceSchedule: %v", err)
	}
	before, _ := a.ListSchedule(id)
	if len(before) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(before))
	}
	var idA, idB int
	for _, e := range before {
		if e.DayOfWeek == 1 && e.Timeslot == 2 {
			idA = e.ID
		}
		if e.DayOfWeek == 3 && e.Timeslot == 4 {
			idB = e.ID
		}
	}
	if idA == 0 || idB == 0 {
		t.Fatalf("could not locate seeded entries: %+v", before)
	}

	// Frontend calls: src.id (=A) should end at (3,4); target.id (=B) should end at (1,2).
	if err := a.SwapEntries(idA, 3, 4, idB, 1, 2); err != nil {
		t.Fatalf("SwapEntries: %v", err)
	}
	after, _ := a.ListSchedule(id)
	for _, e := range after {
		if e.ID == idA && (e.DayOfWeek != 3 || e.Timeslot != 4) {
			t.Errorf("A should be at (3,4), got (%d,%d)", e.DayOfWeek, e.Timeslot)
		}
		if e.ID == idB && (e.DayOfWeek != 1 || e.Timeslot != 2) {
			t.Errorf("B should be at (1,2), got (%d,%d)", e.DayOfWeek, e.Timeslot)
		}
	}
}

// TestHasPreciseSolver ensures the boolean is exposed at the App layer so the
// frontend can show an accurate "OR-Tools available" badge. The default build
// (no -tags ortools) must report false.
func TestHasPreciseSolver(t *testing.T) {
	a := newTestApp(t)
	if a.HasPreciseSolver() {
		t.Fatal("default build (no -tags ortools) should report HasPreciseSolver=false")
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
