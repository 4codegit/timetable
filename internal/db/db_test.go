package db

import (
	"path/filepath"
	"testing"

	"timetable/internal/domain"
)

func TestStoreCRUD(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	sc, err := s.CreateSchool("Тест")
	if err != nil {
		t.Fatal(err)
	}
	sid := sc.ID

	te, _ := s.CreateTeacher(domain.Teacher{SchoolID: sid, Name: "Иванов", MaxHoursPerWeek: 30})
	if _, err := s.CreateSubject(domain.Subject{SchoolID: sid, Name: "Математика"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateClass(domain.SchoolClass{SchoolID: sid, Name: "10А"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateRoom(domain.Room{SchoolID: sid, Name: "301"}); err != nil {
		t.Fatal(err)
	}
	l, err := s.CreateLesson(domain.Lesson{SchoolID: sid, ClassID: 1, SubjectID: 1, TeacherID: te.ID, HoursPerWeek: 5})
	if err != nil {
		t.Fatal(err)
	}
	ts, _ := s.ListTeachers(sid)
	if len(ts) != 1 {
		t.Fatalf("expected 1 teacher, got %d", len(ts))
	}
	ls, lerr := s.ListLessons(sid)
	if lerr != nil {
		t.Fatalf("ListLessons error: %v", lerr)
	}
	if len(ls) != 1 || ls[0].HoursPerWeek != 5 {
		t.Fatalf("lesson not persisted correctly: %+v (err=%v)", ls, lerr)
	}
	if err := s.SaveSchedule([]domain.ScheduleEntry{
		{SchoolID: sid, LessonID: l.ID, ClassID: 1, TeacherID: te.ID, SubjectID: 1, RoomID: 1, DayOfWeek: 0, Timeslot: 0},
	}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.ListSchedule(sid)
	if len(got) != 1 {
		t.Fatalf("expected 1 schedule entry, got %d", len(got))
	}

	// MoveEntry relocates a single entry after a manual edit.
	if err := s.MoveEntry(got[0].ID, 2, 3); err != nil {
		t.Fatal(err)
	}
	moved, _ := s.ListSchedule(sid)
	if len(moved) != 1 || moved[0].DayOfWeek != 2 || moved[0].Timeslot != 3 {
		t.Fatalf("MoveEntry failed: %+v", moved)
	}

	// ReplaceSchedule clears then writes the new set atomically.
	if err := s.ReplaceSchedule(sid, []domain.ScheduleEntry{
		{SchoolID: sid, LessonID: l.ID, ClassID: 1, TeacherID: te.ID, SubjectID: 1, RoomID: 1, DayOfWeek: 1, Timeslot: 1},
		{SchoolID: sid, LessonID: l.ID, ClassID: 1, TeacherID: te.ID, SubjectID: 1, RoomID: 1, DayOfWeek: 4, Timeslot: 6},
	}); err != nil {
		t.Fatal(err)
	}
	repl, _ := s.ListSchedule(sid)
	if len(repl) != 2 {
		t.Fatalf("ReplaceSchedule expected 2 entries, got %d", len(repl))
	}
}
