package solver

import (
	"context"
	"testing"
	"time"

	"timetable/internal/domain"
)

func TestSolveNoConflicts(t *testing.T) {
	teachers := map[int]domain.Teacher{
		1: {ID: 1, Name: "Иванов"},
		2: {ID: 2, Name: "Петрова"},
	}
	classes := map[int]domain.SchoolClass{
		1: {ID: 1, Name: "10А"},
		2: {ID: 2, Name: "11Б"},
	}
	subjects := map[int]domain.Subject{
		1: {ID: 1, Name: "Математика"},
		2: {ID: 2, Name: "Физика"},
	}
	rooms := []domain.Room{
		{ID: 1, Name: "301", RoomType: "any"},
		{ID: 2, Name: "302", RoomType: "any"},
	}
	lessons := []domain.Lesson{
		{ID: 1, ClassID: 1, SubjectID: 1, TeacherID: 1, HoursPerWeek: 5},
		{ID: 2, ClassID: 1, SubjectID: 2, TeacherID: 2, HoursPerWeek: 3},
		{ID: 3, ClassID: 2, SubjectID: 1, TeacherID: 1, HoursPerWeek: 4},
		{ID: 4, ClassID: 2, SubjectID: 2, TeacherID: 2, HoursPerWeek: 3},
	}
	cfg := domain.SchedulingConfig{DaysPerWeek: 6, SlotsPerDay: 8}
	in := SolveInput{
		SchoolID: 1, Lessons: lessons, Teachers: teachers, Classes: classes,
		Rooms: rooms, Subjects: subjects, Constraints: nil, Config: cfg,
	}
	res := Solve(context.Background(), in, 4, 20*time.Second)
	if res.Placed != res.Total {
		t.Fatalf("not all lessons placed: %d/%d", res.Placed, res.Total)
	}
	// verify no hard conflicts
	teacherBusy := map[int]map[int]bool{}
	classBusy := map[int]map[int]bool{}
	roomBusy := map[int]map[int]bool{}
	for _, e := range res.Entries {
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
			t.Fatalf("teacher %d double-booked at %d", e.TeacherID, key)
		}
		if classBusy[e.ClassID][key] {
			t.Fatalf("class %d double-booked at %d", e.ClassID, key)
		}
		if roomBusy[e.RoomID][key] {
			t.Fatalf("room %d double-booked at %d", e.RoomID, key)
		}
		teacherBusy[e.TeacherID][key] = true
		classBusy[e.ClassID][key] = true
		roomBusy[e.RoomID][key] = true
	}
	t.Logf("placed %d/%d, soft violations %d", res.Placed, res.Total, res.Violations)
}

// TestSolvePreciseFallback verifies that SolvePrecise returns a valid result even when the
// OR-Tools build tag is not compiled in (preciseSolver == nil -> pure-Go fallback).
func TestSolvePreciseFallback(t *testing.T) {
	teachers := map[int]domain.Teacher{1: {ID: 1, Name: "Иванов"}}
	classes := map[int]domain.SchoolClass{1: {ID: 1, Name: "10А"}}
	subjects := map[int]domain.Subject{1: {ID: 1, Name: "Математика"}}
	rooms := []domain.Room{{ID: 1, Name: "301"}}
	lessons := []domain.Lesson{{ID: 1, ClassID: 1, SubjectID: 1, TeacherID: 1, HoursPerWeek: 5}}
	in := SolveInput{
		SchoolID: 1, Lessons: lessons, Teachers: teachers, Classes: classes,
		Rooms: rooms, Subjects: subjects, Config: domain.SchedulingConfig{DaysPerWeek: 6, SlotsPerDay: 8},
	}
	res := SolvePrecise(context.Background(), in, 2, 20*time.Second)
	if res.Placed != res.Total {
		t.Fatalf("SolvePrecise fallback placed %d/%d", res.Placed, res.Total)
	}
}
