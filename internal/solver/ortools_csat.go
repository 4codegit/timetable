//go:build linux && ortools

package solver

/*
#cgo linux CFLAGS: -I${SRCDIR}/../../third_party/ortools/include -I${SRCDIR}/../../third_party/ortools_bind
#cgo linux LDFLAGS: ${SRCDIR}/../../third_party/ortools_bind/bind.o -Wl,--start-group -L${SRCDIR}/../../third_party/ortools/lib -lortools -lortools_deps -Wl,--end-group -lstdc++ -lpthread -ldl -Wl,-rpath,$ORIGIN
#include <stdlib.h>
#include "bind.h"
*/
import "C"

import (
	"runtime"
	"time"
	"unsafe"

	"timetable/internal/domain"
)

func init() {
	preciseSolver = ortoolsSolve
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

  n := int(res.count)
  lessonIDs := (*[1 << 30]C.int)(unsafe.Pointer(res.lesson_ids))[:n:n]
  classIDs := (*[1 << 30]C.int)(unsafe.Pointer(res.class_ids))[:n:n]
  teacherIDs := (*[1 << 30]C.int)(unsafe.Pointer(res.teacher_ids))[:n:n]
  subjectIDs := (*[1 << 30]C.int)(unsafe.Pointer(res.subject_ids))[:n:n]
  outRoomIDs := (*[1 << 30]C.int)(unsafe.Pointer(res.room_ids))[:n:n]
  dayArr := (*[1 << 30]C.int)(unsafe.Pointer(res.days))[:n:n]
  slotArr := (*[1 << 30]C.int)(unsafe.Pointer(res.slots))[:n:n]

  entries := make([]domain.ScheduleEntry, 0, n)
  for i := 0; i < n; i++ {
    entries = append(entries, domain.ScheduleEntry{
      SchoolID:  in.SchoolID,
      LessonID:  int(lessonIDs[i]),
      ClassID:   int(classIDs[i]),
      TeacherID: int(teacherIDs[i]),
      SubjectID: int(subjectIDs[i]),
      RoomID:    int(outRoomIDs[i]),
      DayOfWeek: int(dayArr[i]),
      Timeslot:  int(slotArr[i]),
      WeekType:  0,
    })
  }
  return Result{Entries: entries, Placed: n, Total: n, Violations: 0}, true
}
