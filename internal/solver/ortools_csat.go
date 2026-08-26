//go:build ortools

package solver

/*
#cgo CFLAGS: -I${SRCDIR}/../../third_party/ortools/include -I${SRCDIR}/../../third_party/ortools_bind
#cgo LDFLAGS: -L${SRCDIR}/../../third_party/ortools/lib -lortools_full -lstdc++
#include <stdlib.h>
#include "bind.h"
*/
import "C"

import (
	"context"
	"runtime"
	"time"

	"timetable/internal/domain"
)

func init() {
	preciseSolver = ortoolsSolve
}

func constraintTypeCode(t string) int {
	switch t {
	case "teacher_unavailable":
		return 0
	case "class_unavailable":
		return 1
	case "room_unavailable":
		return 2
	case "max_consecutive":
		return 3
	case "lunch_break":
		return 4
	case "max_lessons_per_day":
		return 5
	case "min_lessons_per_day":
		return 6
	case "prefer_morning":
		return 7
	case "max_gaps":
		return 8
	}
	return -1
}

func entityTypeCode(t string) int {
	switch t {
	case "teacher":
		return 0
	case "class":
		return 1
	case "room":
		return 2
	case "school":
		return 3
	}
	return -1
}

// ortoolsSolve delegates to the OR-Tools CP-SAT C++ binding.
func ortoolsSolve(in SolveInput, parallelism int, timeout time.Duration) (Result, bool) {
	days := in.Config.DaysPerWeek
	if days <= 0 {
		days = 6
	}
	slots := in.Config.SlotsPerDay
	if slots <= 0 {
		slots = 8
	}

	nl := len(in.Lessons)
	hours := make([]C.int, nl)
	lclass := make([]C.int, nl)
	lteacher := make([]C.int, nl)
	lsubject := make([]C.int, nl)
	for i, l := range in.Lessons {
		h := C.int(l.HoursPerWeek)
		if h < 1 {
			h = 1
		}
		hours[i] = h
		lclass[i] = C.int(l.ClassID)
		lteacher[i] = C.int(l.TeacherID)
		lsubject[i] = C.int(l.SubjectID)
	}

	// room-type ids (0 = any)
	roomTypeID := map[string]int{"any": 0}
	next := 1
	assignType := func(s string) int {
		if s == "" {
			return 0
		}
		if id, ok := roomTypeID[s]; ok {
			return id
		}
		id := next
		next++
		roomTypeID[s] = id
		return id
	}
	lreq := make([]C.int, nl)
	for i, l := range in.Lessons {
		rt := "any"
		if subj, ok := in.Subjects[l.SubjectID]; ok && subj.RequiresRoomType != "" {
			rt = subj.RequiresRoomType
		}
		lreq[i] = C.int(assignType(rt))
	}

	nr := len(in.Rooms)
	roomIDs := make([]C.int, nr)
	roomTypes := make([]C.int, nr)
	for i, r := range in.Rooms {
		roomIDs[i] = C.int(r.ID)
		roomTypes[i] = C.int(assignType(r.RoomType))
	}

	// constraints
	cts := make([]C.CConstraint, len(in.Constraints))
	for i, c := range in.Constraints {
		cts[i].ctype = C.int(constraintTypeCode(c.Type))
		cts[i].entity_type = C.int(entityTypeCode(c.EntityType))
		cts[i].entity_id = C.int(c.EntityID)
		if c.DayOfWeek != nil {
			cts[i].day = C.int(*c.DayOfWeek)
		} else {
			cts[i].day = -1
		}
		if c.TimeslotStart != nil {
			cts[i].slot_start = C.int(*c.TimeslotStart)
		} else {
			cts[i].slot_start = -1
		}
		if c.TimeslotEnd != nil {
			cts[i].slot_end = C.int(*c.TimeslotEnd)
		} else {
			cts[i].slot_end = -1
		}
		cts[i].value = C.int(c.Weight)
		if c.IsHard {
			cts[i].is_hard = 1
		} else {
			cts[i].is_hard = 0
		}
	}

	workers := parallelism
	if workers <= 0 {
		workers = runtime.NumCPU()
	}

	res := C.ortools_solve(
		C.int(nl), &hours[0], &lclass[0], &lteacher[0], &lsubject[0], &lreq[0],
		C.int(nr), &roomIDs[0], &roomTypes[0],
		C.int(days), C.int(slots),
		C.int(len(cts)), &cts[0],
		C.int(int(timeout.Milliseconds())), C.int(workers),
	)
	if res == nil {
		return Result{}, false
	}
	defer C.free_schedule_result(res)

	entries := make([]domain.ScheduleEntry, 0, int(res.count))
	for i := 0; i < int(res.count); i++ {
		entries = append(entries, domain.ScheduleEntry{
			SchoolID:  in.SchoolID,
			LessonID:  int(res.lesson_ids[i]),
			ClassID:   int(res.class_ids[i]),
			TeacherID: int(res.teacher_ids[i]),
			SubjectID: int(res.subject_ids[i]),
			RoomID:    int(res.room_ids[i]),
			DayOfWeek: int(res.days[i]),
			Timeslot:  int(res.slots[i]),
			WeekType:  0,
		})
	}
	return Result{Entries: entries, Placed: int(res.count), Total: int(res.count), Violations: 0}, true
}
